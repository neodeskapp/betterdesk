package chat

import (
	"context"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// capabilities are what the welcome message says the relay can do.
// Messages travel and are stored as they come, without encryption of
// their own.
var capabilities = []string{"multi_conversation", "contacts", "groups", "history", "read_receipts", "typing", "presence", "file_share"}

// welcome is the answer to hello.
func (r *Relay) welcome(id string) *object {
	list := make([][]byte, len(capabilities))
	for i, c := range capabilities {
		list[i] = jsjson.AppendQuoted(nil, c)
	}
	return newObject().str("type", "welcome").str("device_id", id).num("server_time", r.nowMillis()).raw("capabilities", array(list))
}

// messageText is the text of a message: String(text || ""), cut after
// 2048 UTF-16 units.
func messageText(v jsjson.Value) (string, error) {
	text, err := v.Or(jsjson.OfText("")).Text()
	if err != nil {
		return "", err
	}
	return jsjson.Slice(text, maxText), nil
}

// or is a || b.
func or(a, b jsjson.Value) jsjson.Value { return a.Or(b) }

func text(s string) jsjson.Value { return jsjson.OfText(s) }

// typeOf is frame.type, "" when it is not text; a frame that is null has
// no members to read (errDrop).
func typeOf(frame jsjson.Value) (string, error) {
	if frame.Kind() == jsjson.Null {
		return "", errDrop
	}
	t := frame.Get("type")
	if t.Kind() != jsjson.String {
		return "", nil
	}
	return t.Str(), nil
}

// fileID is frame.file_id || 'file_' + Date.now().
func (r *Relay) fileID(frame jsjson.Value) jsjson.Value {
	if v := frame.Get("file_id"); v.Truthy() {
		return v
	}
	return text("file_" + jsbind.NumberString(r.nowMillis()))
}

// persist stores a message on the API server: the body persistMessage
// made of it, handed to store (client.later, or a call in the running
// task).
func (c *client) persist(store func(func(context.Context)), conversation, from, fromName, toID jsjson.Value, msgText string) {
	if c.persistV2(store, or(conversation, or(from, text("operator"))), msgText) {
		return
	}
	body := newObject().
		val("conversation_id", or(conversation, or(from, text("operator")))).
		val("from_id", or(from, text("unknown"))).
		val("from_name", or(fromName, or(from, text("")))).
		val("to_id", or(toID, text(""))).
		str("text", msgText).json()
	store(func(ctx context.Context) { saveMessage(ctx, c.relay.opts.API, body) })
}

// asAgent runs do for a frame of the agent that names a conversation, when
// the conversation is the agent's: at once for its device's conversation
// (and, with operators, the operators' "operator" and "operator:<name>"),
// as a call in order after the group check for any other. do gets where
// its own calls to the API server go.
func (r *Relay) asAgent(c *client, conversation jsjson.Value, operators bool, do func(store func(func(context.Context)))) {
	asked, err := conversation.Text()
	if err != nil {
		return
	}
	if asked == c.id || operators && (asked == "operator" || strings.HasPrefix(asked, "operator:")) {
		do(c.later)
		return
	}
	c.later(func(ctx context.Context) {
		if !r.agentIn(ctx, c, conversation) {
			return
		}
		do(func(task func(context.Context)) { task(ctx) })
	})
}

// agentFrame handles one message of a device's agent.
func (r *Relay) agentFrame(c *client, frame jsjson.Value) error {
	typ, err := typeOf(frame)
	if err != nil {
		return err
	}
	id := text(c.id)
	switch typ {
	case "hello":
		c.send(r.welcome(c.id))

	case "message":
		msgID := r.nowMillis()
		fromName := text(c.name)
		conversation := or(frame.Get("conversation_id"), id)
		msgText, err := messageText(frame.Get("text"))
		if err != nil {
			return errDrop
		}
		timestamp := or(frame.Get("timestamp"), jsjson.OfNumber(r.nowMillis()))
		r.asAgent(c, conversation, true, func(store func(func(context.Context))) {
			msg := newObject().str("type", "message").num("id", msgID).str("from", c.id).val("from_name", fromName).
				val("conversation_id", conversation).str("text", msgText).val("timestamp", timestamp)
			out := msg.json()
			r.mu.Lock()
			rm := r.roomLocked(c.id)
			rm.appendLocked(out)
			rm.broadcastLocked(string(out), c.conn)
			r.mu.Unlock()
			r.toPanel(c.api, c.id, c.conn, rm, string(out))
			c.persist(store, conversation, id, fromName, jsjson.Value{}, msgText)
		})

	case "typing":
		conversation := or(frame.Get("conversation_id"), id)
		r.asAgent(c, conversation, true, func(func(func(context.Context))) {
			r.toAgentRoom(c, newObject().str("type", "typing").str("from", c.id).val("conversation_id", conversation))
		})

	case "get_contacts":
		// An agent gets its own contacts only
		target := or(frame.Get("device_id"), id)
		if own, err := target.Text(); err != nil || own != c.id {
			return nil
		}
		c.later(func(ctx context.Context) {
			data, ok := loadContacts(ctx, r.opts.API, target)
			contacts := data.Get("contacts")
			if ok && data.Truthy() && contacts.Truthy() && positive(contacts.Get("length")) {
				c.send(newObject().str("type", "contacts").val("contacts", contacts))
				return
			}
			c.send(newObject().str("type", "contacts").raw("contacts", r.fallbackContacts(c.id, nil)))
		})

	case "get_history":
		conversation := frame.Get("conversation_id")
		c.later(func(ctx context.Context) {
			if !r.agentIn(ctx, c, or(conversation, id)) {
				return
			}
			data, ok := loadHistory(ctx, r.opts.API, or(conversation, id))
			if ok && data.Truthy() && data.Get("messages").Truthy() {
				c.send(newObject().str("type", "history").val("conversation_id", conversation).val("messages", data.Get("messages")))
			}
		})

	case "mark_read":
		if conversation := frame.Get("conversation_id"); conversation.Truthy() {
			body := newObject().val("conversation_id", conversation).str("reader_id", c.id).json()
			r.asAgent(c, conversation, false, func(store func(func(context.Context))) {
				store(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })
			})
		}

	case "create_group":
		// An agent makes groups of its own device and operators only
		if frame.Get("name").Truthy() && frame.Get("member_ids").Truthy() && ownGroupMembers(frame.Get("member_ids"), c.id) {
			body := newObject().val("name", frame.Get("name")).val("members", frame.Get("member_ids")).str("created_by", c.id).json()
			c.later(func(ctx context.Context) {
				group, ok := createGroup(ctx, r.opts.API, body)
				if !ok {
					return
				}
				c.send(newObject().str("type", "group_created").spread(group))
				data, ok := loadGroups(ctx, r.opts.API, id)
				if ok && data.Truthy() && data.Get("groups").Truthy() {
					c.send(newObject().str("type", "groups").val("groups", data.Get("groups")))
				}
			})
		}

	case "key_exchange":
		conversation := or(frame.Get("conversation_id"), id)
		r.asAgent(c, conversation, true, func(func(func(context.Context))) {
			r.toAgentRoom(c, newObject().str("type", "key_exchange").str("from", c.id).val("public_key", frame.Get("public_key")).
				val("conversation_id", conversation))
		})

	case "read_receipt":
		ids := listOr(frame.Get("message_ids"))
		asked := frame.Get("conversation_id")
		r.asAgent(c, or(asked, id), false, func(store func(func(context.Context))) {
			r.toAgentRoom(c, newObject().str("type", "read_receipt").str("from", c.id).raw("message_ids", ids).
				val("conversation_id", or(asked, id)).num("timestamp", r.nowMillis()))
			if asked.Truthy() {
				body := newObject().val("conversation_id", asked).str("reader_id", c.id).raw("message_ids", ids).json()
				store(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })
			}
		})

	case "presence_update":
		r.toAgentRoom(c, newObject().str("type", "presence").str("device_id", c.id).boolean("online", !isFalse(frame.Get("online"))).
			val("status", or(frame.Get("status"), text("available"))).num("timestamp", r.nowMillis()))

	case "file_share":
		conversation := or(frame.Get("conversation_id"), id)
		r.asAgent(c, conversation, true, func(func(func(context.Context))) {
			r.toAgentRoom(c, r.fileShare(frame, text(c.id), text(c.name), conversation))
		})
	}
	return nil
}

