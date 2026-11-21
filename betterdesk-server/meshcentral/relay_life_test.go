package meshcentral

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// relayWorld is a gateway whose relay handlers are counted while they run,
// with an agent "nodeA" connected as Mpeer0001.
type relayWorld struct {
	gw      *Gateway
	url     string
	running atomic.Int64
}

func newRelayWorld(t *testing.T, wait time.Duration) *relayWorld {
	t.Helper()
	gw, _ := newTestGateway(t)
	gw.relayWait = wait
	fakeAgent(t, gw, "Mpeer0001", "nodeA")
	w := &relayWorld{gw: gw}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.running.Add(1)
		defer w.running.Add(-1)
		gw.handleRelayWS(rw, r)
	}))
	t.Cleanup(srv.Close)
	w.url = "ws" + strings.TrimPrefix(srv.URL, "http") + "/meshrelay.ashx"
	return w
}

// tunnel opens a relay of the protocol to the agent and returns its id.
func (w *relayWorld) tunnel(t *testing.T, protocol int) string {
	t.Helper()
	id, _, err := w.gw.createRelayTunnel("Mpeer0001", "anna", "/", protocol, "test", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *relayWorld) cookie(t *testing.T, node string) string {
	t.Helper()
	c, err := w.gw.cookies.Encode(&RelayCookieData{RUserID: "anna", NodeID: node, ExpireMin: 240}, 240)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// open dials the relay with a query; it returns the socket, or nil and the
// status of the refusal.
func (w *relayWorld) open(t *testing.T, query string) (*websocket.Conn, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, w.url+"?"+query, nil)
	if err != nil {
		if resp != nil {
			return nil, resp.StatusCode
		}
		t.Fatalf("dial %s: %v", query, err)
	}
	t.Cleanup(func() { c.CloseNow() })
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	return c, http.StatusSwitchingProtocols
}

// handlers waits up to d for the number of running handlers to be n.
func (w *relayWorld) handlers(t *testing.T, n int64, d time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for w.running.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d relay handlers running, want %d", what, w.running.Load(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRelayNeedsACookieOfItsNode: a relay socket needs a valid auth or
// rauth cookie of the node the relay was opened to (BUGS RV162).
func TestRelayNeedsACookieOfItsNode(t *testing.T) {
	w := newRelayWorld(t, time.Minute)
	id := w.tunnel(t, relayProtocolTerminal)
	for what, query := range map[string]string{
		"no cookie":           "p=1&id=" + id,
		"a cookie of another": "p=1&id=" + id + "&auth=" + w.cookie(t, "nodeB"),
		"a cookie of no node": "p=1&id=" + id + "&rauth=" + w.cookie(t, ""),
		"an unknown relay":    "p=1&id=" + newRelayID() + "&auth=" + w.cookie(t, "nodeA"),
		"a broken cookie":     "p=1&id=" + id + "&auth=x" + w.cookie(t, "nodeA"),
	} {
		if c, status := w.open(t, query); c != nil || status != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", what, status)
		}
	}
	if c, status := w.open(t, "p=1&id="+id+"&rauth="+w.cookie(t, "nodeA")); c == nil {
		t.Fatalf("the agent's cookie: %d", status)
	}
	if c, status := w.open(t, "browser=1&p=1&id="+id+"&auth="+w.cookie(t, "nodeA")); c == nil {
		t.Fatalf("the browser's cookie: %d", status)
	}
}

// TestRelayHandlersEnd: a relay handler ends when its client goes away,
// when its partner does not come in time, and when the session ends, for
// a paired relay and a KVM hub alike (BUGS RV162).
func TestRelayHandlersEnd(t *testing.T) {
	const wait = 400 * time.Millisecond
	w := newRelayWorld(t, wait)
	agent := func(id, p string) string { return "p=" + p + "&id=" + id + "&rauth=" + w.cookie(t, "nodeA") }
	browser := func(id, p string) string { return "browser=1&p=" + p + "&id=" + id + "&auth=" + w.cookie(t, "nodeA") }

	// A waiting peer whose client goes away.
	c, _ := w.open(t, agent(w.tunnel(t, relayProtocolTerminal), "1"))
	w.handlers(t, 1, time.Second, "a waiting peer")
	c.Close(websocket.StatusNormalClosure, "")
	w.handlers(t, 0, wait/2+time.Second, "a waiting peer that closed")

	// A waiting peer whose partner never comes.
	w.open(t, agent(w.tunnel(t, relayProtocolTerminal), "1"))
	w.handlers(t, 1, time.Second, "a waiting peer")
	w.handlers(t, 0, wait+2*time.Second, "a waiting peer without a partner")

	// A paired session: one side leaves, both handlers end.
	id := w.tunnel(t, relayProtocolTerminal)
	a, _ := w.open(t, agent(id, "1"))
	w.open(t, browser(id, "1"))
	w.handlers(t, 2, time.Second, "a paired session")
	a.Close(websocket.StatusNormalClosure, "")
	w.handlers(t, 0, 3*time.Second, "a session one side of which left")

	// A KVM hub: a viewer leaves, then the agent.
	id = w.tunnel(t, relayProtocolKVM)
	a, _ = w.open(t, agent(id, "2"))
	v1, _ := w.open(t, browser(id, "2"))
	w.open(t, browser(id, "2"))
	w.handlers(t, 3, time.Second, "a hub")
	v1.Close(websocket.StatusNormalClosure, "")
	w.handlers(t, 2, 3*time.Second, "a hub a viewer left")
	a.Close(websocket.StatusNormalClosure, "")
	w.handlers(t, 0, 3*time.Second, "a hub whose agent left")

	// A KVM viewer alone.
	w.open(t, browser(w.tunnel(t, relayProtocolKVM), "2"))
	w.handlers(t, 1, time.Second, "a viewer alone")
	w.handlers(t, 0, wait+2*time.Second, "a viewer whose agent never came")
}

// TestRelayPipes: the messages of a paired session and of a KVM hub reach
// the other side after the connect signal, both ways.
func TestRelayPipes(t *testing.T) {
	w := newRelayWorld(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dial := func(query string) *websocket.Conn {
		c, _, err := websocket.Dial(ctx, w.url+"?"+query, nil)
		if err != nil {
			t.Fatalf("dial %s: %v", query, err)
		}
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	expect := func(c *websocket.Conn, want ...string) {
		t.Helper()
		for _, text := range want {
			_, data, err := c.Read(ctx)
			if err != nil || string(data) != text {
				t.Fatalf("got %q %v, want %q", data, err, text)
			}
		}
	}
	send := func(c *websocket.Conn, text string) {
		t.Helper()
		if err := c.Write(ctx, websocket.MessageText, []byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"1", "2"} {
		protocol := relayProtocolTerminal
		if p == "2" {
			protocol = relayProtocolKVM
		}
		id := w.tunnel(t, protocol)
		agent := dial("p=" + p + "&id=" + id + "&rauth=" + w.cookie(t, "nodeA"))
		send(agent, "early")
		viewer := dial("browser=1&p=" + p + "&id=" + id + "&auth=" + w.cookie(t, "nodeA"))
		signal := RelayConnectSignal(false)
		if p == "2" {
			expect(viewer, signal, signal)
		} else {
			expect(viewer, signal)
		}
		expect(agent, signal)
		expect(viewer, "early")
		send(agent, "from agent")
		expect(viewer, "from agent")
		send(viewer, "from viewer")
		expect(agent, "from viewer")
	}
}
