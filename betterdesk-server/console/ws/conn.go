package ws

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// Close codes the console's sockets use besides the service's own.
const (
	// CloseNormal is a normal end (1000).
	CloseNormal = 1000
	// CloseInvalidPayload is a text message that is not UTF-8 (1007).
	CloseInvalidPayload = 1007
	// CloseTooBig is a message over the socket's limit (1009).
	CloseTooBig = 1009
)

// DefaultMaxPayload is the largest message the websocket library took
// when a service set no limit: 100 MiB.
const DefaultMaxPayload = 100 << 20

// sendQueue is how many messages may wait for a client that does not
// read. The old console buffered without a limit; a client that lets this
// many pile up is closed.
const sendQueue = 1024

// writeTimeout is how long one message may take to go out.
const writeTimeout = 30 * time.Second

// ErrTooBig is returned by Read for a message over the limit. The socket is
// then being closed with 1009 and no reason, as the library did.
var ErrTooBig = errors.New("ws: message over the size limit")

// ErrInvalidText is returned by Read for a text message that is not UTF-8.
// The socket is then being closed with 1007 and no reason.
var ErrInvalidText = errors.New("ws: text message that is not UTF-8")

// Conn is an accepted socket. Messages go out in the order they were
// given, through a queue, so a client that reads slowly holds up nobody
// else. Every goroutine of the socket ends when it ends.
type Conn struct {
	ws         *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	out        chan outgoing
	maxPayload int64
	protocol   string

	mu      sync.Mutex
	closing bool
	// ended is closed when the writer is done and the connection is gone.
	ended chan struct{}
	wg    sync.WaitGroup
}

// outgoing is one message, or a close when isClose is set.
type outgoing struct {
	typ     websocket.MessageType
	data    []byte
	isClose bool
	code    int
	reason  string
}

func newConn(parent context.Context, c *websocket.Conn, maxPayload int64, protocol string) *Conn {
	if maxPayload <= 0 {
		maxPayload = DefaultMaxPayload
	}
	c.SetReadLimit(-1)
	ctx, cancel := context.WithCancel(parent)
	conn := &Conn{
		ws: c, ctx: ctx, cancel: cancel, out: make(chan outgoing, sendQueue),
		maxPayload: maxPayload, protocol: protocol, ended: make(chan struct{}),
	}
	conn.wg.Add(1)
	go conn.writer()
	return conn
}

// Context ends when the socket ends or the console stops.
func (c *Conn) Context() context.Context { return c.ctx }

// Subprotocol is the subprotocol the handshake named, "" for none.
func (c *Conn) Subprotocol() string { return c.protocol }

// writer sends what the queue holds until a close goes out, the socket
// fails, or the console stops.
func (c *Conn) writer() {
	defer c.wg.Done()
	defer close(c.ended)
	defer c.cancel()
	for {
		select {
		case m := <-c.out:
			if m.isClose {
				_ = c.ws.Close(websocket.StatusCode(m.code), m.reason)
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.ws.Write(ctx, m.typ, m.data)
			cancel()
			if err != nil {
				_ = c.ws.CloseNow()
				return
			}
		case <-c.ctx.Done():
			_ = c.ws.CloseNow()
			return
		}
	}
}

// SendText queues a text message. On a socket that is not open (closing or
// ended) it does nothing, as the old services skipped a socket whose
// readyState was not OPEN.
func (c *Conn) SendText(text string) { c.send(websocket.MessageText, []byte(text)) }

// Send queues a message of the given kind, like SendText.
func (c *Conn) Send(binary bool, data []byte) {
	typ := websocket.MessageText
	if binary {
		typ = websocket.MessageBinary
	}
	c.send(typ, data)
}

func (c *Conn) send(typ websocket.MessageType, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openLocked() {
		return
	}
	select {
	case c.out <- outgoing{typ: typ, data: data}:
	default:
		// The client has not read for a long time: give up on it.
		c.closing = true
		c.cancel()
	}
}

// Open reports whether the socket can still take messages: it is not
// closing and has not ended (readyState OPEN).
func (c *Conn) Open() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.openLocked()
}

