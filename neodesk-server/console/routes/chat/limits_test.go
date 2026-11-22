package chat

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// closeSocket closes a test socket from the client's side.
func (s *testSocket) closeSocket() {
	_ = s.conn.Close(websocket.StatusNormalClosure, "")
}

// rooms lists the rooms the relay holds.
func (tr *testRelay) rooms() []string {
	tr.relay.mu.Lock()
	defer tr.relay.mu.Unlock()
	var ids []string
	for id := range tr.relay.rooms {
		ids = append(ids, id)
	}
	return ids
}

// waitFor waits up to five seconds for cond.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return true
}

// TestRoomsAreDroppedWhenEmpty: a room goes when its last socket leaves,
// with the messages it kept, so rooms do not pile up in memory.
func TestRoomsAreDroppedWhenEmpty(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	op := relay.open(t, "anna", "dev1")
	agent := relay.agent(t, "dev1")
	agent.send(t, `{"type":"message","text":"kept in memory"}`)
	agent.sync(t)
	other := relay.open(t, "anna", "dev2")
	other.sync(t)
	op.sync(t)
	if got := strings.Join(relay.rooms(), ","); got != "dev1,dev2" && got != "dev2,dev1" {
		t.Fatalf("rooms %q, want dev1 and dev2", got)
	}
	other.closeSocket()
	if !waitFor(func() bool { return len(relay.rooms()) == 1 }) {
		t.Errorf("rooms %q after the only operator of dev2 left", relay.rooms())
	}
	agent.closeSocket()
	time.Sleep(100 * time.Millisecond)
	if got := relay.rooms(); len(got) != 1 {
		t.Errorf("rooms %q with an operator still in dev1", got)
	}
	op.closeSocket()
	if !waitFor(func() bool { return len(relay.rooms()) == 0 }) {
		t.Errorf("rooms %q after everybody left", relay.rooms())
	}
	again := relay.open(t, "anna", "dev1")
	want(t, "a new room of dev1", again.take(), `{"type":"status","agent_connected":false}`, `{"type":"history","messages":[]}`)

	alone := relay.agent(t, "dev3")
	alone.sync(t)
	alone.closeSocket()
	if !waitFor(func() bool { return len(relay.rooms()) == 1 }) {
		t.Errorf("rooms %q after the agent alone in dev3 left", relay.rooms())
	}
}

// TestOperatorSocketsPerAccount: an account has at most ten operator
// sockets open, the inbox and rooms together; the eleventh is refused
// with 429 until one of them ends. Other accounts count on their own.
func TestOperatorSocketsPerAccount(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	var first *testSocket
	for i := 0; i < MaxOperatorSockets; i++ {
		room := "panel"
		if i%2 == 1 {
			room = "dev1"
		}
		s := relay.open(t, "anna", room)
		if first == nil {
			first = s
		}
	}
	if s, status := relay.dial(t, "anna", "/ws/chat-operator/panel"); s != nil || status != http.StatusTooManyRequests {
		t.Errorf("the eleventh socket: %d, want 429", status)
	}
	relay.open(t, "ben", "panel")
	if s, status := relay.dial(t, "", "/ws/chat/dev1?token=tok-dev1"); s == nil {
		t.Errorf("an agent: %d", status)
	}
	first.closeSocket()
	if !waitFor(func() bool {
		s, _ := relay.dial(t, "anna", "/ws/chat-operator/panel")
		return s != nil
	}) {
		t.Error("anna could not open a socket after one of hers ended")
	}
}

// TestSilentChatSocketIsClosed: a socket whose client answers no ping is
// closed after two ping intervals; one whose client answers stays open
// although it sends nothing.
func TestSilentChatSocketIsClosed(t *testing.T) {
	const interval = 30 * time.Millisecond
	relay := startRelay(t, RelayOptions{PingInterval: interval})
	quiet := relay.open(t, "anna", "panel")

	for _, path := range []string{"/ws/chat-operator/panel", "/ws/chat/dev1?token=tok-dev1"} {
		conn, err := net.Dial("tcp", relay.addr)
		if err != nil {
			t.Fatal(err)
		}
		session, _ := json.Marshal(map[string]any{"userId": 6, "user": map[string]any{"id": 6, "username": "ben", "role": "operator"}})
		_, err = io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+relay.addr+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nX-Oracle-Session: "+string(session)+"\r\n\r\n")
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_ = conn.SetReadDeadline(start.Add(5 * time.Second))
		got, _ := io.ReadAll(conn)
		conn.Close()
		if !strings.HasPrefix(string(got), "HTTP/1.1 101") {
			t.Fatalf("%s: %q", path, got)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("%s: the silent socket was closed after %v, want about %v", path, took, 2*interval)
		}
	}

	time.Sleep(20 * interval)
	quiet.sync(t)
}
