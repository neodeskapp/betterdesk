package remoterelay

import (
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// One viewer's stop pauses only that viewer: it gets stream-stopped and
// no more frames, the others keep watching, and the agent is told to stop
// once no viewer wants the stream (RM7).
func TestOneViewersStopLeavesTheOthersWatching(t *testing.T) {
	check := wstest.Goroutines(t)
	_, addr, stop := testRelay(t, nil)
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	a := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
		t.Fatalf("the agent got %q", texts(agent.Frames()))
	}
	b := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	b.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 })
	// The agent may register after a joined, so a gets agent-ready before
	// stream-started; wait for stream-started itself, not a frame count.
	if !a.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return hasText(f, `{"type":"stream-started"}`) }) {
		t.Fatalf("the first viewer got %q", texts(a.Frames()))
	}
	before := map[*wstest.Client]int{a: len(a.Frames()), b: len(b.Frames()), agent: len(agent.Frames())}
	since := func(c *wstest.Client) []string { return texts(c.Frames()[before[c]:]) }

	_ = a.Text(`{"type":"stop"}`)
	a.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > before[a] })
	time.Sleep(100 * time.Millisecond)
	if got := since(a); len(got) != 1 || got[0] != `{"type":"stream-stopped","reason":"operator requested stop"}` {
		t.Errorf("the viewer that stopped got %q", got)
	}
	if got := since(b); len(got) != 0 {
		t.Errorf("the other viewer got %q", got)
	}
	if got := since(agent); len(got) != 0 {
		t.Errorf("the agent got %q while a viewer still watches", got)
	}

	_ = agent.Binary([]byte{1, 2, 3})
	b.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > before[b] })
	time.Sleep(100 * time.Millisecond)
	if got := len(b.Frames()) - before[b]; got != 1 {
		t.Errorf("the watching viewer got %d frames, want 1", got)
	}
	if got := len(a.Frames()) - before[a]; got != 1 {
		t.Errorf("the viewer that stopped got %d messages after its stop, want only stream-stopped", got)
	}

	_ = b.Text(`{"type":"stop"}`)
	if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) > before[agent] }) {
		t.Fatal("the agent was not told to stop once nobody watched")
	}
	if got := since(agent); len(got) != 1 || got[0] != `{"type":"stop"}` {
		t.Errorf("the agent got %q", got)
	}
	agent.Close(1000)
	a.Close(1000)
	b.Close(1000)
	stop()
	check()
}

// hasText reports whether one of the frames is the text message want.
func hasText(frames []wstest.Frame, want string) bool {
	for _, got := range texts(frames) {
		if got == want {
			return true
		}
	}
	return false
}
