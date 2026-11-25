package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// hubRelay is a relay with the chat of version 2 on a test database with
// the scope accounts: anna sees dev1, ben dev2, cleo (a viewer) dev1,
// admin every device.
func hubRelay(t *testing.T, db *store.DB) (*testRelay, *chatstore.Store) {
	t.Helper()
	if err := chatstore.Ensure(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	st := chatstore.New(db, nil)
	scope := realScope(t, db)
	hub := NewHub(HubOptions{Store: st, Scope: scope, IsAdmin: func(role string) bool { return role == "admin" }})
	return startRelay(t, RelayOptions{Scope: scope, Hub: hub}), st
}

// v2 opens a socket of version 2 and takes its welcome.
func (tr *testRelay) v2(t *testing.T, who string) *testSocket {
	t.Helper()
	path := "/ws/chat-operator/panel?v=2"
	if strings.HasPrefix(who, "dev") {
		path = "/ws/chat/" + who + "?token=tok-" + who + "&v=2"
	}
	account := who
	if strings.HasPrefix(who, "dev") {
		account = ""
	}
	s, status := tr.dial(t, account, path)
	if s == nil {
		t.Fatalf("%s: refused with %d", who, status)
	}
	got := s.takeAtLeast(1)
	if !strings.Contains(got[0], `"type":"welcome"`) || !strings.Contains(got[0], `"v":2`) {
		t.Fatalf("%s: first frames %q", who, got)
	}
	// What came after the welcome stays for the test to take
	s.mu.Lock()
	s.got = append(got[1:], s.got...)
	s.mu.Unlock()
	return s
}

// frames are the frames of the types given, as decoded JSON.
func frames(got []string, types ...string) []map[string]any {
	var out []map[string]any
	for _, g := range got {
		var f map[string]any
		if json.Unmarshal([]byte(g), &f) != nil {
			continue
		}
		for _, typ := range types {
			if f["type"] == typ {
				out = append(out, f)
			}
		}
	}
	return out
}

func textOf(f map[string]any) string {
	m, _ := f["message"].(map[string]any)
	s, _ := m["text"].(string)
	return s
}

// TestSupportMessagesReachTheDeviceAndWhoSeesIt: a device's message
// reaches the console users who see the device and nobody else; an answer
// reaches the device; the device learns what the console read.
func TestSupportMessagesReachTheDeviceAndWhoSeesIt(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		relay, _ := hubRelay(t, db)
		anna, ben, admin := relay.v2(t, "anna"), relay.v2(t, "ben"), relay.v2(t, "admin")
		dev1 := relay.v2(t, "dev1")
		// dev1's empty history and its helper (nobody yet)
		if got := frames(dev1.takeAtLeast(2), "history", "helper"); len(got) != 2 || got[1]["name"] != nil {
			t.Fatalf("dev1 at the start: %v", got)
		}
		anna.take()
		ben.take()
		admin.take()

		dev1.send(t, `{"type":"send","text":"my printer stopped","client_id":"c1"}`)
		got := frames(anna.takeAtLeast(1), "message.new")
		if len(got) != 1 || textOf(got[0]) != "my printer stopped" {
			t.Fatalf("anna got %v", got)
		}
		msg := got[0]["message"].(map[string]any)
		if msg["conversation_id"] != "dev1" || msg["from"].(map[string]any)["kind"] != "device" || msg["from"].(map[string]any)["name"] != "dev1" {
			t.Errorf("the message: %v", msg)
		}
		if got := frames(admin.takeAtLeast(1), "message.new"); len(got) != 1 {
			t.Errorf("admin got %v", got)
		}
		if got := frames(ben.take(), "message.new"); len(got) != 0 {
			t.Errorf("ben, who doesn't see dev1, got %v", got)
		}
		if got := frames(dev1.takeAtLeast(1), "message.new"); len(got) != 1 || msg["client_id"] != "c1" {
			t.Errorf("dev1's own message back: %v", got)
		}

		anna.send(t, `{"type":"send","conversation_id":"dev1","text":"looking at it","reply_to":"`+msg["id"].(string)+`"}`)
		got = frames(dev1.takeAtLeast(1), "message.new")
		if len(got) != 1 || textOf(got[0]) != "looking at it" {
			t.Fatalf("dev1 got %v", got)
		}
		reply := got[0]["message"].(map[string]any)["reply_to"].(map[string]any)
		if reply["preview"] != "my printer stopped" {
			t.Errorf("the reply's quote: %v", reply)
		}
		anna.send(t, `{"type":"read","conversation_id":"dev1"}`)
		if got := frames(dev1.takeAtLeast(1), "read"); len(got) != 1 {
			t.Errorf("dev1 didn't learn anna read: %v", got)
		}

		// Nobody writes into a device's conversation it doesn't see
		ben.take()
		ben.send(t, `{"type":"send","conversation_id":"dev1","text":"sneaky"}`)
		if got := frames(ben.takeAtLeast(1), "error"); len(got) != 1 || got[0]["code"] != "no_access" {
			t.Errorf("ben writing to dev1: %v", got)
		}
		// A device writes into its own conversation only, whatever it names
		dev1.take()
		dev1.send(t, `{"type":"send","conversation_id":"dev2","text":"to dev2?"}`)
		if got := frames(ben.take(), "message.new"); len(got) != 0 {
			t.Errorf("dev1 reached dev2's conversation: %v", got)
		}
	})
}

