package chat

import (
	"strings"
	"testing"
)

// TestDirectMessagesReachTheirTwoPeopleOnly: a message from one console
// user to another ("operator:<name>") goes to the recipient's sockets and
// the sender's other ones, never to the other people in the inbox. Before
// L492 the relay sent it to every chat user, who saw it under the
// recipient's name. The same holds for what else names the conversation:
// typing, reactions and shared files.
func TestDirectMessagesReachTheirTwoPeopleOnly(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	relay.api.answer("GET /chat/contacts/panel", `{"contacts":[]}`)
	relay.api.answer("POST /chat/messages", `{}`)
	anna := relay.open(t, "anna", "panel")
	annaElsewhere := relay.open(t, "anna", "panel")
	ben := relay.open(t, "ben", "panel")
	cleo := relay.open(t, "cleo", "panel")
	admin := relay.open(t, "admin", "panel")
	for _, s := range []*testSocket{anna, annaElsewhere, ben, cleo, admin} {
		s.takeAtLeast(2)
	}

	anna.send(t, `{"type":"message","text":"just for ben","conversation_id":"operator:ben"}`)
	anna.send(t, `{"type":"typing","conversation_id":"operator:ben"}`)
	anna.send(t, `{"type":"reaction","conversation_id":"operator:ben","message_id":"1","emoji":"x"}`)
	anna.sync(t)

	for name, s := range map[string]*testSocket{"cleo": cleo, "admin": admin} {
		if got := s.take(); len(got) != 0 {
			t.Errorf("%s got anna's direct messages to ben: %q", name, got)
		}
	}
	got := strings.Join(ben.takeAtLeast(3), "\n")
	for _, part := range []string{`"text":"just for ben"`, `"type":"typing"`, `"type":"reaction"`} {
		if !strings.Contains(got, part) {
			t.Errorf("ben did not get %s: %q", part, got)
		}
	}
	if got := strings.Join(annaElsewhere.takeAtLeast(1), "\n"); !strings.Contains(got, `"text":"just for ben"`) {
		t.Errorf("anna's other tab did not get her message: %q", got)
	}

	// Ben's answer goes to anna's tabs, not to cleo
	ben.send(t, `{"type":"message","text":"for anna","conversation_id":"operator:anna"}`)
	ben.sync(t)
	if got := strings.Join(anna.takeAtLeast(1), "\n"); !strings.Contains(got, `"text":"for anna"`) {
		t.Errorf("anna did not get ben's answer: %q", got)
	}
	if got := cleo.take(); len(got) != 0 {
		t.Errorf("cleo got ben's answer to anna: %q", got)
	}

	// The history of ben's conversation holds everything sent to ben:
	// ben reads all of it, anna what she sent, cleo nothing
	relay.api.answer("GET /chat/history/operator%3Aben?limit=100", `{"messages":[`+
		`{"id":1,"from_id":"operator","from_name":"anna","text":"a"},`+
		`{"id":2,"from_id":"operator","from_name":"cleo","text":"c"},`+
		`{"id":3,"from_id":"operator:admin","from_name":"admin","text":"d"}]}`)
	history := func(s *testSocket) string {
		s.send(t, `{"type":"get_history","conversation_id":"operator:ben"}`)
		return strings.Join(s.takeAtLeast(1), "\n")
	}
	if got := history(ben); !strings.Contains(got, `"id":1`) || !strings.Contains(got, `"id":2`) || !strings.Contains(got, `"id":3`) {
		t.Errorf("ben's own history: %s", got)
	}
	if got := history(anna); !strings.Contains(got, `"id":1`) || strings.Contains(got, `"id":2`) || strings.Contains(got, `"id":3`) {
		t.Errorf("anna's view of ben's history: %s, want her message only", got)
	}
	if got := history(admin); strings.Contains(got, `"id":1`) || strings.Contains(got, `"id":2`) || !strings.Contains(got, `"id":3`) {
		t.Errorf("admin's view of ben's history: %s, want admin's message only", got)
	}
}
