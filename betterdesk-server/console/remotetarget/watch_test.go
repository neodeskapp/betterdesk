package remotetarget

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// changingWorld is a session store, a permission table, a target list and
// a device scope that a test changes while a socket is open. The session
// of a request is the one stored under its X-Test-Session header.
type changingWorld struct {
	mu       sync.Mutex
	sessions map[string]string
	revoked  bool
	outside  bool
	gone     bool
}

func (c *changingWorld) session(r *http.Request) ws.Session {
	c.mu.Lock()
	raw := c.sessions[r.Header.Get("X-Test-Session")]
	c.mu.Unlock()
	stub := r.Clone(r.Context())
	stub.Header.Set("X-Oracle-Session", raw)
	return wstest.OracleSession(stub)
}

func (c *changingWorld) permission(_ context.Context, role, permission string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.revoked && role == "operator" && permission == "remote_target.connect", nil
}

// A remote target socket looks at its session every SessionCheck and ends
// with 1008 "Session ended", taking the tunnel with it, once the account
// signed out, another account holds the session, the role lost
// remote_target.connect, or the target is gone or outside the account's
// scope; while all of that holds it stays open (RM4).
func TestRemoteTargetEndsWithItsSession(t *testing.T) {
	const olga = `{"userId":7,"user":{"id":7,"username":"olga","role":"operator"}}`
	for _, c := range []struct {
		name   string
		change func(c *changingWorld)
		ends   bool
	}{
		{"still allowed", func(*changingWorld) {}, false},
		{"signed out", func(c *changingWorld) { delete(c.sessions, "s1") }, true},
		{"another account", func(c *changingWorld) {
			c.sessions["s1"] = `{"userId":8,"user":{"id":8,"username":"other","role":"operator"}}`
		}, true},
		{"permission taken away", func(c *changingWorld) { c.revoked = true }, true},
		{"target out of the scope", func(c *changingWorld) { c.outside = true }, true},
		{"target gone", func(c *changingWorld) { c.gone = true }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			tunnels := &world{}
			changing := &changingWorld{sessions: map[string]string{"s1": olga}}
			g := New(Options{
				RemoteTarget: func(_ context.Context, id string) *goapi.Object {
					changing.mu.Lock()
					defer changing.mu.Unlock()
					if changing.gone {
						return nil
					}
					return goapi.NewObject("id", id)
				},
				// The scope looks at the target only, so another account's
				// session is caught by the account check alone
				CanAccess: func(_ context.Context, _ *session.User, _ *goapi.Object) (bool, error) {
					changing.mu.Lock()
					defer changing.mu.Unlock()
					return !changing.outside, nil
				},
				API:          http.HandlerFunc(tunnels.api),
				SessionCheck: 30 * time.Millisecond,
			})
			reg := ws.New(ws.Options{Session: changing.session, Permission: changing.permission})
			g.Register(reg)
			srv := httptest.NewServer(reg)
			addr := strings.TrimPrefix(srv.URL, "http://")
			browser, res, err := wstest.Dial(addr, "/ws/remote-target/rt_echo-0001", http.Header{"X-Test-Session": {"s1"}})
			if err != nil || browser == nil {
				t.Fatalf("refused: %v %v", res, err)
			}
			_ = browser.Text("x")
			if !browser.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
				t.Fatal("the tunnel did not answer")
			}
			time.Sleep(150 * time.Millisecond)
			if closed, code, _ := browser.Closed(); closed {
				t.Fatalf("closed with %d before anything changed", code)
			}
			changing.mu.Lock()
			c.change(changing)
			changing.mu.Unlock()
			ended := browser.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			if ended != c.ends {
				t.Fatalf("ended %v, want %v", ended, c.ends)
			}
			if ended {
				if _, code, reason := browser.Closed(); code != 1008 || reason != "Session ended" {
					t.Errorf("closed with %d %q, want 1008 \"Session ended\"", code, reason)
				}
			} else {
				browser.Close(1000)
			}
			// The tunnel ends with the browser's socket
			deadline := time.Now().Add(2 * time.Second)
			for {
				_, events := tunnels.take()
				if len(events) > 0 && events[len(events)-1].Close != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the tunnel stays open")
				}
				time.Sleep(20 * time.Millisecond)
			}
			browser.Drop()
			srv.Close()
			reg.Close()
			check()
		})
	}
}
