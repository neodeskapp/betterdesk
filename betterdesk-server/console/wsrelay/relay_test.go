package wsrelay

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
	pb "github.com/unitronix/betterdesk-server/proto"
)

// sink is a TCP server that keeps what each connection sent.
type sink struct {
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	got   map[int][]byte
	wg    sync.WaitGroup
}

func newSink(t *testing.T) *sink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{ln: ln, got: map[int][]byte{}}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			n := len(s.conns)
			s.mu.Unlock()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				buf := make([]byte, 4096)
				for {
					k, err := c.Read(buf)
					s.mu.Lock()
					s.got[n] = append(s.got[n], buf[:k]...)
					s.mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return s
}

func (s *sink) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *sink) received(n int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.got[n]...)
}

func (s *sink) close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

// guestAPI answers the guest link calls: every token is a good link g1
// for TARGET01 until revoked; consume waits on hold when it is set.
type guestAPI struct {
	mu       sync.Mutex
	hold     chan struct{}
	revoked  bool
	consumes int
}

func (a *guestAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	hold, revoked := a.hold, a.revoked
	if strings.HasSuffix(r.URL.Path, "/consume") {
		a.consumes++
	}
	a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/validate"):
		if revoked {
			_, _ = w.Write([]byte(`{"valid":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"valid":true,"id":"g1","peer_ids":["TARGET01"],"expires_at":"2999-01-01T00:00:00Z"}`))
	case strings.HasSuffix(r.URL.Path, "/consume"):
		if hold != nil {
			<-hold
		}
		_, _ = w.Write([]byte(`{"valid":true}`))
	default:
		_, _ = w.Write([]byte(`{"success":true}`))
	}
}

type harness struct {
	t    *testing.T
	svc  *Service
	reg  *ws.Registry
	srv  *httptest.Server
	addr string
	sig  *sink
	api  *guestAPI
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	h := &harness{t: t, sig: newSink(t), api: &guestAPI{}}
	opts.SignalHost, opts.SignalPort = "127.0.0.1", h.sig.port()
	if opts.RelayHost == "" {
		opts.RelayHost, opts.RelayPort = "127.0.0.1", h.sig.port()
		opts.Interfaces = func() []InterfaceAddr { return nil }
	}
	if opts.GoAPI == nil {
		opts.GoAPI = goapi.New(h.api)
	}
	h.svc = New(opts)
	h.reg = ws.New(ws.Options{Session: wstest.OracleSession, Permission: oraclePermission})
	h.svc.Register(h.reg)
	h.srv = httptest.NewServer(h.reg)
	h.addr = strings.TrimPrefix(h.srv.URL, "http://")
	return h
}

func (h *harness) close() {
	h.reg.Close()
	h.srv.Close()
	h.sig.close()
}

func (h *harness) dial(target string, header http.Header) *wstest.Client {
	h.t.Helper()
	c, res, err := wstest.Dial(h.addr, target, header)
	if err != nil {
		h.t.Fatal(err)
	}
	if c == nil {
		h.t.Fatalf("%s refused: %s", target, res.Status)
	}
	return c
}