// agentIn reports whether a conversation is the agent's own: its device's,
// or one of the groups whose members name its id exactly.
func (r *Relay) agentIn(ctx context.Context, c *client, conversation jsjson.Value) bool {
	asked, err := conversation.Text()
	if err != nil {
		return false
	}
	if asked == c.id {
		return true
	}
	data, ok := loadGroups(ctx, r.opts.API, text(c.id))
	if !ok {
		return false
	}
	for _, group := range data.Get("groups").Items() {
		if g := group.Get("id"); g.Kind() == jsjson.String && g.Str() == asked && isMember(group.Get("members"), c.id) {
			return true
		}
	}
	return false
}

// ownGroupMembers reports whether the members an agent names for a new
// group are a list of its own device id and operators ("operator",
// "operator:<name>"), and nothing else.
func ownGroupMembers(members jsjson.Value, id string) bool {
	if members.Kind() != jsjson.Array {
		return false
	}
	for _, m := range members.Items() {
		if m.Kind() != jsjson.String {
			return false
		}
		if name := m.Str(); name != id && name != "operator" && !strings.HasPrefix(name, "operator:") {
			return false
		}
	}
	return true
}

// isMember reports whether a group's members, a comma-separated text or a
// list, name the id exactly.
func isMember(members jsjson.Value, id string) bool {
	if members.Kind() == jsjson.String {
		for _, m := range strings.Split(members.Str(), ",") {
			if strings.TrimSpace(m) == id {
				return true
			}
		}
		return false
	}
	for _, m := range members.Items() {
		if m.Kind() == jsjson.String && m.Str() == id {
			return true
		}
	}
	return false
}

