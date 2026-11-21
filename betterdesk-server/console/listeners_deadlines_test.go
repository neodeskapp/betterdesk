package console

import (
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
	"time"

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