// TestDirectMessagesOfVersion2: a direct message reaches its two people,
// both of them in every tab; anybody else neither gets it nor writes into
// it; edits and deletions follow the sender and admins.
func TestDirectMessagesOfVersion2(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		relay, st := hubRelay(t, db)
		if _, err := st.Direct(context.Background(), 5, 6); err != nil {
			t.Fatal(err)
		}
		anna, annaTab, ben, cleo, admin := relay.v2(t, "anna"), relay.v2(t, "anna"), relay.v2(t, "ben"), relay.v2(t, "cleo"), relay.v2(t, "admin")
		for _, s := range []*testSocket{anna, annaTab, ben, cleo, admin} {
			s.take()
		}
		anna.send(t, `{"type":"send","conversation_id":"dm:5:6","text":"lunch?"}`)
		for name, s := range map[string]*testSocket{"anna": anna, "anna's other tab": annaTab, "ben": ben} {
			if got := frames(s.takeAtLeast(1), "message.new"); len(got) != 1 || textOf(got[0]) != "lunch?" {
				t.Errorf("%s: %v", name, got)
			}
		}
		for name, s := range map[string]*testSocket{"cleo": cleo, "admin": admin} {
			if got := frames(s.take(), "message.new"); len(got) != 0 {
				t.Errorf("%s got anna and ben's message: %v", name, got)
			}
		}
		cleo.send(t, `{"type":"history","conversation_id":"dm:5:6"}`)
		if got := frames(cleo.takeAtLeast(1), "error", "history"); len(got) != 1 || got[0]["code"] != "no_access" {
			t.Errorf("cleo reading the history: %v", got)
		}
		list, _ := st.History(context.Background(), "dm:5:6", 0, 10)
		id := list[0].ID
		idText := `"` + strings.TrimSpace(jsonNumber(id)) + `"`
		ben.send(t, `{"type":"edit","message_id":`+idText+`,"text":"hijack"}`)
		if got := frames(ben.takeAtLeast(1), "error"); len(got) != 1 || got[0]["code"] != "not_yours" {
			t.Errorf("ben editing anna's message: %v", got)
		}
		anna.send(t, `{"type":"edit","message_id":`+idText+`,"text":"lunch at 12?"}`)
		got := frames(ben.takeAtLeast(1), "message.updated")
		if len(got) != 1 || textOf(got[0]) != "lunch at 12?" || got[0]["message"].(map[string]any)["edited_at"] == nil {
			t.Errorf("the edit at ben's: %v", got)
		}
		ben.send(t, `{"type":"delete","message_id":`+idText+`}`)
		if got := frames(ben.takeAtLeast(1), "error"); len(got) != 1 || got[0]["code"] != "not_yours" {
			t.Errorf("ben deleting anna's message: %v", got)
		}
		anna.send(t, `{"type":"delete","message_id":`+idText+`}`)
		got = frames(ben.takeAtLeast(1), "message.updated")
		if len(got) != 1 || got[0]["message"].(map[string]any)["deleted"] != true || textOf(got[0]) != "" {
			t.Errorf("the deletion at ben's: %v", got)
		}
	})
}

// TestVersion1SocketsGetMessagesOfVersion2: an agent of version 1 and the
// old inbox still get what a console user of version 2 writes to the
// device, as frames of version 1.
func TestVersion1SocketsGetMessagesOfVersion2(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		relay, _ := hubRelay(t, db)
		relay.api.answer("GET /chat/contacts/panel", `{"contacts":[]}`)
		old := relay.agent(t, "dev1")
		oldInbox := relay.open(t, "admin", "panel")
		old.take()
		oldInbox.take()
		anna := relay.v2(t, "anna")
		anna.send(t, `{"type":"send","conversation_id":"dev1","text":"hello from version 2"}`)
		got := old.takeAtLeast(1)
		if len(got) != 1 || !strings.Contains(got[0], `"type":"message","id":`) || !strings.Contains(got[0], `"from":"operator","from_name":"anna"`) ||
			!strings.Contains(got[0], `"text":"hello from version 2"`) {
			t.Errorf("the agent of version 1 got %q", got)
		}
		if got := oldInbox.takeAtLeast(1); len(got) != 1 || !strings.Contains(got[0], `"text":"hello from version 2"`) {
			t.Errorf("the inbox of version 1 got %q", got)
		}
	})
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
