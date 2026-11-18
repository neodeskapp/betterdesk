package console

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestClientAPIServerLimits: the client API port's own server reads a
// request within 10 seconds (408 before the first request, as Node), keeps
// an idle connection 5 seconds and takes 16 KB of headers.
func TestClientAPIServerLimits(t *testing.T) {
	g, err := newGoStack(t, storetest.Migrated(t), oracleScenarioNamed(t, "proxy"), makeTestCerts(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	exchange := func(text string, wait time.Duration) (string, time.Duration) {
		conn, err := net.Dial("tcp", g.ports["api"])
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		start := time.Now()
		_, _ = io.WriteString(conn, text)
		_ = conn.SetReadDeadline(start.Add(wait))
		got, _ := io.ReadAll(conn)
		return string(got), time.Since(start)
	}
	type result struct {
		got   string
		after time.Duration
	}
	headers, idle := make(chan result), make(chan result)
	go func() {
		got, after := exchange("GET /api/health HTTP/1.1\r\nHost: x\r\n", 20*time.Second)
		headers <- result{got, after}
	}()
	go func() {
		got, after := exchange("OPTIONS /api/login HTTP/1.1\r\nHost: x\r\n\r\n", 20*time.Second)
		idle <- result{got, after}
	}()
	big := "GET /api/health HTTP/1.1\r\nHost: x\r\nX-Big: " + strings.Repeat("b", 17000) + "\r\nConnection: close\r\n\r\n"
	if got, _ := exchange(big, 5*time.Second); !strings.HasPrefix(got, "HTTP/1.1 431 ") {
		t.Errorf("17 KB of headers: %q", got)
	}
	small := "OPTIONS /api/login HTTP/1.1\r\nHost: x\r\nX-Big: " + strings.Repeat("b", 15000) + "\r\nConnection: close\r\n\r\n"
	if got, _ := exchange(small, 5*time.Second); !strings.HasPrefix(got, "HTTP/1.1 204 ") {
		t.Errorf("15 KB of headers: %q", got)
	}
	h := <-headers
	if h.got != "HTTP/1.1 408 Request Timeout\r\nConnection: close\r\n\r\n" || h.after < 9*time.Second || h.after > 12*time.Second {
		t.Errorf("headers that never end: %q after %v, want the 408 after 10 s", h.got, h.after)
	}
	i := <-idle
	if !strings.HasPrefix(i.got, "HTTP/1.1 204 ") || strings.Count(i.got, "HTTP/1.1") != 1 || i.after < 4*time.Second || i.after > 7*time.Second {
		t.Errorf("idle connection: %q closed after %v, want after 5 s", i.got, i.after)
	}
}
