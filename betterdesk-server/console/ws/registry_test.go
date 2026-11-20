package ws_test

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

func upgrade(target string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "keep-alive, Upgrade")
	return r
}

func TestServesRegisteredPathsOnly(t *testing.T) {
	reg := ws.New(ws.Options{})
	defer reg.Close()
	nothing := func(*ws.Upgrade) {}
	reg.Handle("/ws/device-status", nothing)
	reg.Handle("/ws/chat/{id}", nothing)
	reg.Handle("/api/cdap/devices/{id:[A-Za-z0-9_-]{1,64}}/terminal", nothing)
	for target, want := range map[string]bool{
		"/ws/device-status":                true,
		"/ws/device-status?x=1":            true,
		"/ws/./device-status":              true,
		"/ws/x/../device-status":           true,
		"//evil/ws/device-status":          true,
		"/ws/device-status/":               false,
		"/ws/Device-Status":                false,
		"/ws/device%2Dstatus":              false,
		"/ws/chat/abc":                     true,
		"/ws/chat/a%2Fb":                   true,
		"/ws/chat/":                        false,
		"/ws/chat/a/b":                     false,
		"/api/cdap/devices/d-1_x/terminal": true,
		"/api/cdap/devices/d.1/terminal":   false,
		"/api/cdap/devices/" + strings.Repeat("a", 65) + "/terminal": false,
		"/ws/other": false,
	} {
		if got := reg.Serves(upgrade(target)); got != want {
			t.Errorf("Serves(%q) = %v, want %v", target, got, want)
		}
	}
	plain := httptest.NewRequest(http.MethodGet, "/ws/device-status", nil)
	if reg.Serves(plain) {
		t.Error("a request without Upgrade is served as a socket")
	}
	noConnection := httptest.NewRequest(http.MethodGet, "/ws/device-status", nil)
	noConnection.Header.Set("Upgrade", "websocket")
	if reg.Serves(noConnection) {
		t.Error("a request without Connection: upgrade is served as a socket")
	}
}

func TestHandleRefusesBadPatterns(t *testing.T) {
	for _, pattern := range []string{"ws/x", "/ws/{a}/{b}", "/ws/x{a}", "/ws/{}"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Handle(%q) did not panic", pattern)
				}
			}()
			ws.New(ws.Options{}).Handle(pattern, func(*ws.Upgrade) {})
		}()
	}
}

// TestParamIsTheRawSegment: the parameter is the segment as sent.
func TestParamIsTheRawSegment(t *testing.T) {
	reg := ws.New(ws.Options{})
	got := make(chan string, 1)
	reg.Handle("/ws/chat/{id}", func(u *ws.Upgrade) {
		got <- u.Param("id")
		u.Refuse(http.StatusForbidden)
	})
	srv := httptest.NewServer(reg)
	defer srv.Close()
	defer reg.Close()
	raw, err := wstest.Raw(strings.TrimPrefix(srv.URL, "http://"), "GET /ws/chat/a%20b HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if raw != "HTTP/1.1 403 Forbidden\r\n\r\n" {
		t.Errorf("refusal %q", raw)
	}
	if id := <-got; id != "a%20b" {
		t.Errorf("param %q", id)
	}
}

// TestCloseEndsSocketsAndWaits: Close drops open sockets, waits for the
// handlers, and refuses later upgrades with 503.
func TestCloseEndsSocketsAndWaits(t *testing.T) {
	check := wstest.Goroutines(t)
	reg := ws.New(ws.Options{})
	var ended atomic.Bool
	reg.Handle("/ws/x", func(u *ws.Upgrade) {
		conn, ok := u.Accept(ws.AcceptOptions{})
		if !ok {
			return
		}
		defer conn.Wait()
		_ = conn.Discard()
		time.Sleep(50 * time.Millisecond)
		ended.Store(true)
	})
	srv := httptest.NewServer(reg)
	addr := strings.TrimPrefix(srv.URL, "http://")
	c, _, err := wstest.Dial(addr, "/ws/x", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	reg.Close()
	if !ended.Load() {
		t.Error("Close returned before the handler did")
	}
	if !c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the socket stayed open")
	}
	raw, _ := wstest.Raw(addr, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	if raw != "HTTP/1.1 503 Service Unavailable\r\n\r\n" {
		t.Errorf("after Close: %q", raw)
	}
	c.Drop()
	srv.Close()
	check()
}

func newEcho(t *testing.T, opts ws.AcceptOptions, serve func(c *ws.Conn)) string {
	t.Helper()
	reg := ws.New(ws.Options{})
	reg.Handle("/ws/x", func(u *ws.Upgrade) {
		conn, ok := u.Accept(opts)
		if !ok {
			return
		}
		defer conn.Wait()
		serve(conn)
	})
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
	})
	return strings.TrimPrefix(srv.URL, "http://")
}

