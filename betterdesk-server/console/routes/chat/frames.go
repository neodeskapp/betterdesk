package chat

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
)

// capabilities are what the welcome message says the relay can do.
var capabilities = []string{"multi_conversation", "contacts", "groups", "history", "e2e_encryption", "read_receipts", "typing", "presence", "file_share"}

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
// made of it.
func (c *client) persist(conversation, from, fromName, toID jsjson.Value, msgText string) {
	body := newObject().
		val("conversation_id", or(conversation, or(from, text("operator")))).
		val("from_id", or(from, text("unknown"))).
		val("from_name", or(fromName, or(from, text("")))).
		val("to_id", or(toID, text(""))).
		str("text", msgText).json()
	c.later(func(ctx context.Context) { saveMessage(ctx, c.relay.opts.API, body) })
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
		fromName := or(frame.Get("from_name"), id)
		conversation := or(frame.Get("conversation_id"), text("operator"))
		msgText, err := messageText(frame.Get("text"))
		if err != nil {
			return errDrop
		}
		msg := newObject().str("type", "message").num("id", msgID).str("from", c.id).val("from_name", fromName).
			val("conversation_id", conversation).str("text", msgText).val("timestamp", or(frame.Get("timestamp"), jsjson.OfNumber(r.nowMillis())))
		out := msg.json()
		r.mu.Lock()
		rm := r.roomLocked(c.id)
		rm.appendLocked(out)
		rm.broadcastLocked(string(out), c.conn)
		r.toPanelLocked(string(out), c.conn, rm)
		r.mu.Unlock()
		c.persist(conversation, id, fromName, jsjson.Value{}, msgText)

	case "typing":
		r.toAgentRoom(c, newObject().str("type", "typing").str("from", c.id).
			val("conversation_id", or(frame.Get("conversation_id"), text("operator"))))

	case "get_contacts":
		target := or(frame.Get("device_id"), id)
		c.later(func(ctx context.Context) {
			data, ok := loadContacts(ctx, r.opts.API, target)
			contacts := data.Get("contacts")
			if ok && data.Truthy() && contacts.Truthy() && positive(contacts.Get("length")) {
				c.send(newObject().str("type", "contacts").val("contacts", contacts))
				return
			}
			c.send(newObject().str("type", "contacts").raw("contacts", r.fallbackContacts(c.id)))
		})

	case "get_history":
		conversation := frame.Get("conversation_id")
		c.later(func(ctx context.Context) {
			data, ok := loadHistory(ctx, r.opts.API, or(conversation, id))
			if ok && data.Truthy() && data.Get("messages").Truthy() {
				c.send(newObject().str("type", "history").val("conversation_id", conversation).val("messages", data.Get("messages")))
			}
		})

	case "mark_read":
		if conversation := frame.Get("conversation_id"); conversation.Truthy() {
			body := newObject().val("conversation_id", conversation).str("reader_id", c.id).json()
			c.later(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })
		}

	case "create_group":
		if frame.Get("name").Truthy() && frame.Get("member_ids").Truthy() {
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
		r.toAgentRoom(c, newObject().str("type", "key_exchange").str("from", c.id).val("public_key", frame.Get("public_key")).
			val("conversation_id", or(frame.Get("conversation_id"), id)))

	case "read_receipt":
		ids := listOr(frame.Get("message_ids"))
		r.toAgentRoom(c, newObject().str("type", "read_receipt").str("from", c.id).raw("message_ids", ids).
			val("conversation_id", or(frame.Get("conversation_id"), id)).num("timestamp", r.nowMillis()))
		if conversation := frame.Get("conversation_id"); conversation.Truthy() {
			body := newObject().val("conversation_id", conversation).str("reader_id", c.id).raw("message_ids", ids).json()
			c.later(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })
		}

	case "presence_update":
		r.toAgentRoom(c, newObject().str("type", "presence").str("device_id", c.id).boolean("online", !isFalse(frame.Get("online"))).
			val("status", or(frame.Get("status"), text("available"))).num("timestamp", r.nowMillis()))

	case "file_share":
		r.toAgentRoom(c, r.fileShare(frame, text(c.id), or(frame.Get("from_name"), id), or(frame.Get("conversation_id"), text("operator"))))
	}
	return nil
}

