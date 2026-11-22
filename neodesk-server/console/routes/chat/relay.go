package chat

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// The limits of the relay.
const (
	// MaxTextBytes is the largest message a socket of the relay takes: one
	// that sends a bigger one is closed with 1009. A binary message is left
	// alone and the socket stays open.
	MaxTextBytes = 8192
	// PingInterval is how often a socket is pinged.
	PingInterval = 30 * time.Second
	// HistoryLimit is how many messages a room keeps in memory, for when
	// the API server cannot give the history.
	HistoryLimit = 500
	// maxText is how many UTF-16 units of a message's text are kept.
	maxText = 2048
	// taskQueue is how many calls to the API server a socket may have
	// waiting; a socket with more is not read until there is room.
	taskQueue = 256
)

// panelID is the room of the standalone chat inbox: its operators get the
// messages of every device.
const panelID = "panel"

// RelayOptions configure the chat relay.
type RelayOptions struct {
	// API keeps the messages, contacts and groups.
	API *goapi.Client
	// VerifyDevice checks the token of a device's socket
	// (verifyDeviceWsAuth; authn.Service.VerifyDeviceWSAuth). An error
	// refuses the upgrade with 500.
	VerifyDevice func(ctx context.Context, deviceID, token string) (bool, error)
	// Now is Date.now() for message ids and times. Default time.Now.
	Now func() time.Time
	// PingInterval overrides PingInterval (tests).
	PingInterval time.Duration
	// Scope is the device scope of the accounts (ScopeOf).
	Scope Scope
	// FilesDir is the folder of the chat files (FilesDir), where a file
	// an operator shares is linked to the conversation. "" links nothing.
	FilesDir string
}

// Relay is the chat between devices and operators
// (services/chatRelay.js of the old console): /ws/chat/<device> for the
// device's agent, /ws/chat-operator/<device> for operators in that
// device's room, /ws/chat-operator/panel for the inbox. Rooms live in
// this process only, while a socket is in them; the API server keeps the
// messages. Every socket is pinged every PingInterval and closed when its
// client has not answered for two intervals; one that is just quiet stays
// open.
type Relay struct {
	opts RelayOptions
	// operators counts the open operator sockets per account
	operators openSockets[int64]
	// agentTokens counts the open agent sockets per token (its hash)
	agentTokens openSockets[string]

	mu    sync.Mutex
	rooms map[string]*room
	// order is the ids of the rooms in the order they were made
	order []string
	// panel are the inbox's operators, in the order they came
	panel []*client
}

// room is one device's conversation: its agent, its operators in the
// order they came, and the last HistoryLimit messages as JSON.
type room struct {
	agent     *ws.Conn
	operators []*ws.Conn
	messages  [][]byte
}

// NewRelay returns the relay. Register adds its sockets.
func NewRelay(opts RelayOptions) *Relay {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = PingInterval
	}
	return &Relay{opts: opts, rooms: map[string]*room{}}
}

// Register adds /ws/chat/<device> and /ws/chat-operator/<device>. The id
// is the path segment as it was sent, still percent-encoded.
func (r *Relay) Register(reg *ws.Registry) {
	reg.Handle("/ws/chat/{id}", r.serveAgent)
	reg.Handle("/ws/chat-operator/{id}", r.serveOperator)
}

// nowMillis is Date.now().
func (r *Relay) nowMillis() float64 { return float64(r.opts.Now().UnixMilli()) }

// serveAgent is a device's socket. Before the handshake: a foreign origin
// 403, the inbox's id "panel" 403 (whose contacts are every device, and
// whose room is the inbox's), then the device token (the query's token,
// else a bearer header) checked for the device in the path: refused 403,
// a failed check 500. A second socket of the device replaces the first,
// which is closed with 1001 "New agent connected". MaxAgentSocketsPerToken
// sockets of the token open answer 429. The socket's messages are held to
// AgentFrameBurst at once and AgentFramesPerSecond after that.
func (r *Relay) serveAgent(u *ws.Upgrade) {
	if !u.CheckOrigin("chat-agent " + u.Path) {
		return
	}
	deviceID := u.Param("id")
	if deviceID == panelID {
		u.Refuse(http.StatusForbidden)
		return
	}
	token, _ := u.SearchParam("token")
	if token == "" {
		token = bearerToken(u.R.Header.Get("Authorization"))
	}
	ok, err := r.opts.VerifyDevice(u.R.Context(), deviceID, token)
	if err != nil {
		u.Refuse(http.StatusInternalServerError)
		return
	}
	if !ok {
		u.Refuse(http.StatusForbidden)
		return
	}
	key := tokenKey(token)
	if !r.agentTokens.take(key, MaxAgentSocketsPerToken) {
		u.Refuse(http.StatusTooManyRequests)
		return
	}
	defer r.agentTokens.release(key)
	conn, answers, accepted := accept(u)
	if !accepted {
		return
	}
	defer conn.Wait()
	c := r.newClient(u, conn, answers, deviceID, "")
	c.budget = &frameBudget{}
	c.name = r.deviceName(u.R.Context(), deviceID)
	r.agentJoins(c)
	c.run(r.agentFrame)
	r.agentLeaves(c)
	c.wait()
}