func adminHeader() http.Header {
	return http.Header{"X-Oracle-Session": {`{"userId":1,"user":{"role":"admin"}}`}}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func framed(t *testing.T, payload []byte) []byte {
	t.Helper()
	b, err := EncodeRelayFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// rendezvousBytes encodes punch_hole_request{id} (field 8) or
// request_relay{id} (field 18) by hand.
func rendezvousBytes(field byte, id string) []byte {
	inner := append([]byte{0x0a, byte(len(id))}, id...)
	if field == 18 {
		return append([]byte{0x92, 0x01, byte(len(inner))}, inner...)
	}
	return append([]byte{0x42, byte(len(inner))}, inner...)
}

// TestGuestFramesKeepTheirOrder: while the first use of a guest's link is
// being counted, a later frame waits behind it; the server gets them in
// the order the browser sent them.
func TestGuestFramesKeepTheirOrder(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{})
	defer h.close()
	hold := make(chan struct{})
	h.api.mu.Lock()
	h.api.hold = hold
	h.api.mu.Unlock()
	c := h.dial("/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	punch := framed(t, rendezvousBytes(8, "TARGET01"))
	other := framed(t, []byte{0xd2, 0x01, 0x00}) // hc{}
	_ = c.Binary(punch)
	_ = c.Binary(other)
	time.Sleep(200 * time.Millisecond)
	if got := h.sig.received(1); len(got) != 0 {
		t.Fatalf("the server got %x before the use was counted", got)
	}
	close(hold)
	want := append(append([]byte{}, punch...), other...)
	waitFor(t, "both frames", func() bool { return bytes.Equal(h.sig.received(1), want) })
	c.Close(1000)
}

// TestDialEndsWithTheBridge: a dial that does not get through ends when
// the browser leaves, and nothing of the bridge is left running.
func TestDialEndsWithTheBridge(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	dialing := make(chan struct{})
	h := newHarness(t, Options{Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialing)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer h.close()
	c := h.dial("/ws/rendezvous", adminHeader())
	<-dialing
	c.Close(1000)
}

// TestStopEndsBridges: stopping the registry ends every open bridge.
func TestStopEndsBridges(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{})
	c1 := h.dial("/ws/rendezvous", adminHeader())
	c2 := h.dial("/ws/relay?transport=message", adminHeader())
	waitFor(t, "two connections", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 2 })
	h.close()
	c1.Drop()
	c2.Drop()
}

// TestBadTextCountsDownOnce: a socket the library closes for text that
// is not UTF-8 frees its place once, so the address still holds at most
// five bridges.
func TestBadTextCountsDownOnce(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{})
	defer h.close()
	var clients []*wstest.Client
	for i := 0; i < MaxConnectionsPerIP; i++ {
		clients = append(clients, h.dial("/ws/rendezvous", adminHeader()))
	}
	_ = clients[0].WriteFrame(0x1, []byte{0xff, 0xfe})
	if !clients[0].Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("not closed")
	}
	if _, code, _ := clients[0].Closed(); code != 1007 {
		t.Fatalf("close %d", code)
	}
	count := func() int { h.svc.mu.Lock(); defer h.svc.mu.Unlock(); return h.svc.perIP.Count("127.0.0.1") }
	waitFor(t, "the place freed", func() bool { return count() == 4 })
	clients[0] = h.dial("/ws/rendezvous", adminHeader())
	// The handshake answers before the bridge counts the socket: without
	// this wait the sixth can be counted first and the fifth closed
	waitFor(t, "the fifth bridge counted", func() bool { return count() == MaxConnectionsPerIP })
	over := h.dial("/ws/rendezvous", adminHeader())
	if !over.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("sixth bridge not closed")
	}
	if _, code, reason := over.Closed(); code != 1008 || reason != "Too many connections" {
		t.Fatalf("sixth bridge: %d %q", code, reason)
	}
	for _, c := range clients {
		c.Close(1000)
	}
	over.Drop()
}

// TestRevokedLinkEndsItsBridgesAtOnce: InvalidateGuestGrant ends the
// bridges of the link without waiting for the next check, and leaves
// other bridges open.
func TestRevokedLinkEndsItsBridgesAtOnce(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	h := newHarness(t, Options{})
	defer h.close()
	a := h.dial("/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil)
	b := h.dial("/ws/relay?transport=message&guest=tok&peer_id=TARGET01&guest_session=s2", nil)
	user := h.dial("/ws/rendezvous", adminHeader())
	waitFor(t, "three connections", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 3 })
	if n := h.svc.InvalidateGuestGrant("g1"); n != 0 {
		t.Fatalf("returned %d", n)
	}
	for _, c := range []*wstest.Client{a, b} {
		if !c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
			t.Fatal("guest bridge still open")
		}
		if _, code, _ := c.Closed(); code != 1005 {
			t.Fatalf("close %d", code)
		}
	}
	if closed, _, _ := user.Closed(); closed {
		t.Fatal("a signed-in bridge was ended")
	}
	user.Close(1000)
}

// TestCookieToken pins the old handler's cookie reader.
func TestCookieToken(t *testing.T) {
	for _, c := range []struct{ header, name, want string }{
		{"bd.guest=abc", "bd.guest", "abc"},
		{" x=1 ;  bd.guest=%20abc%20 ", "bd.guest", "abc"},
		{"bd.guest=; betterdesk.guest=t2", "bd.guest", "t2"},
		{"bd.guest=%E0%A4%A; betterdesk.guest=t2", "bd.guest", ""},
		{"betterdesk.guest=t3; bd.guest=t4", "betterdesk.guest", "t3"},
		{"betterdesk.guest=; bd.guest=t4", "betterdesk.guest", "t4"},
		{"bd.guest=a; bd.guest=b", "bd.guest", "a"},
		{"bd.guestx=a", "bd.guest", ""},
		{"bd.guest=%25", "bd.guest", "%"},
		{"bd.guest=%C0%80", "bd.guest", ""},
		{"bd.guest=%C2080", "bd.guest", ""},
		{"bd.guest=%C2%80", "bd.guest", "\u0080"},
		{"bd.guest=%ED%A0%80", "bd.guest", ""},
		{"bd.guest=café%C3%A9", "bd.guest", "caféé"},
	} {
		if got := cookieToken(c.header, c.name); got != c.want {
			t.Errorf("%q (%s): want %q, got %q", c.header, c.name, c.want, got)
		}
	}
	if got := cookieToken(latin1("bd.guest=\xe9"), "bd.guest"); got != "é" {
		t.Errorf("latin1: got %q", got)
	}
}

