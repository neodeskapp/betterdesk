package chat

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// hubs are the hubs of the consoles of this process, one each: the
// sockets (console/sockets.go) and the routes reach the same one.
var hubs sync.Map // *kit.Deps -> *Hub

// HubOf is the console's hub, made on first use.
func HubOf(d *kit.Deps) *Hub {
	if h, ok := hubs.Load(d); ok {
		return h.(*Hub)
	}
	h := NewHub(HubOptions{
		Store: d.Chat,
		Scope: ScopeOf(d),
		Holds: func(ctx context.Context, role, permission string) (bool, error) {
			if d.Access == nil {
				return access.RoleHasPermission(role, permission), nil
			}
			return d.Access.RoleHasEffectivePermission(ctx, role, permission)
		},
		IsAdmin:     access.IsAdminRole,
		Insert:      APIInsert(d.GoAPI),
		FilesDir:    FilesDir(d.Config),
		DisplayName: displayNames(d.Store),
		PictureVersion: func(ctx context.Context, id int64) string {
			if d.Avatars == nil {
				return ""
			}
			return d.Avatars.GetAvatarVersion(ctx, id).String
		},
		Audit: auditTo(d.Audit),
	})
	if d.Authn != nil {
		h.opts.VerifyDevice = d.Authn.VerifyDeviceWSAuth
	}
	actual, _ := hubs.LoadOrStore(d, h)
	return actual.(*Hub)
}

// APIInsert stores a message through the API server (POST /chat/messages),
// which hands it to an agent connected over CDAP, and returns its id.
func APIInsert(api *goapi.Client) chatstore.InsertFunc {
	return func(ctx context.Context, conversation, fromID, fromName, toID, text string) (int64, error) {
		body := newObject().str("conversation_id", conversation).str("from_id", fromID).str("from_name", fromName).
			str("to_id", toID).str("text", text).json()
		resp, err := api.Post(ctx, "/chat/messages", goapi.RawBody(body))
		if err != nil {
			return 0, err
		}
		data := apiData(resp)
		n, err := data.Get("id").Num()
		if err != nil || n <= 0 {
			return 0, errNoID
		}
		return int64(n), nil
	}
}

// errNoID is an answer of the API server without the stored message's id.
var errNoID = errorString("chat: the API server answered no message id")

type errorString string

func (e errorString) Error() string { return string(e) }

// displayNames is an account's first and last name, else its user name;
// "" for an account that isn't there.
func displayNames(db *store.DB) func(ctx context.Context, id int64) string {
	return func(ctx context.Context, id int64) string {
		if db == nil {
			return ""
		}
		var username string
		var first, last sql.NullString
		if err := db.QueryRow(ctx, `SELECT username, first_name, last_name FROM users WHERE id = ?`, id).Scan(&username, &first, &last); err != nil {
			return ""
		}
		if name := strings.TrimSpace(strings.TrimSpace(first.String) + " " + strings.TrimSpace(last.String)); name != "" {
			return name
		}
		return username
	}
}

// auditTo writes the chat's audit entries.
func auditTo(log *auditlog.Log) func(ctx context.Context, userID int64, action string, details map[string]any) {
	return func(ctx context.Context, userID int64, action string, details map[string]any) {
		if log == nil {
			return
		}
		var parts []string
		for _, k := range sortedKeys(details) {
			parts = append(parts, k+"="+toText(details[k]))
		}
		if err := log.Write(ctx, userID, action, strings.Join(parts, " "), ""); err != nil {
			logging.Logf("console", "WARN: [Chat] audit %s: %v", action, err)
		}
	}
}

func toText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// deliverV1 hands a message of version 2 to the sockets of version 1 of a
// device's conversation: the device's agent, the operators in its room
// and the inbox, as frames of version 1.
func (r *Relay) deliverV1(ctx context.Context, msg chatstore.Message) {
	text := msg.Text
	if text == "" && len(msg.Attachments) > 0 {
		text = msg.Attachments[0].Name
	}
	o := newObject().str("type", "message").num("id", float64(msg.CreatedMs))
	if msg.From.Kind == chatstore.KindUser {
		o.str("from", "operator").str("from_name", msg.From.Name).str("conversation_id", msg.ConversationID).
			str("operator", msg.From.Name).str("to_id", msg.ConversationID)
	} else {
		o.str("from", msg.From.ID).str("from_name", msg.From.Name).str("conversation_id", msg.ConversationID)
	}
	out := o.str("text", text).num("timestamp", float64(msg.CreatedMs)).text()
	r.mu.Lock()
	rm := r.rooms[msg.ConversationID]
	if rm != nil {
		rm.appendLocked([]byte(out))
		rm.broadcastLocked(out, nil)
	}
	r.mu.Unlock()
	r.toPanel(ctx, msg.ConversationID, nil, rm, out)
}

