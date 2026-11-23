package wsrelay

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// scopedOptions are a device table with TARGET01 and OTHER01 and a device
// scope in which the account "scoped" sees TARGET01 only.
func scopedOptions() Options {
	return Options{
		Devices: func(_ context.Context, id string) (*goapi.Object, error) {
			if id == "TARGET01" || id == "OTHER01" {
				return goapi.NewObject("id", id), nil
			}
			return nil, nil
		},
		CanAccess: func(_ context.Context, user *session.User, device *goapi.Object) (bool, error) {
			if user == nil || user.Username != "scoped" {
				return true, nil
			}
			return device != nil && device.Str("id") == "TARGET01", nil
		},
	}
}

func scopedHeader() http.Header {
	return http.Header{"X-Oracle-Session": {`{"userId":21,"user":{"id":21,"username":"scoped","role":"operator"}}`}}
}

// TestScopedAccountReachesOnlyItsDevices: the frames of an account with a
// device scope are read, and a punch or relay request for a device outside
// the scope, or one the console does not know, ends the bridge before the
// server gets it; a request for a device in the scope goes through, and an
// account without a limit is not read (RM23).
func TestScopedAccountReachesOnlyItsDevices(t *testing.T) {
	for _, c := range []struct {
		name    string
		path    string
		header  http.Header
		message []byte
		passes  bool
	}{
		{"punch inside", "/ws/rendezvous", scopedHeader(), framedBytes(rendezvousBytes(8, "TARGET01")), true},
		{"punch outside", "/ws/rendezvous", scopedHeader(), framedBytes(rendezvousBytes(8, "OTHER01")), false},
		{"punch unknown", "/ws/rendezvous", scopedHeader(), framedBytes(rendezvousBytes(8, "NOPE0001")), false},
		{"relay request outside", "/ws/rendezvous", scopedHeader(), framedBytes(rendezvousBytes(18, "OTHER01")), false},
		{"health check", "/ws/rendezvous", scopedHeader(), framedBytes([]byte{0xd2, 0x01, 0x00}), true},
		{"relay inside", "/ws/relay?transport=message", scopedHeader(), rendezvousBytes(18, "TARGET01"), true},
		{"relay outside", "/ws/relay?transport=message", scopedHeader(), rendezvousBytes(18, "OTHER01"), false},
		{"without a limit", "/ws/rendezvous", adminHeader(), framedBytes(rendezvousBytes(8, "OTHER01")), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			h := newHarness(t, scopedOptions())
			defer h.close()
			client := h.dial(c.path, c.header)
			waitFor(t, "the server's connection", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 1 })
			_ = client.Binary(c.message)
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

// framedBytes is a payload with the BytesCodec header.
func framedBytes(payload []byte) []byte {
	b, err := EncodeRelayFrame(payload)
	if err != nil {
		panic(err)
	}
	return bytes.Clone(b)
}
