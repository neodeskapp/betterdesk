package cdapproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/neodeskapp/neodesk/neodesk-server/api"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// TestTheSocketActsForTheAccount: the socket to the API server is opened
// for the signed-in account: through the API client, the API server sees
// the account's name as the actor of the call (BUGS G7), and the caller is
// still the client's identity.
func TestTheSocketActsForTheAccount(t *testing.T) {
	standIn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := api.IdentityFromContext(r.Context())
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{r.Header.Get("Sec-WebSocket-Protocol")}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		_ = c.Write(r.Context(), websocket.MessageBinary, []byte(id.Actor+" as "+id.Username))
		_, _, _ = c.Read(r.Context())
	})
	addr := proxyAt(t, Options{Devices: knownDevices{}, CanAccess: everyDevice, API: goapi.New(standIn).Handler()})
	for i, name := range []string{"anna", "Zh中"} {
		c, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(int64(20+i), name))
		if c == nil {
			t.Fatalf("%q: %d, want an open socket", name, status)
		}
		want := name + " as " + goapi.BridgeUsername
		if !c.Wait(5*time.Second, func(frames []wstest.Frame, _ bool) bool { return len(frames) == 1 }) || c.Frames()[0].Text() != want {
			t.Errorf("%q: the API server saw %v, want %q", name, c.Frames(), want)
		}
	}
}

// TestOnlyDeviceIDsAreServed: a device id is 1 to 64 letters, digits, _
// and -; any other path is not one of the sockets and goes on as before.
func TestOnlyDeviceIDsAreServed(t *testing.T) {
	reg := ws.New(ws.Options{})
	New(Options{}).Register(reg)
	for path, want := range map[string]bool{
		"/api/cdap/devices/" + strings.Repeat("a", 64) + "/terminal": true,
		"/api/cdap/devices/" + strings.Repeat("a", 65) + "/terminal": false,
		"/api/cdap/devices/A_b-9/files":                              true,
		"/api/cdap/devices/a.b/files":                                false,
		"/api/cdap/devices/a%20b/audio":                              false,
		"/api/cdap/devices/x/video":                                  true,
		"/api/cdap/devices/x/desktop":                                true,
		"/api/cdap/devices/x/screen":                                 false,
		"/api/cdap/devices//terminal":                                false,
		"/api/cdap/devices/x/terminal/":                              false,
		"/api/cdap/devices/x/Terminal":                               false,
	} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		if got := reg.Serves(r); got != want {
			t.Errorf("%s: served %v, want %v", path, got, want)
		}
	}
}

// TestAnyNameOpensTheSocket: the account's name and role do not go to the
// API server, which never read them, so a name with a control character
// or a character outside Latin-1 opens the socket like any other.
func TestAnyNameOpensTheSocket(t *testing.T) {
	var mu sync.Mutex
	var seen []http.Header
	echo := echoAPI()
	addr := proxyAt(t, Options{
		Devices:   knownDevices{},
		CanAccess: everyDevice,
		API: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Clone())
			mu.Unlock()
			echo.ServeHTTP(w, r)
		}),
	})
	for i, name := range []string{"a\u0001b", "Zh中", "anna"} {
		c, status := open(t, addr, "/api/cdap/devices/dev1/terminal", as(int64(10+i), name))
		if c == nil {
			t.Errorf("%q: %d, want an open socket", name, status)
			continue
		}
		if err := c.Binary([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		if !c.Wait(5*time.Second, func(frames []wstest.Frame, _ bool) bool { return len(frames) == 1 }) {
			t.Errorf("%q: the API server did not answer: %v", name, c.Frames())
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("the API server got %d sockets, want 3", len(seen))
	}
	for _, h := range seen {
		if h.Get("X-Username") != "" || h.Get("X-Role") != "" {
			t.Errorf("the API server got X-Username %q and X-Role %q", h.Get("X-Username"), h.Get("X-Role"))
		}
	}
}