// TestCloseComesAfterQueuedMessages: a close goes out after what was
// queued before it, with its code and reason.
func TestCloseComesAfterQueuedMessages(t *testing.T) {
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		c.SendText("one")
		c.Send(true, []byte{2})
		c.SendText("three")
		c.Close(4000, "done")
		c.SendText("after the close")
		_ = c.Discard()
	})
	c, _, err := wstest.Dial(addr, "/ws/x", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Drop()
	if !c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("not closed")
	}
	f := c.Frames()
	if len(f) != 3 || f[0].Text() != "one" || !f[1].Binary || f[2].Text() != "three" {
		t.Errorf("frames %+v", f)
	}
	if _, code, reason := c.Closed(); code != 4000 || reason != "done" {
		t.Errorf("closed %d %q", code, reason)
	}
}

// TestContextLastsThroughTheClose: a socket's context ends after its close
// went out, so what a service does when the context ends (dropping the
// socket from its lists, telling the peer) comes after the close.
func TestContextLastsThroughTheClose(t *testing.T) {
	gotClose := make(chan struct{})
	ctxErr := make(chan error, 1)
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		c.Close(4000, "done")
		select {
		case <-gotClose:
			ctxErr <- c.Context().Err()
		case <-time.After(3 * time.Second):
			ctxErr <- errors.New("the client got no close")
		}
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake: %v %v", resp, err)
	}
	// The server's first frame is the close; the client does not answer it
	// yet, so the server's side of the handshake is still open.
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil {
		t.Fatalf("no close frame: %v", err)
	}
	payload := make([]byte, head[1]&0x7F)
	if _, err := io.ReadFull(br, payload); err != nil || head[0] != 0x88 || len(payload) < 2 {
		t.Fatalf("frame %x %q: %v", head, payload, err)
	}
	if code, reason := binary.BigEndian.Uint16(payload), string(payload[2:]); code != 4000 || reason != "done" {
		t.Errorf("closed %d %q", code, reason)
	}
	close(gotClose)
	if err := <-ctxErr; err != nil {
		t.Errorf("the context ended before the close went out: %v", err)
	}
}

// TestSizeLimit: a message at the limit passes, one byte more closes the
// socket with 1009 and no reason, and Read reports ErrTooBig, a protocol
// error.
func TestSizeLimit(t *testing.T) {
	result := make(chan error, 1)
	addr := newEcho(t, ws.AcceptOptions{MaxPayload: 10}, func(c *ws.Conn) {
		_, data, err := c.Read()
		if err == nil && len(data) == 10 {
			_, _, err = c.Read()
		}
		result <- err
	})
	c, _, err := wstest.Dial(addr, "/ws/x", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Drop()
	_ = c.Text(strings.Repeat("a", 10))
	_ = c.Text(strings.Repeat("a", 11))
	err = <-result
	if !errors.Is(err, ws.ErrTooBig) || !ws.ProtocolError(err) {
		t.Errorf("Read: %v", err)
	}
	c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	if _, code, reason := c.Closed(); code != 1009 || reason != "" {
		t.Errorf("closed %d %q", code, reason)
	}
}

// TestTextMustBeUTF8: a text message that is not UTF-8 closes with 1007.
func TestTextMustBeUTF8(t *testing.T) {
	result := make(chan error, 1)
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		_, _, err := c.Read()
		result <- err
	})
	c, _, err := wstest.Dial(addr, "/ws/x", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Drop()
	_ = c.WriteFrame(0x1, []byte{0xc3, 0x28})
	if err := <-result; !errors.Is(err, ws.ErrInvalidText) {
		t.Errorf("Read: %v", err)
	}
	c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	if _, code, _ := c.Closed(); code != 1007 {
		t.Errorf("closed %d", code)
	}
}

// TestSlowReaderIsDropped: a client that stops reading is let go once the
// queue is full, instead of the server keeping its messages without end.
func TestSlowReaderIsDropped(t *testing.T) {
	gone := make(chan struct{})
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		big := make([]byte, 256<<10)
		// 32 MiB fill the connection's buffers, so the writer is stuck on
		// a write when the queue runs full: dropping the client must not
		// wait for that write.
		for i := 0; i < 128; i++ {
			c.Send(true, big)
		}
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 4000 && c.Open(); i++ {
			c.Send(true, big)
		}
		<-c.Context().Done()
		close(gone)
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	// The client reads nothing.
	select {
	case <-gone:
	case <-time.After(20 * time.Second):
		t.Fatal("the server kept the client that does not read")
	}
}

