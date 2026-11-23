package remoterelay

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// A viewer's input reaches the agent only as one of the input kinds the
// agent handles (mouse_move, mouse_down, mouse_up, wheel, key_down,
// key_up, key_press, from event_type, else kind). The relay's own start
// and stop, a type that is no text and any other kind are dropped, so one
// viewer cannot start or stop the stream for all through input (RM5).
func TestViewerInputCannotControlTheStream(t *testing.T) {
	check := wstest.Goroutines(t)
	_, addr, stop := testRelay(t, nil)
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
		t.Fatalf("the agent got %q", texts(agent.Frames()))
	}
	for _, input := range []string{
		`{"type":"input","event_type":"stop"}`,
		`{"type":"input","event_type":"start"}`,
		`{"type":"input","kind":"stop"}`,
		`{"type":"input","event_type":5}`,
		`{"type":"input","event_type":{"a":1}}`,
		`{"type":"input","event_type":["mouse_move"]}`,
		`{"type":"input"}`,
		`{"type":"input","event_type":"click"}`,
		`{"type":"input","event_type":"ping"}`,
		`{"type":"input","event_type":"mouse_move","x":1,"y":2}`,
		`{"type":"input","kind":"key_down","key":"a"}`,
		`{"type":"input","event_type":"","kind":"wheel","delta_y":3}`,
	} {
		if err := viewer.Text(input); err != nil {
			t.Fatal(err)
		}
	}
	agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) >= 4 })
	time.Sleep(100 * time.Millisecond)
	got := texts(agent.Frames()[1:])
	want := []string{
		`{"type":"mouse_move","x":1,"y":2}`,
		`{"type":"key_down","kind":"key_down","key":"a"}`,
		`{"type":"wheel","kind":"wheel","delta_y":3}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the agent got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	agent.Close(1000)
	viewer.Close(1000)
	stop()
	check()
}

// A viewer whose account may not use remote control watches: its input
// never reaches the agent, also when the lookup of its features fails.
// The viewer still gets the screen and may stop the stream for itself.
func TestAViewerWithoutRemoteControlOnlyWatches(t *testing.T) {
	for _, c := range []struct {
		name    string
		off     []string
		err     error
		reaches bool
	}{
		{"remote control on", []string{"clipboard", "client_actions"}, nil, true},
		{"remote control off", []string{"remote_control"}, nil, false},
		{"lookup failed", nil, errors.New("store down"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			var mu sync.Mutex
			var asked []int64
			_, addr, stop := testRelay(t, func(o *Options) {
				o.FeaturesOff = func(_ context.Context, userID int64) ([]string, error) {
					mu.Lock()
					defer mu.Unlock()
					asked = append(asked, userID)
					return c.off, c.err
				}
			})
			agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
			viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
			if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
				t.Fatalf("the agent got %q", texts(agent.Frames()))
			}
			if err := viewer.Text(`{"type":"input","event_type":"mouse_move","x":1,"y":2}`); err != nil {
				t.Fatal(err)
			}
			if err := viewer.Text(`{"type":"input","kind":"key_down","key":"a"}`); err != nil {
				t.Fatal(err)
			}
			agent.Wait(300*time.Millisecond, func(f []wstest.Frame, _ bool) bool { return len(f) >= 3 })
			got := texts(agent.Frames()[1:])
			if c.reaches && len(got) != 2 {
				t.Errorf("the agent got %q, want both events", got)
			}
			if !c.reaches && len(got) != 0 {
				t.Errorf("the agent got %q from a viewer that may not control", got)
			}
			mu.Lock()
			if len(asked) != 1 || asked[0] != 7 {
				t.Errorf("features looked up for %v, want once for the viewer's account 7", asked)
			}
			mu.Unlock()
			agent.Close(1000)
			viewer.Close(1000)
			stop()
			check()
		})
	}
}
