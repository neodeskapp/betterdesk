package console

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// remoteAPI is a made-up API server for the remote sockets: guest link g1
// is good for device 900100001 until long after the test, remote target
// rt_test-0001 exists and its tunnel echoes. It notes the identity the
// tunnel's request carried.
type remoteAPI struct {
	mu       sync.Mutex
	identity string
}

func (a *remoteAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/guest/access-links/validate":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"id":"g1","peer_ids":["900100001"],"expires_at":"2999-01-01T00:00:00Z"}`))
	case r.URL.Path == "/api/remote-targets/rt_test-0001":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"rt_test-0001","name":"Test","protocol":"rdp","enabled":true}`))
	case r.URL.Path == "/api/remote-targets/rt_test-0001/tunnel":
		id, _ := api.IdentityFromContext(r.Context())
		a.mu.Lock()
		a.identity = id.Username + "/" + id.Role
		a.mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			_ = c.Write(r.Context(), typ, append([]byte("tunnel:"), data...))
		}
	default:
		http.NotFound(w, r)
	}
}

// cookieHeader is the agent's cookies as one Cookie header.
func (a *agent) cookieHeader() string {
	var pairs []string
	for name, value := range a.cookies {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, "; ")
}

// TestGuestLinkTakenBackEndsTheWebClientsBridge: the web client's
// rendezvous socket of a guest link ends as soon as the link is taken
// back (guestaccess.Revoke, what DELETE /api/guest/access-links/:id
// calls), not at the next check with the API server 15 seconds later.
func TestGuestLinkTakenBackEndsTheWebClientsBridge(t *testing.T) {
	signal, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer signal.Close()
	dialed := make(chan net.Conn, 1)
	go func() {
		conn, err := signal.Accept()
		if err == nil {
			dialed <- conn
		}
	}()
	w := newWorld(t, setup{api: &remoteAPI{}, configure: func(c *config.Config) {
		c.SignalHost = "127.0.0.1"
		c.SignalPort = signal.Addr().(*net.TCPAddr).Port
	}})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	c, res, err := wstest.Dial(addr, "/ws/rendezvous?guest=tok-1&peer_id=900100001&guest_session=s1", nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v %v", err, res)
	}
	defer c.Drop()
	select {
	case conn := <-dialed:
		defer conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("the signal server was not dialed")
	}
	// The count is what invalidateGuestGrant returned: the bridges left
	// after each one ended, so 0
	w.s.Deps().GuestAccess.Revoke("g1")
	if !c.Wait(2*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the bridge stays open after its link was taken back")
	}
}

// TestRemoteRoutesSeeTheSocketsSessions: the token the device API gives
// out opens the agent socket, and the remote routes list the session the
// socket made: routes and sockets share one relay.
func TestRemoteRoutesSeeTheSocketsSessions(t *testing.T) {
	w := newWorld(t, setup{})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	token, _ := w.s.Deps().RemoteRelay.IssueRemoteAgentToken("dev-remote-1")
	c, res, err := wstest.Dial(addr, "/ws/remote-agent/dev-remote-1?token="+token, nil)
	if err != nil || c == nil {
		t.Fatalf("dial: %v %v", err, res)
	}
	defer c.Drop()
	admin := w.agent().signIn("admin")
	got := admin.get("/api/remote/sessions", "Accept", "application/json")
	if got.status != http.StatusOK || strings.TrimSpace(got.body) != `{"sessions":["dev-remote-1"]}` {
		t.Errorf("sessions: %d %s", got.status, got.body)
	}
	state := admin.get("/api/remote/session/dev-remote-1", "Accept", "application/json")
	var s map[string]any
	if err := json.Unmarshal([]byte(state.body), &s); err != nil || s["agentConnected"] != true {
		t.Errorf("session state: %d %s", state.status, state.body)
	}
}

// TestRemoteTargetTunnelIsTheAPIServersInProcess: the remote target
// socket opens the target's tunnel on the API server's handler in this
// process, as the console's identity, and passes messages both ways.
func TestRemoteTargetTunnelIsTheAPIServersInProcess(t *testing.T) {
	fake := &remoteAPI{}
	w := newWorld(t, setup{api: fake})
	addr := strings.TrimPrefix(w.front.URL, "http://")
	admin := w.agent().signIn("admin")
	header := http.Header{"Cookie": {admin.cookieHeader()}, "Origin": {"http://" + addr}}
	c, res, err := wstest.Dial(addr, "/ws/remote-target/rt_test-0001", header)
	if err != nil || c == nil {
		t.Fatalf("dial: %v %v", err, res)
	}
	_ = c.Text("hello")
	if !c.Wait(5*time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) || c.Frames()[0].Text() != "tunnel:hello" {
		t.Errorf("through the tunnel: %+v", c.Frames())
	}
	fake.mu.Lock()
	identity := fake.identity
	fake.mu.Unlock()
	if identity == "" || identity == "/" {
		t.Errorf("the tunnel's request carried no identity: %q", identity)
	}
	c.Close(1000)
}
