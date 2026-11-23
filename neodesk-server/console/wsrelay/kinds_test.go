package wsrelay

import (
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// TestOnlyTheWebClientsMessagesPass: on /ws/rendezvous a guest and an
// account with a device scope may send only what the web client sends
// (punch_hole_request, request_relay, a health check). A key_exchange,
// which would turn the rest of the connection into ciphertext the bridge
// cannot read, another kind of message, and a frame with fields the
// schema does not know (ciphertext read as protobuf is that) end the
// bridge before the signal server gets them (RV108).
func TestOnlyTheWebClientsMessagesPass(t *testing.T) {
	keyExchange, err := proto.Marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_KeyExchange{
		KeyExchange: &pb.KeyExchange{Keys: [][]byte{make([]byte, 32)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	online, err := proto.Marshal(&pb.RendezvousMessage{Union: &pb.RendezvousMessage_OnlineRequest{
		OnlineRequest: &pb.OnlineRequest{Id: "TARGET01", Peers: []string{"OTHER01"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Field 100 (varint) and field 101 (bytes): unknown to the schema.
	unknownOnly := []byte{0xa0, 0x06, 0x01, 0xaa, 0x06, 0x02, 0x41, 0x42}
	punchWithUnknown := append(rendezvousBytes(8, "TARGET01"), 0xa0, 0x06, 0x01)
	innerUnknown := append([]byte{0x0a, 0x08}, "TARGET01"...)
	innerUnknown = append(innerUnknown, 0xa0, 0x06, 0x01)
	nestedUnknown := append([]byte{0x42, byte(len(innerUnknown))}, innerUnknown...)

	guest := "/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1"
	for _, who := range []struct {
		name   string
		path   string
		header http.Header
	}{
		{"guest", guest, nil},
		{"scoped account", "/ws/rendezvous", scopedHeader()},
	} {
		for _, c := range []struct {
			name    string
			payload []byte
			passes  bool
		}{
			{"punch for its device", rendezvousBytes(8, "TARGET01"), true},
			{"relay request for its device", rendezvousBytes(18, "TARGET01"), true},
			{"health check", []byte{0xd2, 0x01, 0x00}, true},
			{"key exchange", keyExchange, false},
			{"online request", online, false},
			{"empty message", []byte{}, false},
			{"unknown fields only", unknownOnly, false},
			{"punch with an unknown field", punchWithUnknown, false},
			{"punch with an unknown field inside", nestedUnknown, false},
		} {
			t.Run(who.name+"/"+c.name, func(t *testing.T) {
				check := wstest.Goroutines(t)
				defer check()
				h := newHarness(t, scopedOptions())
				defer h.close()
				client := h.dial(who.path, who.header)
				waitFor(t, "the server's connection", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 1 })
				_ = client.Binary(framedBytes(c.payload))
				if c.passes {
					waitFor(t, "the message at the server", func() bool { return len(h.sig.received(1)) > 0 })
					if closed, _, _ := client.Closed(); closed {
						t.Error("the bridge ended")
					}
					client.Close(1000)
					return
				}
				if !client.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
					t.Fatal("the bridge stays open")
				}
				time.Sleep(100 * time.Millisecond)
				if got := h.sig.received(1); len(got) != 0 {
					t.Errorf("the server got %x", got)
				}
				client.Drop()
			})
		}
	}
}
