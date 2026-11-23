package goapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/events"
)

func nextFrame(t *testing.T, frames <-chan *Object) *Object {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("the channel is closed")
		}
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
	return nil
}

func TestEventsGivesTheFramesOfTheWebsocket(t *testing.T) {
	bus := events.NewBus()
	c := New(nil, WithEventBus(bus))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames, err := c.Events(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	bus.Publish(events.Event{Type: events.EventPeerIDChanged, Data: map[string]string{"old_id": "PEERBBB2", "new_id": "PEERBBB9", "source": "panel"}})
	frame := nextFrame(t, frames)

	if got := frame.Keys(); len(got) != 3 || got[0] != "type" || got[1] != "timestamp" || got[2] != "data" {
		t.Errorf("keys = %v, want type, timestamp, data", got)
	}
	if frame.Str("type") != "peer_id_changed" {
		t.Errorf("type = %v", frame.Get("type"))
	}
	if got := toJSON(t, frame.Get("data")); got != `{"new_id":"PEERBBB9","old_id":"PEERBBB2","source":"panel"}` {
		t.Errorf("data = %s", got)
	}
	stamp, err := time.Parse(time.RFC3339Nano, frame.Str("timestamp"))
	if err != nil || stamp.Before(before.Add(-time.Second)) || stamp.After(time.Now().Add(time.Second)) {
		t.Errorf("timestamp = %v (%v), want the time of the event as RFC 3339 text", frame.Get("timestamp"), err)
	}
	// The frame is what the websocket sends for the same event.
	if want := string(events.MarshalEvent(events.Event{Type: events.EventPeerIDChanged, Timestamp: stamp,
		Data: map[string]string{"old_id": "PEERBBB2", "new_id": "PEERBBB9", "source": "panel"}})); toJSON(t, frame) != want {
		t.Errorf("frame = %s\n want   %s", toJSON(t, frame), want)
	}

	// An event without data has no "data" key.
	bus.Publish(events.Event{Type: events.EventPeerOnline})
	if frame := nextFrame(t, frames); frame.Has("data") || frame.Str("type") != "peer_online" {
		t.Errorf("frame without data = %s", toJSON(t, frame))
	}

	// Values stay texts, as the bus carries them.
	bus.Publish(events.Event{Type: "device_rejected", Data: map[string]string{"device_id": "D1", "banned": "true"}})
	if frame := nextFrame(t, frames); frame.Object("data").Get("banned") != "true" {
		t.Errorf("banned = %#v, want the text \"true\"", frame.Object("data").Get("banned"))
	}
}

func TestEventsFilterAndEnd(t *testing.T) {
	bus := events.NewBus()
	c := New(nil, WithEventBus(bus))
	ctx, cancel := context.WithCancel(context.Background())
	frames, err := c.Events(ctx, "help_request")
	if err != nil {
		t.Fatal(err)
	}
	if bus.Count() != 1 {
		t.Fatalf("subscribers = %d, want 1", bus.Count())
	}

	bus.Publish(events.Event{Type: events.EventPeerOnline, Data: map[string]string{"id": "P1"}})
	bus.Publish(events.Event{Type: events.EventHelpRequest, Data: map[string]string{"id": "7", "device_id": "P1", "message": "help"}})
	frame := nextFrame(t, frames)
	if frame.Str("type") != "help_request" || frame.Object("data").Str("message") != "help" {
		t.Errorf("first frame = %s, want the help request", toJSON(t, frame))
	}

	cancel()
	select {
	case _, ok := <-frames:
		if ok {
			t.Error("a frame arrived after the context ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the channel was not closed")
	}
	for deadline := time.Now().Add(2 * time.Second); bus.Count() != 0; {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers = %d after the context ended, want 0", bus.Count())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEventsWithoutBus(t *testing.T) {
	frames, err := New(nil).Events(context.Background(), "")
	if !errors.Is(err, ErrNoEventBus) || frames != nil {
		t.Errorf("Events without a bus = %v, %v", frames, err)
	}
}
