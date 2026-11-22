package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestRoutesAskAPermission: the CDAP reads ask cdap.view, the strategies
// and the management channel reads device.view, the statistics, detailed
// health and metrics metrics.view, the LDAP password check answers only the
// console, and an organization's invitations only its owners and admins
// (RV182).
func TestRoutesAskAPermission(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	const proKey, issuerKey, orgAdminKey = "pro-role-api-key-12345", "guest-issuer-api-key-123", "org-admin-api-key-12345"
	addTestAPIKey(t, database, proKey, "pro-key", auth.RolePro)
	addTestAPIKey(t, database, issuerKey, "issuer-key", auth.RoleGuestIssuer)
	addTestAPIKey(t, database, orgAdminKey, "org-admin-key", auth.RoleOperator)
	if err := database.CreateOrganization(&db.Organization{ID: "org-a", Name: "Acme", Slug: "acme", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*db.OrgUser{
		{ID: "ou-1", OrgID: "org-a", Username: "apikey:operator-key", Role: db.OrgRoleUser, PasswordHash: "x", CreatedAt: time.Now().UTC()},
		{ID: "ou-2", OrgID: "org-a", Username: "apikey:org-admin-key", Role: db.OrgRoleAdmin, PasswordHash: "x", CreatedAt: time.Now().UTC()},
	} {
		if err := database.CreateOrgUser(u); err != nil {
			t.Fatal(err)
		}
	}

	refused := map[string][]string{
		issuerKey: {"/api/cdap/status", "/api/cdap/devices", "/api/cdap/alerts", "/api/cdap/devices/PEERAAA1",
			"/api/server/stats", "/api/health/detail", "/metrics"},
		proKey: {"/api/cdap/status", "/api/strategies", "/api/strategies/abc", "/api/bd/mgmt/connected",
			"/api/bd/mgmt/BDM00001/status", "/api/server/stats", "/metrics"},
		testOperatorKey: {"/api/org/org-a/invitations"},
	}
	for key, paths := range refused {
		for _, path := range paths {
			if status, body := listenerCall(t, srv, http.MethodGet, path, "", map[string]string{"X-API-Key": key}); status != http.StatusForbidden {
				t.Errorf("GET %s with %s: %d %s, want 403", path, key[:8], status, body)
			}
		}
	}
	allowed := map[string][]string{
		testViewerKey: {"/api/cdap/status", "/api/server/stats", "/api/health/detail", "/metrics", "/api/strategies"},
		orgAdminKey:   {"/api/org/org-a/invitations"},
	}
	for key, paths := range allowed {
		for _, path := range paths {
			if status, body := listenerCall(t, srv, http.MethodGet, path, "", map[string]string{"X-API-Key": key}); status != http.StatusOK {
				t.Errorf("GET %s with %s: %d %s, want 200", path, key[:8], status, body)
			}
		}
	}

	verify := `{"username":"alice","password":"secret"}`
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/auth/ldap/verify", verify, map[string]string{"X-API-Key": testAPIKey}); status != http.StatusForbidden {
		t.Errorf("LDAP verify over the listener with an admin key: %d %s, want 403", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPost, "/api/auth/ldap/verify", verify); status != http.StatusServiceUnavailable {
		t.Errorf("LDAP verify in process: %d %s, want 503 (LDAP is off)", status, body)
	}
}
