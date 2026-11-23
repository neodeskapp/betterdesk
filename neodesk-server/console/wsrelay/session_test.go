package wsrelay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// changingSessions is a session store and a permission table that a test
// changes while a bridge is open. The session of a request is the one
// stored under its X-Test-Session header.
type changingSessions struct {
	mu       sync.Mutex
	sessions map[string]string
	revoked  bool
}

func (c *changingSessions) session(r *http.Request) ws.Session {
	c.mu.Lock()
	raw := c.sessions[r.Header.Get("X-Test-Session")]
	c.mu.Unlock()
	stub := r.Clone(r.Context())
	stub.Header.Set("X-Oracle-Session", raw)
	return wstest.OracleSession(stub)
}

func (c *changingSessions) permission(_ context.Context, role, permission string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.revoked && role == "operator" && permission == "device.connect", nil
}

// TestSignedInBridgeEndsWithItsSession: a signed-in bridge looks at its
// session every SessionCheck and ends, with its connection to the server,
// once the account signed out, another account holds the session or the
// role lost device.connect; while all of that holds it stays open (RM26).
func TestSignedInBridgeEndsWithItsSession(t *testing.T) {
	const olga = `{"userId":7,"user":{"id":7,"username":"olga","role":"operator"}}`
	for _, c := range []struct {
		name   string
		change func(c *changingSessions)
		ends   bool
	}{
		{"still allowed", func(*changingSessions) {}, false},
		{"signed out", func(c *changingSessions) { delete(c.sessions, "s1") }, true},
		{"another account", func(c *changingSessions) {
			c.sessions["s1"] = `{"userId":8,"user":{"id":8,"username":"other","role":"operator"}}`
		}, true},
		{"permission taken away", func(c *changingSessions) { c.revoked = true }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			sig := newSink(t)
			defer sig.close()
			sessions := &changingSessions{sessions: map[string]string{"s1": olga}}
			svc := New(Options{
				SignalHost: "127.0.0.1", SignalPort: sig.port(), RelayHost: "127.0.0.1", RelayPort: sig.port(),
				Interfaces:   func() []InterfaceAddr { return nil },
				GoAPI:        goapi.New(&guestAPI{}),
				SessionCheck: 30 * time.Millisecond,
			})
			reg := ws.New(ws.Options{Session: sessions.session, Permission: sessions.permission})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			defer func() { reg.Close(); srv.Close() }()
			client, res, err := wstest.Dial(strings.TrimPrefix(srv.URL, "http://"), "/ws/rendezvous", http.Header{"X-Test-Session": {"s1"}})
			if err != nil || client == nil {
				t.Fatalf("refused: %v %v", res, err)
			}
			waitFor(t, "the server's connection", func() bool { sig.mu.Lock(); defer sig.mu.Unlock(); return len(sig.conns) == 1 })
			time.Sleep(150 * time.Millisecond)
			if closed, code, _ := client.Closed(); closed {
				t.Fatalf("closed with %d before anything changed", code)
			}
			sessions.mu.Lock()
			c.change(sessions)
			sessions.mu.Unlock()
			ended := client.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			if ended != c.ends {
				t.Fatalf("ended %v, want %v", ended, c.ends)
			}
			if !ended {
				client.Close(1000)
				return
			}
			waitFor(t, "the place freed", func() bool { svc.mu.Lock(); defer svc.mu.Unlock(); return svc.perIP.Count("127.0.0.1") == 0 })
			client.Drop()
		})
	}
}
