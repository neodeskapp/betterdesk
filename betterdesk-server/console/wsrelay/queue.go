package wsrelay

import "sync"

// writeQueue and writeQueueBytes bound the browser's messages that wait
// for the TCP connection: at most 64 messages and 16 MiB, one message of
// any size when nothing else waits. The old service buffered without a
// limit; here the socket's reader waits until there is room.
const (
	writeQueue      = 64
	writeQueueBytes = 16 << 20
)

// outQueue holds the browser's messages for the TCP connection in order.
type outQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  [][]byte
	bytes  int
	closed bool
}

func newOutQueue() *outQueue {
	q := &outQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push adds a message, waiting while the queue is full; false once the
// queue is closed.
func (q *outQueue) push(p []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) > 0 && (len(q.items) >= writeQueue || q.bytes+len(p) > writeQueueBytes) && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return false
	}
	q.items = append(q.items, p)
	q.bytes += len(p)
	q.cond.Broadcast()
	return true
}

// pop takes the first message, waiting for one; false once the queue is
// closed.
func (q *outQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return nil, false
	}
	p := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	q.bytes -= len(p)
	q.cond.Broadcast()
	return p, true
}

// close drops what waits and wakes every push and pop.
func (q *outQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.items, q.bytes = nil, 0
	q.cond.Broadcast()
}
