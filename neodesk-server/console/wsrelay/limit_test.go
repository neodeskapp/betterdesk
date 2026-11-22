package wsrelay

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestAddressLimitCountsTheTrustedAddress: the bridges are counted per
// client address as the TRUST_PROXY setting gives it, not by the first
// X-Forwarded-For entry the client wrote. Without a trusted proxy a socket
// that says it is 10.0.0.2 shares the budget of its connection's address;
// behind a trusted proxy on loopback it has a budget of its own (RM24).
func TestAddressLimitCountsTheTrustedAddress(t *testing.T) {
	loopback, err := httpx.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		clientIP  func(*http.Request) string
		otherGets bool
	}{
		{"trust nobody", nil, false},
		{"trust loopback", loopback.ClientIP, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			sig := newSink(t)
			defer sig.close()
			svc := New(Options{
				SignalHost: "127.0.0.1", SignalPort: sig.port(), RelayHost: "127.0.0.1", RelayPort: sig.port(),
				Interfaces: func() []InterfaceAddr { return nil },
				GoAPI:      goapi.New(&guestAPI{}),
			})
			reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: oraclePermission, ClientIP: c.clientIP})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			defer func() { reg.Close(); srv.Close() }()
			addr := strings.TrimPrefix(srv.URL, "http://")
			from := func(ip string) http.Header {
				h := adminHeader()
				h.Set("X-Forwarded-For", ip)
				return h
			}
			var five []*wstest.Client
			for i := 0; i < MaxConnectionsPerIP; i++ {
				c, _, err := wstest.Dial(addr, "/ws/rendezvous", from("10.0.0.1"))
				if err != nil || c == nil {
					t.Fatalf("socket %d: %v", i, err)
				}
				five = append(five, c)
				waitFor(t, "the bridge counted", func() bool { return sig.count() == i+1 })
			}
			other, _, err := wstest.Dial(addr, "/ws/rendezvous", from("10.0.0.2"))
			if err != nil || other == nil {
				t.Fatalf("dial: %v", err)
			}
			closed := other.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			if closed == c.otherGets {
				_, code, reason := other.Closed()
				t.Errorf("a socket that says it is 10.0.0.2: closed %v (%d %q), want a bridge of its own %v", closed, code, reason, c.otherGets)
			}
			for _, s := range five {
				s.Close(1000)
			}
			other.Close(1000)
		})
	}
}

// count is how many connections the sink accepted.
func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
