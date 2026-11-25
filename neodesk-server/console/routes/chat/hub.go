package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The chat permissions of version 2 (A242).
const (
	permSupport = "chat.support"
	permTeam    = "chat.team"
	permAssign  = "chat.assign"
)

// Version2 is the protocol version of the hub's sockets.
const Version2 = 2

// MaxFrameBytesV2 is the largest frame a socket of version 2 takes: a
// message of chatstore.MaxText characters fits whatever the characters
// are (at most six bytes each in JSON). Version 1 keeps MaxTextBytes.
const MaxFrameBytesV2 = 32 << 10

// capabilities2 are what the welcome of version 2 says the server can do.
var capabilities2 = []string{"v2", "reply", "edit", "delete", "attachments", "reactions", "read_marks", "typing", "presence", "inbox", "assign"}

// HubOptions configure the hub.
type HubOptions struct {
	// Store keeps the messages and the conversations.
	Store *chatstore.Store
	// Scope is the device scope of the accounts.
	Scope Scope
	// Holds reports whether a role holds a permission, overrides
	// included; nil holds every permission (tests).
	Holds func(ctx context.Context, role, permission string) (bool, error)
	// IsAdmin reports whether a role may delete anyone's message.
	IsAdmin func(role string) bool
	// Insert stores a console user's message to a device through the API
	// server, which hands it to an agent connected over CDAP. nil stores
	// it directly.
	Insert chatstore.InsertFunc
	// FilesDir is the folder of the chat files.
	FilesDir string
	// DisplayName is an account's name to show, "" for its user name.
	DisplayName func(ctx context.Context, id int64) string
	// Audit notes an edit, a deletion, an assignment and the like, by the
	// account (0 for a device).
	Audit func(ctx context.Context, userID int64, action string, details map[string]any)
}

// Hub is the chat of version 2: the console's and the devices' sockets,
// who gets which message, and the frames they send (hubframes.go).
type Hub struct {
	opts  HubOptions
	relay *Relay

	mu      sync.Mutex
	members map[*member]struct{}

	// recent are the messages handed on in the last minute (hubwire.go)
	recentMu sync.Mutex
	recent   map[int64]time.Time
	// sessionLines are the lines from sessions of the last seconds
	sessionLines map[string]time.Time
}

// NewHub returns the hub.
func NewHub(opts HubOptions) *Hub {
	if opts.IsAdmin == nil {
		opts.IsAdmin = func(string) bool { return false }
	}
	return &Hub{opts: opts, members: map[*member]struct{}{}}
}

// member is one socket of the hub: a console user's or a device's.
type member struct {
	conn *ws.Conn
	// user is the console user, nil for a device
	user *session.User
	name string
	// device is the device of a device's socket
	device string
	// what the user may: Support chat, Team chat, assign, delete anyone's
	support, team, assign, admin bool
	status                       string

	// scope remembers which devices the user sees, for a minute
	scopeMu   sync.Mutex
	scope     map[string]bool
	scopeTime time.Time
}

// party is the member as a sender.
func (m *member) party() chatstore.Party {
	if m.user == nil {
		return chatstore.Device(m.device, m.name)
	}
	return chatstore.User(m.user.ID, m.name)
}

func (h *Hub) holds(ctx context.Context, role, permission string) bool {
	if h.opts.Holds == nil {
		return true
	}
	ok, err := h.opts.Holds(ctx, role, permission)
	return err == nil && ok
}

// serveOperator runs a console user's socket of version 2 once the relay
// has made its checks (origin, session, a chat permission).
func (h *Hub) serveOperator(u *ws.Upgrade, user *session.User) {
	ctx := u.R.Context()
	m := &member{user: user, name: user.Username, status: "online"}
	if h.opts.DisplayName != nil {
		if name := h.opts.DisplayName(ctx, user.ID); name != "" {
			m.name = name
		}
	}
	m.support = h.holds(ctx, user.Role, permSupport)
	m.team = h.holds(ctx, user.Role, permTeam)
	m.assign = h.holds(ctx, user.Role, permAssign)
	m.admin = h.opts.IsAdmin(user.Role)
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxFrameBytesV2})
	if !ok {
		return
	}
	defer conn.Wait()
	m.conn = conn
	conn.SendText(frame("welcome", map[string]any{
		"v": Version2, "capabilities": capabilities2, "server_time": time.Now().UnixMilli(),
		"me":  map[string]any{"id": user.ID, "name": m.name, "username": user.Username},
		"may": map[string]bool{"support": m.support, "team": m.team, "assign": m.assign, "delete_any": m.admin},
	}))
	h.join(m)
	defer h.leave(m)
	h.run(m)
}