// TestWithoutSessionsNobodyIsSignedIn: a registry without a session
// source refuses a would-be signed-in upgrade with 401, where the old
// service answered 503 without its session middleware: both refuse.
func TestWithoutSessionsNobodyIsSignedIn(t *testing.T) {
	check := wstest.Goroutines(t)
	defer check()
	svc := New(Options{GoAPI: goapi.New(&guestAPI{}), Interfaces: func() []InterfaceAddr { return nil }})
	reg := ws.New(ws.Options{Permission: oraclePermission})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	defer func() { reg.Close(); srv.Close() }()
	for _, path := range []string{"/ws/rendezvous", "/ws/relay"} {
		c, res, err := wstest.Dial(strings.TrimPrefix(srv.URL, "http://"), path, adminHeader())
		if err != nil || c != nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: %v %v", path, res, err)
		}
	}
}

// TestOnlyItsPaths: the service takes upgrades to its two paths and no
// other.
func TestOnlyItsPaths(t *testing.T) {
	reg := ws.New(ws.Options{})
	New(Options{Interfaces: func() []InterfaceAddr { return nil }}).Register(reg)
	if got := strings.Join(reg.Patterns(), " "); got != "/ws/rendezvous /ws/relay" {
		t.Fatalf("patterns %q", got)
	}
	for path, want := range map[string]bool{"/ws/rendezvous": true, "/ws/relay": true, "/ws/chat": false, "/ws/relay/x": false, "/WS/RELAY": false} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		if reg.Serves(r) != want {
			t.Errorf("%s: serves %v", path, !want)
		}
	}
}

// TestCookieHeader joins the Cookie headers as Node did and reads every
// byte as a character.
func TestCookieHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/ws/rendezvous", nil)
	r.Header["Cookie"] = []string{"a=1", "bd.guest=\xe9x"}
	if got := cookieHeader(r); got != "a=1; bd.guest=éx" {
		t.Fatalf("got %q", got)
	}
}

// TestJSDelay: setTimeout takes a delay over 2^31-1 ms as one millisecond.
func TestJSDelay(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{15 * time.Second, 15 * time.Second},
		{2147483647 * time.Millisecond, 2147483647 * time.Millisecond},
		{2147483648 * time.Millisecond, time.Millisecond},
		{0, time.Millisecond},
	} {
		if got := jsDelay(c.in); got != c.want {
			t.Errorf("%v: got %v", c.in, got)
		}
	}
}

// TestOneofOrderDoesNotLetAGuestThrough: the check reads a guest's frame
// as the signal server does (BUGS RM20). A frame with punch_hole_request
// for the link's device and then request_relay for another, which the
// server reads as the request_relay, and a punch_hole_request tagged as a
// varint, whose bytes the server reads as fields of the top level, end
// the bridge, and the server gets neither.
func TestOneofOrderDoesNotLetAGuestThrough(t *testing.T) {
	inner := append([]byte{0x0a, 0x08}, "TARGET01"...)
	inner = append(inner, rendezvousBytes(18, "OTHER01")...)
	for _, payload := range [][]byte{
		append(rendezvousBytes(8, "TARGET01"), rendezvousBytes(18, "OTHER01")...),
		append([]byte{0x40, byte(len(inner))}, inner...),
	} {
		var server pb.RendezvousMessage
		if err := proto.Unmarshal(payload, &server); err != nil {
			t.Fatal(err)
		}
		if server.GetPunchHoleRequest() != nil || server.GetRequestRelay().GetId() != "OTHER01" {
			t.Fatalf("%x: the signal server reads %v", payload, &server)
		}
		t.Run(hex.EncodeToString(payload), func(t *testing.T) {
			check := wstest.Goroutines(t)
			defer check()
			h := newHarness(t, Options{})
			defer h.close()
			c := h.dial("/ws/rendezvous?guest=tok&peer_id=TARGET01&guest_session=s1", nil)
			waitFor(t, "the signal server's connection", func() bool { h.sig.mu.Lock(); defer h.sig.mu.Unlock(); return len(h.sig.conns) == 1 })
			_ = c.Binary(framed(t, payload))
			if !c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
				t.Fatal("the guest's bridge stays open")
			}
			time.Sleep(100 * time.Millisecond)
			if got := h.sig.received(1); len(got) != 0 {
				t.Errorf("the signal server got %x", got)
			}
			c.Drop()
		})
	}
}
