package remote

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/remoterelay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// scopedWorld is a world with two devices and two remote targets, both
// devices with a remote session, where the account "scoped" sees dev-1
// and rt_inside01 only.
func scopedWorld(t *testing.T) (*world, scene) {
	t.Helper()
	w := newWorld(t, storetest.Migrated(t), "1.0.0", false)
	w.scope = func(user *session.User, device *goapi.Object) bool {
		if user == nil || user.Username != "scoped" {
			return true
		}
		if device == nil {
			return false
		}
		id := device.Str("id")
		return id == "dev-1" || id == "rt_inside01"
	}
	answer := func(body string) apiAnswer {
		return apiAnswer{Status: http.StatusOK, Type: "application/json", Body: body}
	}
	sc := scene{
		Devices: map[string]json.RawMessage{
			"dev-1": json.RawMessage(`{"id":"dev-1","hostname":"Front desk","platform":"Windows","tags":[]}`),
			"dev-2": json.RawMessage(`{"id":"dev-2","hostname":"Back office","platform":"Linux","tags":[]}`),
		},
		API: map[string]apiAnswer{
			"GET /api/peers/dev-1":                answer(`{"id":"dev-1","device_type":"rustdesk"}`),
			"GET /api/peers/dev-2":                answer(`{"id":"dev-2","device_type":"rustdesk"}`),
			"GET /api/health/detail":              answer(`{"status":"ok"}`),
			"GET /api/remote-targets/rt_inside01": answer(`{"id":"rt_inside01","name":"Inside","protocol":"RDP","username":"adm"}`),
			"GET /api/remote-targets/rt_outside1": answer(`{"id":"rt_outside1","name":"Outside","protocol":"RDP","username":"root","cert_fingerprint":"ab:cd"}`),
		},
		Relay: &relayScene{
			Sessions: []string{"dev-1", "dev-2"},
			States: map[string]*remoterelay.SessionState{
				"dev-1": {AgentConnected: true, ViewerCount: 1, Streaming: true},
				"dev-2": {AgentConnected: true, ViewerCount: 2, Streaming: true},
			},
		},
	}
	return w, sc
}

// as is a signed-in account of the scoped world.
func as(id int64, username, role string) *account {
	a := &account{UserID: id}
	a.User.ID, a.User.Username, a.User.Role = id, username, role
	return a
}

// The list of remote sessions and the state of one need device.connect,
// and an account with a device scope sees only the sessions of its
// devices: the others are left out of the list and are "not found"
// (RM14).
func TestSessionsFollowPermissionAndScope(t *testing.T) {
	w, sc := scopedWorld(t)
	const denied = `{"success":false,"error":"Permission denied: device.connect"}`
	for _, c := range []struct {
		who    *account
		path   string
		status int
		body   string
	}{
		{as(3, "e2e_viewer", "viewer"), "/api/remote/sessions", 403, denied},
		{as(6, "e2e_server_admin", "server_admin"), "/api/remote/sessions", 403, denied},
		{as(8, "e2e_guest_issuer", "guest_issuer"), "/api/remote/sessions", 403, denied},
		{as(3, "e2e_viewer", "viewer"), "/api/remote/session/dev-1", 403, denied},
		{as(6, "e2e_server_admin", "server_admin"), "/api/remote/session/dev-1", 403, denied},
		{as(1, "e2e_admin", "admin"), "/api/remote/sessions", 200, `{"sessions":["dev-1","dev-2"]}`},
		{as(5, "e2e_operator", "operator"), "/api/remote/sessions", 200, `{"sessions":["dev-1","dev-2"]}`},
		{as(21, "scoped", "operator"), "/api/remote/sessions", 200, `{"sessions":["dev-1"]}`},
		{as(21, "scoped", "operator"), "/api/remote/session/dev-1", 200, `{"agentConnected":true,"viewerCount":1,"streaming":true}`},
		{as(21, "scoped", "operator"), "/api/remote/session/dev-2", 404, `{"error":"Session not found"}`},
		{as(1, "e2e_admin", "admin"), "/api/remote/session/dev-2", 200, `{"agentConnected":true,"viewerCount":2,"streaming":true}`},
	} {
		rec := w.do(&vector{Request: request{Method: "GET", Path: c.path, Session: c.who}, World: sc})
		if rec.Code != c.status || strings.TrimSpace(rec.Body.String()) != c.body {
			t.Errorf("%s as %s: %d %s, want %d %s", c.path, c.who.User.Username, rec.Code, rec.Body.String(), c.status, c.body)
		}
	}
	// A device outside the scope is not asked for its state
	w.do(&vector{Request: request{Method: "GET", Path: "/api/remote/session/dev-2", Session: as(21, "scoped", "operator")}, World: sc})
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, call := range w.relayCalls {
		if call.Call == "state" {
			t.Errorf("the relay was asked for the state of %s, which the account may not see", call.ID)
		}
	}
}
