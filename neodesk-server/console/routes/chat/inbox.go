package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// The HTTP routes of the chat of version 2 (A242): the support inbox and
// its actions, the team's conversations and people, canned replies, the
// settings, and the trail of a message. The sockets carry the messages
// themselves (hub.go).

type inboxRoutes struct {
	d   *kit.Deps
	hub *Hub
}

// registerInbox adds the routes. Support routes are in the panel section
// "chat" (the guard's rule for /api/chat), Team chat's in "team-chat";
// what both use declares no section and asks for either permission.
func registerInbox(d *kit.Deps, hub *Hub) {
	h := &inboxRoutes{d: d, hub: hub}
	support := d.RequirePermission(permSupport)
	team := d.RequirePermission(permTeam)
	either := d.RequireAnyPermission(ChatPermissions...)
	r := d.Router
	r.Get("/api/chat/inbox", h.inbox, d.RequireAuth, support)
	r.Get("/api/chat/devices", h.devices, d.RequireAuth, support)
	r.Post("/api/chat/conversations/{id}/assign", h.assign, d.RequireAuth, support)
	r.Post("/api/chat/conversations/{id}/close", h.close, d.RequireAuth, support)
	r.Post("/api/chat/conversations/{id}/reopen", h.reopen, d.RequireAuth, support)
	r.Post("/api/chat/conversations/{id}/link", h.link, d.RequireAuth, support)

	teamRouter := r.Section("team-chat")
	teamRouter.Get("/api/chat/team", h.teamList, d.RequireAuth, team)
	teamRouter.Post("/api/chat/team/direct", h.direct, d.RequireAuth, team)
	teamRouter.Post("/api/chat/team/groups", h.newGroup, d.RequireAuth, team)
	teamRouter.Patch("/api/chat/team/groups/{id}", h.editGroup, d.RequireAuth, team)

	shared := r.NoSection()
	shared.Get("/api/chat/summary", h.summary, d.RequireAuth, either)
	shared.Get("/api/chat/conversations/{id}", h.conversation, d.RequireAuth, either)
	shared.Get("/api/chat/canned", h.cannedList, d.RequireAuth, either)
	shared.Post("/api/chat/canned", h.cannedAdd, d.RequireAuth, either)
	shared.Patch("/api/chat/canned/{id}", h.cannedEdit, d.RequireAuth, either)
	shared.Delete("/api/chat/canned/{id}", h.cannedDelete, d.RequireAuth, either)
	shared.Get("/api/chat/me", h.mySettings, d.RequireAuth, either)
	shared.Put("/api/chat/me", h.setMySettings, d.RequireAuth, either)
	shared.Get("/api/chat/settings", h.settings, d.RequireAuth, either)
	shared.Put("/api/chat/settings", h.setSettings, d.RequireAuth, d.RequirePermission("server.config"))
	shared.Get("/api/chat/messages/{id}/trail", h.trail, d.RequireAuth, d.RequirePermission("audit.view"))
}

func (h *inboxRoutes) store() *chatstore.Store { return h.d.Chat }

func (h *inboxRoutes) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, chatstore.ErrNotFound) {
		kit.Fail(w, r, http.StatusNotFound, "Conversation not found")
		return
	}
	if h.d.WebUI == nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Internal Server Error")
		return
	}
	h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
}

// --- People and devices ---

// wirePerson is a console user as the chat shows one.
type wirePerson struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Status   string `json:"status"`
}

