package listen

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

// cannedBadRequest is what net/http writes itself, before any handler, on
// a connection whose request it cannot read (conn.serve in net/http).
const cannedBadRequest = "HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain; charset=utf-8\r\nConnection: close\r\n\r\n400 Bad Request"

// escapeRecordLimit is how much of what a client sent since the server
// last wrote is kept to find the request net/http refused.
const escapeRecordLimit = 16 << 10

// BrokenEscapes wraps the console's listener so that a request whose path
// has a broken percent escape (/api/devices/%zz) is answered by answer.
// net/http refuses such a request line before any handler, with a 400 in
// plain text; the wrapper sends answer's answer in its place, and the
// connection closes as it did. The request answer gets has the method,
// target, headers, client address and TLS state of what arrived, and no
// body.
//
// The wrapper reads above the TLS of a TLS listener's connections, so
// net/http no longer sees a *tls.Conn: the wrapper completes the handshake
// within handshake when net/http asks for the TLS state, and every request
// still carries it (r.TLS).
func BrokenEscapes(inner net.Listener, answer http.Handler, handshake time.Duration) net.Listener {
	return escapeListener{Listener: inner, answer: answer, handshake: handshake}
}

type escapeListener struct {
	net.Listener
	answer    http.Handler
	handshake time.Duration
}

func (l escapeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	conn := &escapeConn{Conn: c, answer: l.answer}
	if tc, ok := c.(*tls.Conn); ok {
		return &escapeTLSConn{escapeConn: conn, tls: tc, handshake: l.handshake}, nil
	}
	return conn, nil
}

// escapeConn keeps what the client sent since the server last wrote, and
// puts the answer of a request with a broken escape in place of
// net/http's own.
type escapeConn struct {
	net.Conn
	answer http.Handler
	// state is the TLS state of a TLS connection, nil for plain HTTP
	state *tls.ConnectionState

	mu   sync.Mutex
	read []byte
}

func (c *escapeConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		if room := escapeRecordLimit - len(c.read); room > 0 {
			c.read = append(c.read, p[:min(n, room)]...)
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *escapeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	var r *http.Request
	if string(p) == cannedBadRequest {
		if r = brokenEscapeRequest(c.read); r != nil {
			r.RemoteAddr, r.TLS = c.RemoteAddr().String(), c.state
		}
	}
	// An idle connection does not hold on to a large buffer
	if cap(c.read) > 4<<10 {
		c.read = nil
	} else {
		c.read = c.read[:0]
	}
	c.mu.Unlock()
	if r == nil {
		return c.Conn.Write(p)
	}
	c.serveBroken(r)
	return len(p), nil
}

// CloseWrite half-closes the connection as net/http does before it closes
// a connection whose request body it did not read.
func (c *escapeConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// serveBroken writes answer's answer to r, with Connection: close.
func (c *escapeConn) serveBroken(r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w := &bufferedAnswer{header: http.Header{}}
	c.answer.ServeHTTP(w, r.WithContext(ctx))
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.header.Get("Date") == "" {
		w.header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	res := &http.Response{
		StatusCode: w.status, ProtoMajor: 1, ProtoMinor: 1, Header: w.header,
		Body: io.NopCloser(&w.body), ContentLength: int64(w.body.Len()), Close: true, Request: r,
	}
	_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = res.Write(c.Conn)
}

// escapeTLSConn is an escapeConn above TLS. net/http gives the TLS state
// of a connection that is no *tls.Conn to its requests when the
// connection has a ConnectionState method.
type escapeTLSConn struct {
	*escapeConn
	tls       *tls.Conn
	handshake time.Duration
}

// ConnectionState completes the handshake, as net/http does for a
// *tls.Conn before it reads the first request: within the handshake
// timeout, for reading and writing.
func (c *escapeTLSConn) ConnectionState() tls.ConnectionState {
	if c.handshake > 0 {
		deadline := time.Now().Add(c.handshake)
		_ = c.tls.SetReadDeadline(deadline)
		_ = c.tls.SetWriteDeadline(deadline)
	}
	err := c.tls.Handshake()
	_ = c.tls.SetReadDeadline(time.Time{})
	_ = c.tls.SetWriteDeadline(time.Time{})
	state := c.tls.ConnectionState()
	if err == nil {
		c.mu.Lock()
		c.state = &state
		c.mu.Unlock()
	}
	return state
}

// brokenEscapeRequest finds the request net/http refused in what the client
// sent since the server last wrote: the first request line there, when its
// target is a path with a broken percent escape. Anything else net/http
// refuses is no such request (nil).
func brokenEscapeRequest(read []byte) *http.Request {
	for len(read) > 0 {
		line, rest, _ := bytes.Cut(read, []byte("\n"))
		r, ok := requestLine(strings.TrimSuffix(string(line), "\r"))
		if !ok {
			read = rest
			continue
		}
		// net/http checks the target after the method and the version
		var escape url.EscapeError
		if _, err := url.ParseRequestURI(r.RequestURI); !strings.HasPrefix(r.RequestURI, "/") || !errors.As(err, &escape) {
			return nil
		}
		header, _ := textproto.NewReader(bufio.NewReader(bytes.NewReader(rest))).ReadMIMEHeader()
		if header == nil {
			header = textproto.MIMEHeader{}
		}
		r.Header, r.Host = http.Header(header), header.Get("Host")
		return r
	}
	return nil
}

// requestLine reads a request line as net/http does: method, target and
// protocol, separated by single spaces. Only a method of capital letters
// and a version net/http reads count.
func requestLine(line string) (*http.Request, bool) {
	method, rest, ok1 := strings.Cut(line, " ")
	target, proto, ok2 := strings.Cut(rest, " ")
	major, minor, ok3 := http.ParseHTTPVersion(proto)
	if !ok1 || !ok2 || !ok3 || method == "" || strings.TrimLeft(method, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return nil, false
	}
	path, query, _ := strings.Cut(target, "?")
	return &http.Request{
		Method: method, URL: &url.URL{Path: path, RawQuery: query}, RequestURI: target,
		Proto: proto, ProtoMajor: major, ProtoMinor: minor, Body: http.NoBody, Close: true,
	}, true
}

// bufferedAnswer is a ResponseWriter that keeps the answer for writing.
type bufferedAnswer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (a *bufferedAnswer) Header() http.Header { return a.header }

func (a *bufferedAnswer) WriteHeader(status int) {
	if a.status == 0 {
		a.status = status
	}
}

func (a *bufferedAnswer) Write(p []byte) (int, error) {
	a.WriteHeader(http.StatusOK)
	return a.body.Write(p)
}
