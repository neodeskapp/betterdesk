package limits

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A request that arrived whole and is not answered in time: Node destroyed
// the socket (its request had no timeout listener left), so the client
// gets no answer at all.
func TestClientAPITimeoutDropsAWholeRequest(t *testing.T) {
	release := make(chan struct{})
	var lateWrite error
	var cancelled bool
	done := make(chan struct{})
	h := wanHandler(WANOptions{Timeout: 30 * time.Millisecond}, func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		<-release
		cancelled = r.Context().Err() != nil
		w.WriteHeader(http.StatusOK)
		_, lateWrite = w.Write([]byte("late"))
	})
	server := httptest.NewServer(h)
	defer server.Close()
	res, err := http.Get(server.URL + "/api/peers")
	if err == nil {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Errorf("slow handler: %d %q, want the connection closed without an answer", res.StatusCode, body)
	}
	close(release)
	<-done
	if !errors.Is(lateWrite, http.ErrHandlerTimeout) || !cancelled {
		t.Errorf("after the timeout: write error %v, context cancelled %v", lateWrite, cancelled)
	}

	// A handler that answers in time is not disturbed
	fast := wanHandler(WANOptions{Timeout: time.Second}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fast", "1")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("fast"))
	})
	rec := hit(fast, wanRequest("GET", "/api/peers", -1))
	if rec.Code != http.StatusAccepted || rec.Body.String() != "fast" || rec.Header().Get("X-Fast") != "1" || rec.Header().Get("Pragma") != "no-cache" {
		t.Errorf("fast handler: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if WANRequestTimeout != 10*time.Second || WANKeepAliveTimeout != 5*time.Second {
		t.Error("the port's timeouts changed")
	}
}

// With GatewayTimeout a request that arrived whole and is still waiting
// when the time is up gets 504 with the port's headers, and the handler's
// late answer goes nowhere.
func TestClientAPITimeoutAnswersAGatewayTimeout(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	var lateWrite error
	h := wanHandler(WANOptions{Timeout: 30 * time.Millisecond, GatewayTimeout: true}, func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		<-release
		_, lateWrite = w.Write([]byte("late"))
	})
	server := httptest.NewServer(h)
	defer server.Close()
	res, err := http.Get(server.URL + "/api/peers")
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusGatewayTimeout || string(body) != `{"error":"Gateway Timeout"}` ||
		res.Header.Get("Content-Type") != "application/json; charset=utf-8" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Errorf("slow handler: %d %q %v", res.StatusCode, body, res.Header)
	}
	close(release)
	<-done
	if !errors.Is(lateWrite, http.ErrHandlerTimeout) {
		t.Errorf("late write: %v", lateWrite)
	}
	// A body still arriving gets 408 as without the option
	got := rawExchange(t, server.Listener.Addr().String(),
		"POST /api/heartbeat HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":", 3*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") {
		t.Errorf("stalled body: %q", got)
	}
}

// rawExchange sends text to the server and reads until it closes.
func rawExchange(t *testing.T, addr, text string, after time.Duration) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, text)
	_ = conn.SetReadDeadline(time.Now().Add(after))
	got, _ := io.ReadAll(conn)
	return string(got)
}

// A body still arriving when the time is up gets 408 with the port's
// headers, as Node's request timeout answered.
func TestClientAPITimeoutAnswersABodyStillArriving(t *testing.T) {
	reached := false
	h := wanHandler(WANOptions{Timeout: 200 * time.Millisecond}, func(http.ResponseWriter, *http.Request) { reached = true })
	server := httptest.NewServer(h)
	defer server.Close()
	got := rawExchange(t, server.Listener.Addr().String(),
		"POST /api/heartbeat HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":", 3*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") || !strings.Contains(got, "X-Frame-Options: DENY") || reached {
		t.Errorf("stalled body: %q (handler reached %v)", got, reached)
	}
}

// A body the listener's read deadline cut off gets 408 with the rate
// limit headers, a broken one 400; both close the connection.
func TestClientAPIBodyReadErrors(t *testing.T) {
	reached := false
	h := wanHandler(WANOptions{}, func(http.ResponseWriter, *http.Request) { reached = true })
	server := httptest.NewUnstartedServer(h)
	server.Config.ReadTimeout = 300 * time.Millisecond
	server.Start()
	defer server.Close()
	got := rawExchange(t, server.Listener.Addr().String(),
		"POST /api/heartbeat HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":", 3*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 408 ") || !strings.Contains(got, "Connection: close") || !strings.Contains(got, "Ratelimit-Limit: 60") || reached {
		t.Errorf("body cut off by the read deadline: %q (handler reached %v)", got, reached)
	}
	got = rawExchange(t, server.Listener.Addr().String(),
		"POST /api/heartbeat HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n{}\r\n0\r\n\r\n", 3*time.Second)
	if !strings.HasPrefix(got, "HTTP/1.1 400 ") || reached {
		t.Errorf("broken chunked body: %q (handler reached %v)", got, reached)
	}
}
