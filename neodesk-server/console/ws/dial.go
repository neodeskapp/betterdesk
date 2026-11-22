package ws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// Upstream is a websocket the console opened itself, to a handler in this
// process (the API server's), as the old console opened one to the API
// server with the websocket library's client: it takes messages up to a
// limit (DefaultMaxPayload unless the dial set one), refuses text that is
// not UTF-8 (both close the socket, with 1009 and 1007), and Close ends it
// without a status code.
type Upstream struct {
	c          *websocket.Conn
	ctx        context.Context
	cancel     context.CancelFunc
	maxPayload int64

	mu      sync.Mutex
	closing bool
	// closed is closed when a closing handshake Close started has ended,
	// or by Wait when none was started
	closed chan struct{}
	// watched is closed when the goroutine that ends the connection with
	// the context has returned
	watched chan struct{}
}

// DialOptions configure DialHandler.
type DialOptions struct {
	// Header is sent with the handshake.
	Header http.Header
	// Subprotocols are offered in Sec-WebSocket-Protocol. When there are
	// some, the answer has to name one of them.
	Subprotocols []string
	// MaxPayload is the largest message the socket takes; 0 is
	// DefaultMaxPayload.
	MaxPayload int64
	// RemoteAddr is the caller address ("host:port") h sees in
	// r.RemoteAddr. Empty leaves the in-memory connection's, "pipe".
	RemoteAddr string
}

// ErrNoSubprotocol is a handshake answer that names no subprotocol
// although the dial offered some.
var ErrNoSubprotocol = errors.New("ws: server sent no subprotocol")

// DialHandler opens a websocket to target ("/api/x?y") on h, without a
// network: the handshake and the frames go over an in-memory connection
// that h's side reads as a normal HTTP connection, so h answers as it
// would on its listener. h's request carries the values of ctx, such as
// the account the socket acts for, but not its end. An answer other than
// 101 is an error. ctx ends the dial; the open socket ends with Close and
// Wait.
func DialHandler(ctx context.Context, h http.Handler, target string, opts DialOptions) (*Upstream, error) {
	if addr := opts.RemoteAddr; addr != "" {
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.RemoteAddr = addr
			inner.ServeHTTP(w, r)
		})
	}
	client, server := net.Pipe()
	l := &pipeListener{conn: server, closed: make(chan struct{})}
	values := context.WithoutCancel(ctx)
	srv := &http.Server{Handler: h, ErrorLog: log.New(io.Discard, "", 0), BaseContext: func(net.Listener) context.Context { return values }}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = srv.Serve(l)
	}()
	transport := &http.Transport{
		DialContext:       func(context.Context, string, string) (net.Conn, error) { return client, nil },
		DisableKeepAlives: true,
	}
	c, res, err := websocket.Dial(ctx, "ws://console.internal"+target, &websocket.DialOptions{
		HTTPClient:      &http.Client{Transport: transport},
		HTTPHeader:      opts.Header,
		Subprotocols:    opts.Subprotocols,
		CompressionMode: websocket.CompressionDisabled,
	})
	// The handler's side has its connection; the server takes no other.
	_ = l.Close()
	<-served
	transport.CloseIdleConnections()
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	if len(opts.Subprotocols) > 0 && !slices.Contains(opts.Subprotocols, c.Subprotocol()) {
		_ = c.CloseNow()
		return nil, ErrNoSubprotocol
	}
	c.SetReadLimit(-1)
	// Once open, the socket lives until it is closed and waited for (Close,
	// Wait), so that a closing handshake is not cut short.
	uctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	maxPayload := opts.MaxPayload
	if maxPayload <= 0 {
		maxPayload = DefaultMaxPayload
	}
	u := &Upstream{c: c, ctx: uctx, cancel: cancel, maxPayload: maxPayload, closed: make(chan struct{}), watched: make(chan struct{})}
	go func() {
		defer close(u.watched)
		<-uctx.Done()
		_ = c.CloseNow()
	}()
	return u, nil
}

// Read returns the next message: whether it is binary, and its bytes. A
// message over the limit is ErrTooBig and text that is not UTF-8
// is ErrInvalidText; the socket is then closed (1009, 1007). Any other
// error means the socket has ended (CloseStatus tells a close frame from
// a broken connection).
func (u *Upstream) Read() (binary bool, data []byte, err error) {
	typ, r, err := u.c.Reader(u.ctx)
	if err != nil {
		return false, nil, err
	}
	data, err = io.ReadAll(io.LimitReader(r, u.maxPayload+1))
	if err != nil {
		return false, nil, err
	}
	if int64(len(data)) > u.maxPayload {
		u.closeWith(websocket.StatusMessageTooBig)
		return false, nil, ErrTooBig
	}
	if typ == websocket.MessageText && !utf8.Valid(data) {
		u.closeWith(websocket.StatusInvalidFramePayloadData)
		return false, nil, ErrInvalidText
	}
	return typ == websocket.MessageBinary, data, nil
}

// Send writes one message. It returns when the message is written or the
// socket has ended.
func (u *Upstream) Send(binary bool, data []byte) error {
	typ := websocket.MessageText
	if binary {
		typ = websocket.MessageBinary
	}
	return u.c.Write(u.ctx, typ, data)
}

// Close starts the closing handshake without a status code, as the
// library's close() without arguments did, and returns at once. Read
// returns the end when the other side has answered.
func (u *Upstream) Close() {
	u.closeWith(websocket.StatusNoStatusRcvd)
}

func (u *Upstream) closeWith(code websocket.StatusCode) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closing {
		return
	}
	u.closing = true
	go func() {
		defer close(u.closed)
		// The library gives the other side five seconds to answer.
		_ = u.c.Close(code, "")
	}()
}

// Wait lets a closing handshake that Close started finish, then ends the
// socket and waits for its goroutines. No closing handshake starts after
// Wait.
func (u *Upstream) Wait() {
	u.mu.Lock()
	if !u.closing {
		u.closing = true
		close(u.closed)
	}
	u.mu.Unlock()
	<-u.closed
	u.cancel()
	<-u.watched
}

// pipeListener hands one connection to an http.Server and then waits
// until it is closed.
type pipeListener struct {
	mu     sync.Mutex
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	c := l.conn
	l.conn = nil
	l.mu.Unlock()
	if c != nil {
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "console.internal" }
