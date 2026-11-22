// Package wstest helps the tests of the console's sockets: raw upgrade
// requests and their answers, a websocket client that keeps every frame,
// and the comparison of handshake answers with Node recordings.
package wstest

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
)

// Raw sends text on a new connection to addr and returns what comes back:
// everything until the server closes the connection, or the head of a 101
// answer and what follows within 150 ms.
func Raw(addr, text string) (string, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, text); err != nil {
		return "", err
	}
	var got []byte
	buf := make([]byte, 4096)
	deadline := time.Now().Add(3 * time.Second)
	for {
		_ = conn.SetReadDeadline(deadline)
		n, err := conn.Read(buf)
		got = append(got, buf[:n]...)
		if strings.HasPrefix(string(got), "HTTP/1.1 101") && strings.Contains(string(got), "\r\n\r\n") {
			if limit := time.Now().Add(150 * time.Millisecond); limit.Before(deadline) {
				deadline = limit
			}
		}
		if err != nil {
			var netErr net.Error
			if errors.Is(err, io.EOF) || (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, net.ErrClosed) {
				return string(got), nil
			}
			return string(got), nil
		}
	}
}

// SameAnswer compares an answer with Node's: byte for byte, except that
// the head of a 101 answer is compared as its status line and its headers
// by name in any letter case and order, without Date. What follows the
// head of a 101 is compared byte for byte.
func SameAnswer(node, got string) bool {
	if !strings.HasPrefix(node, "HTTP/1.1 101") {
		return node == got
	}
	nodeHead, nodeRest, _ := strings.Cut(node, "\r\n\r\n")
	gotHead, gotRest, _ := strings.Cut(got, "\r\n\r\n")
	return HeadKey(nodeHead) == HeadKey(gotHead) && nodeRest == gotRest
}

