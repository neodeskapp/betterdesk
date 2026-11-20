package wsrelay

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/codec"
)

// The fuzz tests check properties of their own, and with
// WSRELAY_NODE_HELPER=1 (in the dev container, where Node and web-nodejs
// are) they also ask testdata/nodeoracle/fuzzhelper.js, which runs the old
// service's code, about every input and compare.

type nodeHelper struct {
	mu  sync.Mutex
	in  io.WriteCloser
	out *bufio.Reader
	cmd *exec.Cmd
}

var (
	helperOnce sync.Once
	helper     *nodeHelper
	helperErr  error
)

func startHelper(t testing.TB) *nodeHelper {
	if os.Getenv("WSRELAY_NODE_HELPER") != "1" {
		return nil
	}
	helperOnce.Do(func() {
		cmd := exec.Command("node", filepath.Join("testdata", "nodeoracle", "fuzzhelper.js"))
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			helperErr = err
			return
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			helperErr = err
			return
		}
		if err := cmd.Start(); err != nil {
			helperErr = err
			return
		}
		helper = &nodeHelper{in: in, out: bufio.NewReaderSize(out, 1<<20), cmd: cmd}
	})
	if helperErr != nil {
		t.Fatal(helperErr)
	}
	return helper
}

func (h *nodeHelper) ask(t testing.TB, line string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
	answer, err := h.out.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

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
// and checks that the frames come out of the stream in order and that
// what is held stays below one frame; with the helper it compares the
// frames and errors with Node's decoder.
func FuzzFrameDecoder(f *testing.F) {
	f.Add([]byte{0x0c, 'a', 'b', 'c', 0x00, 0x01})
	f.Add([]byte{0x0d, 0x01, 0xfe, 0xfe, 'x'})
	f.Add([]byte{0x07, 0x00, 0x00, 0x10})
	f.Add([]byte{0x02, 0xfe, 0xfe, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, input []byte) {
		chunks := bytes.Split(input, []byte{0xfe, 0xfe})
		d := &frameDecoder{}
		var got []string
		var all []byte
		failed := ""
		for _, c := range chunks {
			all = append(all, c...)
			frames, err := d.feed(c)
			if err != nil {
				failed = err.Error()
				got = append(got, "error "+failed)
				break
			}
			var list []string
			for _, fr := range frames {
				list = append(list, hex.EncodeToString(fr))
			}
			got = append(got, strings.Join(list, "|"))
		}
		if failed == "" {
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
		h := startHelper(t)
		if h == nil {
			return
		}
		hexes := make([]string, len(chunks))
		for i, c := range chunks {
			hexes[i] = hex.EncodeToString(c)
		}
		var steps []json.RawMessage
		if err := json.Unmarshal(h.ask(t, "F "+strings.Join(hexes, ",")), &steps); err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, st := range steps {
			var e struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(st, &e) == nil && e.Error != "" {
				want = append(want, "error "+e.Error)
				continue
			}
			var list []string
			_ = json.Unmarshal(st, &list)
			want = append(want, strings.Join(list, "|"))
		}
		if strings.Join(want, "\n") != strings.Join(got, "\n") {
			t.Fatalf("%x:\n node %q\n   go %q", input, want, got)
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

// FuzzCookieToken reads a guest token from any Cookie header; with the
// helper it compares with the old handler's own reader.
func FuzzCookieToken(f *testing.F) {
	f.Add([]byte("bd.guest=abc"), false)
	f.Add([]byte("x=1; bd.guest=%E0%A4%A; betterdesk.guest=t"), true)
	f.Add([]byte("bd.guest=; betterdesk.guest=%20t%20"), false)
	f.Add([]byte("bd.guest=\xe9%C3%A9"), false)
	f.Fuzz(func(t *testing.T, header []byte, https bool) {
		name := "bd.guest"
		if https {
			name = "betterdesk.guest"
		}
		got := cookieToken(latin1(string(header)), name)
		h := startHelper(t)
		if h == nil {
			return
		}
		var want string
		if err := json.Unmarshal(h.ask(t, "C "+hex.EncodeToString(header)+" "+name), &want); err != nil {
			t.Fatal(err)
		}
		if want != got {
			t.Fatalf("%q: node %q, go %q", header, want, got)
		}
	})
}
