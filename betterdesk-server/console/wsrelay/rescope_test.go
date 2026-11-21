package wsrelay

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

// changingScope is a device scope a test changes while a bridge is open:
// the ids the account sees, or every device while unlimited is set.
type changingScope struct {
	mu        sync.Mutex
	unlimited bool
	sees      map[string]bool
}

func (c *changingScope) set(unlimited bool, ids ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unlimited, c.sees = unlimited, map[string]bool{}
	for _, id := range ids {
		c.sees[id] = true
	}
}

func (c *changingScope) canAccess(_ context.Context, _ *session.User, device *goapi.Object) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unlimited {
		return true, nil
	}
	return device != nil && c.sees[device.Str("id")], nil
}

// TestBridgeEndsWhenTheScopeLeavesItsDevice: the check every SessionCheck
// also asks the account's device scope again. A bridge of an account with
// a scope ends once the scope no longer holds a device the bridge sent a
// punch or relay request for; a bridge of an account that had no scope at
// the upgrade, whose frames were not read, ends once the account has one.
// A scope change that keeps the bridge's device leaves it open (RV114).
func TestBridgeEndsWhenTheScopeLeavesItsDevice(t *testing.T) {
	const olga = `{"userId":7,"user":{"id":7,"username":"olga","role":"operator"}}`
	for _, c := range []struct {
		name      string
		path      string
		unlimited bool
		message   []byte
		change    func(s *changingScope)
		ends      bool
	}{
		{"punch, device taken out", "/ws/rendezvous", false, framedBytes(rendezvousBytes(8, "TARGET01")), func(s *changingScope) { s.set(false, "OTHER01") }, true},
		{"relay request on the signal socket, device taken out", "/ws/rendezvous", false, framedBytes(rendezvousBytes(18, "TARGET01")), func(s *changingScope) { s.set(false) }, true},
		{"relay, device taken out", "/ws/relay?transport=message", false, rendezvousBytes(18, "TARGET01"), func(s *changingScope) { s.set(false, "OTHER01") }, true},
		{"punch, another device taken out", "/ws/rendezvous", false, framedBytes(rendezvousBytes(8, "TARGET01")), func(s *changingScope) { s.set(false, "TARGET01") }, false},
		{"no limit, then a scope", "/ws/rendezvous", true, framedBytes(rendezvousBytes(8, "OTHER01")), func(s *changingScope) { s.set(false, "TARGET01") }, true},
		{"no limit, still none", "/ws/rendezvous", true, framedBytes(rendezvousBytes(8, "OTHER01")), func(s *changingScope) { s.set(true) }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			sig := newSink(t)
			defer sig.close()
			sessions := &changingSessions{sessions: map[string]string{"s1": olga}}
			scope := &changingScope{}
			scope.set(c.unlimited, "TARGET01", "OTHER01")
			opts := scopedOptions()
			opts.CanAccess = scope.canAccess
			opts.SignalHost, opts.SignalPort, opts.RelayHost, opts.RelayPort = "127.0.0.1", sig.port(), "127.0.0.1", sig.port()
			opts.Interfaces = func() []InterfaceAddr { return nil }
			opts.GoAPI = goapi.New(&guestAPI{})
			opts.SessionCheck = 30 * time.Millisecond
			svc := New(opts)
			reg := ws.New(ws.Options{Session: sessions.session, Permission: sessions.permission})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			defer func() { reg.Close(); srv.Close() }()
			client, res, err := wstest.Dial(strings.TrimPrefix(srv.URL, "http://"), c.path, http.Header{"X-Test-Session": {"s1"}})
			if err != nil || client == nil {
				t.Fatalf("refused: %v %v", res, err)
			}
			waitFor(t, "the server's connection", func() bool { sig.mu.Lock(); defer sig.mu.Unlock(); return len(sig.conns) == 1 })
			_ = client.Binary(c.message)
			waitFor(t, "the message at the server", func() bool { return len(sig.received(1)) > 0 })
			time.Sleep(150 * time.Millisecond)
			if closed, code, _ := client.Closed(); closed {
				t.Fatalf("closed with %d before the scope changed", code)
			}
			c.change(scope)
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
