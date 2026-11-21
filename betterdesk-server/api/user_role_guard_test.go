package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

const testGlobalAdminKey = "globaladm-api-key-12345"

// TestUserRoutesCheckTheAccountsRole: the user routes change or delete an
// account only when the caller could give it the role it has now; a
// global_admin can no longer take over or remove a super admin, and still
// manages an operator (RV174).
func TestUserRoutesCheckTheAccountsRole(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	addTestAPIKey(t, database, testGlobalAdminKey, "globaladm-key", auth.RoleGlobalAdmin)
	key := map[string]string{"X-API-Key": testGlobalAdminKey}
	accounts := map[string]*db.User{}
	for _, u := range []*db.User{
		{Username: "root1", PasswordHash: "old-hash-1", Role: auth.RoleSuperAdmin},
		{Username: "root2", PasswordHash: "old-hash-2", Role: auth.RoleSuperAdmin},
		{Username: "infra", PasswordHash: "old-hash-3", Role: auth.RoleServerAdmin},
		{Username: "worker", PasswordHash: "old-hash-4", Role: auth.RoleOperator},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		accounts[u.Username] = u
	}
	path := func(name string) string { return fmt.Sprintf("/api/users/%d", accounts[name].ID) }

	for _, c := range []struct {
		method, user, body string
	}{
		{http.MethodPut, "root1", `{"password":"New-pass-123!"}`},
		{http.MethodPut, "root1", `{"username":"root1x"}`},
		{http.MethodPut, "root2", `{"role":"viewer"}`},
		{http.MethodPut, "infra", `{"password":"New-pass-123!"}`},
		{http.MethodDelete, "root1", ""},
		{http.MethodDelete, "infra", ""},
	} {
		if status, body := listenerCall(t, srv, c.method, path(c.user), c.body, key); status != http.StatusForbidden {
			t.Errorf("global_admin %s %s %s: %d %s, want 403", c.method, c.user, c.body, status, body)
		}
	}
	for _, name := range []string{"root1", "root2", "infra"} {
		u, err := database.GetUserByID(accounts[name].ID)
		if err != nil || u == nil {
			t.Fatalf("%s is gone: %v", name, err)
		}
		if u.Username != name || u.Role != accounts[name].Role || u.PasswordHash != accounts[name].PasswordHash {
			t.Errorf("%s changed: %+v", name, u)
		}
	}

	if status, body := listenerCall(t, srv, http.MethodPut, path("worker"), `{"password":"New-pass-123!","role":"viewer"}`, key); status != http.StatusOK {
		t.Errorf("global_admin updates an operator: %d %s, want 200", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodDelete, path("worker"), "", key); status != http.StatusOK {
		t.Errorf("global_admin deletes a viewer: %d %s, want 200", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPut, path("root1"), `{"role":"viewer"}`); status != http.StatusOK {
		t.Errorf("console demotes a super admin: %d %s, want 200", status, body)
	}
}
