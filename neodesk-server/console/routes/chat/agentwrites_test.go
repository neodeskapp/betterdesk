package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// postsAfter waits until the API server got n POST calls, or a moment
// longer, and returns them.
func postsAfter(t *testing.T, tr *testRelay, n int) []string {
	t.Helper()
	var got []string
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < n && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		got = append(got, tr.api.takenPosts()...)
	}
	time.Sleep(150 * time.Millisecond)
	return append(got, tr.api.takenPosts()...)
}

// TestAgentWritesIntoItsOwnConversations: an agent writes messages into
// its device's conversation, the operators' and the groups it is a member
// of (exactly, not as part of another id), and marks read only its
// device's and its groups' conversations. Its messages carry its device's
// name, whatever name the frame gives (BUGS RV163).
func TestAgentWritesIntoItsOwnConversations(t *testing.T) {
	relay := startRelay(t, RelayOptions{Scope: Scope{Device: func(_ context.Context, id string) *goapi.Object {
		if id != "devA" {
			return nil
		}
		o := goapi.NewObject()
		o.Set("id", "devA")
		o.Set("hostname", "office-pc")
		return o
	}}})
	relay.api.answer("GET /chat/groups/devA", `{"groups":[{"id":"group:g1","members":"devA,devB"},{"id":"group:g2","members":"xdevAx,devC"}]}`)
	relay.api.answer("POST /chat/messages", `{"ok":true}`)
	relay.api.answer("POST /chat/read", `{"ok":true}`)
	op := relay.open(t, "admin", "devB")
	dev := relay.agent(t, "devA")
	dev.take()
	op.take()

	for _, frame := range []string{
		`{"type":"message","conversation_id":"devB","from_name":"Support","text":"into devB"}`,
		`{"type":"message","conversation_id":"group:g2","text":"into g2"}`,
		`{"type":"mark_read","conversation_id":"devB"}`,
		`{"type":"read_receipt","conversation_id":"devB","message_ids":[1]}`,
		`{"type":"mark_read","conversation_id":"operator"}`,
		`{"type":"typing","conversation_id":"devB"}`,
	} {
		dev.send(t, frame)
	}
	dev.sync(t)
	if got := postsAfter(t, relay, 0); len(got) != 0 {
		t.Errorf("frames for other conversations reached the API server: %q", got)
	}
	if got := op.take(); len(got) != 0 {
		t.Errorf("the operator of devB got %q", got)
	}

	for _, frame := range []string{
		`{"type":"message","from_name":"Support","text":"to support"}`,
		`{"type":"message","conversation_id":"group:g1","text":"into g1"}`,
		`{"type":"message","conversation_id":"devA","text":"own"}`,
		`{"type":"mark_read","conversation_id":"devA"}`,
		`{"type":"mark_read","conversation_id":"group:g1"}`,
	} {
		dev.send(t, frame)
	}
	dev.sync(t)
	want := []string{
		`POST /chat/messages {"conversation_id":"devA","from_id":"devA","from_name":"office-pc","to_id":"","text":"to support"}`, // L493
		`POST /chat/messages {"conversation_id":"group:g1","from_id":"devA","from_name":"office-pc","to_id":"","text":"into g1"}`,
		`POST /chat/messages {"conversation_id":"devA","from_id":"devA","from_name":"office-pc","to_id":"","text":"own"}`,
		`POST /chat/read {"conversation_id":"devA","reader_id":"devA"}`,
		`POST /chat/read {"conversation_id":"group:g1","reader_id":"devA"}`,
	}
	if got := postsAfter(t, relay, len(want)); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("posts:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
