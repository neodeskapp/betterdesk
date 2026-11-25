package wsrelay

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// testProof is the proof frame of an account in these tests: a fixed
// prefix, the account's id (0 for a guest) and its console session.
func testProof(userID int64, consoleSession string) []byte {
	return append([]byte{0x8c, 0xc2, 0x3e, 0x20, 0x01, 0x02, 0x03, byte(userID)}, consoleSession...)
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
			want := append(testProof(who.userID, ""), punch...)
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

// TestTheProofNamesTheConsoleSession: the proof of a signed-in account
// names the handle of the console session of its upgrade (A239), so the
// relay ticket names the browser whose viewer opened the session and
// saved sign-in hands its one use to that browser only.
func TestTheProofNamesTheConsoleSession(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{SignalProof: testProof})
	defer h.close()
	header := adminHeader()
	header.Set("X-Test-Session-Id", "sid-of-the-first-browser")
	c := h.dial("/ws/rendezvous", header)
	punch := framedBytes(rendezvousBytes(8, "TARGET01"))
	_ = c.Binary(punch)
	want := append(testProof(1, session.Handle("sid-of-the-first-browser")), punch...)
	waitFor(t, "the proof with the console session", func() bool { return bytes.Equal(h.sig.received(1), want) })
	c.Close(1000)
}

// TestEndingTheConsoleSessionClosesItsViewersRelay: when a console session
// ends (signed out from another browser, by an admin, by the limit), the
// relay bridge of its viewer closes its connection to the relay at once,
// so the relay session ends and nothing more, a saved sign-in included,
// goes over it; another browser's bridge of the same account stays.
func TestEndingTheConsoleSessionClosesItsViewersRelay(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{SignalProof: testProof})
	defer h.close()
	request := framedBytes(rendezvousBytes(18, "TARGET01"))
	open := func(sid string) *wstest.Client {
		header := adminHeader()
		header.Set("X-Test-Session-Id", sid)
		c := h.dial("/ws/relay", header)
		_ = c.Binary(request)
		return c
	}
	first := open("sid-a")
	waitFor(t, "the first relay connection", func() bool { return len(h.sig.received(1)) >= len(request) })
	second := open("sid-b")
	waitFor(t, "the second relay connection", func() bool { return len(h.sig.received(2)) >= len(request) })

	h.reg.EndSessions([]string{"sid-a"})
	waitFor(t, "the first bridge's relay connection to close", func() bool { return h.sig.ended(1) })
	if !first.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the first browser's socket stayed open")
	}
	time.Sleep(100 * time.Millisecond)
	if h.sig.ended(2) {
		t.Error("the other browser's relay connection closed too")
	}
	if closed, code, reason := second.Closed(); closed {
		t.Errorf("the other browser's socket closed: %d %q", code, reason)
	}
	second.Close(1000)
}
