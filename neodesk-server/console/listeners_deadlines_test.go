package console

import (
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/listen"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// exchangeRaw writes text on a new connection to addr and reads until the
// connection closes or wait has passed.
func exchangeRaw(t *testing.T, addr string, secure bool, text string, wait time.Duration) string {
	t.Helper()
	var conn net.Conn
	var err error
	if secure {
		conn, err = tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, text)
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	got, _ := io.ReadAll(conn)
	return string(got)
}

// TestConsoleHeaderLimit: the console's listener, plain and with TLS,
// takes 16 KB of request headers as Node did, not net/http's 1 MB
// (BUGS.md RV190).
func TestConsoleHeaderLimit(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, c := range []struct {
		scenario, listener string
		secure             bool
	}{{"proxy", "console", false}, {"https", "https", true}} {
		g, err := newGoStack(t, db, oracleScenarioNamed(t, c.scenario), certs, nil)
		if err != nil {
			t.Fatal(err)
		}
		request := func(header int) string {
			return "GET /health HTTP/1.1\r\nHost: localhost\r\nX-Big: " + strings.Repeat("b", header) + "\r\nConnection: close\r\n\r\n"
		}
		if got := exchangeRaw(t, g.ports[c.listener], c.secure, request(17000), 5*time.Second); !strings.HasPrefix(got, "HTTP/1.1 431 ") {
			t.Errorf("%s: 17 KB of headers: %.80q", c.scenario, got)
		}
		if got := exchangeRaw(t, g.ports[c.listener], c.secure, request(15000), 5*time.Second); !strings.HasPrefix(got, "HTTP/1.1 200 ") {
			t.Errorf("%s: 15 KB of headers: %.80q", c.scenario, got)
		}
	}
}

// timedExchange writes the parts on a new connection to addr, pause apart,
// and reads until the server closes the connection or wait has passed. It
// returns what came back, whether the server closed the connection and
// when the reading ended.
func timedExchange(t *testing.T, addr string, parts []string, pause, wait time.Duration) (got string, closed bool, after time.Duration) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	go func() {
		for i, part := range parts {
			if i > 0 {
				time.Sleep(pause)
			}
			if _, err := io.WriteString(conn, part); err != nil {
				return
			}
		}
	}()
	_ = conn.SetReadDeadline(start.Add(wait))
	data, err := io.ReadAll(conn)
	var ne net.Error
	timedOut := err != nil && errors.As(err, &ne) && ne.Timeout()
	return string(data), !timedOut, time.Since(start)
}

// TestConsoleListenerTimeLimits: the console's listener closes a
// kept-alive connection that stays idle, and a request whose body stops
// arriving or trickles in slower than the body rate after the grace time;
// a body that keeps coming fast enough passes after the grace time
// (BUGS.md RV100). The limits are shortened for the test.
func TestConsoleListenerTimeLimits(t *testing.T) {
	limits := &consoleLimits{idle: time.Second, deadlines: listen.Deadlines{BodyStall: time.Second, BodyGrace: 3 * time.Second, BodyRate: 1 << 10, WriteStall: time.Second}}
	g, err := newGoStackWith(t, storetest.Migrated(t), oracleScenarioNamed(t, "proxy"), makeTestCerts(t), nil, func(s *Server) { s.net.limits = limits })
	if err != nil {
		t.Fatal(err)
	}
	addr := g.ports["console"]
	post := func(length int) string {
		return "POST /api/auth/login HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: " + strconv.Itoa(length) + "\r\n\r\n"
	}

	got, closed, after := timedExchange(t, addr, []string{"GET /health HTTP/1.1\r\nHost: localhost\r\n\r\n"}, 0, 6*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 200 ") || strings.Count(got, "HTTP/1.1 ") != 1 || !closed || after > 4*time.Second {
		t.Errorf("idle connection: closed %v after %v: %.60q", closed, after, got)
	}

	_, closed, after = timedExchange(t, addr, []string{post(100) + `{"a"`}, 0, 6*time.Second)
	if !closed || after > 4*time.Second {
		t.Errorf("stalled body: closed %v after %v", closed, after)
	}

	trickle := []string{post(100)}
	for range 99 {
		trickle = append(trickle, " ")
	}
	_, closed, after = timedExchange(t, addr, trickle, 400*time.Millisecond, 8*time.Second)
	if !closed || after > 6*time.Second {
		t.Errorf("body at 2.5 bytes a second: closed %v after %v", closed, after)
	}

	// 32 KB at 8 KB a second: past the grace time, never behind the rate
	steady := []string{post(8 * 4096)}
	for range 8 {
		steady = append(steady, strings.Repeat(" ", 4096))
	}
	got, _, after = timedExchange(t, addr, steady, 500*time.Millisecond, 10*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 4") || after < 3*time.Second {
		t.Errorf("steady body: after %v: %.60q", after, got)
	}
}

// TestNotFoundLogNamesTheTrustedAddress: the 404 log line of an unmatched
// API path names the client's address as TRUST_PROXY gives it, not what
// X-Forwarded-For claims (BUGS.md RV194).
func TestNotFoundLogNamesTheTrustedAddress(t *testing.T) {
	var logged syncBuffer
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)
	for _, c := range []struct{ trust, want string }{{"", "from 127.0.0.1 "}, {"loopback", "from 203.0.113.99 "}} {
		w := goOnly(t, setup{trustProxy: c.trust})
		before := len(logged.String())
		w.agent().get("/api/rv194-nope", "X-Forwarded-For", "203.0.113.99")
		if line := logged.String()[before:]; !strings.Contains(line, "404 GET /api/rv194-nope "+c.want) {
			t.Errorf("TRUST_PROXY %q: logged %q", c.trust, line)
		}
	}
}

// syncBuffer is a log output that tests read while the server writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