// TestSlowReaderIsDroppedByBytes: the queue of a client that does not
// read is bounded by its bytes too: messages of 2 MiB fill 16 MiB long
// before 1,024 of them wait, and the client is let go (BUGS WS12).
func TestSlowReaderIsDroppedByBytes(t *testing.T) {
	queued := make(chan int, 1)
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		big := make([]byte, 2<<20)
		n := 0
		for ; n < 200 && c.Open(); n++ {
			c.Send(true, big)
		}
		queued <- n
		<-c.Context().Done()
	})
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	// The client reads nothing.
	select {
	case n := <-queued:
		if n == 200 {
			t.Errorf("the server took %d messages of 2 MiB for a client that does not read", n)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server kept sending")
	}
}

// TestReadingClientGetsMoreThanTheQueueHolds: the bytes a client has read
// leave the queue, so a client that reads gets far more than 16 MiB over
// the socket's life.
func TestReadingClientGetsMoreThanTheQueueHolds(t *testing.T) {
	const messages = 48
	addr := newEcho(t, ws.AcceptOptions{}, func(c *ws.Conn) {
		big := make([]byte, 1<<20)
		for i := 0; i < messages && c.Open(); i++ {
			c.Send(true, big)
			// The client reads at its pace; the queue never holds much
			time.Sleep(5 * time.Millisecond)
		}
		c.Close(ws.CloseNormal, "")
	})
	c, _, err := wstest.Dial(addr, "/ws/x", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Drop()
	got := 0
	if !c.Wait(20*time.Second, func(frames []wstest.Frame, closed bool) bool {
		got = 0
		for _, f := range frames {
			got += len(f.Data)
		}
		return closed
	}) || got != messages<<20 {
		t.Errorf("the client got %d bytes, want %d", got, messages<<20)
	}
}

func TestProtocolError(t *testing.T) {
	for err, want := range map[error]bool{
		nil:                     false,
		ws.ErrTooBig:            true,
		ws.ErrInvalidText:       true,
		io.EOF:                  false,
		net.ErrClosed:           false,
		context.Canceled:        false,
		errors.New("bad frame"): true,
	} {
		if got := ws.ProtocolError(err); got != want {
			t.Errorf("ProtocolError(%v) = %v", err, got)
		}
	}
}

func TestAddrCounter(t *testing.T) {
	c := ws.NewAddrCounter()
	c.Add("a")
	c.Add("a")
	c.Done("a")
	if c.Count("a") != 1 {
		t.Errorf("count %d", c.Count("a"))
	}
	c.Done("a")
	c.Done("a")
	if c.Count("a") != 0 {
		t.Errorf("count below zero: %d", c.Count("a"))
	}
}

// TestClientIPIgnoresForwardedWithoutTrustProxy: ClientIP is the address
// the console's TRUST_PROXY gives. Without a trusted proxy, and without
// the option, X-Forwarded-For is ignored and the connection's address
// counts; a trusted proxy (here loopback) speaks for the client with the
// last entry it added.
func TestClientIPIgnoresForwardedWithoutTrustProxy(t *testing.T) {
	trusted, err := httpx.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatal(err)
	}
	untrusted, err := httpx.ParseTrustProxy("")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		clientIP func(*http.Request) string
		want     map[string]string
	}{
		{"no option", nil, map[string]string{
			"": "127.0.0.1",
			"X-Forwarded-For: 10.0.0.1, 10.0.0.2\r\n": "127.0.0.1",
		}},
		{"trust nobody", untrusted.ClientIP, map[string]string{
			"": "127.0.0.1",
			"X-Forwarded-For: 10.0.0.1, 10.0.0.2\r\n": "127.0.0.1",
		}},
		{"trust loopback", trusted.ClientIP, map[string]string{
			"": "127.0.0.1",
			"X-Forwarded-For: 10.0.0.1, 10.0.0.2\r\n":      "10.0.0.2",
			"X-Forwarded-For: a\r\nX-Forwarded-For: b\r\n": "b",
		}},
	} {
		reg := ws.New(ws.Options{ClientIP: c.clientIP})
		got := make(chan string, 1)
		reg.Handle("/ws/x", func(u *ws.Upgrade) {
			got <- u.ClientIP()
			u.Refuse(http.StatusForbidden)
		})
		srv := httptest.NewServer(reg)
		addr := strings.TrimPrefix(srv.URL, "http://")
		for header, want := range c.want {
			_, _ = wstest.Raw(addr, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+header+"\r\n")
			if g := <-got; g != want {
				t.Errorf("%s, %q: %q, want %q", c.name, header, g, want)
			}
		}
		srv.Close()
		reg.Close()
	}
}

