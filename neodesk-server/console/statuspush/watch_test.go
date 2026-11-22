package statuspush

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// liveSession is a session and a permission table the test changes while
// the sockets are open.
type liveSession struct {
	mu      sync.Mutex
	session string
	revoked bool
	fails   bool
}

func (l *liveSession) set(f func(l *liveSession)) {
	l.mu.Lock()
	f(l)
	l.mu.Unlock()
}

func (l *liveSession) options() ws.Options {
	return ws.Options{
		Session: func(r *http.Request) ws.Session {
			l.mu.Lock()
			text := l.session
			l.mu.Unlock()
			r = r.Clone(r.Context())
			r.Header.Set("X-Oracle-Session", text)
			return wstest.OracleSession(r)
		},
		Permission: func(_ context.Context, role, _ string) (bool, error) {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.fails {
				return true, errors.New("lookup failed")
			}
			return !l.revoked && (role == "admin" || role == "operator"), nil
		},
	}
}

const adminSession = `{"userId":1,"user":{"id":1,"username":"admin","role":"admin"}}`

// TestSocketsEndWithTheirSession: both sockets read their session again
// every SessionCheck, as the viewer socket does, and close with 1008 once
// the account signed out, another account holds the session, its role
// lost the socket's permission or the lookup fails (RV110).
func TestSocketsEndWithTheirSession(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(l *liveSession)
	}{
		{"signed out", func(l *liveSession) { l.session = "" }},
		{"another account", func(l *liveSession) { l.session = `{"userId":2,"user":{"id":2,"username":"bob","role":"admin"}}` }},
		{"permission lost", func(l *liveSession) { l.revoked = true }},
		{"lookup fails", func(l *liveSession) { l.fails = true }},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			live := &liveSession{session: adminSession}
			reg := ws.New(live.options())
			svc := New(Options{SessionCheck: 30 * time.Millisecond})
			svc.Register(reg, Scope{})
			srv := httptest.NewServer(reg)
			addr := strings.TrimPrefix(srv.URL, "http://")
			var clients []*wstest.Client
			for _, path := range []string{"/ws/device-status", "/ws/panel-events"} {
				client, res, err := wstest.Dial(addr, path, nil)
				if err != nil || client == nil {
					t.Fatalf("%s: %v %v", path, err, res)
				}
				clients = append(clients, client)
			}
			waitClients(t, svc, 1, 1)
			time.Sleep(100 * time.Millisecond)
			for _, client := range clients {
				if closed, _, _ := client.Closed(); closed {
					t.Fatal("a socket of a good session was closed")
				}
			}
			live.set(c.change)
			for i, client := range clients {
				client.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
				if closed, code, reason := client.Closed(); !closed || code != 1008 || reason != "Session ended" {
					t.Errorf("socket %d: closed %v %d %q", i, closed, code, reason)
				}
			}
			waitClients(t, svc, 0, 0)
			svc.Handle(context.Background(), parse(t, `{"type":"peer_online","timestamp":"t1","data":{"id":"dev1"}}`))
			time.Sleep(50 * time.Millisecond)
			for i, client := range clients {
				if n := len(client.Frames()); n != 0 {
					t.Errorf("socket %d got %d messages after its session ended", i, n)
				}
				client.Drop()
			}
			srv.Close()
			reg.Close()
			check()
		})
	}
}

// TestSocketsTakeTheAccountFromTheRecheck: the account whose scope a
// message follows is the one the last check read, not the one of the
// upgrade: an admin whose role becomes operator, a role with a device
// scope that does not hold dev2, no longer gets dev2's status (RV110).
func TestSocketsTakeTheAccountFromTheRecheck(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		check := wstest.Goroutines(t)
		live := &liveSession{session: adminSession}
		reg := ws.New(live.options())
		svc := New(Options{SessionCheck: 30 * time.Millisecond})
		svc.Register(reg, realScope(t, db))
		srv := httptest.NewServer(reg)
		addr := strings.TrimPrefix(srv.URL, "http://")
		client, res, err := wstest.Dial(addr, "/ws/device-status", nil)
		if err != nil || client == nil {
			t.Fatalf("dial: %v %v", err, res)
		}
		waitClients(t, svc, 1, 0)
		ctx := context.Background()
		svc.Handle(ctx, parse(t, `{"type":"peer_online","timestamp":"t1","data":{"id":"dev2"}}`))
		if !client.Wait(3*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
			t.Fatal("the admin did not get dev2's status")
		}
		live.set(func(l *liveSession) { l.session = `{"userId":1,"user":{"id":1,"username":"admin","role":"operator"}}` })
		time.Sleep(200 * time.Millisecond)
		svc.Handle(ctx, parse(t, `{"type":"peer_offline","timestamp":"t2","data":{"id":"dev2"}}`))
		time.Sleep(100 * time.Millisecond)
		if n := len(client.Frames()); n != 1 {
			t.Errorf("%d messages: the socket kept the role it was opened with", n)
		}
		if closed, _, _ := client.Closed(); closed {
			t.Error("the socket was closed though its role keeps device.view")
		}
		client.Drop()
		srv.Close()
		reg.Close()
		check()
	})
}
