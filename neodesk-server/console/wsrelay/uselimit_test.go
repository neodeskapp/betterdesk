package wsrelay

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
	"github.com/neodeskapp/neodesk/neodesk-server/guestaccess"
)

// lastUseAPI is the API server for a link with max_uses 1: good until its
// one use is counted, then validate answers that the use limit is
// reached, as guestaccess.Store.Validate does; revoked answers that the
// link was taken back.
type lastUseAPI struct {
	mu      sync.Mutex
	used    bool
	revoked bool
}

func (a *lastUseAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/validate"):
		switch {
		case a.revoked:
			_, _ = w.Write([]byte(`{"valid":false,"error":"guest link revoked"}`))
		case a.used:
			_, _ = w.Write([]byte(`{"valid":false,"error":"` + guestaccess.UseLimitReached + `"}`))
		default:
			_, _ = w.Write([]byte(`{"valid":true,"id":"g1","peer_ids":["TARGET01","OTHER01"],"expires_at":"2023-11-15T00:00:00Z"}`))
		}
	case strings.HasSuffix(r.URL.Path, "/consume"):
		if a.used {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"` + guestaccess.UseLimitReached + `"}`))
			return
		}
		a.used = true
		_, _ = w.Write([]byte(`{"valid":true}`))
	default:
		_, _ = w.Write([]byte(`{"success":true}`))
	}
}

// TestTheLastUseOfALinkKeepsItsSession: the use that reaches a link's
// max_uses is the guest session's own. Its relay upgrade is let in and
// its bridges stay open at the rechecks, though validate now answers that
// the use limit is reached; another guest session, the same session for
// another device and a link taken back are still refused (RV113).
func TestTheLastUseOfALinkKeepsItsSession(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	clock := &fakeClock{now: time.UnixMilli(1700000000000)}
	api := &lastUseAPI{}
	h := newHarness(t, Options{GoAPI: goapi.New(api), Clock: clock})
	defer h.close()
	signal := h.dial("/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	waitFor(t, "the signal server's connection", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 1 })
	_ = signal.Binary(framedBytes(rendezvousBytes(8, "TARGET01")))
	waitFor(t, "the punch at the server", func() bool { return len(h.sig.received(1)) > 0 })

	relay := h.dial("/ws/relay?transport=message&guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	waitFor(t, "the relay's connection", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 2 })
	_ = relay.Binary(rendezvousBytes(18, "TARGET01"))
	waitFor(t, "the relay request at the server", func() bool { return len(h.sig.received(2)) > 0 })

	for _, refused := range []string{
		"/ws/relay?transport=message&guest=tok&peer_id=TARGET01&guest_session=s2",
		"/ws/relay?transport=message&guest=tok&peer_id=OTHER01&guest_session=s1",
	} {
		if c, res, err := wstest.Dial(h.addr, refused, nil); err != nil || c != nil || res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: %v %v", refused, res, err)
			if c != nil {
				c.Drop()
			}
		}
	}

	clock.Advance(GuestRevalidationInterval)
	time.Sleep(200 * time.Millisecond)
	for name, c := range map[string]*wstest.Client{"signal": signal, "relay": relay} {
		if closed, _, _ := c.Closed(); closed {
			t.Errorf("the %s bridge ended at the recheck after its own use", name)
		}
	}

	api.mu.Lock()
	api.revoked = true
	api.mu.Unlock()
	clock.Advance(GuestRevalidationInterval)
	for name, c := range map[string]*wstest.Client{"signal": signal, "relay": relay} {
		if !c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Errorf("the %s bridge stays open after the link was taken back", name)
		}
		c.Drop()
	}
}
