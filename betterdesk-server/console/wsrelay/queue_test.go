package wsrelay

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// TestWaitingMessagesAreBoundedByBytes: what the browser sends while the
// server's connection is not up waits in the bridge up to
// writeQueueBytes; beyond that the bridge stops reading the socket, so a
// browser that keeps sending is held up instead of filling the console's
// memory with up to 64 messages of 100 MiB each (RM41).
func TestWaitingMessagesAreBoundedByBytes(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer h.close()
	c := h.dial("/ws/relay", adminHeader())
	const size, count = 1 << 20, 48
	var sent atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		message := make([]byte, size)
		for range count {
			if c.Binary(message) != nil {
				return
			}
			sent.Add(1)
		}
	}()
	// Wait until the browser cannot send more
	last, still := int64(-1), 0
	for still < 50 && sent.Load() < count {
		time.Sleep(20 * time.Millisecond)
		if n := sent.Load(); n != last {
			last, still = n, 0
			continue
		}
		still++
	}
	// The bridge holds writeQueueBytes and the message it waits to queue;
	// the rest is in the sockets' buffers of the operating system
	if n := sent.Load(); n*size > writeQueueBytes+size+12<<20 {
		t.Errorf("the browser sent %d MiB while nothing went to the server", n)
	}
	c.Drop()
	<-done
}