// serveDevice runs a device's socket of version 2 once the relay has
// checked its token.
func (h *Hub) serveDevice(u *ws.Upgrade, device, name string) {
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxFrameBytesV2})
	if !ok {
		return
	}
	defer conn.Wait()
	m := &member{conn: conn, device: device, name: name}
	ctx := conn.Context()
	conn.SendText(frame("welcome", map[string]any{
		"v": Version2, "capabilities": capabilities2, "server_time": time.Now().UnixMilli(),
		"device_id": device, "name": name,
	}))
	h.join(m)
	defer h.leave(m)
	h.sendHistory(ctx, m, device, 0, 50)
	h.sendHelper(ctx, m)
	h.run(m)
}

// run reads the member's frames until its socket ends.
func (h *Hub) run(m *member) {
	m.conn.KeepAlive(PingInterval)
	budget := &frameBudget{}
	for {
		binary, data, err := m.conn.Read()
		if err != nil {
			return
		}
		if binary {
			continue
		}
		if m.user == nil && !budget.allow(time.Now()) {
			continue
		}
		var f map[string]json.RawMessage
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		h.handle(m.conn.Context(), m, f)
	}
}

func (h *Hub) join(m *member) {
	h.mu.Lock()
	h.members[m] = struct{}{}
	h.mu.Unlock()
	if m.user != nil {
		h.presence(m.conn.Context(), m)
	} else {
		h.devicePresence(m.conn.Context(), m.device, true)
	}
}

func (h *Hub) leave(m *member) {
	h.mu.Lock()
	delete(h.members, m)
	h.mu.Unlock()
	ctx := context.Background()
	if m.user != nil {
		h.presence(ctx, m)
	} else {
		h.devicePresence(ctx, m.device, h.DeviceConnected(m.device))
	}
}

// all are the hub's members now.
func (h *Hub) all() []*member {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*member, 0, len(h.members))
	for m := range h.members {
		out = append(out, m)
	}
	return out
}

// DeviceConnected reports whether a device has a socket of version 2
// open: its chat window can show a message now.
func (h *Hub) DeviceConnected(device string) bool {
	for _, m := range h.all() {
		if m.user == nil && m.device == device {
			return true
		}
	}
	return false
}

// Online are the console accounts with a socket open, and their status:
// online, busy or away, the most present of their sockets (one tab online
// is online).
func (h *Hub) Online() map[int64]string {
	rank := map[string]int{"online": 3, "busy": 2, "away": 1}
	out := map[int64]string{}
	for _, m := range h.all() {
		if m.user == nil {
			continue
		}
		if rank[m.status] > rank[out[m.user.ID]] {
			out[m.user.ID] = m.status
		}
	}
	return out
}

// sees reports whether a user sees a device, remembered for a minute.
func (h *Hub) sees(ctx context.Context, m *member, device string) bool {
	m.scopeMu.Lock()
	defer m.scopeMu.Unlock()
	if m.scope == nil || time.Since(m.scopeTime) > time.Minute {
		m.scope, m.scopeTime = map[string]bool{}, time.Now()
	}
	if v, ok := m.scope[device]; ok {
		return v
	}
	v := h.opts.Scope.sees(ctx, m.user, device)
	m.scope[device] = v
	return v
}

// errNoAccess is a conversation the member may not read.
var errNoAccess = errors.New("chat: no access")

// mayRead reports whether a member reads a conversation: a device its
// own; a console user a device's with Support chat when it sees the
// device, a direct or group conversation with Team chat when it is in it,
// old messages of version 1 when it is an admin.
func (h *Hub) mayRead(ctx context.Context, m *member, conv string) bool {
	if m.user == nil {
		return conv == m.device
	}
	switch chatstore.KindOf(conv) {
	case chatstore.ConvSupport:
		return m.support && h.sees(ctx, m, conv)
	case chatstore.ConvDirect, chatstore.ConvGroup:
		if !m.team || h.opts.Store == nil {
			return false
		}
		in, err := h.opts.Store.IsMember(ctx, conv, m.user.ID)
		return err == nil && in
	case chatstore.ConvOld:
		// The history of version 1 that belongs to no two accounts
		return m.admin
	}
	return false
}

