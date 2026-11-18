package cdapproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

// TestBadNameAfterTheHandshakeChecks: a name that cannot go into a
// header drops the connection only for a handshake the library would
// take; a bad handshake gets the library's answer first, as in Node,
// where the throw came after the handshake checks.
func TestBadNameAfterTheHandshakeChecks(t *testing.T) {
	check := wstest.Goroutines(t)
	reg := ws.New(ws.Options{
		Session:    wstest.OracleSession,
		Permission: func(context.Context, string, string) (bool, error) { return true, nil },
	})
	New(Options{API: http.NotFoundHandler()}).Register(reg)
	srv := httptest.NewServer(reg)
	addr := strings.TrimPrefix(srv.URL, "http://")
	request := func(key string) string {
		return "GET /api/cdap/devices/x/terminal HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n" +
			`X-Oracle-Session: {"userId":7,"user":{"username":"a\u0001b","role":"admin"}}` + "\r\n\r\n"
	}
	got, err := wstest.Raw(addr, request("short"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "HTTP/1.1 400 Bad Request") || !strings.HasSuffix(got, "Missing or invalid Sec-WebSocket-Key header") {
		t.Errorf("bad key: %q", got)
	}
	got, err = wstest.Raw(addr, request("dGhlIHNhbXBsZSBub25jZQ=="))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("good key: %q, want the connection to end without an answer", got)
	}
	srv.Close()
	reg.Close()
	check()
}
