package chat

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestReplacedAgentLeavesAfterItsSuccessor: a device's first socket, told
// to close when a second one came, ends only after the second has left
// and its room is gone. The relay goes on serving: its lock is free and a
// new socket is let in (BUGS RV160).
func TestReplacedAgentLeavesAfterItsSuccessor(t *testing.T) {
	relay := startRelay(t, RelayOptions{})

	// The first socket's client never answers the close, so its handler
	// ends only when the closing handshake gives up.
	first, err := net.Dial("tcp", relay.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	_, err = io.WriteString(first, "GET /ws/chat/dev1?token=tok-dev1 HTTP/1.1\r\nHost: "+relay.addr+
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 12)
	if _, err := io.ReadFull(first, head); err != nil || !strings.HasPrefix(string(head), "HTTP/1.1 101") {
		t.Fatalf("first socket: %q %v", head, err)
	}
	// The handshake's answer goes out before the relay puts the socket in
	// its room. The second socket replaces the first only when it comes
	// after that; otherwise the late first socket replaces the second.
	if !waitFor(func() bool {
		relay.relay.mu.Lock()
		defer relay.relay.mu.Unlock()
		rm := relay.relay.rooms["dev1"]
		return rm != nil && rm.agent != nil
	}) {
		t.Fatal("the first socket never became its room's agent")
	}

	second := relay.agent(t, "dev1")
	second.sync(t)
	second.closeSocket()
	if !waitFor(func() bool { return len(relay.rooms()) == 0 }) {
		t.Fatalf("rooms %q after the second socket left", relay.rooms())
	}

	// Wait for the server to drop the first connection.
	_ = first.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, _ = io.Copy(io.Discard, first)
	time.Sleep(200 * time.Millisecond)

	if !waitFor(func() bool {
		if !relay.relay.mu.TryLock() {
			return false
		}
		relay.relay.mu.Unlock()
		return true
	}) {
		t.Fatal("the relay's lock is still held after the first socket ended")
	}
	op := relay.open(t, "anna", "dev1")
	op.sync(t)
	want(t, "a new operator", op.takeAtLeast(2), `{"type":"status","agent_connected":false}`, `{"type":"history","messages":[]}`)
}