// HeadKey is the head of an answer in a form that compares: the status
// line, then the headers by name in lower case, sorted, without Date.
func HeadKey(head string) string {
	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 {
		return ""
	}
	var fields []string
	for _, line := range lines[1:] {
		name, value, _ := strings.Cut(line, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "date" {
			continue
		}
		fields = append(fields, name+":"+strings.TrimSpace(value))
	}
	sort.Strings(fields)
	return lines[0] + "\n" + strings.Join(fields, "\n")
}

// Frame is one message a Client got.
type Frame struct {
	Binary bool
	Data   []byte
}

// Text is the frame's data as text.
func (f Frame) Text() string { return string(f.Data) }

// Client is a websocket client for tests: it answers pings and keeps
// every message and the close.
type Client struct {
	conn net.Conn
	br   *bufio.Reader
	// Response is the handshake's answer.
	Response *http.Response

	writeMu sync.Mutex
	mu      sync.Mutex
	frames  []Frame
	closed  bool
	code    int
	reason  string
	pings   int
	signal  chan struct{}
	done    chan struct{}
}

// Dial opens a socket at target ("/ws/x?y") on addr with the extra
// headers. A refused handshake returns the answer and no error; the
// Client is then nil.
func Dial(addr, target string, header http.Header) (*Client, *http.Response, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	all := http.Header{}
	all.Set("Upgrade", "websocket")
	all.Set("Connection", "Upgrade")
	all.Set("Sec-WebSocket-Key", encodeKey(key))
	all.Set("Sec-WebSocket-Version", "13")
	for name, values := range header {
		all[http.CanonicalHeaderKey(name)] = values
	}
	var b strings.Builder
	b.WriteString("GET " + target + " HTTP/1.1\r\nHost: " + addr + "\r\n")
	_ = all.Write(&b)
	b.WriteString("\r\n")
	if _, err := io.WriteString(conn, b.String()); err != nil {
		conn.Close()
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	if res.StatusCode != http.StatusSwitchingProtocols {
		_, _ = io.ReadAll(res.Body)
		conn.Close()
		return nil, res, nil
	}
	_ = conn.SetDeadline(time.Time{})
	c := &Client{conn: conn, br: br, Response: res, signal: make(chan struct{}, 1), done: make(chan struct{})}
	go c.read()
	return c, res, nil
}

func encodeKey(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// Goroutines notes how many goroutines run now and returns a check for
// the end of a test: it fails when more run then, after giving them five
// seconds to end. The PostgreSQL driver's goroutines are not counted: they
// belong to the test's database, which outlives the check, and on a slow
// database a connection's background reader or close runs on for longer
// than five seconds after the test ended its query (BUGS L150).
func Goroutines(t testing.TB) func() {
	t.Helper()
	before := countGoroutines(allStacks())
	return func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			stacks := allStacks()
			now := countGoroutines(stacks)
			if now <= before {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d goroutines still run, %d before the test:\n%s", now, before, stacks)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// allStacks is the stack of every goroutine, as runtime.Stack writes them.
func allStacks() []byte {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, 2*len(buf))
	}
}

// countGoroutines counts the goroutines of a dump of allStacks, without
// those the PostgreSQL driver (github.com/jackc) started.
func countGoroutines(stacks []byte) int {
	n := 0
	for _, g := range bytes.Split(stacks, []byte("\n\n")) {
		if !bytes.HasPrefix(bytes.TrimSpace(g), []byte("goroutine ")) {
			continue
		}
		if bytes.Contains(g, []byte("\ncreated by github.com/jackc/")) {
			continue
		}
		n++
	}
	return n
}

func (c *Client) read() {
	defer close(c.done)
	var partial []byte
	var partialBinary bool
	for {
		var head [2]byte
		if _, err := io.ReadFull(c.br, head[:]); err != nil {
			c.finish(1006, "")
			return
		}
		fin, opcode := head[0]&0x80 != 0, head[0]&0x0F
		length := uint64(head[1] & 0x7F)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				c.finish(1006, "")
				return
			}
			length = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.br, ext[:]); err != nil {
				c.finish(1006, "")
				return
			}
			length = binary.BigEndian.Uint64(ext[:])
		}
		if length > 64<<20 {
			c.finish(1006, "")
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			c.finish(1006, "")
			return
		}
		switch opcode {
		case 0x0:
			partial = append(partial, payload...)
		case 0x1, 0x2:
			partial, partialBinary = payload, opcode == 0x2
		case 0x8:
			code, reason := 1005, ""
			if len(payload) >= 2 {
				code, reason = int(binary.BigEndian.Uint16(payload)), string(payload[2:])
			}
			echo := []byte{}
			if len(payload) >= 2 {
				echo = payload[:2]
			}
			_ = c.WriteFrame(0x8, echo)
			c.finish(code, reason)
			return
		case 0x9:
			c.mu.Lock()
			c.pings++
			c.mu.Unlock()
			_ = c.WriteFrame(0xA, payload)
			c.wake()
			continue
		case 0xA:
			continue
		}
		if fin && (opcode == 0x0 || opcode == 0x1 || opcode == 0x2) {
			c.mu.Lock()
			c.frames = append(c.frames, Frame{Binary: partialBinary, Data: partial})
			c.mu.Unlock()
			partial = nil
			c.wake()
		}
	}
}

