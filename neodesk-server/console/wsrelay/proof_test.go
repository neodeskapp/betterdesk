package wsrelay

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// testProof is the proof frame of an account in these tests: a fixed
// prefix and the account's id, 0 for a guest.
func testProof(userID int64) []byte {
	return []byte{0x8c, 0xc2, 0x3e, 0x20, 0x01, 0x02, 0x03, byte(userID)}
}

// TestTheSignalServerGetsTheProofFirst: with Options.SignalProof a
// bridge's connection to the signal server starts with the proof, before
// the browser's first frame: the proof of the signed-in account, or for a
// guest the proof of the account that made the link (created_by_id), so
// that account's client features hold the guest's session too (L164); a
// link without a creator id gives a guest's proof. A connection to the
// relay never carries it.
func TestTheSignalServerGetsTheProofFirst(t *testing.T) {
	punch := framedBytes(rendezvousBytes(8, "TARGET01"))
	for _, who := range []struct {
		name    string
		path    string
		header  http.Header
		creator int64
		userID  int64
	}{
		{"account", "/ws/rendezvous", adminHeader(), 0, 1},
		{"guest", "/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil, 7, 7},
		{"guest of a link without a creator id", "/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil, 0, 0},
	} {
		t.Run(who.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			h := newHarness(t, Options{SignalProof: testProof})
			defer h.close()
			h.api.mu.Lock()
			h.api.creator = who.creator
			h.api.mu.Unlock()
			c := h.dial(who.path, who.header)
			_ = c.Binary(punch)
			want := append(testProof(who.userID), punch...)
			waitFor(t, "the proof and the punch", func() bool { return bytes.Equal(h.sig.received(1), want) })
			c.Close(1000)
		})
	}

	t.Run("relay", func(t *testing.T) {
		check := wstest.Goroutines(t)
		defer check()
		h := newHarness(t, Options{SignalProof: testProof})
		defer h.close()
		c := h.dial("/ws/relay", adminHeader())
		request := framedBytes(rendezvousBytes(18, "TARGET01"))
		_ = c.Binary(request)
		waitFor(t, "the relay request", func() bool { return len(h.sig.received(1)) >= len(request) })
		if got := h.sig.received(1); !bytes.Equal(got, request) {
			t.Fatalf("the relay got %x, want only the request %x", got, request)
		}
		c.Close(1000)
	})
}
