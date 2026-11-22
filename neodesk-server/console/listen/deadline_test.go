package listen

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// rawExchange writes the parts on a new connection, pause apart, and
// reads until the server closes it or wait has passed.
func rawExchange(t *testing.T, addr string, parts []string, pause, wait time.Duration) (got string, closed bool, after time.Duration) {
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
	return string(data), !(errors.As(err, &ne) && ne.Timeout()), time.Since(start)
}

var testDeadlines = Deadlines{BodyStall: 500 * time.Millisecond, BodyGrace: 2 * time.Second, BodyRate: 1 << 10, WriteStall: 500 * time.Millisecond}

// TestDeadlinesCutStalledBodies: a body that stops arriving ends the
// request after BodyStall, whether the handler reads it or answers
// without reading it; one that trickles in slower than BodyRate ends
// after BodyGrace; one that keeps coming faster passes.
func TestDeadlinesCutStalledBodies(t *testing.T) {
	read := make(chan error, 10)
	srv := httptest.NewServer(testDeadlines.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/read" {
			_, err := io.Copy(io.Discard, r.Body)
			read <- err
			if err != nil {
				return
			}
		}
		w.WriteHeader(http.StatusForbidden)
	})))
	defer srv.Close()
	addr := srv.Listener.Addr().String()
	head := func(path string, length int) string {
		return "POST " + path + " HTTP/1.1\r\nHost: x\r\nContent-Length: " + strconv.Itoa(length) + "\r\n\r\n"
	}

	for _, path := range []string{"/read", "/answer"} {
		got, closed, after := rawExchange(t, addr, []string{head(path, 100) + "abc"}, 0, 4*time.Second)
		if !closed || after > 2*time.Second {
			t.Errorf("%s, stalled body: closed %v after %v: %.40q", path, closed, after, got)
		}
		if path == "/read" {
			if err := <-read; err == nil {
				t.Error("the handler read a stalled body to its end")
			}
		}
	}

	trickle := []string{head("/read", 100)}
	for range 99 {
		trickle = append(trickle, "x")
	}
	if _, closed, after := rawExchange(t, addr, trickle, 200*time.Millisecond, 6*time.Second); !closed || after > 4*time.Second {
		t.Errorf("trickling body: closed %v after %v", closed, after)
	}
	<-read

	steady := []string{head("/read", 12*1024)}
	for range 12 {
		steady = append(steady, strings.Repeat("x", 1024))
	}
	got, _, after := rawExchange(t, addr, steady, 250*time.Millisecond, 8*time.Second)
	if err := <-read; err != nil || !strings.HasPrefix(got, "HTTP/1.1 403 ") || after < 2*time.Second {
		t.Errorf("steady body: %v after %v: %.40q", err, after, got)
	}
}

// TestDeadlinesCutUnreadAnswers: a client that does not read its answer
// makes the handler's write fail after WriteStall, while a client that
// reads gets an answer of any length.
func TestDeadlinesCutUnreadAnswers(t *testing.T) {
	const size = 64 << 20
	written := make(chan error, 2)
	srv := httptest.NewServer(testDeadlines.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		var err error
		for i := 0; i < size/len(chunk) && err == nil; i++ {
			_, err = w.Write(chunk)
		}
		written <- err
	})))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	select {
	case err := <-written:
		if err == nil {
			t.Errorf("64 MB written to a client that reads nothing")
		}
		if after := time.Since(start); after > 5*time.Second {
			t.Errorf("the write failed after %v", after)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write still blocks after 10 s")
	}

	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if err != nil || n != size || <-written != nil {
		t.Errorf("a reading client got %d bytes: %v", n, err)
	}
}

// TestDeadlinesLeaveHijackedConnections: a connection the handler takes
// over keeps no deadline.
func TestDeadlinesLeaveHijackedConnections(t *testing.T) {
	echoed := make(chan string, 1)
	srv := httptest.NewServer(testDeadlines.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n\r\n")
		_ = rw.Flush()
		line, err := rw.ReadString('\n')
		if err != nil {
			echoed <- "error: " + err.Error()
			return
		}
		echoed <- line
	})))
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\nab")
	if line, _ := bufio.NewReader(conn).ReadString('\n'); !strings.HasPrefix(line, "HTTP/1.1 101") {
		t.Fatalf("answer %q", line)
	}
	time.Sleep(3 * testDeadlines.BodyStall)
	_, _ = io.WriteString(conn, "later\n")
	if got := <-echoed; got != "later\n" {
		t.Errorf("the hijacked connection read %q", got)
	}
}

// TestClientAPIAnswerDeadline: on the client API port an answer the client
// does not read ends the handler's write after the write stall, also in
// proxy mode where the server's API writes the answer.
func TestClientAPIAnswerDeadline(t *testing.T) {
	written := make(chan error, 1)
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		var err error
		for i := 0; i < 64 && err == nil; i++ {
			_, err = w.Write(chunk)
		}
		written <- err
	})
	origin, _ := url.Parse("http://127.0.0.1:1")
	port := NewClientAPI(ClientAPIOptions{Deps: &kit.Deps{}, Proxy: true, APIOrigin: origin, API: api, WriteStall: 500 * time.Millisecond})
	if err := port.Start(context.Background(), "127.0.0.1", 0, nil); err != nil {
		t.Fatal(err)
	}
	defer port.Stop()
	conn, err := net.Dial("tcp", port.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /api/health HTTP/1.1\r\nHost: x\r\n\r\n")
	select {
	case err := <-written:
		if err == nil {
			t.Error("64 MB written to a client that reads nothing")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the write still blocks after 8 s")
	}
}
