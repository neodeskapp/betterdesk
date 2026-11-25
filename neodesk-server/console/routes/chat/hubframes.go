package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The frames a socket of version 2 sends (neodesk-server/console/routes/chat/PROTOCOL.md). A frame
// that names a conversation the sender may not read, a message of one, or
// nothing it knows is answered with an error frame and changes nothing.

func str(f map[string]json.RawMessage, key string) string {
	var s string
	if raw, ok := f[key]; ok {
		if json.Unmarshal(raw, &s) != nil {
			// A number is taken as its text (message ids)
			var n json.Number
			if json.Unmarshal(raw, &n) == nil {
				return n.String()
			}
		}
	}
	return s
}

func id64(f map[string]json.RawMessage, key string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(str(f, key)), 10, 64)
	return n
}

func boolean(f map[string]json.RawMessage, key string, def bool) bool {
	var b bool
	if raw, ok := f[key]; ok && json.Unmarshal(raw, &b) == nil {
		return b
	}
	return def
}

// fail answers a frame with an error code.
func fail(m *member, code string, f map[string]json.RawMessage) {
	fields := map[string]any{"code": code}
	if c := str(f, "client_id"); c != "" {
		fields["client_id"] = c
	}
	if t := str(f, "type"); t != "" {
		fields["for"] = t
	}
	m.conn.SendText(frame("error", fields))
}

// conversationOf is the conversation a frame names; a device's is always
// its own.
func conversationOf(m *member, f map[string]json.RawMessage) string {
	if m.user == nil {
		return m.device
	}
	return strings.TrimSpace(str(f, "conversation_id"))
}

func (h *Hub) handle(ctx context.Context, m *member, f map[string]json.RawMessage) {
	if h.opts.Store == nil {
		return
	}
	switch str(f, "type") {
	case "send":
		h.send(ctx, m, f)
	case "edit":
		h.edit(ctx, m, f)
	case "delete":
		h.remove(ctx, m, f)
	case "react":
		h.react(ctx, m, f)
	case "read":
		conv := conversationOf(m, f)
		if !h.mayRead(ctx, m, conv) {
			fail(m, "no_access", f)
			return
		}
		upTo, err := h.opts.Store.MarkRead(ctx, conv, m.party(), id64(f, "up_to"))
		if err != nil {
			fail(m, "failed", f)
			return
		}
		h.publish(ctx, conv, frame("read", map[string]any{"conversation_id": conv,
			"reader": map[string]string{"kind": m.party().Kind, "id": m.party().ID}, "up_to": strconv.FormatInt(upTo, 10)}), nil, false)
	case "typing":
		conv := conversationOf(m, f)
		if h.mayRead(ctx, m, conv) {
			h.publish(ctx, conv, frame("typing", map[string]any{"conversation_id": conv, "from": partyOut(m.party())}), m, false)
		}
	case "history":
		conv := conversationOf(m, f)
		if !h.mayRead(ctx, m, conv) {
			fail(m, "no_access", f)
			return
		}
		limit := int(id64(f, "limit"))
		if limit <= 0 || limit > 100 {
			limit = 50
		}
		h.sendHistory(ctx, m, conv, id64(f, "before"), limit)
	case "session_line":
		h.sessionLine(ctx, m, f)
	case "presence":
		if m.user == nil {
			return
		}
		switch s := str(f, "status"); s {
		case "online", "away", "busy":
			m.status = s
			h.presence(ctx, m)
		}
	}
}

// send stores and hands on a message. A console user's message to a
// device goes through the API server, so an agent on CDAP gets it too.
func (h *Hub) send(ctx context.Context, m *member, f map[string]json.RawMessage) {
	conv := conversationOf(m, f)
	if !h.mayRead(ctx, m, conv) {
		fail(m, "no_access", f)
		return
	}
	var ids []string
	if raw, ok := f["attachments"]; ok {
		_ = json.Unmarshal(raw, &ids)
	}
	attachments, ok := h.attachments(ctx, m, conv, ids)
	if !ok {
		fail(m, "bad_attachment", f)
		return
	}
	msg := chatstore.Message{ConversationID: conv, From: m.party(), Text: str(f, "text"), ReplyTo: id64(f, "reply_to"),
		Attachments: attachments, ClientID: str(f, "client_id")}
	var insert chatstore.InsertFunc
	if m.user != nil && chatstore.KindOf(conv) == chatstore.ConvSupport {
		insert = h.opts.Insert
	}
	stored, err := h.opts.Store.Post(ctx, msg, insert)
	if errors.Is(err, chatstore.ErrEmpty) {
		fail(m, "empty", f)
		return
	}
	if err != nil {
		logging.Logf("console", "WARN: [Chat] store a message in %s: %v", conv, err)
		fail(m, "failed", f)
		return
	}
	for _, a := range stored.Attachments {
		_ = addLink(h.opts.FilesDir, a.ID, link{Conversation: conv})
	}
	// The sender has read what it wrote and everything before
	_, _ = h.opts.Store.MarkRead(ctx, conv, m.party(), stored.ID)
	h.Published(ctx, stored)
}