// TestHeartbeatDropsAPeerThatDoesNotAnswer: Heartbeat pings every interval
// and drops the connection when two pings in a row got no answer, so a
// peer that is gone ends after two to three intervals. A peer that
// answers stays, and KeepAlive alone drops nobody.
func TestHeartbeatDropsAPeerThatDoesNotAnswer(t *testing.T) {
	check := wstest.Goroutines(t)
	const interval = 50 * time.Millisecond
	type end struct {
		name  string
		after time.Duration
	}
	ended := make(chan end, 4)
	reg := ws.New(ws.Options{})
	reg.Handle("/ws/{name}", func(u *ws.Upgrade) {
		conn, ok := u.Accept(ws.AcceptOptions{})
		if !ok {
			return
		}
		defer conn.Wait()
		if strings.HasPrefix(u.Param("name"), "heartbeat") {
			conn.Heartbeat(interval)
		} else {
			conn.KeepAlive(interval)
		}
		start := time.Now()
		_ = conn.Discard()
		ended <- end{u.Param("name"), time.Since(start)}
	})
	srv := httptest.NewServer(reg)
	addr := strings.TrimPrefix(srv.URL, "http://")
	// silent opens a socket and never reads from it again, so no ping is
	// answered.
	silent := func(name string) net.Conn {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, "GET /ws/"+name+" HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
		head, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || !strings.HasPrefix(head, "HTTP/1.1 101") {
			t.Fatalf("%s: %q %v", name, head, err)
		}
		return conn
	}
	gone := silent("heartbeat-silent")
	defer gone.Close()
	quiet := silent("keepalive-silent")
	defer quiet.Close()
	answering, _, err := wstest.Dial(addr, "/ws/heartbeat-answering", nil)
	if err != nil || answering == nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case e := <-ended:
		if e.name != "heartbeat-silent" {
			t.Fatalf("%s ended first", e.name)
		}
		if e.after < 2*interval {
			t.Errorf("dropped after %v, before two pings went unanswered", e.after)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the peer that does not answer was not dropped")
	}
	time.Sleep(10 * interval)
	select {
	case e := <-ended:
		t.Errorf("%s ended after %v", e.name, e.after)
	default:
	}
	if answering.Pings() < 3 {
		t.Errorf("the answering peer got %d pings", answering.Pings())
	}
	answering.Close(1000)
	gone.Close()
	quiet.Close()
	srv.Close()
	reg.Close()
	check()
}

// TestHeartbeatCountsMissesInARow: a peer that leaves every other ping
// unanswered is never dropped, since no two misses come in a row.
func TestHeartbeatCountsMissesInARow(t *testing.T) {
	check := wstest.Goroutines(t)
	const interval = 30 * time.Millisecond
	ended := make(chan struct{})
	reg := ws.New(ws.Options{})
	reg.Handle("/ws/x", func(u *ws.Upgrade) {
		conn, ok := u.Accept(ws.AcceptOptions{})
		if !ok {
			return
		}
		defer conn.Wait()
		conn.Heartbeat(interval)
		_ = conn.Discard()
		close(ended)
	})
	srv := httptest.NewServer(reg)
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "GET /ws/x HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReader(conn)
	if head, err := br.ReadString('\n'); err != nil || !strings.HasPrefix(head, "HTTP/1.1 101") {
		t.Fatalf("%q %v", head, err)
	}
	for line := ""; line != "\r\n"; {
		if line, err = br.ReadString('\n'); err != nil {
			t.Fatal(err)
		}
	}
	var pings atomic.Int32
	go func() {
		for {
			var head [2]byte
			if _, err := io.ReadFull(br, head[:]); err != nil {
				return
			}
			payload := make([]byte, head[1]&0x7f)
			if _, err := io.ReadFull(br, payload); err != nil {
				return
			}
			if head[0]&0x0f != 0x9 {
				continue
			}
			// Every second ping gets its pong (masked with a zero key)
			if pings.Add(1)%2 == 0 {
				_, _ = conn.Write(append([]byte{0x8a, 0x80 | byte(len(payload)), 0, 0, 0, 0}, payload...))
			}
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for pings.Load() < 8 && time.Now().Before(deadline) {
		select {
		case <-ended:
			t.Fatalf("dropped after %d pings, every second one answered", pings.Load())
		case <-time.After(interval):
		}
	}
	if pings.Load() < 8 {
		t.Fatalf("only %d pings", pings.Load())
	}
	conn.Close()
	srv.Close()
	reg.Close()
	check()
}
