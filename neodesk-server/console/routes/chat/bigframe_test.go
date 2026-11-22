package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestBigFrameClosesTheSocket: a chat socket reads no message over
// MaxTextBytes; one that sends a bigger one is closed with 1009, while a
// message up to the limit is read (BUGS RV166).
func TestBigFrameClosesTheSocket(t *testing.T) {
	relay := startRelay(t, RelayOptions{})
	for _, path := range []string{"/ws/chat/dev1?token=tok-dev1", "/ws/chat-operator/panel"} {
		who := ""
		if strings.Contains(path, "operator") {
			who = "anna"
		}
		s, status := relay.dial(t, who, path)
		if s == nil {
			t.Fatalf("%s: %d", path, status)
		}
		s.send(t, `{"type":"hello","pad":"`+strings.Repeat("x", MaxTextBytes-40)+`"}`)
		s.sync(t)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := s.conn.Write(ctx, websocket.MessageText, []byte(strings.Repeat("y", MaxTextBytes+1)))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the socket stayed open after a message over the limit", path)
		}
	}
}
