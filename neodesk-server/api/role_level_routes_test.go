package api

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestRoleLevelRoutesAskAPermission: wake-on-LAN, telemetry commands,
// session grants, CDAP video and audio, mesh share links and the
// management channel ask device.connect, the access policy read asks
// device.view, and pairing codes and the enrollment lists ask
// enrollment.approve, so the role map and its overrides decide them
// instead of the role level (RV170, A99).
func TestRoleLevelRoutesAskAPermission(t *testing.T) {
	type route struct{ method, path, body string }
	groups := []struct {
		perm   string
		routes []route
	}{
		{auth.PermDeviceConnect, []route{
			{http.MethodPost, "/api/peers/PEERAAA1/wol", `{}`},
			{http.MethodPost, "/api/peers/PEERAAA1/telemetry/command", `{"command":"files.browse"}`},
			{http.MethodPost, "/api/peers/PEERAAA1/session-grant", `{}`},
			{http.MethodGet, "/api/cdap/devices/PEERAAA1/video", ""},
			{http.MethodGet, "/api/cdap/devices/PEERAAA1/audio", ""},
			{http.MethodPost, "/api/mesh/devices/PEERAAA1/share", `{}`},
			{http.MethodPost, "/api/nd/mgmt/PEERAAA1/send", `{"type":"ping"}`},
		}},
		{auth.PermDeviceView, []route{
			{http.MethodGet, "/api/peers/PEERAAA1/access-policy", ""},
		}},
		{auth.PermEnrollmentApprove, []route{
			{http.MethodGet, "/api/enrollment/pending", ""},
			{http.MethodGet, "/api/enrollment/history", ""},
			{http.MethodGet, "/api/pairing/codes", ""},
			{http.MethodPost, "/api/pairing/codes", `{}`},
			{http.MethodDelete, "/api/pairing/codes/999999", ""},
		}},
	}
	const refusal = `{"error":"Insufficient permissions"}`

	for _, g := range groups {
		t.Run(g.perm, func(t *testing.T) {
			srv, database := newInternalServer(t, nil)
			call := func(key string, rt route) (int, string) {
				return listenerCall(t, srv, rt.method, rt.path, rt.body, map[string]string{"X-API-Key": key})
			}

			// A viewer that holds the permission (by default or by an
			// override) gets past the guard although its level is below
			// operator.
			if err := database.SetRolePermission(auth.RoleViewer, g.perm, true); err != nil {
				t.Fatal(err)
			}
			for _, rt := range g.routes {
				if status, body := call(testViewerKey, rt); status == http.StatusForbidden && body == refusal {
					t.Errorf("%s %s as a viewer granted %s: refused, want past the guard", rt.method, rt.path, g.perm)
				}
			}

			// Taken away by an override, it is refused to the viewer and the
			// operator alike.
			for _, role := range []string{auth.RoleViewer, auth.RoleOperator} {
				if err := database.SetRolePermission(role, g.perm, false); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{testViewerKey, testOperatorKey} {
				for _, rt := range g.routes {
					if status, body := call(key, rt); status != http.StatusForbidden || body != refusal {
						t.Errorf("%s %s with %s and %s taken away: %d %s, want 403", rt.method, rt.path, key[:8], g.perm, status, body)
					}
				}
			}

			// The console's bridge calls as admin and keeps every route.
			for _, rt := range g.routes {
				if status, body := call(testAPIKey, rt); status == http.StatusForbidden && body == refusal {
					t.Errorf("%s %s as admin: refused, want past the guard", rt.method, rt.path)
				}
			}
		})
	}
}

// TestServerAdminKeepsOffDeviceConnectRoutes: server_admin holds neither
// device.connect nor enrollment.approve, so its role level no longer lets
// it wake devices, queue commands or mint pairing codes (RV170).
func TestServerAdminKeepsOffDeviceConnectRoutes(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	const serverAdminKey = "server-admin-api-key-12345"
	addTestAPIKey(t, database, serverAdminKey, "server-admin-key", auth.RoleServerAdmin)
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/peers/PEERAAA1/wol", `{}`},
		{http.MethodPost, "/api/peers/PEERAAA1/telemetry/command", `{"command":"process.terminate"}`},
		{http.MethodPost, "/api/pairing/codes", `{}`},
		{http.MethodGet, "/api/enrollment/pending", ""},
	} {
		status, body := listenerCall(t, srv, rt.method, rt.path, rt.body, map[string]string{"X-API-Key": serverAdminKey})
		if status != http.StatusForbidden || body != `{"error":"Insufficient permissions"}` {
			t.Errorf("%s %s as server_admin: %d %s, want 403", rt.method, rt.path, status, body)
		}
	}
}