func (c *Conn) openLocked() bool { return !c.closing && c.ctx.Err() == nil }

// Close starts the closing handshake with a code and a reason, after the
// messages already queued. On a socket that is not open (a second Close,
// or one that ended) it does nothing.
func (c *Conn) Close(code int, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openLocked() {
		return
	}
	c.closing = true
	select {
	case c.out <- outgoing{isClose: true, code: code, reason: reason}:
	default:
		c.cancel()
	}
}

// CloseNow ends the socket without a closing handshake.
func (c *Conn) CloseNow() {
	c.mu.Lock()
	c.closing = true
	c.mu.Unlock()
	c.cancel()
}

// Read returns the next message: whether it is binary, and its bytes. A
// message over the socket's limit is ErrTooBig and a text message that is
// not UTF-8 is ErrInvalidText; the socket is then closed (1009, 1007). Any
// other error means the socket has ended: the client closed it
// (CloseStatus tells the code), the connection broke, or the console
// stops.
func (c *Conn) Read() (binary bool, data []byte, err error) {
	typ, r, err := c.ws.Reader(c.ctx)
	if err != nil {
		return false, nil, err
	}
	data, err = io.ReadAll(io.LimitReader(r, c.maxPayload+1))
	if err != nil {
		return false, nil, err
	}
	if int64(len(data)) > c.maxPayload {
		c.Close(CloseTooBig, "")
		return false, nil, ErrTooBig
	}
	if typ == websocket.MessageText && !utf8.Valid(data) {
		c.Close(CloseInvalidPayload, "")
		return false, nil, ErrInvalidText
	}
	return typ == websocket.MessageBinary, data, nil
}

// Discard reads and drops every message until the socket ends, the way a
// socket without a message listener was read: control frames are still
// answered and the size limit still applies. It returns how the socket
// ended.
func (c *Conn) Discard() error {
	for {
		if _, _, err := c.Read(); err != nil {
			return err
		}
	}
}

// KeepAlive sends a ping every interval until the socket ends. A missing
// answer is not acted on.
func (c *Conn) KeepAlive(interval time.Duration) { c.ping(interval, false) }

// Heartbeat sends a ping every interval until the socket ends, like
// KeepAlive, and drops the connection when two pings in a row got no
// answer within an interval each: a peer that is gone ends after two to
// three intervals, not when TCP gives up. The socket must be read
// meanwhile, since the answers arrive through Read.
func (c *Conn) Heartbeat(interval time.Duration) { c.ping(interval, true) }

func (c *Conn) ping(interval time.Duration, drop bool) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		missed := 0
		for {
			select {
			case <-t.C:
				ctx, cancel := context.WithTimeout(c.ctx, interval)
				err := c.ws.Ping(ctx)
				cancel()
				switch {
				case err == nil:
					missed = 0
				case c.ctx.Err() != nil:
					return
				default:
					if missed++; drop && missed >= 2 {
						c.CloseNow()
						return
					}
				}
			case <-c.ctx.Done():
				return
			}
		}
	}()
}

// Wait ends the socket's goroutines: it lets a closing handshake the
// server started finish (the library gives it at most ten seconds), then
// drops the connection. Handlers call it before they return.
func (c *Conn) Wait() {
	c.mu.Lock()
	closing := c.closing
	c.mu.Unlock()
	if closing {
		select {
		case <-c.ended:
		case <-time.After(15 * time.Second):
		}
	}
	c.cancel()
	c.wg.Wait()
}

// CloseStatus is the code of the close frame the client sent, or -1 when
// err is not one.
func CloseStatus(err error) int {
	return int(websocket.CloseStatus(err))
}

// ProtocolError reports whether a socket ended by err broke the protocol
// or a limit, where the library emitted "error" before "close": a message
// over the limit, text that is not UTF-8, a frame it could not read. A
// close frame, the connection going away and the console stopping are not
// errors.
func ProtocolError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTooBig) || errors.Is(err, ErrInvalidText) {
		return true
	}
	if websocket.CloseStatus(err) != -1 {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return false
	}
	return true
}
