package cdap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// sessionBrowser is the browser side of a session: the socket the gateway
// writes to, and what the browser got.
func sessionBrowser(t *testing.T) (*websocket.Conn, <-chan string) {
	t.Helper()
	got := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				close(got)
				return
			}
			got <- string(data)
		}
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, got
}

// nothingFor fails the test when the browser gets a message within d.
func nothingFor(t *testing.T, got <-chan string, d time.Duration, what string) {
	t.Helper()
	select {
	case m, ok := <-got:
		if ok {
			t.Errorf("%s: the browser got %s", what, m)
		} else {
			t.Errorf("%s: the browser was closed", what)
		}
	case <-time.After(d):
	}
}

// TestDeviceReachesOnlyItsOwnSessions: a device's terminal output, frames,
// file responses, audio and session ends reach only the sessions opened
// on its own connection; another device that names a session's id gets
// nowhere (BUGS RV164).
func TestDeviceReachesOnlyItsOwnSessions(t *testing.T) {
	w := newCDAPWorld(t)
	createAPIKey(t, w.db, "apikey-a-0123456789abcdef", "a", "global_admin", "")
	createAPIKey(t, w.db, "apikey-b-0123456789abcdef", "b", "global_admin", "")
	devA := w.connect(t, "apikey-a-0123456789abcdef", "dev-A")
	devB := w.connect(t, "apikey-b-0123456789abcdef", "dev-B")
	devA.drain()
	devB.drain()
	w.settle(t, 2)
	connA := w.g.GetDeviceConn("dev-A")

	term, termGot := sessionBrowser(t)
	w.g.terminalSessions.Store("term_a", &TerminalSession{ID: "term_a", DeviceID: "dev-A", browser: term, deviceConn: connA})
	desk, deskGot := sessionBrowser(t)
	w.g.desktopSessions.Store("desk_a", &DesktopSession{ID: "desk_a", DeviceID: "dev-A", browser: desk, deviceConn: connA})
	files, filesGot := sessionBrowser(t)
	w.g.fileSessions.Store("file_a", &FileSession{ID: "file_a", DeviceID: "dev-A", browser: files, deviceConn: connA})
	video, videoGot := sessionBrowser(t)
	w.g.videoSessions.Store("vid_a", &VideoSession{ID: "vid_a", DeviceID: "dev-A", browser: video, deviceConn: connA})
	audio, audioGot := sessionBrowser(t)
	w.g.audioSessions.Store("aud_a", &AudioSession{ID: "aud_a", DeviceID: "dev-A", browser: audio, deviceConn: connA})

	frame := make([]byte, frameHeaderSize+4)
	copy(frame, "desk_a")
	copy(frame[frameHeaderSize:], "JPEG")
	for _, dev := range []*cdapClient{devB, devA} {
		dev.send("terminal_output", map[string]any{"session_id": "term_a", "data": "output"})
		dev.send("desktop_frame", map[string]any{"session_id": "desk_a", "data": "frame"})
		dev.send("file_list_response", map[string]any{"session_id": "file_a", "entries": []any{}})
		dev.send("video_frame", map[string]any{"session_id": "vid_a", "data": "frame"})
		dev.send("audio_frame", map[string]any{"session_id": "aud_a", "data": "frame"})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := dev.conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Fatal(err)
		}
		cancel()
		if dev == devB {
			for end, id := range map[string]string{"terminal_end": "term_a", "desktop_end": "desk_a", "desktop_consent_denied": "desk_a",
				"file_end": "file_a", "video_end": "vid_a", "audio_end": "aud_a"} {
				dev.send(end, map[string]any{"session_id": id, "reason": "ended by dev-B"})
			}
			for what, got := range map[string]<-chan string{"terminal": termGot, "desktop": deskGot, "files": filesGot, "video": videoGot, "audio": audioGot} {
				nothingFor(t, got, 300*time.Millisecond, "dev-B into the "+what+" session of dev-A")
			}
			continue
		}
		for what, got := range map[string]<-chan string{"terminal": termGot, "files": filesGot, "video": videoGot, "audio": audioGot} {
			select {
			case <-got:
			case <-time.After(3 * time.Second):
				t.Errorf("dev-A's own %s session got nothing", what)
			}
		}
		for i := 0; i < 2; i++ {
			select {
			case <-deskGot:
			case <-time.After(3 * time.Second):
				t.Errorf("dev-A's own desktop session got %d of its two frames", i)
			}
		}
	}
}

// TestSessionIDsAreRandom: a session id is its kind and 32 random hex
// digits, nothing a device could work out from the time (BUGS RV164).
func TestSessionIDsAreRandom(t *testing.T) {
	pattern := regexp.MustCompile(`^term_[0-9a-f]{32}$`)
	a, b := newSessionID("term"), newSessionID("term")
	if !pattern.MatchString(a) || a == b {
		t.Fatalf("session ids %q and %q, want term_ and 32 random hex digits each", a, b)
	}
}
