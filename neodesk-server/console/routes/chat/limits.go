package chat

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
)

// MaxOperatorSockets is how many operator sockets an account may have
// open at once (DECISIONS A56). A device has one agent socket: a second
// one replaces the first. A limit on sockets per account and a timeout
// for a missing answer to a ping would belong in console/ws; the relay
// keeps its own until the package has them.
const MaxOperatorSockets = 10

// MaxAgentSocketsPerToken is how many agent sockets one token may have
// open at once, whatever device ids they name. A token belongs to one
// device (a device token, or an enrollment token bound to the device that
// used it first; one no device used yet passes only for ids the server
// does not know), and a device has one agent socket: a reconnect replaces
// it, and the replaced socket counts until its close handshake ends, a
// few seconds at most. So a real agent holds two now and then; four
// leaves room for one that reconnects twice within those seconds.
const MaxAgentSocketsPerToken = 4

// An agent socket may send AgentFrameBurst messages at once and
// AgentFramesPerSecond after that; the relay drops what comes faster and
// keeps the socket open. A person sends a chat message every few seconds
// at most, typing notices and read receipts follow what people type and
// read, and an agent that connects asks for its contacts, groups and
// history at once: real agents stay far below both numbers, while a
// flood no longer turns into stored messages and broadcasts without end.
const (
	AgentFrameBurst      = 40
	AgentFramesPerSecond = 4
)

// openSockets counts open sockets per key: per account for operators,
// per token for agents.
type openSockets[K comparable] struct {
	mu   sync.Mutex
	open map[K]int
}

// take counts one more socket of the key, unless it has limit open.
func (a *openSockets[K]) take(key K, limit int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open[key] >= limit {
		return false
	}
	if a.open == nil {
		a.open = map[K]int{}
	}
	a.open[key]++
	return true
}

// release counts one socket less.
func (a *openSockets[K]) release(key K) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.open[key]--; a.open[key] <= 0 {
		delete(a.open, key)
	}
}

// frameBudget is the rate of an agent socket's messages: a bucket of
// AgentFrameBurst that fills by AgentFramesPerSecond. Only the socket's
// reading goroutine uses it.
type frameBudget struct {
	left float64
	last time.Time
	// dropped counts the messages the budget refused
	dropped int
}

// allow takes one message from the budget at now, false when it is used
// up.
func (b *frameBudget) allow(now time.Time) bool {
	if b.last.IsZero() {
		b.left = AgentFrameBurst
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.left = min(AgentFrameBurst, b.left+elapsed*AgentFramesPerSecond)
	}
	b.last = now
	if b.left < 1 {
		b.dropped++
		return false
	}
	b.left--
	return true
}

// heard notes when the client of a socket last sent anything: a message,
// a control frame, the answer to a ping. It sits between the upgrade's
// connection and the websocket library, so it sees what the library reads.
type heard struct {
	last atomic.Int64
}

func (h *heard) now() { h.last.Store(time.Now().UnixNano()) }

// listen makes the upgrade's connection, once taken over, note what the
// client sends. It is called before the upgrade is answered.
func (h *heard) listen(u *ws.Upgrade) {
	h.now()
	u.W = &heardWriter{ResponseWriter: u.W, heard: h}
}

// closeSilent closes the socket once the client has sent nothing, not even
// the answer to a ping, for timeout. The returned stop ends the watch.
func (h *heard) closeSilent(conn *ws.Conn, timeout time.Duration) (stop func()) {
	h.now()
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(timeout / 4)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if time.Since(time.Unix(0, h.last.Load())) > timeout {
					conn.CloseNow()
					return
				}
			case <-conn.Context().Done():
				return
			case <-quit:
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-done
	}
}

// heardWriter hands the websocket library a connection that notes what it
// reads.
type heardWriter struct {
	http.ResponseWriter
	heard *heard
}

func (w *heardWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return &heardConn{Conn: conn, heard: w.heard}, rw, nil
}

func (w *heardWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type heardConn struct {
	net.Conn
	heard *heard
}

func (c *heardConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.heard.now()
	}
	return n, err
}
