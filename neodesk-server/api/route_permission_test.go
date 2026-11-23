package api

import (
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestRoutesAskTheirNeighboursPermission: enrollment decisions ask
// enrollment.approve, the exec routes the mesh terminal's permission and the
// CDAP command cdap.command, so the role map and its overrides decide them
// instead of the role level (RV170).
func TestRoutesAskTheirNeighboursPermission(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	const serverAdminKey = "server-admin-api-key-12345"
	addTestAPIKey(t, database, serverAdminKey, "server-admin-key", auth.RoleServerAdmin)

	refused := []struct{ method, path, body string }{
		{http.MethodPost, "/api/enrollment/approve/PEERBBB2", `{}`},
		{http.MethodPost, "/api/enrollment/reject/PEERBBB2", `{"ban":true}`},
		{http.MethodPost, "/api/enrollment/clear-rejection/PEERBBB2", ""},
		{http.MethodPost, "/api/peers/PEERAAA1/exec", `{"command":"whoami"}`},
		{http.MethodPost, "/api/mesh/devices/PEERAAA1/exec", `{"command":"whoami"}`},
	}
	for _, key := range []string{testOperatorKey, serverAdminKey} {
		for _, rt := range refused {
			status, body := listenerCall(t, srv, rt.method, rt.path, rt.body, map[string]string{"X-API-Key": key})
			if status != http.StatusForbidden || body != `{"error":"Insufficient permissions"}` {
				t.Errorf("%s %s with %s: %d %s, want 403", rt.method, rt.path, key[:8], status, body)
			}
		}
	}

	// The role map decides, overrides included.
	for _, o := range []db.RolePermission{
		{Role: auth.RoleOperator, Permission: auth.PermEnrollmentApprove, Granted: true},
		{Role: auth.RoleOperator, Permission: auth.PermCDAPCommand, Granted: false},
	} {
		if err := database.SetRolePermission(o.Role, o.Permission, o.Granted); err != nil {
			t.Fatal(err)
		}
	}
	key := map[string]string{"X-API-Key": testOperatorKey}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/enrollment/reject/NOSUCH99", `{}`, key); status != http.StatusNotFound {
		t.Errorf("reject with enrollment.approve granted: %d %s, want past the permission (404)", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/cdap/devices/PEERBBB2/command", `{"widget_id":"w","action":"set"}`, key); status != http.StatusForbidden {
		t.Errorf("CDAP command with cdap.command taken away: %d %s, want 403", status, body)
	}
}
