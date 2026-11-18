package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unitronix/betterdesk-server/logging"
)

// TLSListener gives every connection of inner TLS with cfg. A connection
// whose first five bytes are the start of a plain HTTP request is closed
// without an answer, as the old console's TLS listeners did; Go's own
// "Client sent an HTTP request to an HTTPS server" is never sent.
func TLSListener(inner net.Listener, cfg *tls.Config) net.Listener {
	return tls.NewListener(sniffListener{inner}, cfg)
}

type sniffListener struct{ net.Listener }

func (l sniffListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &sniffConn{Conn: c}, nil
}

var errPlainHTTP = errors.New("plain HTTP on a TLS port")

// sniffConn looks at the first record header before TLS reads it.
type sniffConn struct {
	net.Conn
	checked bool
	head    []byte
}

func (c *sniffConn) Read(p []byte) (int, error) {
	if !c.checked {
		c.checked = true
		head := make([]byte, 5)
		n, err := io.ReadFull(c.Conn, head)
		c.head = head[:n]
		if err == nil && looksLikeHTTP(c.head) {
			// What else the client sent is read first, so the close is a
			// clean one and not a reset
			_ = c.Conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, _ = io.CopyN(io.Discard, c.Conn, 64<<10)
			return 0, errPlainHTTP
		}
		if err != nil && n == 0 {
			return 0, err
		}
	}
	if len(c.head) > 0 {
		n := copy(p, c.head)
		c.head = c.head[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// looksLikeHTTP is the test net/http makes before it answers a TLS
// handshake in plain text.
func looksLikeHTTP(head []byte) bool {
	switch string(head) {
	case "GET /", "HEAD ", "POST ", "PUT /", "OPTIO":
		return true
	}
	return false
}

// PlainListener wraps a listener for plain HTTP. A connection that times
// out before any request of it reached a handler gets "408 Request
// Timeout" and Connection: close, as Node's HTTP server answered a
// request that did not arrive in time. Servers that use it set
// ConnContext to ConnContext.
func PlainListener(inner net.Listener) net.Listener {
	return plainListener{inner}
}

type plainListener struct{ net.Listener }

func (l plainListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &requestConn{Conn: c}, nil
}

// requestTimeout is what Node's HTTP server wrote on a request timeout.
const requestTimeout = "HTTP/1.1 408 Request Timeout\r\nConnection: close\r\n\r\n"

type requestConn struct {
	net.Conn
	served   atomic.Bool
	timedOut atomic.Bool
	once     sync.Once
}

func (c *requestConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	var timeout interface{ Timeout() bool }
	if err != nil && !c.served.Load() && errors.As(err, &timeout) && timeout.Timeout() {
		c.once.Do(func() {
			c.timedOut.Store(true)
			_ = c.Conn.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = io.WriteString(c.Conn, requestTimeout)
		})
	}
	return n, err
}

// Write drops what net/http writes after the 408 (its own 400 for the
// half request).
func (c *requestConn) Write(p []byte) (int, error) {
	if c.timedOut.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

type connKey struct{}

// ConnContext keeps the connection where Served finds it.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if rc, ok := c.(*requestConn); ok {
		return context.WithValue(ctx, connKey{}, rc)
	}
	return ctx
}

// Served marks the request's connection as one that had a request reach
// a handler: a later timeout closes it without an answer.
func Served(r *http.Request) {
	if rc, ok := r.Context().Value(connKey{}).(*requestConn); ok {
		rc.served.Store(true)
	}
}

// closeUnanswered ends a connection without an answer, for a request Node
// destroyed the socket of (CONNECT, which it had no handler for).
func closeUnanswered(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

// ErrorLog is the http.Server error log of the listeners: TLS handshakes
// that fail (scanners, plain HTTP on a TLS port) are not logged, as the old
// console did not log them; everything else goes to the console's log.
func ErrorLog(component string) *log.Logger {
	return log.New(logWriter(component), "", 0)
}

type logWriter string

func (w logWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if !strings.Contains(line, "TLS handshake error") {
		logging.Logf(string(w), "%s", line)
	}
	return len(p), nil
}