// people are the console accounts, by id, with their names and status.
func (h *inboxRoutes) people(ctx context.Context) (map[int64]wirePerson, error) {
	rows, err := h.d.Store.Query(ctx, `SELECT id, username, role, first_name, last_name FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	online := h.hub.Online()
	out := map[int64]wirePerson{}
	for rows.Next() {
		var p wirePerson
		var role, first, last sql.NullString
		if err := rows.Scan(&p.ID, &p.Username, &role, &first, &last); err != nil {
			return nil, err
		}
		p.Role = role.String
		p.Name = strings.TrimSpace(strings.TrimSpace(first.String) + " " + strings.TrimSpace(last.String))
		if p.Name == "" {
			p.Name = p.Username
		}
		p.Status = online[p.ID]
		if p.Status == "" {
			p.Status = "offline"
		}
		out[p.ID] = p
	}
	return out, rows.Err()
}

// wireDevice is a computer as the chat shows one: its name, what it is,
// whose and where, and whether its person can be reached.
type wireDevice struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Hostname      string `json:"hostname"`
	Owner         string `json:"owner"`
	Folder        string `json:"folder"`
	Platform      string `json:"platform"`
	Online        bool   `json:"online"`
	ChatConnected bool   `json:"chat_connected"`
	AgentChat     bool   `json:"agent_chat"`
}

// devicesSeen are the devices the account sees, by id.
func (h *inboxRoutes) devicesSeen(ctx context.Context, user *session.User) map[string]wireDevice {
	scope := h.hub.opts.Scope
	var all []*goapi.Object
	if scope.Devices != nil {
		all = scope.Devices(ctx)
	}
	folders := map[string]string{}
	if rows, err := h.d.Store.Query(ctx, `SELECT id, name FROM folders`); err == nil {
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) == nil {
				folders[strconv.FormatInt(id, 10)] = name
			}
		}
		rows.Close()
	}
	view := scope.viewOf(ctx, user)
	out := map[string]wireDevice{}
	for _, o := range all {
		id, _ := o.Get("id").(string)
		if id == "" || view.hides(id) || o.Bool("soft_deleted") || strings.HasPrefix(id, "rt_") {
			continue
		}
		out[id] = deviceOut(o, folders, h.hub)
	}
	return out
}

func deviceOut(o *goapi.Object, folders map[string]string, hub *Hub) wireDevice {
	text := func(key string) string { s, _ := o.Get(key).(string); return strings.TrimSpace(s) }
	id := text("id")
	d := wireDevice{ID: id, Hostname: text("hostname"), Owner: text("username"), Platform: text("platform"),
		Online: o.Bool("online"), AgentChat: o.Bool("cdap_connected")}
	d.Name = text("display_name")
	if d.Name == "" {
		d.Name = d.Hostname
	}
	if d.Name == "" {
		d.Name = id
	}
	switch f := o.Get("folder_id").(type) {
	case float64:
		d.Folder = folders[strconv.FormatInt(int64(f), 10)]
	case string:
		d.Folder = folders[f]
	}
	if hub != nil {
		d.ChatConnected = hub.DeviceConnected(id)
	}
	return d
}

// --- Support inbox ---

// wireLast is the newest message of a conversation, for its row.
type wireLast struct {
	Text     string `json:"text"`
	FromKind string `json:"from_kind"`
	FromName string `json:"from_name"`
	At       int64  `json:"at"`
	Deleted  bool   `json:"deleted,omitempty"`
	Files    int    `json:"files,omitempty"`
	Event    string `json:"event,omitempty"`
}

// wireInboxItem is a row of the inbox.
type wireInboxItem struct {
	wireConversation
	Device wireDevice `json:"device"`
	Last   *wireLast  `json:"last_message"`
	Unread int        `json:"unread"`
}

func (h *inboxRoutes) last(ctx context.Context, c chatstore.Conversation) *wireLast {
	if c.LastMessageID == 0 {
		return nil
	}
	m, err := h.store().Get(ctx, c.LastMessageID)
	if err != nil {
		return nil
	}
	out := &wireLast{Text: cutChars(m.Text, 160), FromKind: m.From.Kind, FromName: m.From.Name, At: m.CreatedMs,
		Deleted: m.Deleted(), Files: len(m.Attachments)}
	if m.From.Kind == chatstore.KindSystem {
		out.Text, out.Event = "", m.Text
	}
	return out
}

// inbox answers GET /api/chat/inbox?filter=open|mine|closed&q=: the
// support conversations of the devices the account sees, newest first,
// with the counts of the three filters. Open lists the unassigned ones
// first. q finds by the device's name, id, host name, owner and folder.
func (h *inboxRoutes) inbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := kit.User(r)
	list, err := h.store().List(ctx, chatstore.Filter{Kinds: []string{chatstore.ConvSupport}, WithMessages: true, Limit: 2000})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	devices := h.devicesSeen(ctx, user)
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	filter := r.URL.Query().Get("filter")
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	counts := map[string]int{"open": 0, "mine": 0, "closed": 0, "unassigned": 0}
	var kept []chatstore.Conversation
	var ids []string
	for _, c := range list {
		dev, ok := devices[c.ID]
		if !ok {
			continue
		}
		if q != "" && !matchesDevice(dev, q) {
			continue
		}
		open := c.State == chatstore.StateOpen
		mine := open && c.AssigneeID == user.ID
		switch {
		case open:
			counts["open"]++
			if c.AssigneeID == 0 {
				counts["unassigned"]++
			}
		default:
			counts["closed"]++
		}
		if mine {
			counts["mine"]++
		}
		if filter == "mine" && !mine || filter == "closed" && open || (filter == "" || filter == "open") && !open {
			continue
		}
		kept = append(kept, c)
		ids = append(ids, c.ID)
	}
	unread := map[string]int{}
	if len(ids) > 0 {
		if unread, err = h.store().Unread(ctx, chatstore.User(user.ID, user.Username), ids); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if filter == "" || filter == "open" {
		// Waiting for somebody first, then the newest
		sort.SliceStable(kept, func(i, j int) bool {
			return kept[i].AssigneeID == 0 && kept[j].AssigneeID != 0
		})
	}
	out := make([]wireInboxItem, 0, len(kept))
	for _, c := range kept {
		item := wireInboxItem{wireConversation: conversationOut(c, people[c.AssigneeID].Name), Device: devices[c.ID],
			Last: h.last(ctx, c), Unread: unread[c.ID]}
		out = append(out, item)
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversations": out, "counts": counts})
}

func matchesDevice(d wireDevice, q string) bool {
	for _, s := range []string{d.Name, d.ID, d.Hostname, d.Owner, d.Folder} {
		if strings.Contains(strings.ToLower(s), q) {
			return true
		}
	}
	return false
}

// devices answers GET /api/chat/devices?q=: up to 20 devices the account
// sees whose name, id, host name, owner or folder has q, to start a
// conversation with; online first.
func (h *inboxRoutes) devices(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	var out []wireDevice
	for _, d := range h.devicesSeen(r.Context(), kit.User(r)) {
		if q == "" || matchesDevice(d, q) {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if len(out) > 20 {
		out = out[:20]
	}
	if out == nil {
		out = []wireDevice{}
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "devices": out})
}

// conversation answers GET /api/chat/conversations/{id}: a conversation
// the account reads, with its device or its people.
func (h *inboxRoutes) conversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	user := kit.User(r)
	if !h.hub.MayRead(ctx, user, id) {
		kit.Fail(w, r, http.StatusNotFound, "Conversation not found")
		return
	}
	c, err := h.store().Conversation(ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := map[string]any{"success": true, "conversation": conversationOut(c, people[c.AssigneeID].Name)}
	if c.Kind == chatstore.ConvSupport {
		if dev, ok := h.devicesSeen(ctx, user)[id]; ok {
			out["device"] = dev
		}
		// Who the conversation can be handed to: Support chat users who
		// see the computer
		assignable := []wirePerson{}
		for _, p := range people {
			if h.hub.MayRead(ctx, &session.User{ID: p.ID, Username: p.Username, Role: p.Role}, id) {
				assignable = append(assignable, p)
			}
		}
		sort.Slice(assignable, func(i, j int) bool { return strings.ToLower(assignable[i].Name) < strings.ToLower(assignable[j].Name) })
		out["assignable"] = assignable
	} else {
		members, err := h.store().Members(ctx, id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		list := []wirePerson{}
		for _, m := range members {
			if p, ok := people[m]; ok {
				list = append(list, p)
			}
		}
		out["people"] = list
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// supportConversation is the support conversation of the path, one the
// account reads; it answers 404 otherwise.
func (h *inboxRoutes) supportConversation(w http.ResponseWriter, r *http.Request) (chatstore.Conversation, bool) {
	id := r.PathValue("id")
	if chatstore.KindOf(id) != chatstore.ConvSupport || !h.hub.MayRead(r.Context(), kit.User(r), id) {
		kit.Fail(w, r, http.StatusNotFound, "Conversation not found")
		return chatstore.Conversation{}, false
	}
	c, err := h.store().Conversation(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return c, false
	}
	return c, true
}

// systemLine adds a line of the system to a conversation and hands it on.
func (h *inboxRoutes) systemLine(ctx context.Context, conv string, event map[string]any) {
	b, _ := json.Marshal(event)
	msg, err := h.store().Post(ctx, chatstore.Message{ConversationID: conv, From: chatstore.System(), Text: string(b)}, nil)
	if err == nil {
		h.hub.Published(ctx, msg)
	}
}

// changed tells everyone of the conversation its new state.
func (h *inboxRoutes) changed(ctx context.Context, c chatstore.Conversation) {
	name := ""
	if c.AssigneeID != 0 {
		if people, err := h.people(ctx); err == nil {
			name = people[c.AssigneeID].Name
		}
	}
	h.hub.ConversationChanged(ctx, c, name)
}

// assign answers POST /api/chat/conversations/{id}/assign {account_id}:
// 0 or no account gives it to nobody. It needs chat.assign; the new
// assignee must read the conversation. The one who had it learns that
// (A242 b), and the thread gets a line.
func (h *inboxRoutes) assign(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !kit.Access(r).HasPermission(permAssign) {
		kit.Fail(w, r, http.StatusForbidden, "Permission denied: "+permAssign)
		return
	}
	c, ok := h.supportConversation(w, r)
	if !ok {
		return
	}
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "account_id must be a number")
		return
	}
	user := kit.User(r)
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if body.AccountID != 0 {
		p, known := people[body.AccountID]
		if !known || !h.hub.MayRead(ctx, &session.User{ID: p.ID, Username: p.Username, Role: p.Role}, c.ID) {
			kit.Fail(w, r, http.StatusBadRequest, "That person cannot see this computer")
			return
		}
	}
	before := c.AssigneeID
	if before == body.AccountID {
		kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, people[c.AssigneeID].Name)})
		return
	}
	c, err = h.store().Assign(ctx, c.ID, body.AccountID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	event := map[string]any{"event": "assigned", "by": people[user.ID].Name, "to": people[body.AccountID].Name}
	if body.AccountID == 0 {
		event["event"] = "unassigned"
	} else if body.AccountID == user.ID {
		event["event"] = "taken"
	}
	h.systemLine(ctx, c.ID, event)
	if before != 0 && before != user.ID {
		h.hub.toAccount(before, frame("notice", map[string]any{"code": "taken_over", "conversation_id": c.ID, "by": people[user.ID].Name}))
	}
	h.changed(ctx, c)
	h.hub.audit(ctx, &member{user: user}, "chat_assigned", map[string]any{"conversation_id": c.ID, "to": body.AccountID, "from": before})
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, people[c.AssigneeID].Name)})
}

// close answers POST /api/chat/conversations/{id}/close {note,
// resolve_help_request}: the assignee or anyone with chat.assign closes
// it, and with resolve_help_request the device's open help request is
// resolved too (A242 h).
func (h *inboxRoutes) close(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := h.supportConversation(w, r)
	if !ok {
		return
	}
	user := kit.User(r)
	if c.AssigneeID != user.ID && !kit.Access(r).HasPermission(permAssign) {
		kit.Fail(w, r, http.StatusForbidden, "Permission denied: "+permAssign)
		return
	}
	var body struct {
		Note               string `json:"note"`
		ResolveHelpRequest bool   `json:"resolve_help_request"`
	}
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	people, _ := h.people(ctx)
	name := people[user.ID].Name
	c, err := h.store().Close(ctx, c.ID, name, body.Note)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resolved := 0
	if body.ResolveHelpRequest {
		resolved = h.resolveHelpRequests(ctx, c.ID)
	}
	h.systemLine(ctx, c.ID, map[string]any{"event": "closed", "by": name, "note": c.CloseNote})
	h.changed(ctx, c)
	h.hub.audit(ctx, &member{user: user}, "chat_closed", map[string]any{"conversation_id": c.ID, "help_requests_resolved": resolved})
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, people[c.AssigneeID].Name), "help_requests_resolved": resolved})
}

// resolveHelpRequests resolves the device's open help requests through
// the API server and returns how many.
func (h *inboxRoutes) resolveHelpRequests(ctx context.Context, device string) int {
	n := 0
	for _, status := range []string{"pending", "acknowledged"} {
		resp, err := h.d.GoAPI.Get(ctx, "/help/requests", goapi.Query("device_id", device), goapi.Query("status", status))
		if err != nil {
			continue
		}
		list, _ := goapi.Dig(resp.Data(), "requests").([]any)
		for _, item := range list {
			o, _ := item.(*goapi.Object)
			id, _ := o.Get("id").(float64)
			if id <= 0 {
				continue
			}
			if _, err := h.d.GoAPI.Post(ctx, "/help/requests/"+strconv.FormatInt(int64(id), 10)+"/resolve", goapi.RawBody([]byte(`{}`))); err == nil {
				n++
			}
		}
	}
	return n
}

// reopen answers POST /api/chat/conversations/{id}/reopen.
func (h *inboxRoutes) reopen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := h.supportConversation(w, r)
	if !ok {
		return
	}
	c, err := h.store().Reopen(ctx, c.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	people, _ := h.people(ctx)
	h.systemLine(ctx, c.ID, map[string]any{"event": "reopened", "by": people[kit.UserID(r)].Name})
	h.changed(ctx, c)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, people[c.AssigneeID].Name)})
}

// link answers POST /api/chat/conversations/{id}/link {ticket_id,
// help_request_id}: what the conversation is about.
func (h *inboxRoutes) link(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, ok := h.supportConversation(w, r)
	if !ok {
		return
	}
	var body struct {
		TicketID      int64 `json:"ticket_id"`
		HelpRequestID int64 `json:"help_request_id"`
	}
	if err := kit.Bind(r, &body); err != nil || body.TicketID < 0 || body.HelpRequestID < 0 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	c, err := h.store().Link(ctx, c.ID, body.HelpRequestID, body.TicketID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.changed(ctx, c)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// summary answers GET /api/chat/summary: what the badges count. Support:
// the open conversations with unread messages that are the account's or
// nobody's, and how many wait for someone; Team: the unread messages of
// the account's conversations.
func (h *inboxRoutes) summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := kit.User(r)
	me := chatstore.User(user.ID, user.Username)
	out := map[string]any{"success": true}
	acc := kit.Access(r)
	if acc.HasPermission(permSupport) {
		list, err := h.store().List(ctx, chatstore.Filter{Kinds: []string{chatstore.ConvSupport}, State: chatstore.StateOpen, WithMessages: true, Limit: 2000})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		var ids []string
		waiting := 0
		for _, c := range list {
			if (c.AssigneeID == 0 || c.AssigneeID == user.ID) && h.hub.MayRead(ctx, user, c.ID) {
				ids = append(ids, c.ID)
				if c.AssigneeID == 0 && c.WaitingSinceMs != 0 {
					waiting++
				}
			}
		}
		unread := map[string]int{}
		if len(ids) > 0 {
			if unread, err = h.store().Unread(ctx, me, ids); err != nil {
				h.fail(w, r, err)
				return
			}
		}
		out["support"] = map[string]any{"unread_conversations": len(unread), "waiting": waiting, "unread": unread}
	}
	if acc.HasPermission(permTeam) {
		list, err := h.store().List(ctx, chatstore.Filter{Kinds: []string{chatstore.ConvDirect, chatstore.ConvGroup}, Member: user.ID})
		if err != nil {
			h.fail(w, r, err)
			return
		}
		ids := make([]string, 0, len(list))
		for _, c := range list {
			ids = append(ids, c.ID)
		}
		unread := map[string]int{}
		if len(ids) > 0 {
			if unread, err = h.store().Unread(ctx, me, ids); err != nil {
				h.fail(w, r, err)
				return
			}
		}
		total := 0
		for _, n := range unread {
			total += n
		}
		out["team"] = map[string]any{"unread": total, "by_conversation": unread}
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// --- Team ---

// wireTeamItem is a row of the team's list.
type wireTeamItem struct {
	wireConversation
	People []wirePerson `json:"people"`
	Last   *wireLast    `json:"last_message"`
	Unread int          `json:"unread"`
}

// teamList answers GET /api/chat/team: the account's direct and group
// conversations and the colleagues it may write to (the accounts with
// Team chat), with their status.
func (h *inboxRoutes) teamList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := kit.User(r)
	list, err := h.store().List(ctx, chatstore.Filter{Kinds: []string{chatstore.ConvDirect, chatstore.ConvGroup}, Member: user.ID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	unread := map[string]int{}
	if len(ids) > 0 {
		if unread, err = h.store().Unread(ctx, chatstore.User(user.ID, user.Username), ids); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	items := make([]wireTeamItem, 0, len(list))
	for _, c := range list {
		members, err := h.store().Members(ctx, c.ID)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		item := wireTeamItem{wireConversation: conversationOut(c, ""), People: []wirePerson{}, Last: h.last(ctx, c), Unread: unread[c.ID]}
		for _, m := range members {
			if p, ok := people[m]; ok && (m != user.ID || c.Kind == chatstore.ConvGroup) {
				item.People = append(item.People, p)
			}
		}
		items = append(items, item)
	}
	colleagues := []wirePerson{}
	for _, p := range people {
		if p.ID == user.ID {
			continue
		}
		if !h.mayTeam(ctx, p) {
			continue
		}
		colleagues = append(colleagues, p)
	}
	sort.Slice(colleagues, func(i, j int) bool { return strings.ToLower(colleagues[i].Name) < strings.ToLower(colleagues[j].Name) })
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversations": items, "people": colleagues})
}

// mayTeam reports whether an account may take part in Team chat.
func (h *inboxRoutes) mayTeam(ctx context.Context, p wirePerson) bool {
	if h.hub.opts.Holds == nil {
		return true
	}
	ok, err := h.hub.opts.Holds(ctx, p.Role, permTeam)
	return err == nil && ok
}

// direct answers POST /api/chat/team/direct {account_id}: the direct
// conversation with a colleague who has Team chat, made when missing.
func (h *inboxRoutes) direct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		AccountID int64 `json:"account_id"`
	}
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "account_id must be a number")
		return
	}
	people, err := h.people(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, ok := people[body.AccountID]
	user := kit.User(r)
	if !ok || p.ID == user.ID || !h.mayTeam(ctx, p) {
		kit.Fail(w, r, http.StatusBadRequest, "You can't write to that person")
		return
	}
	c, err := h.store().Direct(ctx, user.ID, p.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// newGroup answers POST /api/chat/team/groups {title, members}: a group
// of colleagues with Team chat and the account itself.
func (h *inboxRoutes) newGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Title   string  `json:"title"`
		Members []int64 `json:"members"`
	}
	if err := kit.Bind(r, &body); err != nil || strings.TrimSpace(body.Title) == "" || len(body.Members) == 0 || len(body.Members) > 200 {
		kit.Fail(w, r, http.StatusBadRequest, "A group needs a name and people")
		return
	}
	members, ok := h.teamMembers(ctx, body.Members)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "You can't add one of these people")
		return
	}
	c, err := h.store().NewGroup(ctx, body.Title, kit.UserID(r), members)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.groupChanged(ctx, c)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// editGroup answers PATCH /api/chat/team/groups/{id} {title, members}:
// a member renames the group or changes who is in it.
func (h *inboxRoutes) editGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("id")
	if chatstore.KindOf(id) != chatstore.ConvGroup || !h.hub.MayRead(ctx, kit.User(r), id) {
		kit.Fail(w, r, http.StatusNotFound, "Conversation not found")
		return
	}
	var body struct {
		Title   string  `json:"title"`
		Members []int64 `json:"members"`
	}
	if err := kit.Bind(r, &body); err != nil || len(body.Members) > 200 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	var members []int64
	if body.Members != nil {
		var ok bool
		if members, ok = h.teamMembers(ctx, body.Members); !ok {
			kit.Fail(w, r, http.StatusBadRequest, "You can't add one of these people")
			return
		}
	}
	c, err := h.store().SetGroup(ctx, id, body.Title, members)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.groupChanged(ctx, c)
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "conversation": conversationOut(c, "")})
}

// teamMembers are the accounts of a group's list that have Team chat;
// ok is false when one is unknown or has none.
func (h *inboxRoutes) teamMembers(ctx context.Context, ids []int64) ([]int64, bool) {
	people, err := h.people(ctx)
	if err != nil {
		return nil, false
	}
	var out []int64
	for _, id := range ids {
		p, known := people[id]
		if !known || !h.mayTeam(ctx, p) {
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

// groupChanged tells the group's members its new state.
func (h *inboxRoutes) groupChanged(ctx context.Context, c chatstore.Conversation) {
	members, _ := h.store().Members(ctx, c.ID)
	text := frame("conversation.updated", map[string]any{"conversation": conversationOut(c, "")})
	for _, m := range members {
		h.hub.toAccount(m, text)
	}
}

// --- Canned replies ---

// cannedList answers GET /api/chat/canned: the shared replies and the
// account's own.
func (h *inboxRoutes) cannedList(w http.ResponseWriter, r *http.Request) {
	list, err := h.store().CannedFor(r.Context(), kit.UserID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "replies": list,
		"may_manage_shared": kit.Access(r).HasPermission("chat.canned_manage")})
}

// cannedAdd answers POST /api/chat/canned {title, body, shared}: a reply
// of one's own, or a shared one with chat.canned_manage.
func (h *inboxRoutes) cannedAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		Shared bool   `json:"shared"`
	}
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	owner := kit.UserID(r)
	if body.Shared {
		if !kit.Access(r).HasPermission("chat.canned_manage") {
			kit.Fail(w, r, http.StatusForbidden, "Permission denied: chat.canned_manage")
			return
		}
		owner = 0
	}
	c, err := h.store().AddCanned(r.Context(), owner, kit.UserID(r), body.Title, body.Body)
	if errors.Is(err, chatstore.ErrEmpty) {
		kit.Fail(w, r, http.StatusBadRequest, "A canned reply needs a title and text")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "reply": c})
}

// cannedMine reads the reply of the path that the account may change: its
// own, or a shared one with chat.canned_manage.
func (h *inboxRoutes) cannedMine(w http.ResponseWriter, r *http.Request) (chatstore.Canned, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		kit.Fail(w, r, http.StatusNotFound, "Canned reply not found")
		return chatstore.Canned{}, false
	}
	c, err := h.store().CannedByID(r.Context(), id)
	if err != nil || (!c.Shared() && c.OwnerID != kit.UserID(r)) {
		kit.Fail(w, r, http.StatusNotFound, "Canned reply not found")
		return c, false
	}
	if c.Shared() && !kit.Access(r).HasPermission("chat.canned_manage") {
		kit.Fail(w, r, http.StatusForbidden, "Permission denied: chat.canned_manage")
		return c, false
	}
	return c, true
}

func (h *inboxRoutes) cannedEdit(w http.ResponseWriter, r *http.Request) {
	c, ok := h.cannedMine(w, r)
	if !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	c, err := h.store().UpdateCanned(r.Context(), c.ID, body.Title, body.Body)
	if errors.Is(err, chatstore.ErrEmpty) {
		kit.Fail(w, r, http.StatusBadRequest, "A canned reply needs a title and text")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "reply": c})
}

func (h *inboxRoutes) cannedDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := h.cannedMine(w, r)
	if !ok {
		return
	}
	if err := h.store().DeleteCanned(r.Context(), c.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true})
}

// --- Settings ---

// Settings keys of the chat (chat_settings).
const (
	settingMailAfter   = "mail_after_minutes"
	settingMailPreview = "mail_preview"
	accountSettings    = "account:"
)

// ServerSettings are the chat's settings for everyone.
type ServerSettings struct {
	// MailAfterMinutes is how long a person waits for an answer before
	// the console mails (0: never)
	MailAfterMinutes int `json:"mail_after_minutes"`
	// MailPreview puts the start of the waiting message into the mail
	MailPreview bool `json:"mail_preview"`
}

// AccountSettings are an account's chat notifications.
type AccountSettings struct {
	Browser bool `json:"browser"`
	Sound   bool `json:"sound"`
	Mail    bool `json:"mail"`
}

// LoadServerSettings reads the chat's settings: a mail after 15 minutes
// with a preview unless an admin changed them (A242 c).
func LoadServerSettings(ctx context.Context, s *chatstore.Store) ServerSettings {
	out := ServerSettings{MailAfterMinutes: 15, MailPreview: true}
	if v, err := s.Setting(ctx, settingMailAfter); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			out.MailAfterMinutes = n
		}
	}
	if v, err := s.Setting(ctx, settingMailPreview); err == nil && v != "" {
		out.MailPreview = v == "1"
	}
	return out
}

// LoadAccountSettings reads an account's chat notifications: browser
// notifications and mail on, sound off, until it changes them.
func LoadAccountSettings(ctx context.Context, s *chatstore.Store, account int64) AccountSettings {
	out := AccountSettings{Browser: true, Mail: true}
	v, err := s.Setting(ctx, accountSettings+strconv.FormatInt(account, 10))
	if err == nil && v != "" {
		parts := strings.Split(v, ",")
		if len(parts) == 3 {
			out = AccountSettings{Browser: parts[0] == "1", Sound: parts[1] == "1", Mail: parts[2] == "1"}
		}
	}
	return out
}

func bit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (h *inboxRoutes) mySettings(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "settings": LoadAccountSettings(r.Context(), h.store(), kit.UserID(r))})
}

func (h *inboxRoutes) setMySettings(w http.ResponseWriter, r *http.Request) {
	var body AccountSettings
	if err := kit.Bind(r, &body); err != nil {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid request")
		return
	}
	key := accountSettings + strconv.FormatInt(kit.UserID(r), 10)
	if err := h.store().SetSetting(r.Context(), key, bit(body.Browser)+","+bit(body.Sound)+","+bit(body.Mail)); err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "settings": body})
}

func (h *inboxRoutes) settings(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "settings": LoadServerSettings(r.Context(), h.store())})
}

func (h *inboxRoutes) setSettings(w http.ResponseWriter, r *http.Request) {
	var body ServerSettings
	if err := kit.Bind(r, &body); err != nil || body.MailAfterMinutes < 0 || body.MailAfterMinutes > 7*24*60 {
		kit.Fail(w, r, http.StatusBadRequest, "mail_after_minutes must be between 0 and 10080")
		return
	}
	ctx := r.Context()
	if err := h.store().SetSetting(ctx, settingMailAfter, strconv.Itoa(body.MailAfterMinutes)); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.store().SetSetting(ctx, settingMailPreview, bit(body.MailPreview)); err != nil {
		h.fail(w, r, err)
		return
	}
	h.hub.audit(ctx, &member{user: kit.User(r)}, "chat_settings_changed", map[string]any{"mail_after_minutes": body.MailAfterMinutes, "mail_preview": body.MailPreview})
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "settings": body})
}

// trail answers GET /api/chat/messages/{id}/trail: the old texts of an
// edited or deleted message, for accounts with audit.view that read its
// conversation or see every device.
func (h *inboxRoutes) trail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		kit.Fail(w, r, http.StatusNotFound, "Message not found")
		return
	}
	msg, err := h.store().Get(ctx, id)
	if err != nil || !h.hub.MayRead(ctx, kit.User(r), msg.ConversationID) {
		kit.Fail(w, r, http.StatusNotFound, "Message not found")
		return
	}
	list, err := h.store().Trail(ctx, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "trail": list})
}
