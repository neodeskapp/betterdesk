package wsrelay

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// TestSplitRelayRequestReachesTheRelayWhole: a guest's relay request that
// comes in two messages is checked once it decodes and then reaches the
// relay whole, in one frame, not only its last part (RM22).
func TestSplitRelayRequestReachesTheRelayWhole(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{})
	defer h.close()
	c := h.dial("/ws/relay?transport=message&guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	waitFor(t, "the relay's connection", func() bool { return h.sig.count() == 1 })
	// request_relay{id: TARGET01, uuid: uuid-1}
	request, _ := hex.DecodeString("9201120a0854415247455430311206757569642d31")
	_ = c.Binary(request[:6])
	_ = c.Binary(request[6:])
	want := framed(t, request)
	waitFor(t, "the request at the relay", func() bool { return len(h.sig.received(1)) > 0 })
	time.Sleep(100 * time.Millisecond)
	if got := h.sig.received(1); !bytes.Equal(got, want) {
		t.Errorf("the relay got %x, want %x", got, want)
	}
	c.Close(1000)
}
