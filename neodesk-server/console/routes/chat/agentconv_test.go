package chat

import (
	"strings"
	"testing"
)

// TestAgentMessagesGoToTheirDevicesConversation: a message, typing or a
// file an agent sends without naming a conversation belongs to its
// device's conversation, where the console shows the device. Before L493
// it went to a conversation named "operator" that no list shows, so the
// person's first message never appeared in the device's thread.
func TestAgentMessagesGoToTheirDevicesConversation(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	relay.api.answer("GET /chat/contacts/panel", `{"contacts":[]}`)
	relay.api.answer("POST /chat/messages", `{}`)
	inbox := relay.open(t, "admin", "panel")
	inbox.takeAtLeast(2)
	dev1 := relay.agent(t, "dev1")
	dev1.takeAtLeast(2)
	inbox.take()
	relay.api.takenPosts()

	dev1.send(t, `{"type":"message","text":"my printer stopped"}`)
	dev1.send(t, `{"type":"typing"}`)
	dev1.sync(t)
	got := strings.Join(inbox.takeAtLeast(2), "\n")
	if !strings.Contains(got, `"text":"my printer stopped"`) || !strings.Contains(got, `"conversation_id":"dev1","text":"my printer stopped"`) {
		t.Errorf("the inbox got %s, want the message in dev1's conversation", got)
	}
	if !strings.Contains(got, `{"type":"typing","from":"dev1","conversation_id":"dev1"}`) {
		t.Errorf("the inbox got %s, want the typing in dev1's conversation", got)
	}
	var stored []string
	waitFor(func() bool {
		stored = append(stored, relay.api.takenPosts()...)
		return len(stored) > 0
	})
	posts := strings.Join(stored, "\n")
	if !strings.Contains(posts, `"conversation_id":"dev1"`) {
		t.Errorf("stored as %s, want dev1's conversation", posts)
	}
}