// toAgentRoom sends what an agent says to its room and to the inbox.
func (r *Relay) toAgentRoom(c *client, o *object) {
	out := o.text()
	r.mu.Lock()
	rm := r.roomLocked(c.id)
	rm.broadcastLocked(out, c.conn)
	r.mu.Unlock()
	r.toPanel(c.api, c.id, c.conn, rm, out)
}

// fileShare is the message that passes on an encrypted file's metadata.
func (r *Relay) fileShare(frame, from, fromName, conversation jsjson.Value) *object {
	return newObject().str("type", "file_share").val("from", from).val("from_name", fromName).val("conversation_id", conversation).
		val("file_id", r.fileID(frame)).
		val("file_name_encrypted", or(frame.Get("file_name_encrypted"), text(""))).
		val("file_size", or(frame.Get("file_size"), jsjson.OfNumber(0))).
		val("encrypted_metadata", or(frame.Get("encrypted_metadata"), text(""))).
		num("timestamp", r.nowMillis())
}

// listOr is v || [] written as JSON.
func listOr(v jsjson.Value) []byte {
	if v.Truthy() {
		b, _ := v.MarshalJSON()
		return b
	}
	return []byte("[]")
}

// isFalse is v === false.
func isFalse(v jsjson.Value) bool {
	return v.Kind() == jsjson.Bool && !v.Truthy()
}