// sessionLine stores a line the person at a device wrote in the chat of a
// remote session that this console user's browser has open with it
// (session-bridge.ts): the session is end to end, so only the viewer sees
// it. It goes into the device's conversation, from the device. When two
// viewers have a session with the device, both pass the same line on; the
// second within a few seconds is dropped.
func (h *Hub) sessionLine(ctx context.Context, m *member, f map[string]json.RawMessage) {
	conv := conversationOf(m, f)
	text := strings.TrimSpace(str(f, "text"))
	if m.user == nil || chatstore.KindOf(conv) != chatstore.ConvSupport || text == "" || !h.mayRead(ctx, m, conv) {
		return
	}
	if !h.firstSessionLine(conv, text) {
		return
	}
	name := conv
	if h.opts.Scope.Device != nil {
		if d := h.opts.Scope.Device(ctx, conv); d != nil {
			if host, _ := d.Get("hostname").(string); strings.TrimSpace(host) != "" {
				name = host
			}
		}
	}
	msg, err := h.opts.Store.Post(ctx, chatstore.Message{ConversationID: conv, From: chatstore.Device(conv, name), Text: text, ClientID: "session"}, nil)
	if err != nil {
		fail(m, "failed", f)
		return
	}
	h.Published(ctx, msg)
}

// firstSessionLine reports whether a line from a session is new: not the
// same text of the same device within five seconds.
func (h *Hub) firstSessionLine(conv, text string) bool {
	h.recentMu.Lock()
	defer h.recentMu.Unlock()
	if h.sessionLines == nil {
		h.sessionLines = map[string]time.Time{}
	}
	key := conv + "|" + text
	now := time.Now()
	for k, t := range h.sessionLines {
		if now.Sub(t) > 5*time.Second {
			delete(h.sessionLines, k)
		}
	}
	if _, seen := h.sessionLines[key]; seen {
		return false
	}
	h.sessionLines[key] = now
	return true
}

// attachments are the files a message names: chat files the sender may
// use, with what the upload noted of them.
func (h *Hub) attachments(ctx context.Context, m *member, conv string, ids []string) ([]chatstore.Attachment, bool) {
	if len(ids) > chatstore.MaxAttachments {
		return nil, false
	}
	var out []chatstore.Attachment
	for _, raw := range ids {
		id := lowerASCII(strings.TrimSpace(raw))
		if !isFileID(id) || h.opts.FilesDir == "" {
			return nil, false
		}
		name, err := findFile(h.opts.FilesDir, id)
		if err != nil || name == "" {
			return nil, false
		}
		links, err := readLinks(h.opts.FilesDir, id)
		if err != nil {
			return nil, false
		}
		mine := m.user != nil && links.owner == m.user.ID || m.user == nil && links.device == m.device
		if !mine {
			return nil, false
		}
		meta := links.meta
		if meta.Name == "" {
			meta.Name = name
		}
		out = append(out, chatstore.Attachment{ID: id, Name: meta.Name, Type: meta.Type, Size: meta.Size, Width: meta.Width, Height: meta.Height})
	}
	return out, true
}

// own reads a message the member may change: one of a conversation it
// reads.
func (h *Hub) own(ctx context.Context, m *member, f map[string]json.RawMessage) (chatstore.Message, bool) {
	msg, err := h.opts.Store.Get(ctx, id64(f, "message_id"))
	if err != nil || !h.mayRead(ctx, m, msg.ConversationID) {
		fail(m, "not_found", f)
		return msg, false
	}
	return msg, true
}

func (h *Hub) edit(ctx context.Context, m *member, f map[string]json.RawMessage) {
	msg, ok := h.own(ctx, m, f)
	if !ok {
		return
	}
	edited, err := h.opts.Store.Edit(ctx, msg.ID, m.party(), str(f, "text"))
	if code := storeError(err); code != "" {
		fail(m, code, f)
		return
	}
	h.audit(ctx, m, "chat_message_edited", map[string]any{"message_id": msg.ID, "conversation_id": msg.ConversationID})
	h.updated(ctx, edited)
}

func (h *Hub) remove(ctx context.Context, m *member, f map[string]json.RawMessage) {
	msg, ok := h.own(ctx, m, f)
	if !ok {
		return
	}
	gone, err := h.opts.Store.Delete(ctx, msg.ID, m.party(), m.user != nil && m.admin)
	if code := storeError(err); code != "" {
		fail(m, code, f)
		return
	}
	h.audit(ctx, m, "chat_message_deleted", map[string]any{"message_id": msg.ID, "conversation_id": msg.ConversationID,
		"sender": msg.From.Name})
	h.updated(ctx, gone)
}

func (h *Hub) react(ctx context.Context, m *member, f map[string]json.RawMessage) {
	msg, ok := h.own(ctx, m, f)
	if !ok {
		return
	}
	changed, err := h.opts.Store.React(ctx, msg.ID, m.party(), str(f, "emoji"), boolean(f, "on", true))
	if code := storeError(err); code != "" {
		fail(m, code, f)
		return
	}
	h.updated(ctx, changed)
}

// storeError is the error code of a store's answer, "" for none.
func storeError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, chatstore.ErrNotAllowed):
		return "not_yours"
	case errors.Is(err, chatstore.ErrTooLate):
		return "too_late"
	case errors.Is(err, chatstore.ErrDeleted):
		return "deleted"
	case errors.Is(err, chatstore.ErrEmpty):
		return "empty"
	case errors.Is(err, chatstore.ErrNotFound):
		return "not_found"
	}
	logging.Logf("console", "WARN: [Chat] %v", err)
	return "failed"
}

func (h *Hub) audit(ctx context.Context, m *member, action string, details map[string]any) {
	if h.opts.Audit == nil {
		return
	}
	var userID int64
	if m.user != nil {
		userID = m.user.ID
	} else {
		details["device_id"] = m.device
	}
	h.opts.Audit(ctx, userID, action, details)
}
