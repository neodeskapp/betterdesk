package remoterelay

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"unicode/utf8"
)

// The fuzz tests check properties of their own, and with
// REMOTERELAY_NODE_HELPER=1 (in the dev container, where Node is) they
// also ask testdata/nodeoracle/fuzzhelper.js, which runs the old
// service's message code, about every input and compare.

type nodeHelper struct {
	mu  sync.Mutex
	in  io.WriteCloser
	out *bufio.Reader
}

var (
	helperOnce sync.Once
	helper     *nodeHelper
	helperErr  error
)

func startHelper(t testing.TB) *nodeHelper {
	if os.Getenv("REMOTERELAY_NODE_HELPER") != "1" {
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
		helper = &nodeHelper{in: in, out: bufio.NewReaderSize(out, 1<<20)}
	})
	if helperErr != nil {
		t.Fatal(helperErr)
	}
	return helper
}

func (h *nodeHelper) ask(t testing.TB, kind string, data []byte) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, err := io.WriteString(h.in, kind+" "+hex.EncodeToString(data)+"\n"); err != nil {
		t.Fatal(err)
	}
	answer, err := h.out.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	return answer
}

var verdictNames = map[verdict]string{ignore: "ignore", forward: "forward", stop: "stop", tooDeep: "drop", drop: "drop"}

// seedMessages are the text messages of the recording and a few more.
func seedMessages(f *testing.F) {
	rec := loadRecording(f)
	for _, sc := range rec.Scenarios {
		for _, s := range sc.Steps {
			if s.Step.Text != nil {
				f.Add(*s.Step.Text)
			}
		}
	}
	for _, s := range []string{`null`, ` null `, `{"type":"input","event_type":null,"kind":0}`, `{"type":"status","a":{"1":[],"0":{}}}`,
		`{"__proto__":{"type":"status"},"type":"status"}`, `{"type":"input","type":"stop"}`, `"\ud800"`, `{"type":"status","x":-0.0e-0}`} {
		f.Add(s)
	}
}

func checkAgainstNode(t *testing.T, kind, text string, v verdict, out string) {
	h := startHelper(t)
	if h == nil {
		return
	}
	var want struct {
		V    string `json:"v"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(h.ask(t, kind, []byte(text)), &want); err != nil {
		t.Fatal(err)
	}
	if want.V != verdictNames[v] || want.Text != out {
		t.Fatalf("%s %q: node %s %q, go %s %q", kind, text, want.V, want.Text, verdictNames[v], out)
	}
}

// FuzzAgentMessage reads any agent text message without failing and, with
// the helper, decides as Node did and writes the same text.
func FuzzAgentMessage(f *testing.F) {
	seedMessages(f)
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) {
			// The socket closes on such text with 1007 before the relay sees it
			return
		}
		v, out := agentMessage(text)
		if v == forward && !utf8.ValidString(out) {
			t.Fatalf("%q: forwarded text that is not UTF-8: %q", text, out)
		}
		checkAgainstNode(t, "A", text, v, out)
	})
}

// FuzzViewerMessage is FuzzAgentMessage for a viewer's messages, with an
// agent there to get the input events.
func FuzzViewerMessage(f *testing.F) {
	seedMessages(f)
	f.Fuzz(func(t *testing.T, text string) {
		if !utf8.ValidString(text) {
			return
		}
		v, out := viewerMessage(text)
		checkAgainstNode(t, "V", text, v, out)
	})
}

// FuzzDecodeURIComponent decodes any agent path id as JavaScript's
// decodeURIComponent: the same text, or a URIError at the same inputs.
func FuzzDecodeURIComponent(f *testing.F) {
	for _, s := range []string{"dev1", "%64ev1", "%E0%A4%A", "%ED%A0%80", "%F0%9F%98%80", "%C2080", "%%", "a%2", "%c3%a9", "%C0%80", "%F4%90%80%80"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				// A path reaches the socket percent-encoded
				return
			}
		}
		got, ok := decodeURIComponent(s)
		if ok && !utf8.ValidString(got) {
			t.Fatalf("%q decoded to text that is not UTF-8", s)
		}
		h := startHelper(t)
		if h == nil {
			return
		}
		var want struct {
			OK    bool   `json:"ok"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(h.ask(t, "U", []byte(s)), &want); err != nil {
			t.Fatal(err)
		}
		if want.OK != ok || want.Value != got {
			t.Fatalf("%q: node %v %q, go %v %q", s, want.OK, want.Value, ok, got)
		}
	})
}
