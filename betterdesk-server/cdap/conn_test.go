package cdap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/events"
	"github.com/unitronix/betterdesk-server/peer"
)

// cdapWorld is a gateway on a test server with its own database.
type cdapWorld struct {
	g   *Gateway
	db  db.Database
	url string
}

func newCDAPWorld(t *testing.T) *cdapWorld {
	t.Helper()
	database, err := db.OpenSQLite(filepath.Join(t.TempDir(), "cdap.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		database.Close()
		t.Fatal(err)
	}
	g := New(config.DefaultConfig(), database, peer.NewMap(), events.NewBus())
	g.SetJWTManager(auth.NewJWTManager("cdap-conn-test-secret-0123456789", time.Hour))
	g.SetRateLimiter(nil)
	ctx, cancel := context.WithCancel(context.Background())
	g.ctx, g.cancel = ctx, cancel
	srv := httptest.NewServer(http.HandlerFunc(g.handleWebSocket))
	t.Cleanup(func() {
		srv.Close()
		cancel()
		_ = database.Close()
	})
	return &cdapWorld{g: g, db: database, url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/cdap"}
}

// cdapClient is a device's socket of a test.
type cdapClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func (w *cdapWorld) dial(t *testing.T) *cdapClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, w.url, &websocket.DialOptions{Subprotocols: []string{"cdap-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadLimit(16 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return &cdapClient{t: t, conn: conn}
}

func (c *cdapClient) send(typ string, payload any) {
	c.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		c.t.Fatal(err)
	}
	data, _ := json.Marshal(Message{Type: typ, Payload: raw})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.t.Fatalf("send %s: %v", typ, err)
	}
}

// read returns the next message, failing the test after five seconds.
func (c *cdapClient) read() Message {
	c.t.Helper()
	m, err := c.tryRead(5 * time.Second)
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	return m
}

func (c *cdapClient) tryRead(d time.Duration) (Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		c.t.Fatalf("message %q: %v", data, err)
	}
	return m, nil
}

// drain reads and drops what comes until the socket ends, so the socket
// answers a close the way a device does.
func (c *cdapClient) drain() {
	go func() {
		for {
			if _, _, err := c.conn.Read(context.Background()); err != nil {
				return
			}
		}
	}()
}

// authWith signs in with an API key under a device id and returns the
// answer's type ("auth_result" or "error").
func (c *cdapClient) authWith(key, deviceID string) Message {
	c.t.Helper()
	c.send("auth", AuthPayload{Method: "api_key", Key: key, DeviceID: deviceID})
	return c.read()
}

// register sends a manifest of a device with that name and returns the
// answer.
func (c *cdapClient) register(name string) Message {
	c.t.Helper()
	c.send("register", map[string]any{"manifest": map[string]any{
		"device":  map[string]any{"name": name, "type": "iot"},
		"bridge":  map[string]any{"protocol": "test", "version": "1"},
		"widgets": []any{map[string]any{"id": "temp", "type": "gauge", "label": "Temp"}},
	}})
	return c.read()
}

// connect signs in and registers, failing the test when either is refused.
func (w *cdapWorld) connect(t *testing.T, key, deviceID string) *cdapClient {
	t.Helper()
	c := w.dial(t)
	if m := c.authWith(key, deviceID); m.Type != "auth_result" {
		t.Fatalf("auth of %s: %s %s", deviceID, m.Type, m.Payload)
	}
	if m := c.register(deviceID + "-name"); m.Type != "registered" {
		t.Fatalf("register of %s: %s %s", deviceID, m.Type, m.Payload)
	}
	return c
}

