package chat

import (
	"context"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// searchWorld is an inbox world with messages in every kind of
// conversation that have "printer" in them.
func searchWorld(t *testing.T, db *store.DB) *inboxWorld {
	t.Helper()
	ctx := context.Background()
	w := newInboxWorld(t, db)
	w.legacy([3]string{"operator:ghost", "anna", "old printer note"})
	post := func(conv string, from chatstore.Party, text string) chatstore.Message {
		m, err := w.st.Post(ctx, chatstore.Message{ConversationID: conv, From: from, Text: text}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	anna, ben, cleo, admin := chatstore.User(5, "anna"), chatstore.User(6, "ben"), chatstore.User(8, "cleo"), chatstore.User(1, "admin")
	post("dev1", chatstore.Device("dev1", "reception-01"), "The printer blinks orange")
	post("dev2", chatstore.Device("dev2", "warehouse"), "printer jammed again")
	post("dm:5:6", anna, "Can you look at the printer at reception?")
	post("dm:1:8", admin, "printer budget, between us")
	group, err := w.st.NewGroup(ctx, "Night shift", 6, []int64{6, 8})
	if err != nil {
		t.Fatal(err)
	}
	post(group.ID, ben, "the printer group")
	gone := post("dev1", chatstore.Device("dev1", "reception-01"), "printer password is hunter2")
	if _, err := w.st.Delete(ctx, gone.ID, cleo, true); err != nil {
		t.Fatal(err)
	}
	post("dev1", chatstore.Device("dev1", "reception-01"), "battery at 100% now")
	post("dev1", chatstore.Device("dev1", "reception-01"), "battery at 100 percent")
	return w
}

// found are the texts of a search's messages, newest first, and its
// conversations' names by id.
func (w *inboxWorld) search(who string, query url.Values) (int, []string, map[string]string) {
	w.t.Helper()
	status, out := w.get(who, "/api/chat/search?"+query.Encode())
	var texts []string
	list, _ := out["messages"].([]any)
	for _, m := range list {
		text, _ := m.(map[string]any)["text"].(string)
		texts = append(texts, text)
	}
	names := map[string]string{}
	convs, _ := out["conversations"].(map[string]any)
	for id, c := range convs {
		names[id], _ = c.(map[string]any)["name"].(string)
	}
	return status, texts, names
}

// TestSearchFindsWhatTheAccountReads: search finds messages by their text
// in the conversations the account reads, and nowhere else (the lead's
// call on question 2): Support in the computers of its scope, Team in its
// own direct conversations and groups, old messages for admins. Deleted
// messages are never found. The answer names each conversation.
func TestSearchFindsWhatTheAccountReads(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := searchWorld(t, db)
		q := func(pairs ...string) url.Values {
			v := url.Values{}
			for i := 0; i+1 < len(pairs); i += 2 {
				v.Set(pairs[i], pairs[i+1])
			}
			return v
		}
		for who, want := range map[string][]string{
			// anna sees dev1 and is in dm:5:6
			"anna": {"Can you look at the printer at reception?", "The printer blinks orange"},
			// ben sees dev2 and is in dm:5:6 and the group
			"ben": {"the printer group", "Can you look at the printer at reception?", "printer jammed again"},
			// admin sees every computer, is in dm:1:8 and reads old messages
			"admin": {"printer budget, between us", "printer jammed again", "The printer blinks orange", "old printer note"},
		} {
			status, got, _ := w.search(who, q("q", "PRINTER"))
			if status != 200 || !slices.Equal(got, want) {
				t.Errorf("%s found %d %q, want %q", who, status, got, want)
			}
		}

		_, got, names := w.search("anna", q("q", "printer"))
		if len(got) != 2 || names["dev1"] != "dev1" || names["dm:5:6"] != "ben" {
			t.Errorf("anna's conversations: %v", names)
		}
		if _, got, _ = w.search("anna", q("q", "printer", "section", "team")); !slices.Equal(got, []string{"Can you look at the printer at reception?"}) {
			t.Errorf("anna in Team: %q", got)
		}
		if _, got, _ = w.search("anna", q("q", "printer", "conversation_id", "dev1")); !slices.Equal(got, []string{"The printer blinks orange"}) {
			t.Errorf("anna in dev1: %q", got)
		}
		if _, got, _ = w.search("anna", q("q", "printer", "conversation_id", "dev2")); len(got) != 0 {
			t.Errorf("anna in dev2, which she doesn't see: %q", got)
		}
		// % and _ are text, not patterns
		if _, got, _ = w.search("admin", q("q", "0%")); !slices.Equal(got, []string{"battery at 100% now"}) {
			t.Errorf("a search for 0%%: %q", got)
		}
		if _, got, _ = w.search("admin", q("q", "hunter2")); len(got) != 0 {
			t.Errorf("a deleted message was found: %q", got)
		}
		if _, got, _ = w.search("admin", q("q", "p")); len(got) != 0 {
			t.Errorf("one character found %q", got)
		}
	})
}

// TestSearchNeedsChat: search needs a session and Support chat or Team
// chat.
func TestSearchNeedsChat(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newInboxWorld(t, db)
		for role, want := range map[string]int{"": 401, "server_admin": 403, "guest_issuer": 403, "viewer": 200} {
			r := httptest.NewRequest("GET", "/api/chat/search?q=printer", nil)
			if role != "" {
				r.Header.Set("X-Test-Role", role)
			}
			rec := httptest.NewRecorder()
			w.handler.ServeHTTP(rec, r)
			if rec.Code != want {
				t.Errorf("search as %q: %d %s, want %d", role, rec.Code, rec.Body, want)
			}
		}
	})
}
