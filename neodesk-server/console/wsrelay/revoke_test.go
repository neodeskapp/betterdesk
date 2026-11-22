package wsrelay

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestRevokeRightAfterTheBridgeIsListed takes a guest link back as soon as
// its bridge is listed, while nothing else of the bridge has run (its dial
// waits): the bridge ends and is no longer listed, and under -race the
// revoke and the start of the bridge share nothing unordered (NL5).
func TestRevokeRightAfterTheBridgeIsListed(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer h.close()
	listed := make(chan struct{})
	go func() {
		defer close(listed)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.svc.mu.Lock()
			n := len(h.svc.grants["g1"])
			h.svc.mu.Unlock()
			if n > 0 {
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()
	c, _, err := wstest.Dial(h.addr, "/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	<-listed
	if n := h.svc.InvalidateGuestGrant("g1"); n != 0 {
		t.Errorf("%d bridges still listed after the revoke", n)
	}
	if !c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the guest's bridge stays open")
	}
	c.Drop()
}
