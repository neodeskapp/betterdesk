package cdap_test

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
)

// A call the API server fails is answered with its status and its body,
// and 500 {"error": message} when it gave no answer (BUGS RC7): the web
// app reads a 404 of the manifest and the state as "none".
func TestFailuresKeepTheAPIStatus(t *testing.T) {
	w := proxytest.NewWorld(t, withScope(t, nil))
	admin := account(1, "admin")
	command := `{"widget_id":"power","action":"set"}`
	link := `{"linked_peer_id":"dev-9"}`
	routes := []proxytest.Request{
		{Method: "GET", Path: "/api/cdap/status"},
		{Method: "GET", Path: "/api/cdap/devices"},
		{Method: "GET", Path: "/api/cdap/devices/gw-9"},
		{Method: "GET", Path: "/api/cdap/devices/gw-9/manifest"},
		{Method: "GET", Path: "/api/cdap/devices/gw-9/state"},
		{Method: "POST", Path: "/api/cdap/devices/gw-9/command", Type: "application/json", Body: &command},
		{Method: "GET", Path: "/api/cdap/alerts?device_id=gw-9"},
		{Method: "GET", Path: "/api/cdap/devices/gw-9/linked"},
		{Method: "POST", Path: "/api/cdap/devices/gw-9/link", Type: "application/json", Body: &link},
	}
	for _, c := range []struct {
		api    *proxytest.Answer
		status int
		body   string
	}{
		{&proxytest.Answer{Status: 404, Type: "application/json", Body: `{"error":"device not found"}`}, 404, `{"error":"device not found"}`},
		{&proxytest.Answer{Status: 400, Type: "application/json", Body: `{"error":"bad request","details":["a"]}`}, 400, `{"error":"bad request","details":["a"]}`},
		{&proxytest.Answer{Status: 503}, 503, `{"error":"Request failed with status code 503"}`},
		{&proxytest.Answer{Hang: true}, 500, `{"error":"socket hang up"}`},
	} {
		for _, req := range routes {
			req.Session = admin
			rec, calls := w.Do(req, c.api)
			if rec.Code != c.status || rec.Body.String() != c.body || len(calls) != 1 {
				t.Errorf("%s %s with the API server's %d: %d %s, %d calls; want %d %s", req.Method, req.Path, c.api.Status, rec.Code, rec.Body, len(calls), c.status, c.body)
			}
		}
	}
	// A working call is answered as before
	rec, _ := w.Do(proxytest.Request{Method: "GET", Path: "/api/cdap/devices/gw-9/manifest", Session: admin},
		&proxytest.Answer{Status: 200, Type: "application/json", Body: `{"widgets":[]}`})
	if rec.Code != http.StatusOK || rec.Body.String() != `{"widgets":[]}` {
		t.Errorf("manifest: %d %s", rec.Code, rec.Body)
	}
}
