package remotetarget

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// gateway is a gateway on the oracle's world.
func gateway(t *testing.T, w *world, opts func(*Options)) (string, func()) {
	t.Helper()
	o := w.options()
	if opts != nil {
		opts(&o)
	}
	g := New(o)
	reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: w.permission})
	g.Register(reg)
	srv := httptest.NewServer(reg)
	return strings.TrimPrefix(srv.URL, "http://"), func() {
		srv.Close()
		reg.Close()
	}
}

var operator = http.Header{"X-Oracle-Session": {`{"userId":7,"user":{"id":7,"username":"olga","role":"operator"}}`}}

func open(t *testing.T, addr, id string) *wstest.Client {
	t.Helper()
	c, res, err := wstest.Dial(addr, "/ws/remote-target/"+id, operator)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatalf("refused with %d", res.StatusCode)
	}
	return c
}

// The tunnel is the API server's path of the target, its id escaped as
// encodeURIComponent escapes it.
func TestTunnelPath(t *testing.T) {
	check := wstest.Goroutines(t)
	var mu sync.Mutex
	var paths []string
	w := &world{}
	addr, stop := gateway(t, w, func(o *Options) {
		o.API = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.URL.EscapedPath())
			mu.Unlock()
			http.NotFound(rw, r)
		})
	})
	c := open(t, addr, "rt_echo-0001")
	c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	c.Drop()
	stop()
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(paths) != "[/api/remote-targets/rt_echo-0001/tunnel]" {
		t.Errorf("tunnel paths %v", paths)
	}
	check()
}

// A tunnel the API server answers with a redirect is a failed tunnel:
// the browser's socket is closed without a code and nothing follows it.
func TestRedirectsAreNotFollowed(t *testing.T) {
	check := wstest.Goroutines(t)
	w := &world{}
	addr, stop := gateway(t, w, func(o *Options) {
		o.API = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/tunnel") {
				http.Redirect(rw, r, "/elsewhere"+r.URL.Path, http.StatusFound)
				return
			}
			w.api(rw, r)
		})
	})
	c := open(t, addr, "rt_echo-0001")
	if !c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the browser's socket stays open after a redirect")
	}
	if closed, code, _ := c.Closed(); !closed || code != 1005 {
		t.Errorf("closed %v with %d, want a close without a code", closed, code)
	}
	if _, events := w.take(); len(events) != 0 {
		t.Errorf("the redirect was followed: %v", events)
	}
	c.Drop()
	stop()
	check()
}

// What the browser sends while the tunnel connects waits, in order, also
// when it is more than the queue holds: the browser is then not read
// until the tunnel is open.
func TestMessagesWaitForTheTunnel(t *testing.T) {
	check := wstest.Goroutines(t)
	w := &world{}
	addr, stop := gateway(t, w, nil)
	c := open(t, addr, "rt_slow-0001")
	const n = 3000
	for i := 0; i < n; i++ {
		if err := c.Text(fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Wait(20*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == n }) {
		t.Fatalf("%d echoes of %d", len(c.Frames()), n)
	}
	for i, f := range c.Frames() {
		if f.Text() != fmt.Sprint("echo:", i) {
			t.Fatalf("echo %d is %q", i, f.Text())
		}
	}
	c.Close(1000)
	stop()
	check()
}

// Stopping the console ends the browser's socket and the tunnel.
func TestStopEndsTheTunnel(t *testing.T) {
	check := wstest.Goroutines(t)
	w := &world{}
	addr, stop := gateway(t, w, nil)
	c := open(t, addr, "rt_echo-0001")
	_ = c.Text("x")
	c.Wait(2*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 })
	stop()
	if !c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the browser's socket stays open")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, events := w.take()
		if len(events) > 0 && events[len(events)-1].Close != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tunnel stays open")
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.Drop()
	check()
}

// The tunnel gets the account in X-Username and X-Role, no API key (the
// handler carries the console's identity), no Origin and no cookie of
// the browser.
func TestTunnelHandshake(t *testing.T) {
	check := wstest.Goroutines(t)
	var mu sync.Mutex
	var got http.Header
	w := &world{}
	addr, stop := gateway(t, w, func(o *Options) {
		o.API = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			mu.Lock()
			got = r.Header.Clone()
			mu.Unlock()
			c, err := websocket.Accept(rw, r, nil)
			if err != nil {
				return
			}
			_ = c.Close(websocket.StatusNormalClosure, "")
		})
	})
	header := operator.Clone()
	header.Set("Cookie", "bd.sid=s%3Asecret")
	header.Set("Origin", "http://"+addr)
	c, _, err := wstest.Dial(addr, "/ws/remote-target/rt_echo-0001", header)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	mu.Lock()
	defer mu.Unlock()
	for name, want := range map[string]string{"X-Api-Key": "", "X-Username": "olga", "X-Role": "operator", "Origin": "", "Cookie": ""} {
		if v := got.Get(name); v != want {
			t.Errorf("%s: %q, want %q", name, v, want)
		}
	}
	c.Drop()
	stop()
	check()
}

// A name with a character above U+00FF, or a control character other than
// a tab, cannot go into a header: the browser gets the 101 and then the
// gateway's 502 on the same connection, and no tunnel is opened. A name
// in Latin-1 goes as one byte per character.
func TestNamesThatCannotBeAHeader(t *testing.T) {
	check := wstest.Goroutines(t)
	var mu sync.Mutex
	var names []string
	w := &world{}
	addr, stop := gateway(t, w, func(o *Options) {
		o.API = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			mu.Lock()
			names = append(names, r.Header.Get("X-Username"))
			mu.Unlock()
			http.NotFound(rw, r)
		})
	})
	request := func(name string) string {
		return "GET /ws/remote-target/rt_echo-0001 HTTP/1.1\r\nHost: " + addr + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n" +
			`X-Oracle-Session: {"userId":7,"user":{"id":7,"username":"` + name + `","role":"operator"}}` + "\r\n\r\n"
	}
	want := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n" +
		"HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n"
	for _, name := range []string{`Łukasz`, `bell\u0007`, `nul\u0000`, `del\u007f`} {
		got, err := wstest.Raw(addr, request(name))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
	// A handshake the library refuses gets the library's answer first
	bad, err := wstest.Raw(addr, strings.Replace(request(`Łukasz`), "Version: 13", "Version: 7", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(bad, "HTTP/1.1 400 Bad Request\r\n") {
		t.Errorf("a bad handshake: %q", bad)
	}
	got, err := wstest.Raw(addr, request(`jörg`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "HTTP/1.1 101") {
		t.Errorf("a Latin-1 name: %q", got)
	}
	stop()
	mu.Lock()
	defer mu.Unlock()
	if len(names) != 1 || names[0] != "j\xf6rg" {
		t.Errorf("tunnels opened for %q, want one for the Latin-1 name in one byte per character", names)
	}
	check()
}
