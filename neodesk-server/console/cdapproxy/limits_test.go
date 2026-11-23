package cdapproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// knownDevices are the devices of the tests: any id but "unknown".
type knownDevices struct{}

func (knownDevices) GetDeviceByID(_ context.Context, id string, _ bool) *goapi.Object {
	if id == "unknown" {
		return nil
	}
	return goapi.NewObject("id", id)
}

// everyDevice is a scope that has every device.
func everyDevice(context.Context, *session.User, *goapi.Object) (bool, error) { return true, nil }

// echoAPI is an API server whose device sockets send back what they get.
func echoAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{r.Header.Get("Sec-WebSocket-Protocol")}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if err := c.Write(r.Context(), typ, data); err != nil {
				return
			}
		}
	})
}

// proxyAt runs the proxy with opts on a test server where every role has
// every permission; it returns the server's address.
func proxyAt(t *testing.T, opts Options) string {
	t.Helper()
	check := wstest.Goroutines(t)
	if opts.API == nil {
		opts.API = echoAPI()
	}
	reg := ws.New(ws.Options{
		Session:    wstest.OracleSession,
		Permission: func(context.Context, string, string) (bool, error) { return true, nil },
	})
	New(opts).Register(reg)
	srv := httptest.NewServer(reg)
	t.Cleanup(func() {
		srv.Close()
		reg.Close()
		check()
	})
	return strings.TrimPrefix(srv.URL, "http://")
}

// as is the session header of an account.
func as(id int64, name string) http.Header {
	s, _ := json.Marshal(map[string]any{"userId": id, "user": map[string]any{"id": id, "username": name, "role": "admin"}})
	return http.Header{"X-Oracle-Session": {string(s)}, "Sec-WebSocket-Protocol": {"cdap-terminal"}}
}

// open opens a device socket and returns it, or nil and the status of the
// refusal.
func open(t *testing.T, addr, path string, header http.Header) (*wstest.Client, int) {
	t.Helper()
	c, res, err := wstest.Dial(addr, path, header)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		return nil, res.StatusCode
	}
	t.Cleanup(c.Drop)
	return c, res.StatusCode
}

// TestOutOfScopeDeviceIsRefused: an account opens the sockets of the
// devices in its scope only, as /ws/remote-viewer: 404 for a device it
// does not see and for one nobody knows, 403 when the check fails.
func TestOutOfScopeDeviceIsRefused(t *testing.T) {
	addr := proxyAt(t, Options{
		Devices: knownDevices{},
		CanAccess: func(_ context.Context, user *session.User, device *goapi.Object) (bool, error) {
			switch user.Username {
			case "broken":
				return false, errors.New("scope down")
			case "admin":
				// An account without a limit passes any device, also none
				return true, nil
			}
			return user.Username == "anna" && device.Str("id") == "dev1", nil
		},
	})
	for _, ch := range Channels {
		cases := []struct {
			device, who string
			want        int
		}{
			{"dev1", "anna", http.StatusSwitchingProtocols},
			{"dev2", "anna", http.StatusNotFound},
			{"unknown", "anna", http.StatusNotFound},
			{"dev2", "admin", http.StatusSwitchingProtocols},
			{"unknown", "admin", http.StatusNotFound},
			{"dev1", "ben", http.StatusNotFound},
			{"dev1", "broken", http.StatusForbidden},
		}
		for _, c := range cases {
			header := as(5, c.who)
			header.Set("Sec-WebSocket-Protocol", ch.Subprotocol)
			if _, status := open(t, addr, "/api/cdap/devices/"+c.device+"/"+ch.Name, header); status != c.want {
				t.Errorf("%s of %s as %s: %d, want %d", ch.Name, c.device, c.who, status, c.want)
			}
		}
	}
}

// TestSocketsPerAccount: an account has at most ten sockets of a channel
// open; the eleventh is refused with 429 until one of them ends. Other
// channels and other accounts count on their own.
func TestSocketsPerAccount(t *testing.T) {
	addr := proxyAt(t, Options{Devices: knownDevices{}, CanAccess: everyDevice})
	var first *wstest.Client
	for i := 0; i < MaxSocketsPerAccount; i++ {
		c, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(5, "anna"))
		if status != http.StatusSwitchingProtocols {
			t.Fatalf("socket %d: %d", i+1, status)
		}
		if first == nil {
			first = c
		}
	}
	if _, status := open(t, addr, "/api/cdap/devices/dev2/terminal", as(5, "anna")); status != http.StatusTooManyRequests {
		t.Errorf("the eleventh terminal: %d, want 429", status)
	}
	desktop := as(5, "anna")
	desktop.Set("Sec-WebSocket-Protocol", "cdap-desktop")
	if _, status := open(t, addr, "/api/cdap/devices/dev1/desktop", desktop); status != http.StatusSwitchingProtocols {
		t.Errorf("a desktop of the same account: %d", status)
	}
	if _, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(6, "ben")); status != http.StatusSwitchingProtocols {
		t.Errorf("a terminal of another account: %d", status)
	}
	first.Close(1000)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(5, "anna"))
		if status == http.StatusSwitchingProtocols {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after one socket ended: %d", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestSilentSocketIsClosed: a socket whose client answers no ping is
// closed after two ping intervals; one whose client answers stays open
// although it sends nothing.
func TestSilentSocketIsClosed(t *testing.T) {
	// Long enough that a client slowed down by the race detector on a busy
	// machine still answers a ping within one interval
	const interval = 100 * time.Millisecond
	addr := proxyAt(t, Options{Devices: knownDevices{}, CanAccess: everyDevice, PingInterval: interval})

	quiet, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(5, "anna"))
	if quiet == nil {
		t.Fatalf("the quiet socket: %d", status)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	session, _ := json.Marshal(map[string]any{"userId": 6, "user": map[string]any{"id": 6, "username": "ben", "role": "admin"}})
	_, err = io.WriteString(conn, "GET /api/cdap/devices/dev1/terminal HTTP/1.1\r\nHost: "+addr+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: cdap-terminal\r\n"+
		"X-Oracle-Session: "+string(session)+"\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = conn.SetReadDeadline(start.Add(5 * time.Second))
	got, _ := io.ReadAll(conn)
	if !strings.HasPrefix(string(got), "HTTP/1.1 101") {
		t.Fatalf("the silent socket: %q", got)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("the silent socket was closed after %v, want about %v", took, 2*interval)
	}

	time.Sleep(20 * interval)
	if closed, _, _ := quiet.Closed(); closed {
		t.Fatal("the quiet socket was closed")
	}
	if quiet.Pings() < 5 {
		t.Errorf("the quiet socket got %d pings in %v", quiet.Pings(), 20*interval)
	}
	if err := quiet.Binary([]byte("still here")); err != nil {
		t.Fatal(err)
	}
	if !quiet.Wait(5*time.Second, func(frames []wstest.Frame, _ bool) bool { return len(frames) == 1 }) {
		t.Errorf("the quiet socket got %v", quiet.Frames())
	}
}