// deviceName is the host name of the device with the id, the id when
// there is none.
func (r *Relay) deviceName(ctx context.Context, id string) string {
	if r.opts.Scope.Device == nil {
		return id
	}
	if device := r.opts.Scope.Device(ctx, id); device != nil {
		if name, _ := device.Get("hostname").(string); strings.TrimSpace(name) != "" {
			return name
		}
	}
	return id
}

// serveOperator is a console user's socket. Before the handshake: a
// foreign origin 403, no signed-in session 401, the role without
// chat.access (or its lookup failing) 403, a room that is no device in
// the account's device scope 404 (the inbox is open to every account),
// MaxOperatorSockets of the account open 429. The operator's name is the
// session's user name, "operator" without one.
func (r *Relay) serveOperator(u *ws.Upgrade) {
	if !u.CheckOrigin("chat-operator " + u.Path) {
		return
	}
	session := u.Session()
	if !session.SignedIn() {
		u.Refuse(http.StatusUnauthorized)
		return
	}
	allowed, err := u.RoleHasPermission(session.UserRole(), "chat.access")
	if err != nil || !allowed {
		u.Refuse(http.StatusForbidden)
		return
	}
	if id := u.Param("id"); id != panelID && !r.opts.Scope.room(u.R.Context(), session.User(), id) {
		u.Refuse(http.StatusNotFound)
		return
	}
	account := session.UserID()
	if !r.operators.take(account, MaxOperatorSockets) {
		u.Refuse(http.StatusTooManyRequests)
		return
	}
	defer r.operators.release(account)
	conn, answers, accepted := accept(u)
	if !accepted {
		return
	}
	defer conn.Wait()
	name := session.Username()
	if name == "" {
		name = "operator"
	}
	c := r.newClient(u, conn, answers, u.Param("id"), name)
	c.user = session.User()
	r.operatorJoins(c)
	c.run(r.operatorFrame)
	r.operatorLeaves(c)
	c.wait()
}

// accept answers the handshake of an upgrade whose client's answers to
// pings are noted.
func accept(u *ws.Upgrade) (*ws.Conn, *heard, bool) {
	answers := &heard{}
	answers.listen(u)
	conn, ok := u.Accept(ws.AcceptOptions{MaxPayload: MaxTextBytes})
	return conn, answers, ok
}

// client is one open socket of the relay.
type client struct {
	relay *Relay
	conn  *ws.Conn
	// answers notes when the client last sent anything
	answers *heard
	// id is the device id of the path: the room
	id string
	// operator is the operator's name, "" for an agent
	operator string
	// name is an agent's name in its messages: its device's host name,
	// else its id
	name string
	// user is the operator's account, nil for an agent
	user *session.User
	// budget holds an agent's messages to their rate, nil for an operator
	budget *frameBudget
	// api is the context of the socket's calls to the API server: it ends
	// with the console, not with the socket, so a message sent just
	// before the socket closed is still stored
	api   context.Context
	tasks chan func(ctx context.Context)
	// done is closed when the last call has returned
	done chan struct{}
}

func (r *Relay) newClient(u *ws.Upgrade, conn *ws.Conn, answers *heard, id, operator string) *client {
	c := &client{relay: r, conn: conn, answers: answers, id: id, operator: operator, api: u.Context(),
		tasks: make(chan func(context.Context), taskQueue), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		for task := range c.tasks {
			task(c.api)
		}
	}()
	return c
}

// errDrop is a message that ended the old console's process (a member of
// null, String() of an object with its own toString). This console drops
// the socket's connection instead (DECISIONS A19).
var errDrop = errors.New("chat: message would have ended the process")

