package chat

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestAgentCreatesGroupsOnlyWithItsOwnDevice: an agent's group may hold
// its own device and operators, nothing else: a group with another
// device, or members that are no list of names, is not created (BUGS
// L56).
func TestAgentCreatesGroupsOnlyWithItsOwnDevice(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	relay.api.answer("POST /chat/groups", `{"id":"group:new","name":"G","members":"devA,operator:anna"}`)
	relay.api.answer("GET /chat/groups/devA", `{"groups":[]}`)
	dev := relay.agent(t, "devA")
	for _, frame := range []string{
		`{"type":"create_group","name":"G","member_ids":["devA","devB"]}`,
		`{"type":"create_group","name":"G","member_ids":["devB"]}`,
		`{"type":"create_group","name":"G","member_ids":["devA","operator:anna","devC"]}`,
		`{"type":"create_group","name":"G","member_ids":"devA,devB"}`,
		`{"type":"create_group","name":"G","member_ids":["devA",5]}`,
	} {
		dev.send(t, frame)
	}
	dev.sync(t)
	if got := postsAfter(t, relay, 0); len(got) != 0 {
		t.Errorf("groups with other members reached the API server: %q", got)
	}

	dev.send(t, `{"type":"create_group","name":"G","member_ids":["devA","operator:anna","operator"]}`)
	dev.sync(t)
	want := `POST /chat/groups {"name":"G","members":["devA","operator:anna","operator"],"created_by":"devA"}`
	if got := postsAfter(t, relay, 1); strings.Join(got, "\n") != want {
		t.Errorf("posts:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

// TestAgentSocketsPerToken: one token has at most
// MaxAgentSocketsPerToken agent sockets open, whatever device ids they
// name; the next is refused with 429 until one of them ends. Another
// token counts on its own (BUGS L56).
func TestAgentSocketsPerToken(t *testing.T) {
	relay := startRelay(t, RelayOptions{VerifyDevice: func(_ context.Context, deviceID, token string) (bool, error) {
		// An enrollment token no device used yet passes for any new id
		return token == "shared-token" || token == "tok-"+deviceID, nil
	}})
	var first *testSocket
	for i := 0; i < MaxAgentSocketsPerToken; i++ {
		s, status := relay.dial(t, "", "/ws/chat/dev"+strconv.Itoa(i)+"?token=shared-token")
		if s == nil {
			t.Fatalf("socket %d of the token: refused with %d", i+1, status)
		}
		if first == nil {
			first = s
		}
	}
	if s, status := relay.dial(t, "", "/ws/chat/devX?token=shared-token"); s != nil || status != http.StatusTooManyRequests {
		t.Errorf("one socket more of the token: %d, want 429", status)
	}
	relay.agent(t, "devY")
	first.closeSocket()
	if !waitFor(func() bool {
		s, _ := relay.dial(t, "", "/ws/chat/devZ?token=shared-token")
		return s != nil
	}) {
		t.Error("the token could not open a socket after one of its sockets ended")
	}
}

// TestAgentMessageRate: an agent socket may send AgentFrameBurst
// messages at once and AgentFramesPerSecond after that; the relay drops
// what comes faster, and the socket stays open (BUGS L56).
func TestAgentMessageRate(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	relay := startRelay(t, RelayOptions{Now: clock})
	relay.api.answer("POST /chat/messages", `{"ok":true}`)
	dev := relay.agent(t, "devA")
	for i := 0; i < AgentFrameBurst+10; i++ {
		dev.send(t, `{"type":"message","conversation_id":"devA","text":"m`+strconv.Itoa(i)+`"}`)
	}
	if got := postsAfter(t, relay, AgentFrameBurst+10); len(got) != AgentFrameBurst {
		t.Errorf("%d messages stored of %d sent at once, want %d", len(got), AgentFrameBurst+10, AgentFrameBurst)
	}

	mu.Lock()
	now = now.Add(time.Second)
	mu.Unlock()
	for i := 0; i < AgentFramesPerSecond+5; i++ {
		dev.send(t, `{"type":"message","conversation_id":"devA","text":"later"}`)
	}
	if got := postsAfter(t, relay, AgentFramesPerSecond+5); len(got) != AgentFramesPerSecond {
		t.Errorf("%d messages stored a second later, want %d", len(got), AgentFramesPerSecond)
	}

	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	dev.sync(t)
}
