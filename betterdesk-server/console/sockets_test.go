package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestUpgradeGoesToTheSocketOrThePipeline: an upgrade to a path a socket
// has goes to it, outside the pipeline; any other upgrade passes the
// pipeline like a plain request and ends in its 404.
func TestUpgradeGoesToTheSocketOrThePipeline(t *testing.T) {
	w := newWorld(t, setup{})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	// The socket refuses a signed-out upgrade with a bare status line,
	// without the pipeline's headers.
	got, err := wstest.Raw(addr, "GET /ws/device-status HTTP/1.1\r\nHost: x\r\n"+
		"Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if got != "HTTP/1.1 401 Unauthorized\r\n\r\n" {
		t.Errorf("socket: %q", got)
	}
	res := w.agent().get("/ws/echo", "Upgrade", "websocket", "Connection", "Upgrade",
		"Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version", "13")
	if res.status != http.StatusNotFound {
		t.Errorf("an upgrade no socket takes: %d %.100s, want the 404", res.status, res.body)
	}
	wantSecurityHeaders(t, res)

	// Which paths the sockets take
	for path, want := range map[string]bool{"/ws/device-status": true, "/ws/bd-signal": true, "/ws/echo": false, "/ws/chat/1": true,
		"/ws/chat-operator/panel": true, "/api/cdap/devices/1/terminal": true, "/api/cdap/devices/1/audio": true, "/control.ashx": true,
		"/api/cdap/devices/1/other": false, "/api/cdap/devices/./terminal": false, "/ws/chat/a/b": false} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		if got := w.s.Deps().Sockets.Serves(r); got != want {
			t.Errorf("an upgrade to %s goes to a socket: %v, want %v", path, got, want)
		}
	}
}

// TestServerTerminalIsGone: the console has no server terminal (A194).
// An upgrade to its old address, even by an admin with a session and the
// right Origin, passes the pipeline like any unknown path and ends in its
// 404; no shell starts.
func TestServerTerminalIsGone(t *testing.T) {
	w := newWorld(t, setup{})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	c, res, err := wstest.Dial(addr, "/ws/server-management/terminal", http.Header{
		"Cookie": {w.agent().signIn("server_admin").cookieHeader()}, "Origin": {"http://" + addr},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if c != nil {
		c.Drop()
		t.Fatal("the old terminal address opened a socket")
	}
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("an upgrade to the old terminal: %d, want the 404", res.StatusCode)
	}
	r := httptest.NewRequest("GET", "/ws/server-management/terminal", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "Upgrade")
	if w.s.Deps().Sockets.Serves(r) {
		t.Error("a socket takes the old terminal's address")
	}
}

// TestStopEndsOpenSockets: an open socket ends with the console, its
// timers stop, and nothing the console started is left.
func TestStopEndsOpenSockets(t *testing.T) {
	db := storetest.Migrated(t)
	check := wstest.Goroutines(t)
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
		Getenv: func(key string) string {
			if key == "SESSION_SECRET" {
				return testSecret
			}
			return ""
		},
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	id, token, _ := s.Deps().Relay.CreateRelaySession("dev2", "dev1")
	q := url.Values{"session": {id}, "token": {token}, "role": {"initiator"}}
	c, _, err := wstest.Dial(s.Addr(), "/ws/bd-relay?"+q.Encode(), nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if closed, code, reason := c.Closed(); closed {
		t.Fatalf("closed early: %d %q", code, reason)
	}
	s.Stop()
	if !c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the socket outlived the console")
	}
	c.Drop()
	check()
}
