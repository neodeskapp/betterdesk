package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/events"
)

// runEventLinkSuite joins two buses through links made by newLink and
// checks an event crosses once, marked remote, and doesn't come back.
func runEventLinkSuite(t *testing.T, newLink func(t *testing.T) events.Link, ready func(ctx context.Context) error) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, b := events.NewBus(), events.NewBus()
	a.Join(ctx, "pod-a", newLink(t))
	b.Join(ctx, "pod-b", newLink(t))
	subA := a.Subscribe("")
	subB := b.Subscribe("")
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	if err := ready(rctx); err != nil {
		t.Fatalf("links not ready: %v", err)
	}

	a.Publish(events.Event{Type: events.EventPeerOnline, Data: map[string]string{"id": "123456789"}})

	got := recv(t, subA.Ch)
	if got.Remote || got.Data["id"] != "123456789" {
		t.Fatalf("local subscriber got %+v", got)
	}
	got = recv(t, subB.Ch)
	if !got.Remote || got.Type != events.EventPeerOnline || got.Data["id"] != "123456789" {
		t.Fatalf("other pod got %+v, want the event marked remote", got)
	}
	// Neither side gets a second copy: no echo back to the publisher and no
	// re-forwarding of a remote event.
	select {
	case e := <-subA.Ch:
		t.Fatalf("publisher heard its own event again: %+v", e)
	case e := <-subB.Ch:
		t.Fatalf("other pod heard the event twice: %+v", e)
	case <-time.After(300 * time.Millisecond):
	}
}

func recv(t *testing.T, ch <-chan events.Event) events.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("no event arrived")
		return events.Event{}
	}
}

func TestMemEventLink(t *testing.T) {
	hub := events.NewMemHub()
	runEventLinkSuite(t, func(*testing.T) events.Link { return hub }, func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
}

func TestValkeyEventLink(t *testing.T) {
	v := testValkey(t)
	l := NewEventLink(v)
	runEventLinkSuite(t, func(*testing.T) events.Link { return NewEventLink(v) }, func(ctx context.Context) error {
		return l.Ready(ctx, 2)
	})
}