// toAgentRoom sends what an agent says to its room and to the inbox.
func (r *Relay) toAgentRoom(c *client, o *object) {
	out := o.text()
	r.mu.Lock()
	defer r.mu.Unlock()
	rm := r.roomLocked(c.id)
	rm.broadcastLocked(out, c.conn)
	r.toPanelLocked(out, c.conn, rm)
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
// and a room operator's to the room.
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
		c.later(func(ctx context.Context) {
			data, ok := loadContacts(ctx, r.opts.API, target)
			r.sendContacts(c, data, ok)
		})

	case "get_groups":
		target := or(frame.Get("device_id"), id)
		c.later(func(ctx context.Context) {
			data, ok := loadGroups(ctx, r.opts.API, target)
			r.sendGroups(c, data, ok)
		})

	case "get_history":
		asked := frame.Get("conversation_id")
		c.later(func(ctx context.Context) {
			data, ok := loadHistory(ctx, r.opts.API, conversation)
			if ok && data.Truthy() && data.Get("messages").Truthy() {
				c.send(newObject().str("type", "history").val("conversation_id", asked).val("messages", data.Get("messages")))
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
		r.routeLocked(c, rm, target, out)
		r.mu.Unlock()
		c.send(msg)
		c.persist(conversation, text("operator"), name, conversation, msgText)

	case "typing":
		r.route(c, conversation, newObject().str("type", "typing").str("from", "operator").str("operator", c.operator).
			val("conversation_id", conversation))

	case "key_exchange":
		r.route(c, conversation, newObject().str("type", "key_exchange").str("from", "operator").str("operator", c.operator).
			val("public_key", frame.Get("public_key")).val("conversation_id", conversation))

	case "read_receipt":
		ids := listOr(frame.Get("message_ids"))
		r.route(c, conversation, newObject().str("type", "read_receipt").str("from", "operator").str("operator", c.operator).
			raw("message_ids", ids).val("conversation_id", conversation).num("timestamp", r.nowMillis()))
		body := newObject().val("conversation_id", conversation).str("reader_id", c.operator).raw("message_ids", ids).json()
		c.later(func(ctx context.Context) { markRead(ctx, r.opts.API, body) })

	case "mark_read":
		if asked := frame.Get("conversation_id"); asked.Truthy() {
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
		r.mu.Lock()
		if c.id == panelID {
			r.toPanelLocked(presence, c.conn, nil)
		} else {
			r.roomLocked(c.id).broadcastLocked(presence, c.conn)
		}
		r.mu.Unlock()

	case "reaction":
		return r.reaction(c, frame)

	case "file_share":
		r.route(c, conversation, r.fileShare(frame, text("operator"), name, conversation))
	}
	return nil
}

// route sends what an operator says (typing, keys, receipts, files) where
// its conversation is: see operatorFrame.
func (r *Relay) route(c *client, conversation jsjson.Value, o *object) {
	out := o.text()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routeLocked(c, r.roomLocked(c.id), r.findLocked(conversation), out)
}

func (r *Relay) routeLocked(c *client, rm, target *room, out string) {
	switch {
	case target != nil && target != rm:
		target.broadcastLocked(out, c.conn)
	case c.id == panelID:
		r.toPanelLocked(out, c.conn, nil)
	default:
		rm.broadcastLocked(out, c.conn)
	}
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
	if messageID == "" || emoji == "" {
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
	if c.id == panelID {
		r.toPanelLocked(out, c.conn, nil)
	} else {
		rm.broadcastLocked(out, c.conn)
	}
	r.mu.Unlock()
	c.send(reaction)
	return nil
}
