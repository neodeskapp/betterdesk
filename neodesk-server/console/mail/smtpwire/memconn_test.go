package smtpwire

import (
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// memPipe returns the two ends of a connection that lives in memory. Unlike
// net.Pipe a write never waits for the other side to read, as with a socket
// that has a send buffer, so two ends that write at the same time do not
// block each other.
func memPipe() (net.Conn, net.Conn) {
	a, b := newMemStream(), newMemStream()
	return &memConn{in: a, out: b}, &memConn{in: b, out: a}
}

// memStream is one direction of a memPipe.
type memStream struct {
	mu       sync.Mutex
	data     []byte
	closed   bool // the writer closed: EOF after the data
	gone     bool // the reader closed: writes fail
	deadline time.Time
	changed  chan struct{}
}

func newMemStream() *memStream { return &memStream{changed: make(chan struct{})} }

// wake lets a waiting reader look again. The lock is held.
func (s *memStream) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *memStream) read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		switch {
		case s.gone:
			s.mu.Unlock()
			return 0, net.ErrClosed
		case len(s.data) > 0:
			n := copy(p, s.data)
			s.data = s.data[n:]
			s.mu.Unlock()
			return n, nil
		case s.closed:
			s.mu.Unlock()
			return 0, io.EOF
		}
		deadline, changed := s.deadline, s.changed
		s.mu.Unlock()
		if deadline.IsZero() {
			<-changed
			continue
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(wait)
		select {
		case <-changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (s *memStream) write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.gone {
		return 0, io.ErrClosedPipe
	}
	s.data = append(s.data, p...)
	s.wake()
	return len(p), nil
}

type memConn struct {
	in, out *memStream
}

func (c *memConn) Read(p []byte) (int, error)  { return c.in.read(p) }
func (c *memConn) Write(p []byte) (int, error) { return c.out.write(p) }

// CloseWrite ends the sending side: the other end reads what was written
// and then the end of the stream.
func (c *memConn) CloseWrite() error {
	c.out.mu.Lock()
	c.out.closed = true
	c.out.wake()
	c.out.mu.Unlock()
	return nil
}

func (c *memConn) Close() error {
	_ = c.CloseWrite()
	c.in.mu.Lock()
	c.in.gone = true
	c.in.wake()
	c.in.mu.Unlock()
	return nil
}

func (c *memConn) SetReadDeadline(t time.Time) error {
	c.in.mu.Lock()
	c.in.deadline = t
	c.in.wake()
	c.in.mu.Unlock()
	return nil
}

func (c *memConn) SetWriteDeadline(time.Time) error { return nil }

func (c *memConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *memConn) LocalAddr() net.Addr  { return memAddr{} }
func (c *memConn) RemoteAddr() net.Addr { return memAddr{} }

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "mem" }
