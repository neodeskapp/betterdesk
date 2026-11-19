package console

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestUpgradeGoesToTheGoSocketOrToNode: an upgrade to a path a Go socket
// has goes to it, outside the pipeline; any other upgrade still goes to
// Node. The routing report says the same.
func TestUpgradeGoesToTheGoSocketOrToNode(t *testing.T) {
	w := mixed(t, setup{debug: true})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	request := func(path string) string {
		return "GET " + path + " HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	}
	before := w.node.count()
	// The Go socket refuses a signed-out upgrade with a bare status line,
	// without the pipeline's headers.
	got, err := wstest.Raw(addr, request("/ws/device-status"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "HTTP/1.1 401 Unauthorized\r\n\r\n" {
		t.Errorf("Go socket: %q", got)
	}
	if w.node.count() != before {
		t.Error("the upgrade to a Go socket reached Node")
	}
	c, _, err := wstest.Dial(addr, "/ws/echo", nil)
	if err != nil || c == nil {
		t.Fatalf("Node socket: %v", err)
	}
	_ = c.Text("hello")
	if !c.Wait(5*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > 0 }) || c.Frames()[0].Text() != "node:hello" {
		t.Errorf("Node socket answered %+v", c.Frames())
	}
	c.Close(1000)
	for path, want := range map[string]string{"/ws/device-status": "go", "/ws/bd-signal": "go", "/ws/echo": "node", "/ws/chat/1": "go",
		"/ws/chat-operator/panel": "go", "/api/cdap/devices/1/terminal": "go", "/api/cdap/devices/1/audio": "go", "/control.ashx": "go",
		"/api/cdap/devices/1/other": "node", "/api/cdap/devices/./terminal": "node", "/ws/chat/a/b": "node"} {
		res := w.agent().get("/__bd/routing?method=GET&upgrade=websocket&path=" + url.QueryEscape(path))
		if res.json(t)["handler"] != want {
			t.Errorf("routing report for an upgrade to %s: %s", path, res.body)
		}
	}
	// Without upgrade the report still answers for the route tables
	if res := w.agent().get("/__bd/routing?method=GET&path=/ws/device-status"); res.json(t)["handler"] != "node" {
		t.Errorf("routing report for a plain GET: %s", res.body)
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