// later queues a call to the API server and what follows from its answer.
// The calls of a socket run one after the other, in the order its
// messages came.
func (c *client) later(task func(ctx context.Context)) {
	c.tasks <- task
}

// run reads the socket's messages until it ends and hands each to handle,
// while its calls to the API server run in order beside it. It pings the
// socket and closes it when the client stops answering.
func (c *client) run(handle func(*client, jsjson.Value) error) {
	c.conn.KeepAlive(c.relay.opts.PingInterval)
	defer c.answers.closeSilent(c.conn, 2*c.relay.opts.PingInterval)()
	for {
		binary, data, err := c.conn.Read()
		if err != nil {
			break
		}
		if binary {
			continue
		}
		if c.budget != nil && !c.budget.allow(c.relay.opts.Now()) {
			if c.budget.dropped == 1 {
				logging.Logf("console", "WARN: [Chat] agent %s sends more messages than %d at once and %d a second: the relay drops the rest", c.id, AgentFrameBurst, AgentFramesPerSecond)
			}
			continue
		}
		frame, err := jsjson.Parse(string(data))
		if err != nil {
			continue
		}
		if err := handle(c, frame); err != nil {
			logging.Logf("console", "WARN: [Chat] message from %s dropped the connection: %v", c.id, err)
			c.conn.CloseNow()
			break
		}
	}
}

// wait lets the calls the socket started finish: their answers go
// nowhere, but what they store is stored.
func (c *client) wait() {
	close(c.tasks)
	<-c.done
}

// send writes a message to the socket when it is open (sendTo).
func (c *client) send(o *object) { c.conn.SendText(o.text()) }

// --- Rooms ---

// roomLocked is getRoom: the room of id, made when there is none.
func (r *Relay) roomLocked(id string) *room {
	rm, ok := r.rooms[id]
	if !ok {
		rm = &room{}
		r.rooms[id] = rm
		r.order = append(r.order, id)
	}
	return rm
}

// findLocked is rooms.get(id): the room of a conversation id that is
// text, nil for any other value or an id without a room.
func (r *Relay) findLocked(id jsjson.Value) *room {
	if id.Kind() != jsjson.String {
		return nil
	}
	return r.rooms[id.Str()]
}

// appendLocked keeps a message in the room's memory.
func (rm *room) appendLocked(msg []byte) {
	rm.messages = append(rm.messages, msg)
	if over := len(rm.messages) - HistoryLimit; over > 0 {
		rm.messages = append([][]byte(nil), rm.messages[over:]...)
	}
}

// broadcastLocked sends text to the room's agent and operators, except
// to one socket.
func (rm *room) broadcastLocked(text string, except *ws.Conn) {
	if rm.agent != nil && rm.agent != except {
		rm.agent.SendText(text)
	}
	for _, c := range rm.operators {
		if c != except {
			c.SendText(text)
		}
	}
}

func (rm *room) hasOperator(c *ws.Conn) bool {
	for _, op := range rm.operators {
		if op == c {
			return true
		}
	}
	return false
}

// toPanel sends texts to the inbox's operators that see subject (the id
// of the device or conversation they are about; "" is no device's),
// except to one socket and to those in rm, which get them through their
// room. The scope is checked outside the relay's lock.
func (r *Relay) toPanel(ctx context.Context, subject string, except *ws.Conn, rm *room, texts ...string) {
	r.mu.Lock()
	var targets []*client
	for _, op := range r.panel {
		if op.conn == except || (rm != nil && rm.hasOperator(op.conn)) {
			continue
		}
		targets = append(targets, op)
	}
	r.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	device := r.opts.Scope.device(ctx, subject)
	for _, op := range targets {
		if !r.opts.Scope.reaches(ctx, op.user, device) {
			continue
		}
		for _, text := range texts {
			op.conn.SendText(text)
		}
	}
}

// fallbackContacts is the contact list when the API server gives none:
// the support operator and every other device whose agent is connected,
// without those the view hides.
func (r *Relay) fallbackContacts(current string, v *view) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	contacts := [][]byte{supportContact(r.nowMillis())}
	for _, id := range r.order {
		rm := r.rooms[id]
		if id == current || rm.agent == nil || !rm.agent.Open() || v.hides(id) {
			continue
		}
		contacts = append(contacts, newObject().str("id", id).str("name", id).str("hostname", "").
			boolean("online", true).num("last_seen", r.nowMillis()).num("unread", 0).str("avatar_color", "").json())
	}
	return array(contacts)
}

