package cdapproxy

import (
	"bufio"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
)

// The limits of the sockets (DECISIONS A56). A limit on sockets per
// account and a timeout for a missing answer to a ping would belong in
// console/ws; the proxy keeps its own until the package has them.
const (
	// MaxSocketsPerAccount is how many sockets of one channel an account
	// may have open at once.
	MaxSocketsPerAccount = 10
	// PingInterval is how often a socket is pinged.
	PingInterval = 30 * time.Second
)

// accountChannel is the key of the socket count.
type accountChannel struct {
	account int64
	channel string
}

// sockets counts the open sockets per account and channel.
type sockets struct {
	mu   sync.Mutex
	open map[accountChannel]int
}

// take counts one more socket of the account on the channel, unless it
// has MaxSocketsPerAccount open.
func (s *sockets) take(key accountChannel) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[key] >= MaxSocketsPerAccount {
		return false
	}
	if s.open == nil {
		s.open = map[accountChannel]int{}
	}
	s.open[key]++
	return true
}

// release counts one socket less.
func (s *sockets) release(key accountChannel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[key]--; s.open[key] <= 0 {
		delete(s.open, key)
	}
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
