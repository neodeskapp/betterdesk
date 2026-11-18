package console

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	for path, want := range map[string]string{"/ws/device-status": "go", "/ws/bd-signal": "go", "/ws/echo": "node", "/ws/chat/1": "node"} {
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

// TestBridgeTakesOnlyNodesCalls: a bridge call needs POST, a loopback
// address and the token; anything else goes on like a request without a
// Go route, to Node.
func TestBridgeTakesOnlyNodesCalls(t *testing.T) {
	w := mixed(t, setup{})
	b := w.s.bridge
	if b == nil || len(b.token) != 64 {
		t.Fatal("no bridge with a Node upstream")
	}
	for _, c := range []struct {
		method, path, remote, token string
		want                        bool
	}{
		{"POST", "/__bd/node/relay", "127.0.0.1:5", b.token, true},
		{"POST", "/__bd/node/relay", "[::1]:5", b.token, true},
		{"POST", "/__bd/node/relay", "10.0.0.1:5", b.token, false},
		{"POST", "/__bd/node/relay", "127.0.0.1:5", "", false},
		{"POST", "/__bd/node/relay", "127.0.0.1:5", strings.Repeat("0", 64), false},
		{"GET", "/__bd/node/relay", "127.0.0.1:5", b.token, false},
		{"POST", "/api/node/relay", "127.0.0.1:5", b.token, false},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.RemoteAddr = c.remote
		if c.token != "" {
			r.Header.Set(bridgeTokenHeader, c.token)
		}
		if got := b.takes(r); got != c.want {
			t.Errorf("%s %s from %s token %q: %v", c.method, c.path, c.remote, c.token, got)
		}
	}
	// Through the front door: the wrong token reaches Node, which has no
	// such route.
	res := w.agent().post("/__bd/node/relay", `{"call":"online"}`, bridgeTokenHeader, "wrong")
	if _, ok := res.node(); !ok {
		t.Errorf("a call with the wrong token: %d %s", res.status, res.body)
	}
}

// TestBridgeCalls: the relay calls bd-api makes and a panel event, with
// the token, through the front door.
func TestBridgeCalls(t *testing.T) {
	w := mixed(t, setup{})
	token := w.s.bridge.token
	call := func(body string) string {
		res := w.agent().post("/__bd/node/relay", body, bridgeTokenHeader, token)
		if res.status != http.StatusOK {
			t.Fatalf("%s: %d %s", body, res.status, res.body)
		}
		return res.body
	}
	var created struct{ SessionID, InitiatorToken, TargetToken string }
	if err := json.Unmarshal([]byte(call(`{"call":"create","args":["dev2","dev1"]}`)), &created); err != nil || len(created.InitiatorToken) != 64 {
		t.Fatalf("create: %+v %v", created, err)
	}
	got := call(`{"call":"get","args":["` + created.SessionID + `"]}`)
	if !strings.HasPrefix(got, `{"sessionId":"`+created.SessionID+`","initiatorId":"dev2","targetId":"dev1","status":"pending","createdAt":`) {
		t.Errorf("get: %s", got)
	}
	if got := call(`{"call":"pending","args":["dev1"]}`); !strings.Contains(got, `"session_id":"`+created.SessionID+`"`) {
		t.Errorf("pending: %s", got)
	}
	if got := call(`{"call":"isOnline","args":["dev1"]}`); got != `{}` {
		t.Errorf("isOnline of a device never seen: %s", got)
	}
	if got := call(`{"call":"notify","args":["dev1",{"type":"x"}]}`); got != "false" {
		t.Errorf("notify without a socket: %s", got)
	}
	if got := call(`{"call":"online"}`); got != "[]" {
		t.Errorf("online: %s", got)
	}
	if got := call(`{"call":"delete","args":["` + created.SessionID + `"]}`); got != "true" {
		t.Errorf("delete: %s", got)
	}
	if got := call(`{"call":"get","args":["` + created.SessionID + `"]}`); got != "null" {
		t.Errorf("get after delete: %s", got)
	}
	res := w.agent().post("/__bd/node/panel-event", `{"type":"registration_pending","n":1}`, bridgeTokenHeader, token)
	if res.status != http.StatusNoContent {
		t.Errorf("panel event: %d %s", res.status, res.body)
	}
}

// TestBridgeIsNamedToNode: every request the front door passes on carries
// the bridge's address and token, whatever the client sent under that
// name.
func TestBridgeIsNamedToNode(t *testing.T) {
	w := mixed(t, setup{})
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	w.s.listener = ln
	port := ln.Addr().(*net.TCPAddr).Port
	res := w.agent().get("/unported", "X-BD-Bridge", "http://evil.example:1 "+strings.Repeat("a", 64))
	got, ok := res.node()
	if !ok {
		t.Fatalf("not Node's answer: %d", res.status)
	}
	want := "http://127.0.0.1:" + strconv.Itoa(port) + " " + w.s.bridge.token
	if got.Bridge != want {
		t.Errorf("Node saw X-BD-Bridge %q, want %q", got.Bridge, want)
	}
}

// TestStopEndsOpenSockets: an open socket ends with the console, its
// timers stop, and nothing the console started is left.
func TestStopEndsOpenSockets(t *testing.T) {
	db := storetest.Migrated(t)
	check := wstest.Goroutines(t)
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
		LangDir: "../../web-nodejs/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
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
