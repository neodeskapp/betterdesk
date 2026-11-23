package wsrelay

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/codec"
)

// The fuzz tests check properties of their own. The answers of the old
// service for the recorded inputs are pinned by the replays of
// testdata/node_codec.json and testdata/node_relay.json.

// FuzzRendezvousDecode decodes any payload without failing to stop and
// reads it as the signal server does (codec.DecodeUDP): decoded or not,
// and the request with its id. The check reads frames as the servers do
// (RM20), so protobufjs is not asked.
func FuzzRendezvousDecode(f *testing.F) {
	rec := loadCodec(f)
	for _, v := range rec.Vectors {
		b, _ := hex.DecodeString(v.Hex)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		msg, err := decodeRendezvous(payload)
		if (msg == nil) == (err == nil) {
			t.Fatalf("message %v and error %v", msg, err)
		}
		server, serverErr := codec.DecodeUDP(payload)
		if (serverErr == nil) != (msg != nil) {
			t.Fatalf("%x: decoded %v, the server %v", payload, err, serverErr)
		}
		if msg == nil {
			return
		}
		read := func(r *rendezvousRequest) string {
			if r == nil {
				return "none"
			}
			return "id " + r.ID
		}
		punch, relay := "none", "none"
		if p := server.GetPunchHoleRequest(); p != nil {
			punch = "id " + p.GetId()
		}
		if r := server.GetRequestRelay(); r != nil {
			relay = "id " + r.GetId()
		}
		if read(msg.PunchHoleRequest) != punch || read(msg.RequestRelay) != relay {
			t.Fatalf("%x: the server reads %v, the check %+v %+v", payload, server, msg.PunchHoleRequest, msg.RequestRelay)
		}
	})
}

// FuzzFrameDecoder feeds a stream in chunks (the input split at 0xFE 0xFE)
// without failing and, when the chunks decode, checks that the decoder
// fed the whole stream at once makes no bytes up.
func FuzzFrameDecoder(f *testing.F) {
	f.Add([]byte{0x0c, 'a', 'b', 'c', 0x00, 0x01})
	f.Add([]byte{0x0d, 0x01, 0xfe, 0xfe, 'x'})
	f.Add([]byte{0x07, 0x00, 0x00, 0x10})
	f.Add([]byte{0x02, 0xfe, 0xfe, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, input []byte) {
		chunks := bytes.Split(input, []byte{0xfe, 0xfe})
		d := &frameDecoder{}
		var all []byte
		failed := false
		for _, c := range chunks {
			all = append(all, c...)
			if _, err := d.feed(c); err != nil {
				failed = true
				break
			}
		}
		if !failed {
			// The frames and what is held are the stream again.
			var rebuilt []byte
			rd := &frameDecoder{}
			frames, _ := rd.feed(all)
			for _, fr := range frames {
				rebuilt = append(rebuilt, fr...)
			}
			if len(rebuilt) > len(all) || len(rd.buf) > len(all) {
				t.Fatalf("decoder made bytes up")
			}
		}
	})
}

// FuzzEncodeRelayFrame: every payload up to the limit comes back out of
// the decoder as it went in, with the shortest header.
func FuzzEncodeRelayFrame(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add([]byte("abc"), 70)
	f.Add([]byte{1}, 16400)
	f.Fuzz(func(t *testing.T, seed []byte, extra int) {
		if extra < 0 {
			extra = -extra
		}
		payload := append(append([]byte{}, seed...), make([]byte, extra%(1<<22+64))...)
		frame, err := EncodeRelayFrame(payload)
		if err != nil {
			t.Fatal(err)
		}
		header := len(frame) - len(payload)
		want := 1
		switch {
		case len(payload) > 0x3FFFFF:
			want = 4
		case len(payload) > 0x3FFF:
			want = 3
		case len(payload) > 0x3F:
			want = 2
		}
		if header != want || int(frame[0]&3)+1 != want {
			t.Fatalf("length %d: header of %d bytes", len(payload), header)
		}
		d := &frameDecoder{}
		frames, err := d.feed(frame)
		if err != nil || len(frames) != 1 || !bytes.Equal(frames[0], payload) || len(d.buf) != 0 {
			t.Fatalf("length %d: round trip failed: %v", len(payload), err)
		}
	})
}

// FuzzCookieToken reads a guest token from any Cookie header without
// failing.
func FuzzCookieToken(f *testing.F) {
	f.Add([]byte("nd.guest=abc"), false)
	f.Add([]byte("x=1; nd.guest=%E0%A4%A; neodesk.guest=t"), true)
	f.Add([]byte("nd.guest=; neodesk.guest=%20t%20"), false)
	f.Add([]byte("nd.guest=\xe9%C3%A9"), false)
	f.Fuzz(func(t *testing.T, header []byte, https bool) {
		name := "nd.guest"
		if https {
			name = "neodesk.guest"
		}
		_ = cookieToken(latin1(string(header)), name)
	})
}
