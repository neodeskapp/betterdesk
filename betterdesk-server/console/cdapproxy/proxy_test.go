package cdapproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

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
