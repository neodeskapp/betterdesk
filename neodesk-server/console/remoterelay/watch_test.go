package remoterelay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// changingWorld is a session store, a permission table and a device scope
// that a test changes while a socket is open. The session of a request is
// the one stored under its X-Test-Session header.
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
	return !c.revoked && role == "operator" && permission == "device.connect", nil
}

func (c *changingWorld) set(f func(c *changingWorld)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c)
}

// A viewer's socket looks at its session every SessionCheck and ends with
// 1008 "Session ended" once the account signed out, another account holds
// the session, the role lost device.connect, or the device left the
// account's scope or is gone; while all of that holds it stays open
// (RM4).
func TestViewerEndsWithItsSession(t *testing.T) {
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
		{"device out of the scope", func(c *changingWorld) { c.outside = true }, true},
		{"device gone", func(c *changingWorld) { c.gone = true }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			world := &changingWorld{sessions: map[string]string{"s1": olga}}
			svc := New(Options{
				VerifyDevice: func(context.Context, string, string) (bool, error) { return false, nil },
				Devices: func(_ context.Context, id string) (*goapi.Object, error) {
					world.mu.Lock()
					defer world.mu.Unlock()
					if world.gone {
						return nil, nil
					}
					return goapi.NewObject("id", id), nil
				},
				// The scope looks at the device only, so another account's
				// session is caught by the account check alone
				CanAccess: func(_ context.Context, _ *session.User, _ *goapi.Object) (bool, error) {
					world.mu.Lock()
					defer world.mu.Unlock()
					return !world.outside, nil
				},
				SessionCheck: 30 * time.Millisecond,
			})
			reg := ws.New(ws.Options{Session: world.session, Permission: world.permission})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			addr := strings.TrimPrefix(srv.URL, "http://")
			viewer := dial(t, addr, "/ws/remote-viewer/dev1", http.Header{"X-Test-Session": {"s1"}})
			if !viewer.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
				t.Fatalf("the viewer got %q", texts(viewer.Frames()))
			}
			// A few checks pass while nothing changes
			time.Sleep(150 * time.Millisecond)
			if closed, code, _ := viewer.Closed(); closed {
				t.Fatalf("closed with %d before anything changed", code)
			}
			world.set(c.change)
			ended := viewer.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			if ended != c.ends {
				t.Fatalf("ended %v, want %v", ended, c.ends)
			}
			if ended {
				if _, code, reason := viewer.Closed(); code != 1008 || reason != "Session ended" {
					t.Errorf("closed with %d %q, want 1008 \"Session ended\"", code, reason)
				}
				deadline := time.Now().Add(2 * time.Second)
				for state := svc.SessionState("dev1"); state != nil && state.ViewerCount != 0; state = svc.SessionState("dev1") {
					if time.Now().After(deadline) {
						t.Errorf("the session still counts the viewer: %+v", state)
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				viewer.Close(1000)
			}
			srv.Close()
			reg.Close()
			check()
		})
	}
}
