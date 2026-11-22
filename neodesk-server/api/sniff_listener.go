package api

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// protocolSniffListener lets the API port also serve relay traffic.
//
// Background: some RustDesk builds resolve a *portless* `relay-server` value
// with the API port instead of RELAY_PORT, so they dial `host:21114` using relay
// framing. That listener speaks HTTP, so the connection dies and the session
// fails with a relay pair timeout. Telling every operator to rebuild their
// agent bundle is not a fix we can ship: deployed agents keep their baked value.
//
// The two protocols are unambiguous at byte level, so the first byte decides:
//
//   - HTTP request methods start with an ASCII letter (GET/POST/HEAD/…).
//   - RustDesk's BytesCodec header starts with a length byte. Control frames on
//     this port are far below 64 KiB, so byte 0 is never an ASCII letter.
//
// Anything that looks like HTTP reaches the HTTP server untouched; anything else
// goes to the relay handler, which reads the normal relay framing.
//
// Each new connection is classified in its own goroutine, so a client that
// connects and sends nothing holds up no one else; at most
// sniffMaxClassifying connections wait for their first byte at once.
type protocolSniffListener struct {
	net.Listener
	relay   relayHandlerFunc
	results chan sniffResult
	slots   chan struct{}
	done    chan struct{}
	closed  sync.Once
}

// sniffResult is what Accept hands the HTTP server: a connection that
// looks like HTTP or TLS, or an error of the inner listener.
type sniffResult struct {
	conn net.Conn
	err  error
}

const (
	sniffMaxClassifying = 256
	sniffFirstByteWait  = 5 * time.Second
)

// relayHandlerFunc consumes a connection that carries relay framing.
type relayHandlerFunc func(net.Conn)

// sniffConn replays the sniffed byte before the real reader sees the stream.
type sniffConn struct {
	net.Conn
	reader io.Reader
}

func (c *sniffConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// RelayFallbackConnections counts connections served by the relay fallback on
// the API port. Non-zero means at least one client is using the compatibility
// path, which is worth surfacing in diagnostics.
var RelayFallbackConnections atomic.Int64

func newProtocolSniffListener(inner net.Listener, relay relayHandlerFunc) net.Listener {
	l := &protocolSniffListener{
		Listener: inner,
		relay:    relay,
		results:  make(chan sniffResult),
		slots:    make(chan struct{}, sniffMaxClassifying),
		done:     make(chan struct{}),
	}
	go l.acceptLoop()
	return l
}

// Accept returns the next connection that looks like HTTP or TLS. Relay
// connections are dispatched to the relay handler and never returned.
func (l *protocolSniffListener) Accept() (net.Conn, error) {
	select {
	case r := <-l.results:
		return r.conn, r.err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

// Close closes the inner listener and ends Accept.
func (l *protocolSniffListener) Close() error {
	l.closed.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// acceptLoop takes the inner listener's connections and starts their
// classification. Errors go to Accept, which the HTTP server retries on
// temporary ones; the loop ends when the listener is closed.
func (l *protocolSniffListener) acceptLoop() {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			select {
			case l.results <- sniffResult{err: err}:
			case <-l.done:
				return
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		select {
		case l.slots <- struct{}{}:
		case <-l.done:
			conn.Close()
			return
		}
		go l.classify(conn)
	}
}

// classify reads the first byte of conn and hands the connection to the
// HTTP server or the relay handler.
func (l *protocolSniffListener) classify(conn net.Conn) {
	var first [1]byte
	err := conn.SetReadDeadline(time.Now().Add(sniffFirstByteWait))
	if err == nil {
		_, err = io.ReadFull(conn, first[:])
		// Whichever handler receives the connection owns its timeouts from here.
		_ = conn.SetReadDeadline(time.Time{})
	}
	<-l.slots
	if err != nil {
		conn.Close()
		return
	}

	replay := &sniffConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(first[:]), conn)}
	if isHTTPFirstByte(first[0]) {
		select {
		case l.results <- sniffResult{conn: replay}:
		case <-l.done:
			conn.Close()
		}
		return
	}
	if l.relay != nil {
		RelayFallbackConnections.Add(1)
		l.relay(replay)
		return
	}
	conn.Close()
}

// isHTTPFirstByte reports whether b can start either an HTTP request line or a
// TLS ClientHello.
//
//   - RFC 9110 methods are all ASCII letters, so a letter cannot collide with the
//     length-prefixed relay framing used by RustDesk's BytesCodec.
//   - A TLS ClientHello starts with a handshake record (0x16). Clients that send
//     a portless relay-server value dial plaintext HTTP, but the same listener
//     may still accept TLS, so a handshake must reach the TLS server untouched.
func isHTTPFirstByte(b byte) bool {
	if b == 0x16 { // TLS handshake record
		return true
	}
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}