// MayRead is mayRead for a signed-in account outside a socket (the HTTP
// routes).
func (h *Hub) MayRead(ctx context.Context, user *session.User, conv string) bool {
	if user == nil {
		return false
	}
	m := &member{user: user, support: h.holds(ctx, user.Role, permSupport), team: h.holds(ctx, user.Role, permTeam), admin: h.opts.IsAdmin(user.Role)}
	return h.mayRead(ctx, m, conv)
}

// publish sends a frame to everyone who reads the conversation, except
// one member. A line of the system goes to console users only.
func (h *Hub) publish(ctx context.Context, conv, text string, except *member, consoleOnly bool) {
	for _, m := range h.all() {
		if m == except || (consoleOnly && m.user == nil) {
			continue
		}
		if h.mayRead(ctx, m, conv) {
			m.conn.SendText(text)
		}
	}
}

// toAccount sends a frame to every socket of an account.
func (h *Hub) toAccount(account int64, text string) {
	for _, m := range h.all() {
		if m.user != nil && m.user.ID == account {
			m.conn.SendText(text)
		}
	}
}

// Published hands a stored message to everyone who reads its
// conversation, and to the sockets of version 1 of a device's
// conversation (relay.go).
func (h *Hub) Published(ctx context.Context, msg chatstore.Message) {
	h.remember(msg.ID)
	out := messageOut(ctx, h.opts.Store, msg, nil)
	h.publish(ctx, msg.ConversationID, frame("message.new", map[string]any{"message": out}), nil, msg.From.Kind == chatstore.KindSystem)
	if h.relay != nil && chatstore.KindOf(msg.ConversationID) == chatstore.ConvSupport && msg.From.Kind != chatstore.KindSystem {
		h.relay.deliverV1(ctx, msg)
	}
}

// updated hands a changed message (edit, deletion, reactions) on.
func (h *Hub) updated(ctx context.Context, msg chatstore.Message) {
	out := messageOut(ctx, h.opts.Store, msg, nil)
	h.publish(ctx, msg.ConversationID, frame("message.updated", map[string]any{"message": out}), nil, msg.From.Kind == chatstore.KindSystem)
}

// presence tells the Team chat users an account's status: the most
// present of its sockets, offline without one.
func (h *Hub) presence(ctx context.Context, m *member) {
	status := h.Online()[m.user.ID]
	if status == "" {
		status = "offline"
	}
	text := frame("presence", map[string]any{"user_id": m.user.ID, "status": status})
	for _, o := range h.all() {
		if o.user != nil && o.team {
			o.conn.SendText(text)
		}
	}
}

// devicePresence tells the Support chat users who see a device whether
// its chat window is connected.
func (h *Hub) devicePresence(ctx context.Context, device string, connected bool) {
	text := frame("device", map[string]any{"device_id": device, "chat_connected": connected})
	for _, o := range h.all() {
		if o.user != nil && o.support && h.sees(ctx, o, device) {
			o.conn.SendText(text)
		}
	}
}

// ConversationChanged tells the console users who read a support
// conversation its new state, and the device who helps it now.
func (h *Hub) ConversationChanged(ctx context.Context, c chatstore.Conversation, assignee string) {
	h.publish(ctx, c.ID, frame("conversation.updated", map[string]any{"conversation": conversationOut(c, assignee)}), nil, true)
	for _, m := range h.all() {
		if m.user == nil && m.device == c.ID {
			h.sendHelper(ctx, m)
		}
	}
}

// wireConversation is a conversation's state on the wire.
type wireConversation struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	Title        string `json:"title,omitempty"`
	AssigneeID   int64  `json:"assignee_id,omitempty"`
	AssigneeName string `json:"assignee_name,omitempty"`
	LastMessage  string `json:"last_message_id"`
	LastAt       int64  `json:"last_message_at"`
	WaitingSince int64  `json:"waiting_since,omitempty"`
	ClosedAt     int64  `json:"closed_at,omitempty"`
	ClosedBy     string `json:"closed_by,omitempty"`
	CloseNote    string `json:"close_note,omitempty"`
	HelpRequest  int64  `json:"help_request_id,omitempty"`
	Ticket       int64  `json:"ticket_id,omitempty"`
}

