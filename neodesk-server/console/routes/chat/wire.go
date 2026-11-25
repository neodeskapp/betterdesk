package chat

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
)

// The chat of version 2 (A242) speaks plain JSON on both sockets:
// /ws/chat-operator/panel?v=2 for the console and /ws/chat/<device>?v=2
// for a device's chat window. neodesk-server/console/routes/chat/PROTOCOL.md describes every frame.
// A socket without v=2 keeps the frames of version 1 (relay.go,
// frames.go).

// wireParty is a sender, reader or reactor on the wire.
type wireParty struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func partyOut(p chatstore.Party) wireParty { return wireParty{Kind: p.Kind, ID: p.ID, Name: p.Name} }

// wireReply is the message a reply answers, as a line to show above it.
type wireReply struct {
	ID       string `json:"id"`
	FromName string `json:"from_name"`
	Preview  string `json:"preview"`
	Deleted  bool   `json:"deleted,omitempty"`
}

// wireAttachment is a file of a message.
type wireAttachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	URL    string `json:"url"`
}

// wireMessage is a message on the wire.
type wireMessage struct {
	ID             string                 `json:"id"`
	ConversationID string                 `json:"conversation_id"`
	From           wireParty              `json:"from"`
	Text           string                 `json:"text"`
	ReplyTo        *wireReply             `json:"reply_to"`
	Attachments    []wireAttachment       `json:"attachments"`
	CreatedAt      int64                  `json:"created_at"`
	EditedAt       *int64                 `json:"edited_at"`
	Deleted        bool                   `json:"deleted"`
	Reactions      map[string][]wireParty `json:"reactions"`
	ClientID       string                 `json:"client_id,omitempty"`
	// Event is what a line of the system says, for the console to word:
	// {"event":"assigned","by":"Anna","to":"Ben"} and the like
	Event json.RawMessage `json:"event,omitempty"`
}

// previewLength is how many characters of a replied message travel with
// the reply.
const previewLength = 140

// messageOut is a message on the wire. replies are messages the reply
// may name, by id; one that is not among them is read from the store.
func messageOut(ctx context.Context, store *chatstore.Store, m chatstore.Message, replies map[int64]chatstore.Message) wireMessage {
	out := wireMessage{
		ID:             strconv.FormatInt(m.ID, 10),
		ConversationID: m.ConversationID,
		From:           partyOut(m.From),
		Text:           m.Text,
		Attachments:    []wireAttachment{},
		CreatedAt:      m.CreatedMs,
		Deleted:        m.Deleted(),
		Reactions:      map[string][]wireParty{},
		ClientID:       m.ClientID,
	}
	if m.EditedMs != 0 {
		edited := m.EditedMs
		out.EditedAt = &edited
	}
	for _, a := range m.Attachments {
		out.Attachments = append(out.Attachments, wireAttachment{ID: a.ID, Name: a.Name, Type: a.Type, Size: a.Size,
			Width: a.Width, Height: a.Height, URL: "/api/chat/files/" + a.ID})
	}
	for emoji, who := range m.Reactions {
		for _, p := range who {
			out.Reactions[emoji] = append(out.Reactions[emoji], partyOut(p))
		}
	}
	if m.From.Kind == chatstore.KindSystem && json.Valid([]byte(m.Text)) && strings.HasPrefix(m.Text, "{") {
		out.Event = json.RawMessage(m.Text)
		out.Text = ""
	}
	if m.ReplyTo != 0 {
		r, ok := replies[m.ReplyTo]
		if !ok && store != nil {
			if got, err := store.Get(ctx, m.ReplyTo); err == nil {
				r, ok = got, true
			}
		}
		if ok {
			preview := r.Text
			if preview == "" && len(r.Attachments) > 0 {
				preview = r.Attachments[0].Name
			}
			out.ReplyTo = &wireReply{ID: strconv.FormatInt(r.ID, 10), FromName: r.From.Name,
				Preview: cutChars(preview, previewLength), Deleted: r.Deleted()}
		}
	}
	return out
}

// messagesOut is a page of history on the wire.
func messagesOut(ctx context.Context, store *chatstore.Store, list []chatstore.Message) []wireMessage {
	byID := make(map[int64]chatstore.Message, len(list))
	for _, m := range list {
		byID[m.ID] = m
	}
	out := make([]wireMessage, 0, len(list))
	for _, m := range list {
		out = append(out, messageOut(ctx, store, m, byID))
	}
	return out
}

// cutChars keeps the first n characters of s.
func cutChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// frame is a frame of version 2 as JSON text.
func frame(typ string, fields map[string]any) string {
	out := map[string]any{"type": typ}
	for k, v := range fields {
		out[k] = v
	}
	b, _ := json.Marshal(out)
	return string(b)
}
