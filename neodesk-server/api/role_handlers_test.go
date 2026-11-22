package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/peer"
)

// Only super admins may change role permissions. A server_admin holds
// server.config, which used to be enough to grant its own role anything.
func TestRolePermissionOverridesNeedSuperAdmin(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")

	withRole := func(req *http.Request, role string) *http.Request {
		ctx := context.WithValue(req.Context(), ctxKeyRole, role)
		ctx = context.WithValue(ctx, ctxKeyUsername, "someone")
		return req.WithContext(ctx)
	}
	set := func(role string) int {
		body := `{"role":"server_admin","permission":"user.delete","granted":true}`
		req := withRole(httptest.NewRequest(http.MethodPost, "/api/role-permissions", strings.NewReader(body)), role)
		rec := httptest.NewRecorder()
		srv.handleSetRolePermission(rec, req)
		return rec.Code
	}
	del := func(role string) int {
		req := withRole(httptest.NewRequest(http.MethodDelete, "/api/role-permissions/server_admin/user.delete", nil), role)
		req.SetPathValue("role", "server_admin")
		req.SetPathValue("permission", "user.delete")
		rec := httptest.NewRecorder()
		srv.handleDeleteRolePermission(rec, req)
		return rec.Code
	}

	for _, role := range []string{auth.RoleServerAdmin, auth.RoleGlobalAdmin, auth.RoleOperator} {
		if code := set(role); code != http.StatusForbidden {
			t.Fatalf("%s set override: status %d, want 403", role, code)
		}
		overrides, err := database.ListRolePermissions(auth.RoleServerAdmin)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range overrides {
			if o.Permission == "user.delete" {
				t.Fatalf("%s managed to set an override on server_admin user.delete", role)
			}
		}
	}

	if code := set(auth.RoleSuperAdmin); code != http.StatusOK {
		t.Fatalf("super_admin set override: status %d, want 200", code)
	}
	if code := del(auth.RoleServerAdmin); code != http.StatusForbidden {
		t.Fatalf("server_admin delete override: status %d, want 403", code)
	}
	// The console's own API key has the admin role
	if code := del(auth.RoleAdmin); code != http.StatusOK {
		t.Fatalf("admin delete override: status %d, want 200", code)
	}
}

// PA6: while the overrides cannot be read, the built-in set would give
// back a permission an override took away. The answer is an error.
func TestRolePermissionsFailWhenOverridesCannotBeRead(t *testing.T) {
	database := testSetupDB(t)
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermDeviceConnect, false); err != nil {
		t.Fatal(err)
	}
	get := func() (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/roles/operator/permissions", nil)
		req.SetPathValue("role", auth.RoleOperator)
		rec := httptest.NewRecorder()
		srv.handleGetRolePermissions(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := get(); code != http.StatusOK || strings.Contains(body, `"`+auth.PermDeviceConnect+`"`) {
		t.Fatalf("with the override: %d %s", code, body)
	}
	database.Close()
	if code, body := get(); code != http.StatusInternalServerError || strings.Contains(body, `"`+auth.PermDeviceConnect+`"`) {
		t.Errorf("overrides unreadable: %d %s, want 500", code, body)
	}
}

// PA7: guest_issuer takes overrides (auth.ValidRole), so the role list and
// the override list show it.
func TestRoleListsIncludeGuestIssuer(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	if err := database.SetRolePermission(auth.RoleGuestIssuer, auth.PermChatAccess, true); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleListRoles(rec, httptest.NewRequest(http.MethodGet, "/api/roles", nil))
	if !strings.Contains(rec.Body.String(), `"name":"guest_issuer"`) {
		t.Errorf("role list without guest_issuer: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.handleListRolePermissionOverrides(rec, httptest.NewRequest(http.MethodGet, "/api/role-permissions", nil))
	if !strings.Contains(rec.Body.String(), `{"role":"guest_issuer","permission":"chat.access","granted":true}`) {
		t.Errorf("override list without guest_issuer's override: %s", rec.Body.String())
	}
}

// G21: the permission lists come in the order of auth.AllPermissions, the
// same on every call; they were built by ranging over a map.
func TestRolePermissionsKeepTheirOrder(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	srv := New(config.DefaultConfig(), database, peer.NewMap(), nil, "test")
	if err := database.SetRolePermission(auth.RoleOperator, "zz.made_up", true); err != nil {
		t.Fatal(err)
	}
	effective := func() []string {
		req := httptest.NewRequest(http.MethodGet, "/api/roles/operator/permissions", nil)
		req.SetPathValue("role", auth.RoleOperator)
		rec := httptest.NewRecorder()
		srv.handleGetRolePermissions(rec, req)
		var body struct {
			Permissions []string `json:"permissions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Permissions
	}
	var want []string
	for _, p := range auth.AllPermissions {
		if auth.DefaultRolePermissions[auth.RoleOperator][p] {
			want = append(want, p)
		}
	}
	want = append(want, "zz.made_up")
	for i := 0; i < 5; i++ {
		if got := effective(); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("call %d: %v, want %v", i, got, want)
		}
	}
	listed := func() string {
		rec := httptest.NewRecorder()
		srv.handleListRoles(rec, httptest.NewRequest(http.MethodGet, "/api/roles", nil))
		return rec.Body.String()
	}
	first := listed()
	for i := 0; i < 5; i++ {
		if got := listed(); got != first {
			t.Fatalf("the role list changed from call to call:\n %s\n %s", first, got)
		}
	}
	if !strings.Contains(first, `"name":"operator","level":2,"is_super_admin":false,"is_server_level":false,"permissions":["`+strings.Join(want[:len(want)-1], `","`)+`"]`) {
		t.Errorf("operator is not listed in permission order: %s", first)
	}
}
