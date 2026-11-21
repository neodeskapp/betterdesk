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
	"github.com/unitronix/betterdesk-server/console/servermgmt"
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

// RV89: the server terminal asks the account's panel access policy, so
// the console hands its socket registry the policy lookup. An account
// with write in server-management gets the terminal; one whose policy
// lowers that section to read is refused at the upgrade.
func TestServerTerminalFollowsThePanelAccessPolicy(t *testing.T) {
	w := newWorld(t, setup{})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	open := func(role string) (int, *wstest.Client) {
		t.Helper()
		a := w.agent().signIn(role)
		var pairs []string
		for name, value := range a.cookies {
			pairs = append(pairs, name+"="+value)
		}
		c, res, err := wstest.Dial(addr, servermgmt.TerminalPath, http.Header{
			"Cookie": {strings.Join(pairs, "; ")}, "Origin": {"http://" + addr},
		})
		if err != nil {
			t.Fatalf("%s: dial: %v", role, err)
		}
		if c != nil {
			return http.StatusSwitchingProtocols, c
		}
		return res.StatusCode, nil
	}
	status, c := open("server_admin")
	if status != http.StatusSwitchingProtocols {
		t.Fatalf("server_admin with write in server-management: %d, want the terminal", status)
	}
	c.Drop()
	w.setSection("server_admin", "server-management", "read")
	if status, c := open("server_admin"); status != http.StatusForbidden {
		if c != nil {
			c.Drop()
		}
		t.Errorf("server_admin with read in server-management: %d, want 403", status)
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