func (c *Client) wake() {
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

func (c *Client) finish(code int, reason string) {
	c.mu.Lock()
	if !c.closed {
		c.closed, c.code, c.reason = true, code, reason
	}
	c.mu.Unlock()
	c.conn.Close()
	c.wake()
}

// WriteFrame sends one masked frame with the given opcode.
func (c *Client) WriteFrame(opcode byte, payload []byte) error {
	return c.WriteRaw(true, opcode, payload)
}

// WriteRaw sends one frame; fin false starts or continues a fragmented
// message.
func (c *Client) WriteRaw(fin bool, opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	first := opcode
	if fin {
		first |= 0x80
	}
	frame := []byte{first}
	n := len(payload)
	switch {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 0xFFFF:
		frame = append(frame, 0x80|126, byte(n>>8), byte(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	frame = append(frame, mask[:]...)
	start := len(frame)
	frame = append(frame, payload...)
	for i := range payload {
		frame[start+i] ^= mask[i%4]
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.conn.Write(frame)
	return err
}

// Text sends a text message.
func (c *Client) Text(s string) error { return c.WriteFrame(0x1, []byte(s)) }

// Binary sends a binary message.
func (c *Client) Binary(b []byte) error { return c.WriteFrame(0x2, b) }

// Close sends a close frame with code and waits up to two seconds for the
// server's answer.
func (c *Client) Close(code int) {
	payload := binary.BigEndian.AppendUint16(nil, uint16(code))
	if c.WriteFrame(0x8, payload) == nil {
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
		}
	}
	c.conn.Close()
	<-c.done
}

// Drop ends the connection without a close frame.
func (c *Client) Drop() {
	c.conn.Close()
	<-c.done
}

// Wait waits up to limit until cond is true for the frames, the close
// state and the number of pings, and reports whether it became true.
func (c *Client) Wait(limit time.Duration, cond func(frames []Frame, closed bool) bool) bool {
	deadline := time.Now().Add(limit)
	for {
		c.mu.Lock()
		frames, closed := append([]Frame(nil), c.frames...), c.closed
		c.mu.Unlock()
		if cond(frames, closed) {
			return true
		}
		left := time.Until(deadline)
		if left <= 0 {
			return false
		}
		select {
		case <-c.signal:
		case <-time.After(left):
		}
	}
}

// Frames returns the messages so far.
func (c *Client) Frames() []Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Frame(nil), c.frames...)
}

// Take returns the messages so far and forgets them.
func (c *Client) Take() []Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.frames
	c.frames = nil
	return out
}

// Closed reports whether the server closed the socket, with its code and
// reason (1006 when the connection ended without a close frame).
func (c *Client) Closed() (bool, int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed, c.code, c.reason
}

// Pings is how many pings the server sent.
func (c *Client) Pings() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pings
}

// OracleSession is the session stub of the Node oracles: the session is
// the JSON in the X-Oracle-Session header, none without it. It reads the
// fields as the old services did, with JavaScript's truthiness.
func OracleSession(r *http.Request) ws.Session {
	raw := r.Header.Get("X-Oracle-Session")
	if raw == "" {
		return oracleSession{}
	}
	v, err := goapi.Parse([]byte(raw))
	if err != nil {
		return oracleSession{}
	}
	o, _ := v.(*goapi.Object)
	return oracleSession{o: o}
}

type oracleSession struct{ o *goapi.Object }

func (s oracleSession) SignedIn() bool { return s.o != nil && goapi.Truthy(s.o.Get("userId")) }

func (s oracleSession) UserID() int64 {
	if n, ok := s.o.Number("userId"); ok {
		return int64(n)
	}
	return 0
}

func (s oracleSession) UserRole() string { return s.o.Object("user").Str("role") }

func (s oracleSession) Role() string {
	if role := s.o.Object("user").Str("role"); role != "" {
		return role
	}
	return s.o.Str("role")
}

func (s oracleSession) Username() string {
	if name := s.o.Object("user").Str("username"); name != "" {
		return name
	}
	return s.o.Str("username")
}

// User is the session's user as session.Session.User reads it: the id,
// name and role of the "user" object, the name and role at the top as a
// fallback, nil when nobody is signed in.
func (s oracleSession) User() *session.User {
	if !s.SignedIn() {
		return nil
	}
	u := &session.User{ID: s.UserID(), Username: s.Username(), Role: s.Role()}
	if n, ok := s.o.Object("user").Number("id"); ok && n == float64(int64(n)) && n != 0 {
		u.ID = int64(n)
	}
	u.PreferredLanguage = s.o.Object("user").Str("preferred_language")
	return u
}

// squeezed is a run of repeated text a recording holds in short form, as
// it stands in the JSON text: \u0001RUN:<count>:<piece>\u0001.
var squeezed = regexp.MustCompile(`\\u0001RUN:(\d+):(.*?)\\u0001`)

// Expand writes the runs of a recording out in full: each run marker
// becomes its piece repeated, still JSON-escaped, so the result parses as
// the recording did before it was squeezed.
func Expand(raw []byte) []byte {
	return squeezed.ReplaceAllFunc(raw, func(m []byte) []byte {
		parts := squeezed.FindSubmatch(m)
		n, err := strconv.Atoi(string(parts[1]))
		if err != nil {
			return m
		}
		return bytes.Repeat(parts[2], n)
	})
}
