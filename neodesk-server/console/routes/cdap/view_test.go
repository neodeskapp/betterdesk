package cdap_test

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
)

// TestReadingNeedsViewIoTDevices: the gateway's state, its devices, one
// device with its manifest, state and linked devices, and the alerts need
// View CDAP devices (cdap.view), as on the API server. An account without
// it gets 403 before the API server is asked; one with it gets the API
// server's answer (BUGS L393).
func TestReadingNeedsViewIoTDevices(t *testing.T) {
	w := proxytest.NewWorld(t, withScope(t, map[int64][]string{6: {"dev-1"}}))
	ok := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"ok":true}`}
	paths := []string{
		"/api/cdap/status",
		"/api/cdap/devices",
		"/api/cdap/devices/dev-1",
		"/api/cdap/devices/dev-1/manifest",
		"/api/cdap/devices/dev-1/state",
		"/api/cdap/devices/dev-1/linked",
		"/api/cdap/alerts",
	}
	for _, path := range paths {
		rec, calls := w.Do(proxytest.Request{Method: "GET", Path: path, Session: account(7, "server_admin")}, ok)
		if rec.Code != http.StatusForbidden || len(calls) != 0 {
			t.Errorf("GET %s as server_admin (no cdap.view): %d %s, %d calls to the API server; want 403 and none", path, rec.Code, rec.Body, len(calls))
		}
		rec, calls = w.Do(proxytest.Request{Method: "GET", Path: path, Session: account(6, "viewer")}, ok)
		if rec.Code != http.StatusOK || len(calls) == 0 {
			t.Errorf("GET %s as viewer (cdap.view): %d %s, %d calls; want the API server's answer", path, rec.Code, rec.Body, len(calls))
		}
	}
}
