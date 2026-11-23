package devices_test

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// L83: GET /api/remote-targets/readiness passes on what the server says
// about the credential vault and guacd, for the target form's warnings;
// 502 when the server does not answer. It is not taken for the target
// with the id "readiness".
func TestRemoteTargetReadiness(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/remote-targets/readiness", 200, `{"guacd":false,"vault":true}`)
	w := routetest.New(t, routetest.Options{API: api, Env: raisedLimits, Config: nodeLanguages})
	got := w.As("operator").Do("GET", "/api/remote-targets/readiness", "")
	if want := `{"success":true,"data":{"guacd":false,"vault":true}}`; got.Status != 200 || got.Body != want {
		t.Errorf("readiness: %d %s, want 200 %s", got.Status, got.Body, want)
	}
	for _, call := range api.Calls() {
		if call != "GET /api/remote-targets/readiness" {
			t.Errorf("the route called %s", call)
		}
	}

	down := routetest.NewAPI()
	down.On("GET /api/remote-targets/readiness", 500, `{"error":"internal"}`)
	w = routetest.New(t, routetest.Options{API: down, Env: raisedLimits, Config: nodeLanguages})
	if got := w.As("operator").Do("GET", "/api/remote-targets/readiness", ""); got.Status != 502 {
		t.Errorf("readiness while the server fails: %d %s, want 502", got.Status, got.Body)
	}
}