// persistV2 stores a message a socket of version 1 sent in the store of
// version 2, through the API server as before, and hands it to the
// sockets of version 2.
func (c *client) persistV2(store func(func(context.Context)), conversation jsjson.Value, msgText string) bool {
	hub := c.relay.opts.Hub
	if hub == nil || hub.opts.Store == nil {
		return false
	}
	conv, err := conversation.Text()
	if err != nil || conv == "" {
		return false
	}
	from := chatstore.Device(c.id, c.name)
	if c.user != nil {
		from = chatstore.User(c.user.ID, c.operator)
	}
	insert := APIInsert(c.relay.opts.API)
	store(func(ctx context.Context) {
		msg, err := hub.opts.Store.Post(ctx, chatstore.Message{ConversationID: conv, From: from, Text: msgText}, insert)
		if err != nil {
			logging.Logf("console", "WARN: [Chat] Failed to persist chat message: %v", err)
			return
		}
		hub.remember(msg.ID)
		out := messageOut(ctx, hub.opts.Store, msg, nil)
		hub.publish(ctx, conv, frame("message.new", map[string]any{"message": out}), nil, false)
	})
	return true
}

// WatchEvents hands the messages the API server stored for agents (CDAP,
// the device chat route) to the sockets, from the server's event bus,
// until ctx ends. The relay's own messages go through the API server too:
// they were handed on already and are skipped.
func (h *Hub) WatchEvents(ctx context.Context, events func(ctx context.Context, filter string) (<-chan *goapi.Object, error)) {
	if events == nil || h.opts.Store == nil {
		return
	}
	frames, err := events(ctx, "chat_message")
	if err != nil {
		return
	}
	for f := range frames {
		if kind, _ := f.Get("type").(string); kind != "chat_message" {
			continue
		}
		data, _ := f.Get("data").(*goapi.Object)
		idText, _ := data.Get("id").(string)
		id, _ := strconv.ParseInt(idText, 10, 64)
		if id <= 0 {
			continue
		}
		// The relay's own message finishes storing its details first
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(750 * time.Millisecond):
			}
			if h.seenRecently(id) {
				return
			}
			if err := h.opts.Store.CatchUp(ctx); err != nil {
				return
			}
			msg, err := h.opts.Store.Get(ctx, id)
			if err != nil {
				return
			}
			h.Published(ctx, msg)
		}()
	}
}

// remember notes a message the hub handed on, so the event of its
// storing is skipped.
func (h *Hub) remember(id int64) {
	h.recentMu.Lock()
	defer h.recentMu.Unlock()
	if h.recent == nil {
		h.recent = map[int64]time.Time{}
	}
	now := time.Now()
	h.recent[id] = now
	for k, t := range h.recent {
		if now.Sub(t) > time.Minute {
			delete(h.recent, k)
		}
	}
}

func (h *Hub) seenRecently(id int64) bool {
	h.recentMu.Lock()
	defer h.recentMu.Unlock()
	_, ok := h.recent[id]
	return ok
}

// CloseTokenRefused is the close code of a computer's chat socket whose
// credential ended: its token was revoked or replaced, or the computer was
// deleted or banned (L539). A client reads it as a refused token.
const CloseTokenRefused = 4403

// credentialEnds are the events after which a computer's chat socket may
// have lost its credential.
var credentialEnds = map[string]bool{
	"device_credentials_ended": true, "peer_banned": true, "peer_deleted": true, "peer_revoked": true,
	"peer_id_changed": true, "blocklist_add": true,
}

// WatchDeviceCredentials rechecks the computers' chat sockets when a
// device credential ends (L539): the device the event names, all of them
// when it names none.
func (h *Hub) WatchDeviceCredentials(ctx context.Context, events func(ctx context.Context, filter string) (<-chan *goapi.Object, error)) {
	if events == nil {
		return
	}
	frames, err := events(ctx, "")
	if err != nil {
		return
	}
	for f := range frames {
		kind, _ := f.Get("type").(string)
		if !credentialEnds[kind] {
			continue
		}
		only := ""
		if data, ok := f.Get("data").(*goapi.Object); ok {
			only, _ = data.Get("id").(string)
		}
		h.RecheckDevices(ctx, only)
	}
}

// RunDeviceRecheck rechecks every computer's chat socket every minute,
// for credentials that end without an event (an expiry, a token an
// ended API token had made).
func (h *Hub) RunDeviceRecheck(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		h.RecheckDevices(ctx, "")
	}
}

// RecheckDevices closes the chat sockets of computers whose credential no
// longer holds, with CloseTokenRefused; only names one computer ("" for
// all). A check that fails keeps the socket.
func (h *Hub) RecheckDevices(ctx context.Context, only string) {
	if h.opts.VerifyDevice == nil {
		return
	}
	for _, m := range h.all() {
		if m.user != nil || only != "" && m.device != only {
			continue
		}
		ok, err := h.opts.VerifyDevice(ctx, m.device, m.token)
		if err == nil && !ok {
			m.conn.Close(CloseTokenRefused, "token_refused")
		}
	}
}
