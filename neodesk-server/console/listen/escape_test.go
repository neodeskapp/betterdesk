package listen

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestBrokenEscapes: on a listener BrokenEscapes wraps, a request whose
// path has a broken percent escape gets the answer handler's answer in
// place of net/http's plain 400, on plain HTTP and over TLS, also as the
// second request of a connection, which then closes. What else net/http
// refuses keeps net/http's answer, and requests over TLS keep their TLS
// state.
func TestBrokenEscapes(t *testing.T) {
	c := makeCerts(t)
	f, _ := ReadFiles(c.selfSigned, c.selfKey, "", func(string, ...any) {})
	cert, err := f.Certificate()
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "tls=%v", r.TLS != nil && r.TLS.HandshakeComplete)
	})
	answer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Answer", "console")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, "%s %s %s %s from %v tls=%v", r.Method, r.RequestURI, r.Header.Get("Accept"), r.Host, r.RemoteAddr != "", r.TLS != nil && r.TLS.HandshakeComplete)
	})
	for _, secure := range []bool{false, true} {
		inner, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		ln := inner
		if secure {
			ln = TLSListener(inner, ServerTLS(cert))
		}
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ErrorLog: ErrorLog("test")}
		go func() { _ = srv.Serve(BrokenEscapes(ln, answer, 2*time.Second)) }()
		t.Cleanup(func() { _ = srv.Close() })
		// exchange sends the texts one after the other, each after the
		// answers to the one before had time to come, and reads until the
		// server closes
		exchange := func(texts ...string) ([]*http.Response, []string) {
			t.Helper()
			var conn net.Conn
			if secure {
				conn, err = tls.Dial("tcp", inner.Addr().String(), &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test certificate
			} else {
				conn, err = net.Dial("tcp", inner.Addr().String())
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			go func() {
				for _, text := range texts {
					_, _ = io.WriteString(conn, text)
					time.Sleep(200 * time.Millisecond)
				}
			}()
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			raw, err := io.ReadAll(conn)
			if err != nil {
				t.Errorf("secure %v, %q: the connection did not close: %v", secure, texts, err)
			}
			var answers []*http.Response
			var bodies []string
			br := bufio.NewReader(bytes.NewReader(raw))
			for {
				res, err := http.ReadResponse(br, nil)
				if err != nil {
					return answers, bodies
				}
				body, _ := io.ReadAll(res.Body)
				answers, bodies = append(answers, res), append(bodies, string(body))
			}
		}
		const broken = "GET /api/devices/%zz?q=1 HTTP/1.1\r\nHost: example.com\r\nAccept: application/json\r\n\r\n"
		answers, bodies := exchange("GET /ok HTTP/1.1\r\nHost: x\r\n\r\n", broken)
		want := fmt.Sprintf("GET /api/devices/%%zz?q=1 application/json example.com from true tls=%v", secure)
		if len(answers) != 2 || bodies[0] != fmt.Sprintf("tls=%v", secure) || answers[1].StatusCode != http.StatusBadRequest ||
			answers[1].Header.Get("X-Answer") != "console" || !answers[1].Close || bodies[1] != want {
			t.Errorf("secure %v: answers %d, bodies %q, want %q", secure, len(answers), bodies, want)
		}
		answers, bodies = exchange("POST /x/a%z HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\n{}")
		if len(answers) != 1 || !strings.HasPrefix(bodies[0], "POST /x/a%z ") {
			t.Errorf("secure %v, a POST: %q", secure, bodies)
		}
		if secure {
			// A client that never finishes the handshake is closed after the
			// handshake timeout
			conn, err := net.Dial("tcp", inner.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			start := time.Now()
			if _, err := io.ReadAll(conn); err != nil || time.Since(start) > 4*time.Second {
				t.Errorf("a silent client: %v after %v", err, time.Since(start))
			}
			conn.Close()
		}
		// Requests net/http refuses for something else keep its answer
		for _, text := range []string{
			"GET /a\x01b HTTP/1.1\r\nHost: x\r\n\r\n",
			"get /%zz HTTP/1.1\r\nHost: x\r\n\r\n",
			"GET /%zz HTTP/x\r\nHost: x\r\n\r\n",
			"GET /ok HTTP/1.1\r\nBad Header\r\n\r\n",
		} {
			answers, bodies := exchange(text)
			if len(answers) != 1 || answers[0].StatusCode != http.StatusBadRequest || answers[0].Header.Get("X-Answer") != "" || bodies[0] != "400 Bad Request" {
				t.Errorf("secure %v, %q: %q", secure, text, bodies)
			}
		}
	}
}
