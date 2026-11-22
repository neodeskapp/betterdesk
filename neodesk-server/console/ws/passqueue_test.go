package ws

import (
	"sync"
	"testing"
	"time"
)

// TestPassQueueIsBoundedByBytes: the messages that wait for the upstream
// are held to passQueueBytes as well as to passQueue messages; the socket
// is not read further until a message has gone out. One message larger
// than the bound still waits when nothing else does.
func TestPassQueueIsBoundedByBytes(t *testing.T) {
	newPassing := func() *passing {
		p := &passing{opts: PassOptions{Queue: true}}
		p.cond = sync.NewCond(&p.mu)
		return p
	}
	pushed := func(p *passing, m passMessage) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			p.push(m)
			close(done)
		}()
		return done
	}
	waits := func(done <-chan struct{}) bool {
		select {
		case <-done:
			return false
		case <-time.After(100 * time.Millisecond):
			return true
		}
	}

	const size = 1 << 20
	p := newPassing()
	for i := 0; i < passQueueBytes/size; i++ {
		if waits(pushed(p, passMessage{data: make([]byte, size)})) {
			t.Fatalf("message %d of %d bytes waited with %d bytes queued", i, size, i*size)
		}
	}
	one := pushed(p, passMessage{data: []byte("x")})
	if !waits(one) {
		t.Fatalf("a message went into a queue of %d bytes", passQueueBytes)
	}
	if m, ok := p.next(); !ok || len(m.data) != size {
		t.Fatalf("next: %d bytes, %v", len(m.data), ok)
	}
	if waits(one) {
		t.Fatal("the message still waits after one went out")
	}

	big := newPassing()
	if waits(pushed(big, passMessage{data: make([]byte, passQueueBytes+1)})) {
		t.Fatal("a message over the bound waited in an empty queue")
	}
	after := pushed(big, passMessage{data: []byte("x")})
	if !waits(after) {
		t.Fatal("a message went in after one over the bound")
	}
	big.mu.Lock()
	big.socketGone = true
	big.cond.Broadcast()
	big.mu.Unlock()
	<-after
}