// supportContact is the contact an agent always has.
func supportContact(now float64) []byte {
	return newObject().str("id", "operator").str("name", "Support").str("hostname", "").boolean("online", true).
		num("last_seen", now).num("unread", 0).str("avatar_color", "#4f6ef7").str("role", "operator").json()
}

// --- Joining and leaving ---

// agentJoins puts the agent into its room in place of an earlier one,
// tells the room and the inbox, and loads its history, contacts and
// groups.
func (r *Relay) agentJoins(c *client) {
	rm := r.agentEnters(c)
	r.toPanel(c.api, c.id, nil, rm,
		newObject().str("type", "status").str("device_id", c.id).boolean("agent_connected", true).text(),
		newObject().str("type", "presence").str("device_id", c.id).boolean("online", true).text())

	id := jsjson.OfText(c.id)
	c.later(func(ctx context.Context) {
		data, ok := loadHistory(ctx, r.opts.API, id)
		r.sendHistory(ctx, c, data, ok, id)
	})
	c.later(func(ctx context.Context) {
		data, ok := loadContacts(ctx, r.opts.API, id)
		contacts := data.Get("contacts")
		if ok && data.Truthy() && contacts.Truthy() && positive(contacts.Get("length")) {
			c.send(newObject().str("type", "contacts").val("contacts", contacts))
			return
		}
		c.send(newObject().str("type", "contacts").raw("contacts", array([][]byte{supportContact(r.nowMillis())})))
	})
	c.later(func(ctx context.Context) {
		data, ok := loadGroups(ctx, r.opts.API, id)
		if ok && data.Truthy() && data.Get("groups").Truthy() {
			c.send(newObject().str("type", "groups").val("groups", data.Get("groups")))
		}
	})
}

// agentEnters makes the agent its room's, closing an earlier one,
// and tells the room. It takes the relay's lock.
func (r *Relay) agentEnters(c *client) *room {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm := r.roomLocked(c.id)
	if rm.agent != nil {
		rm.agent.Close(1001, "New agent connected")
	}
	rm.agent = c.conn
	logging.Logf("console", "[Chat] Agent connected: %s", c.id)
	rm.broadcastLocked(newObject().str("type", "status").boolean("agent_connected", true).text(), c.conn)
	rm.broadcastLocked(newObject().str("type", "presence").str("device_id", c.id).boolean("online", true).text(), c.conn)
	return rm
}

// sendHistory sends a room's history: the API server's messages for the
// conversation, else the messages the room keeps in memory (without a
// conversation id). An operator gets the messages of devices it sees.
func (r *Relay) sendHistory(ctx context.Context, c *client, data jsjson.Value, ok bool, conversation jsjson.Value) {
	messages := data.Get("messages")
	if ok && data.Truthy() && messages.Truthy() {
		c.send(newObject().str("type", "history").val("conversation_id", conversation).raw("messages", r.operatorView(ctx, c).messages(messages)))
		return
	}
	c.send(newObject().str("type", "history").raw("messages", r.memory(c.id)))
}

// memory is the messages a room keeps, as a JSON array; none when the
// room is gone.
func (r *Relay) memory(id string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rm := r.rooms[id]; rm != nil {
		return array(rm.messages)
	}
	return array(nil)
}

// agentLeaves takes the agent out of its room, unless another one has
// replaced it, and tells the room and the inbox. A replaced agent may end
// after its successor has left and the room is gone.
func (r *Relay) agentLeaves(c *client) {
	rm := r.agentExits(c)
	if rm == nil {
		return
	}
	r.toPanel(c.api, c.id, nil, rm,
		newObject().str("type", "status").str("device_id", c.id).boolean("agent_connected", false).text(),
		newObject().str("type", "presence").str("device_id", c.id).boolean("online", false).text())
}

// agentExits takes the agent out of its room and tells the room. It
// returns the room, nil when the agent was no longer the room's. It takes
// the relay's lock.
func (r *Relay) agentExits(c *client) *room {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm := r.rooms[c.id]
	if rm == nil || rm.agent != c.conn {
		return nil
	}
	rm.agent = nil
	logging.Logf("console", "[Chat] Agent disconnected: %s", c.id)
	rm.broadcastLocked(newObject().str("type", "status").boolean("agent_connected", false).text(), nil)
	rm.broadcastLocked(newObject().str("type", "presence").str("device_id", c.id).boolean("online", false).text(), nil)
	r.dropIfEmptyLocked(c.id, rm)
	return rm
}

