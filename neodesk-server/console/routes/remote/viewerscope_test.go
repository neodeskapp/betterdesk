package remote

import (
	"net/http"
	"strings"
	"testing"
)

// The viewer's data of a device or remote target outside the account's
// device scope is "not found", as the viewer's socket refuses it, and
// nothing of the device is looked up at the API server. A guest link
// holder keeps the link's own checks (RM15).
func TestViewerDataFollowsTheScope(t *testing.T) {
	w, sc := scopedWorld(t)
	sc.API["GET /api/guest/access-links/validate"] = apiAnswer{Status: http.StatusOK, Type: "application/json",
		Body: `{"valid":true,"id":"g1","peer_ids":["dev-2"],"view_only":false,"expires_at":"2099-01-01T00:00:00Z"}`}
	scoped := as(21, "scoped", "operator")
	operator := as(5, "e2e_operator", "operator")
	for _, c := range []struct {
		who    *account
		path   string
		status int
	}{
		{scoped, "/api/remote/viewer/dev-1", 200},
		{scoped, "/api/remote/viewer/dev-2", 404},
		{scoped, "/api/remote/viewer/dev-9", 404},
		{scoped, "/api/remote/viewer/rt_inside01", 200},
		{scoped, "/api/remote/viewer/rt_outside1", 404},
		{operator, "/api/remote/viewer/dev-2", 200},
		{operator, "/api/remote/viewer/rt_outside1", 200},
		{operator, "/api/remote/viewer/dev-9", 200},
		{nil, "/api/remote/viewer/dev-2?guest=tok", 200},
	} {
		rec := w.do(&vector{Request: request{Method: "GET", Path: c.path, Session: c.who}, World: sc})
		who := "a guest"
		if c.who != nil {
			who = c.who.User.Username
		}
		if rec.Code != c.status {
			t.Errorf("%s as %s: %d %s, want %d", c.path, who, rec.Code, rec.Body.String(), c.status)
			continue
		}
		body := rec.Body.String()
		if c.status == 404 && (!strings.Contains(body, `"code":"not_found"`) || strings.Contains(body, "Back office") || strings.Contains(body, "root")) {
			t.Errorf("%s as %s: %s, want not_found and nothing of the device", c.path, who, body)
		}
	}
	w.do(&vector{Request: request{Method: "GET", Path: "/api/remote/viewer/dev-2", Session: scoped}, World: sc})
	w.mu.Lock()
	calls := w.calls
	w.mu.Unlock()
	if len(calls) != 0 {
		t.Errorf("the viewer's data of a device outside the scope asked the API server: %s", describeCalls(calls))
	}
}