// settle waits until the gateway has n connections open.
func (w *cdapWorld) settle(t *testing.T, n int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.g.ActiveConnections() != n {
		if time.Now().After(deadline) {
			t.Fatalf("%d connections open, want %d", w.g.ActiveConnections(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}

func (w *cdapWorld) manifestStored(t *testing.T, deviceID string) bool {
	t.Helper()
	v, _ := w.db.GetConfig("cdap_manifest_" + deviceID)
	return v != ""
}

// TestEndedConnectionRemovesOnlyItself: a connection that signs in under
// a device's id and ends, and the connection a reconnect replaced, leave
// the device's live connection, its manifest and its online state alone
// (BUGS RV158).
func TestEndedConnectionRemovesOnlyItself(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-plc-0123456789abcdef", "plc", "operator", "")

	live := w.connect(t, "apikey-plc-0123456789abcdef", "plc-1")
	w.settle(t, 1)
	liveConn := w.g.GetDeviceConn("plc-1")

	// A second connection signs in under the id and goes away.
	other := w.dial(t)
	if m := other.authWith("apikey-plc-0123456789abcdef", "plc-1"); m.Type != "auth_result" {
		t.Fatalf("second sign-in: %s %s", m.Type, m.Payload)
	}
	other.conn.Close(websocket.StatusNormalClosure, "")
	w.settle(t, 1)
	if got := w.g.GetDeviceConn("plc-1"); got != liveConn {
		t.Fatalf("after a second sign-in ended the device's connection is %p, want the live one %p", got, liveConn)
	}
	if !w.manifestStored(t, "plc-1") {
		t.Fatal("the live device's manifest is gone")
	}
	if p, _ := w.db.GetPeer("plc-1"); p == nil || p.Status != "ONLINE" {
		t.Fatalf("the live device is %+v, want ONLINE", p)
	}
	live.send("heartbeat", map[string]any{})
	if m := live.read(); m.Type != "ping" {
		t.Fatalf("the live device got %s %s, want ping", m.Type, m.Payload)
	}
	live.drain()

	// A reconnect: the new connection replaces the old one, whose end
	// must not undo the new registration.
	again := w.connect(t, "apikey-plc-0123456789abcdef", "plc-1")
	w.settle(t, 1)
	if got := w.g.GetDeviceConn("plc-1"); got == nil || got == liveConn {
		t.Fatalf("after a reconnect the device's connection is %p, want the new one", got)
	}
	if !w.manifestStored(t, "plc-1") {
		t.Fatal("the manifest of the reconnected device is gone")
	}
	again.send("heartbeat", map[string]any{})
	if m := again.read(); m.Type != "ping" {
		t.Fatalf("the reconnected device got %s %s, want ping", m.Type, m.Payload)
	}
}

// TestRegisterKeepsToTheDevicesOfThePrincipal: a sign-in with an account
// or API key registers only a device id of the right form that is new or
// was registered by the same account or key; registering never enables a
// disabled device (BUGS RV157).
func TestRegisterKeepsToTheDevicesOfThePrincipal(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-mine-0123456789abcdef", "mine", "operator", "")
	createAPIKey(t, w.db, "apikey-other-0123456789abcdef", "other", "viewer", "")
	if err := w.db.UpsertPeer(&db.Peer{ID: "900100001", Hostname: "real-host", Status: "OFFLINE", Disabled: true}); err != nil {
		t.Fatal(err)
	}

	refused := func(what, key, deviceID string) {
		t.Helper()
		c := w.dial(t)
		if m := c.authWith(key, deviceID); m.Type != "auth_result" {
			t.Fatalf("%s: auth %s %s", what, m.Type, m.Payload)
		}
		if m := c.register("evil-name"); m.Type != "error" {
			t.Errorf("%s: register answered %s %s, want an error", what, m.Type, m.Payload)
		}
		c.conn.CloseNow()
		w.settle(t, 0)
	}
	refused("a device someone else enrolled", "apikey-other-0123456789abcdef", "900100001")
	if p, _ := w.db.GetPeer("900100001"); p == nil || p.Hostname != "real-host" || !p.Disabled || p.Status == "ONLINE" {
		t.Fatalf("the enrolled device became %+v", p)
	}
	for _, id := range []string{"a b", "../x", "dev/1", strings.Repeat("x", 65)} {
		refused("the id "+id, "apikey-mine-0123456789abcdef", id)
	}

	mine := w.connect(t, "apikey-mine-0123456789abcdef", "plc-new")
	mine.drain()
	mine.conn.Close(websocket.StatusNormalClosure, "")
	w.settle(t, 0)
	refused("a device another key registered", "apikey-other-0123456789abcdef", "plc-new")

	p, _ := w.db.GetPeer("plc-new")
	p.Disabled = true
	if err := w.db.UpsertPeer(p); err != nil {
		t.Fatal(err)
	}
	again := w.connect(t, "apikey-mine-0123456789abcdef", "plc-new")
	again.drain()
	if p, _ := w.db.GetPeer("plc-new"); p == nil || !p.Disabled {
		t.Fatalf("the reconnected device is %+v, want it still disabled", p)
	}
}