// operatorJoins puts the operator into its room (and the inbox for the
// panel), tells it whether the room's agent is there, and loads the
// panel's contacts and groups, or the room's history.
func (r *Relay) operatorJoins(c *client) {
	connected := r.operatorEnters(c)
	logging.Logf("console", "[Chat] Operator %s connected to %s", c.operator, c.id)
	c.send(newObject().str("type", "status").boolean("agent_connected", connected))

	id := jsjson.OfText(c.id)
	if c.id == panelID {
		c.later(func(ctx context.Context) {
			data, ok := loadContacts(ctx, r.opts.API, id)
			r.sendContacts(ctx, c, data, ok)
		})
		c.later(func(ctx context.Context) {
			data, ok := loadGroups(ctx, r.opts.API, id)
			r.sendGroups(c, data, ok)
		})
		return
	}
	c.later(func(ctx context.Context) {
		data, ok := loadHistory(ctx, r.opts.API, id)
		r.sendHistory(ctx, c, data, ok, id)
	})
}

// operatorEnters puts the operator into its room and the inbox and
// reports whether the room's agent is there. It takes the relay's lock.
func (r *Relay) operatorEnters(c *client) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm := r.roomLocked(c.id)
	rm.operators = append(rm.operators, c.conn)
	if c.id == panelID {
		r.panel = append(r.panel, c)
	}
	return rm.agent != nil && rm.agent.Open()
}

// sendContacts sends the API server's contacts when there are some, else
// the fallback list (an operator's get_contacts and the panel), without
// the devices the operator does not see.
func (r *Relay) sendContacts(ctx context.Context, c *client, data jsjson.Value, ok bool) {
	v := r.operatorView(ctx, c)
	contacts := data.Get("contacts")
	if ok && contacts.Get("length").Truthy() {
		c.send(newObject().str("type", "contacts").raw("contacts", v.contacts(contacts)))
		return
	}
	c.send(newObject().str("type", "contacts").raw("contacts", r.fallbackContacts(c.id, v)))
}

// operatorView is what an operator sees of the devices, nil for an agent.
func (r *Relay) operatorView(ctx context.Context, c *client) *view {
	if c.operator == "" {
		return nil
	}
	return r.opts.Scope.viewOf(ctx, c.user)
}

// sees reports whether an operator sees the conversation or device id a
// frame names: its text (String()), which cannot be one it does not see.
func (r *Relay) sees(c *client, id jsjson.Value) bool {
	if r.opts.Scope.Of == nil {
		return true
	}
	text, err := id.Text()
	return err == nil && r.opts.Scope.sees(c.conn.Context(), c.user, text)
}

// sendGroups sends the groups when the answer has some.
func (r *Relay) sendGroups(c *client, data jsjson.Value, ok bool) {
	if ok && data.Get("groups").Truthy() {
		c.send(newObject().str("type", "groups").val("groups", data.Get("groups")))
	}
}

// operatorLeaves takes the operator out of its room and the inbox.
func (r *Relay) operatorLeaves(c *client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rm := r.rooms[c.id]; rm != nil {
		rm.operators = without(rm.operators, c.conn)
		r.dropIfEmptyLocked(c.id, rm)
	}
	panel := r.panel[:0:0]
	for _, op := range r.panel {
		if op != c {
			panel = append(panel, op)
		}
	}
	r.panel = panel
	logging.Logf("console", "[Chat] Operator %s left %s", c.operator, c.id)
}

// dropIfEmptyLocked removes a room nobody is in any more, with the
// messages it kept in memory.
func (r *Relay) dropIfEmptyLocked(id string, rm *room) {
	if rm.agent != nil || len(rm.operators) > 0 {
		return
	}
	delete(r.rooms, id)
	for i, known := range r.order {
		if known == id {
			r.order = append(r.order[:i:i], r.order[i+1:]...)
			break
		}
	}
}

func without(list []*ws.Conn, c *ws.Conn) []*ws.Conn {
	out := list[:0:0]
	for _, x := range list {
		if x != c {
			out = append(out, x)
		}
	}
	return out
}

// positive is v > 0 for a length: a number, or what a text or an array
// spells.
func positive(v jsjson.Value) bool {
	n, err := v.Num()
	return err == nil && n > 0
}
