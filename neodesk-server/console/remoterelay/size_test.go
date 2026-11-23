package remoterelay

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// allocated is how many bytes f allocates.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// zeros is a JSON object with the type typ and an array of n zeros, the
// message that costs the most per byte to read as a tree.
func zeros(typ string, n int) string {
	return `{"type":"` + typ + `","a":[0` + strings.Repeat(",0", n-1) + `]}`
}

// TestMessagesTheRelayDropsAreNotBuilt: a text message whose type the
// relay does not act on is dropped after its top level is read, without
// building its values, so it costs no more than its own size; one it acts
// on is still read whole (RV112).
func TestMessagesTheRelayDropsAreNotBuilt(t *testing.T) {
	const n = 256 << 10
	for _, c := range []struct {
		name string
		text string
		read func(string) (verdict, string)
		want verdict
	}{
		{"agent, other type", zeros("other", n), agentMessage, ignore},
		{"agent, no type", `{"a":[0` + strings.Repeat(",0", n-1) + `]}`, agentMessage, ignore},
		{"viewer, other type", zeros("other", n), viewerMessage, ignore},
		{"viewer, array", `[0` + strings.Repeat(",0", n-1) + `]`, viewerMessage, ignore},
	} {
		t.Run(c.name, func(t *testing.T) {
			var v verdict
			bytes := allocated(func() { v, _ = c.read(c.text) })
			if v != c.want {
				t.Errorf("verdict %d, want %d", v, c.want)
			}
			if limit := uint64(len(c.text)); bytes > limit {
				t.Errorf("%d bytes allocated for a message of %d", bytes, len(c.text))
			}
		})
	}
	if v, out := agentMessage(zeros("status", 3)); v != forward || out != zeros("status", 3) {
		t.Errorf("a status message: %d %q", v, out)
	}
}

// TestAgentTextOverItsLimitIsDropped: an agent's text message over
// MaxAgentText is dropped unread, whatever its type; one at the limit is
// read (RV112).
func TestAgentTextOverItsLimitIsDropped(t *testing.T) {
	at := `{"type":"status","s":"` + strings.Repeat("x", MaxAgentText-len(`{"type":"status","s":""}`)) + `"}`
	if len(at) != MaxAgentText {
		t.Fatalf("message of %d bytes", len(at))
	}
	if v, out := agentMessage(at); v != forward || out != at {
		t.Errorf("a status message at the limit: %d", v)
	}
	over := `{"type":"status","s":"` + strings.Repeat("x", MaxAgentText-len(`{"type":"status","s":""}`)+1) + `"}`
	if v, _ := agentMessage(over); v != ignore {
		t.Errorf("a status message over the limit: %d", v)
	}
	var v verdict
	big := zeros("status", MaxAgentText)
	if bytes := allocated(func() { v, _ = agentMessage(big) }); v != ignore || bytes > MaxAgentText {
		t.Errorf("a status message of zeros over the limit: %d, %d bytes allocated", v, bytes)
	}
}

// TestViewerTextOverItsLimitClosesTheSocket: the viewer socket takes text
// up to MaxViewerMessage; a bigger message closes it with 1009 (RV112).
func TestViewerTextOverItsLimitClosesTheSocket(t *testing.T) {
	check := wstest.Goroutines(t)
	_, addr, stop := testRelay(t, nil)
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
		t.Fatalf("the agent got %q", texts(agent.Frames()))
	}
	pad := func(size int) string {
		head := `{"type":"input","event_type":"mouse_move","p":""}`
		return `{"type":"input","event_type":"mouse_move","p":"` + strings.Repeat("x", size-len(head)) + `"}`
	}
	if err := viewer.Text(pad(MaxViewerMessage)); err != nil {
		t.Fatal(err)
	}
	if !agent.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 2 }) {
		t.Fatal("the input at the limit did not reach the agent")
	}
	_ = viewer.Text(pad(MaxViewerMessage + 1))
	if !viewer.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the viewer stays open after a message over the limit")
	}
	if _, code, _ := viewer.Closed(); code != 1009 {
		t.Errorf("closed with %d, want 1009", code)
	}
	time.Sleep(50 * time.Millisecond)
	for _, text := range texts(agent.Frames()[2:]) {
		if strings.Contains(text, `"p":`) {
			t.Errorf("the input over the limit reached the agent")
		}
	}
	agent.Close(1000)
	viewer.Drop()
	stop()
	check()
}