func conversationOut(c chatstore.Conversation, assignee string) wireConversation {
	return wireConversation{ID: c.ID, Kind: c.Kind, State: c.State, Title: c.Title, AssigneeID: c.AssigneeID, AssigneeName: assignee,
		LastMessage: strconv.FormatInt(c.LastMessageID, 10), LastAt: c.LastMessageMs, WaitingSince: c.WaitingSinceMs,
		ClosedAt: c.ClosedMs, ClosedBy: c.ClosedBy, CloseNote: c.CloseNote, HelpRequest: c.HelpRequestID, Ticket: c.TicketID}
}

// sendHelper tells a device who helps its person: the assignee's name,
// null when nobody took the conversation, and whether it is open.
func (h *Hub) sendHelper(ctx context.Context, m *member) {
	if h.opts.Store == nil {
		return
	}
	c, err := h.opts.Store.Conversation(ctx, m.device)
	if err != nil {
		return
	}
	var name any
	if c.AssigneeID != 0 && h.opts.DisplayName != nil {
		name = h.opts.DisplayName(ctx, c.AssigneeID)
	}
	m.conn.SendText(frame("helper", map[string]any{"name": name, "state": c.State}))
}

// sendHistory sends a page of a conversation's history and its read
// marks. A device gets no lines of the system.
func (h *Hub) sendHistory(ctx context.Context, m *member, conv string, before int64, limit int) {
	if h.opts.Store == nil {
		return
	}
	list, err := h.opts.Store.History(ctx, conv, before, limit)
	if err != nil {
		logging.Logf("console", "WARN: [Chat] history of %s: %v", conv, err)
		m.conn.SendText(frame("error", map[string]any{"code": "history_failed", "conversation_id": conv}))
		return
	}
	more := len(list) >= limit
	if m.user == nil {
		kept := list[:0]
		for _, msg := range list {
			if msg.From.Kind != chatstore.KindSystem {
				kept = append(kept, msg)
			}
		}
		list = kept
	}
	marks, _ := h.opts.Store.Reads(ctx, conv)
	reads := make([]map[string]any, 0, len(marks))
	for _, r := range marks {
		reads = append(reads, map[string]any{"reader": map[string]string{"kind": r.Reader.Kind, "id": r.Reader.ID}, "up_to": strconv.FormatInt(r.LastReadID, 10)})
	}
	m.conn.SendText(frame("history", map[string]any{"conversation_id": conv, "messages": messagesOut(ctx, h.opts.Store, list),
		"reads": reads, "more": more, "before": strconv.FormatInt(before, 10)}))
}

// wantsV2 reports whether an upgrade asks for version 2 (v=2 in the
// query).
func wantsV2(u *ws.Upgrade) bool {
	v, _ := u.SearchParam("v")
	return strings.TrimSpace(v) == "2"
}

// refuseNoHub refuses an upgrade of version 2 on a relay without a hub.
func refuseNoHub(u *ws.Upgrade) { u.Refuse(http.StatusNotFound) }

// ComputersOn reports whether computers may open the chat ("Chat with
// computers", A259; on unless an admin turned it off).
func (h *Hub) ComputersOn(ctx context.Context) bool {
	if h.opts.Store == nil {
		return true
	}
	return LoadServerSettings(ctx, h.opts.Store).Computers
}

// ComputersChanged is told when "Chat with computers" changed: off closes
// the computers' open sockets, which then meet the refusal when they come
// back.
func (h *Hub) ComputersChanged(ctx context.Context) {
	if h.ComputersOn(ctx) {
		return
	}
	for _, m := range h.all() {
		if m.user == nil {
			m.conn.Close(1008, "chat_off")
		}
	}
}

// refuseChatOff answers a computer's socket of version 2 while chat with
// computers is off: 503 with the code chat_off in X-NeoDesk-Chat and the
// body, and Retry-After in seconds (PROTOCOL.md).
func refuseChatOff(u *ws.Upgrade) {
	body := `{"success":false,"error":"Chat with computers is off","code":"chat_off"}`
	u.RefuseRaw("HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\nRetry-After: 600\r\nX-NeoDesk-Chat: chat_off\r\n\r\n" + body)
}
