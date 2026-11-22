package remoterelay

import (
	"testing"
	"unicode/utf8"
)

// The fuzz tests check properties of their own. What the old service
// decided for each message of the recording is pinned by the replay of
// testdata/node_remote.json.

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

// FuzzAgentMessage reads any agent text message without failing and
// forwards only UTF-8 text.
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
		if v == forward && !utf8.ValidString(out) {
			t.Fatalf("%q: forwarded text that is not UTF-8: %q", text, out)
		}
	})
}

// FuzzDecodeURIComponent decodes any agent path id without failing and
// gives only UTF-8 text.
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
	})
}