// operatorFrame handles one message of an operator. A message for a
// conversation that has a room other than the operator's goes to that
// room; otherwise an inbox operator's goes to the other inbox operators
// and a room operator's to the room. A frame that names a conversation
// or device the operator does not see is left alone, and the inbox's
// operators get what concerns a conversation only when they see it.
func (r *Relay) operatorFrame(c *client, frame jsjson.Value) error {
	typ, err := typeOf(frame)
	if err != nil {
		return err
	}
	id := text(c.id)
	name := text(c.operator)
	conversation := or(frame.Get("conversation_id"), id)
	switch typ {
	case "hello":
		c.send(r.welcome(c.id))

	case "get_contacts":
		target := or(frame.Get("device_id"), id)
		if !r.sees(c, target) {
			return nil
		}
		c.later(func(ctx context.Context) {
			data, ok := loadContacts(ctx, r.opts.API, target)
			r.sendContacts(ctx, c, data, ok)
		})

	case "get_groups":
		target := or(frame.Get("device_id"), id)
		if !r.sees(c, target) {
			return nil
		}
		c.later(func(ctx context.Context) {
			data, ok := loadGroups(ctx, r.opts.API, target)
			r.sendGroups(c, data, ok)
		})

	case "get_history":
		asked := frame.Get("conversation_id")
		if !r.sees(c, conversation) {
			return nil
		}
		c.later(func(ctx context.Context) {
			data, ok := loadHistory(ctx, r.opts.API, conversation)
			if ok && data.Truthy() && data.Get("messages").Truthy() {
				messages := data.Get("messages")
				if recipient, direct := strings.CutPrefix(subject(conversation), "operator:"); direct && recipient != c.operator {
					messages = sentBy(messages, c.operator)
				}
				c.send(newObject().str("type", "history").val("conversation_id", asked).raw("messages", r.operatorView(ctx, c).messages(messages)))
				return
			}
			if asked.Kind() == jsjson.String && asked.Str() == c.id {
				c.send(newObject().str("type", "history").str("conversation_id", c.id).raw("messages", r.memory(c.id)))
			}
		})

	case "message":
		msgID := r.nowMillis()
		msgText, err := messageText(frame.Get("text"))
		if err != nil {
			return errDrop
		}
		if !r.sees(c, conversation) {
			return nil
		}
		msg := newObject().str("type", "message").num("id", msgID).str("from", "operator").str("from_name", c.operator).
			val("conversation_id", conversation).str("operator", c.operator).val("to_id", conversation).
			str("text", msgText).num("timestamp", r.nowMillis())
		out := msg.text()
		r.mu.Lock()
		rm := r.roomLocked(c.id)
		target := r.findLocked(conversation)
		if target != nil {
			target.appendLocked([]byte(out))
		} else {
			rm.appendLocked([]byte(out))
		}
		panel := r.routeLocked(c, rm, target, out)
		r.mu.Unlock()
		if panel {
			r.toPanel(c.api, subject(conversation), c.conn, nil, out)
		}
		c.send(msg)
		c.persist(c.later, conversation, text("operator"), name, conversation, msgText)

	case "typing":
		r.route(c, conversation, newObject().str("type", "typing").str("from", "operator").str("operator", c.operator).
			val("conversation_id", conversation))

	case "key_exchange":
		r.route(c, conversation, newObject().str("type", "key_exchange").str("from", "operator").str("operator", c.operator).
			val("public_key", frame.Get("public_key")).val("conversation_id", conversation))

	case "read_receipt":
		if !r.sees(c, conversation) {
			return nil
		}
		ids := listOr(frame.Get("message_ids"))
		r.route(c, conversation, newObject().str("type", "read_receipt").str("from", "operator").str("operator", c.operator).
			raw("message_ids", ids).val("conversation_id", conversation).num("timestamp", r.nowMillis()))
		body := newObject().val("conversation_id", conversation).str("reader_id", c.operator).raw("message_ids", ids).json()
		c.later(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })

	case "mark_read":
		if asked := frame.Get("conversation_id"); asked.Truthy() && r.sees(c, asked) {
			body := newObject().val("conversation_id", asked).str("reader_id", c.operator).json()
			c.later(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })
		}

	case "create_group":
		if frame.Get("name").Truthy() && frame.Get("member_ids").Kind() == jsjson.Array {
			groupName, err := frame.Get("name").Text()
			if err != nil {
				return errDrop
			}
			body := newObject().str("name", jsjson.Slice(groupName, 120)).val("members", frame.Get("member_ids")).
				str("created_by", c.operator).json()
			c.later(func(ctx context.Context) {
				group, ok := createGroup(ctx, r.opts.API, body)
				if !ok {
					return
				}
				c.send(newObject().str("type", "group_created").spread(group))
				data, ok := loadGroups(ctx, r.opts.API, id)
				r.sendGroups(c, data, ok)
			})
		}

	case "presence_update":
		presence := newObject().str("type", "presence").str("device_id", "operator:"+c.operator).
			boolean("online", !isFalse(frame.Get("online"))).val("status", or(frame.Get("status"), text("available"))).
			num("timestamp", r.nowMillis()).text()
		if c.id == panelID {
			r.toPanel(c.api, "", c.conn, nil, presence)
			return nil
		}
		r.mu.Lock()
		r.roomLocked(c.id).broadcastLocked(presence, c.conn)
		r.mu.Unlock()

	case "reaction":
		return r.reaction(c, frame)

	case "file_share":
		if !r.sees(c, conversation) {
			return nil
		}
		r.linkFile(c, frame.Get("file_id"), conversation)
		r.route(c, conversation, r.fileShare(frame, text("operator"), name, conversation))
	}
	return nil
}

