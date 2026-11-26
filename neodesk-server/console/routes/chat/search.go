package chat

import (
	"context"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// Search over the text of messages (A242, the lead's call on question 2),
// on the server, in the conversations the account reads: Support in the
// computers it sees (with Support chat), Team in its own direct
// conversations and groups (with Team chat), and for admins the old
// messages of version 1. Deleted messages and lines of the system are
// never found.

const (
	// searchMinRunes is the shortest text searched for
	searchMinRunes = 2
	// searchMaxRunes is where a longer text is cut
	searchMaxRunes = 200
	// searchResults is the most messages an answer has
	searchResults = 50
	// searchScan is the most matching messages looked at for one answer
	searchScan = 2000
	// searchPage is how many matching messages one query reads
	searchPage = 200
)

// wireFoundConversation names a conversation a search found messages in.
type wireFoundConversation struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// search answers GET /api/chat/search?q=<text>[&section=support|team]
// [&conversation_id=<id>]: up to 50 messages that have the text, newest
// first, as the wire has them, and the names of their conversations by
// id. Case does not count for Latin letters; % and _ are text. A text
// shorter than two characters finds nothing; a longer one than 200 is cut.
func (h *inboxRoutes) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := h.user(r)
	query := r.URL.Query()
	text := []rune(strings.TrimSpace(query.Get("q")))
	f := &finder{h: h, user: user, ctx: ctx, section: query.Get("section"),
		names: map[string]wireFoundConversation{}, reads: map[string]bool{}}
	found := []chatstore.Message{}
	if len(text) >= searchMinRunes && user != nil {
		if len(text) > searchMaxRunes {
			text = text[:searchMaxRunes]
		}
		var err error
		if found, err = f.find(string(text), strings.TrimSpace(query.Get("conversation_id"))); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	kit.JSON(w, r, http.StatusOK, map[string]any{"success": true, "messages": messagesOut(ctx, h.store(), found), "conversations": f.names})
}

// finder is one search's view of what the account reads.
type finder struct {
	h       *inboxRoutes
	user    *session.User
	ctx     context.Context
	section string
	// devices the account sees and the accounts, read on first need
	devices map[string]wireDevice
	people  map[int64]wirePerson
	names   map[string]wireFoundConversation
	reads   map[string]bool
}

// find pages through the matching messages, newest first, and keeps those
// of conversations the account reads.
func (f *finder) find(text, conversation string) ([]chatstore.Message, error) {
	out := []chatstore.Message{}
	var before int64
	for scanned := 0; len(out) < searchResults && scanned < searchScan; {
		list, err := f.h.store().Find(f.ctx, chatstore.Search{Text: text, Conversation: conversation, Before: before, Limit: searchPage})
		if err != nil {
			return nil, err
		}
		for _, m := range list {
			scanned++
			before = m.ID
			if f.mayRead(m.ConversationID) {
				out = append(out, m)
				if len(out) == searchResults {
					break
				}
			}
		}
		if len(list) < searchPage {
			break
		}
	}
	return out, nil
}

// mayRead reports whether the account reads a conversation in the section
// asked for, and names the conversation for the answer.
func (f *finder) mayRead(conv string) bool {
	if v, ok := f.reads[conv]; ok {
		return v
	}
	v := f.check(conv)
	f.reads[conv] = v
	return v
}

func (f *finder) check(conv string) bool {
	kind := chatstore.KindOf(conv)
	switch kind {
	case chatstore.ConvSupport:
		// The computers the inbox lists: ones the account sees
		if f.section == "team" || !f.h.hub.holds(f.ctx, f.user.Role, permSupport) {
			return false
		}
		if f.devices == nil {
			f.devices = f.h.devicesSeen(f.ctx, f.user)
		}
		d, ok := f.devices[conv]
		if !ok {
			return false
		}
		f.names[conv] = wireFoundConversation{ID: conv, Kind: kind, Name: d.Name}
		return true
	case chatstore.ConvDirect, chatstore.ConvGroup, chatstore.ConvTeam, chatstore.ConvOld:
		if f.section == "support" || !f.h.hub.MayRead(f.ctx, f.user, conv) {
			return false
		}
		c, err := f.h.store().Conversation(f.ctx, conv)
		if err != nil {
			return false
		}
		name := c.Title
		if a, b, ok := chatstore.DirectPeople(conv); ok {
			if f.people == nil {
				f.people, _ = f.h.people(f.ctx)
			}
			other := a
			if a == f.user.ID {
				other = b
			}
			name = f.people[other].Name
		}
		f.names[conv] = wireFoundConversation{ID: conv, Kind: kind, Name: name}
		return true
	}
	return false
}