// linkFile notes that the operator shared a chat file into a
// conversation, so the accounts that see the conversation may download it
// (shareFile).
func (r *Relay) linkFile(c *client, fileID, conversation jsjson.Value) {
	if fileID.Kind() != jsjson.String || conversation.Kind() != jsjson.String {
		return
	}
	err := shareFile(c.conn.Context(), r.opts.FilesDir, r.opts.Scope, c.user, lowerASCII(fileID.Str()), conversation.Str())
	if err != nil {
		logging.Logf("console", "WARN: [Chat] Failed to link a shared file: %v", err)
	}
}

// route sends what an operator says (typing, keys, receipts, files) where
// its conversation is (see operatorFrame), when the operator sees it.
func (r *Relay) route(c *client, conversation jsjson.Value, o *object) {
	if !r.sees(c, conversation) {
		return
	}
	out := o.text()
	r.mu.Lock()
	panel := r.routeLocked(c, r.roomLocked(c.id), r.findLocked(conversation), out)
	r.mu.Unlock()
	if panel {
		r.toPanel(c.api, subject(conversation), c.conn, nil, out)
	}
}

// routeLocked sends out to the room of the conversation or to the
// operator's room, or reports that it goes to the inbox.
func (r *Relay) routeLocked(c *client, rm, target *room, out string) (toPanel bool) {
	switch {
	case target != nil && target != rm:
		target.broadcastLocked(out, c.conn)
	case c.id == panelID:
		return true
	default:
		rm.broadcastLocked(out, c.conn)
	}
	return false
}

// subject is the text of the conversation id a message is about, "" when
// it has none.
func subject(conversation jsjson.Value) string {
	s, err := conversation.Text()
	if err != nil {
		return ""
	}
	return s
}

// reaction passes an operator's reaction to a message on: to the room of
// its conversation, to the inbox or the operator's room, and back to the
// operator. Reactions are not stored.
func (r *Relay) reaction(c *client, frame jsjson.Value) error {
	conversation, err := or(frame.Get("conversation_id"), text(c.id)).Text()
	if err != nil {
		return errDrop
	}
	conversation = jsjson.Slice(conversation, 128)
	messageID, err := or(frame.Get("message_id"), text("")).Text()
	if err != nil {
		return errDrop
	}
	messageID = jsjson.Slice(messageID, 64)
	emoji, err := or(frame.Get("emoji"), text("")).Text()
	if err != nil {
		return errDrop
	}
	emoji = jsjson.Slice(emoji, 16)
	if messageID == "" || emoji == "" || !r.sees(c, text(conversation)) {
		return nil
	}
	reaction := newObject().str("type", "reaction").str("from", c.operator).str("operator", c.operator).
		str("conversation_id", conversation).str("message_id", messageID).str("emoji", emoji).num("timestamp", r.nowMillis())
	out := reaction.text()
	r.mu.Lock()
	rm := r.roomLocked(c.id)
	if target := r.rooms[conversation]; target != nil && target != rm {
		target.broadcastLocked(out, c.conn)
	}
	if c.id != panelID {
		rm.broadcastLocked(out, c.conn)
	}
	r.mu.Unlock()
	if c.id == panelID {
		r.toPanel(c.api, conversation, c.conn, nil, out)
	}
	c.send(reaction)
	return nil
}
