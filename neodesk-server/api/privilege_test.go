package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// everyRole is every role an account or a key can have.
var everyRole = []string{
	auth.RoleSuperAdmin, auth.RoleAdmin, auth.RoleServerAdmin, auth.RoleGlobalAdmin,
	auth.RoleOperator, auth.RoleViewer, auth.RoleGuestIssuer, auth.RolePro,
}

// signedIn gives every role a person with a sign-in token on srv: the
// account "person_<role>" and its bearer header.
func signedIn(t *testing.T, srv *Server, database db.Database) map[string]map[string]string {
	t.Helper()
	jm := auth.NewJWTManager("privilege-test-secret-0123456789", time.Hour)
	srv.SetJWTManager(jm)
	out := map[string]map[string]string{}
	for _, role := range everyRole {
		name := "person_" + role
		if err := database.CreateUser(&db.User{Username: name, PasswordHash: "x", Role: role}); err != nil {
			t.Fatal(err)
		}
		token, err := jm.Generate(name, role)
		if err != nil {
			t.Fatal(err)
		}
		out[role] = bearer(token)
	}
	return out
}

// TestAPIKeysChangeNoAccountsRolesOrKeys: an API key, the console's own
// (console-bridge, role admin) included, cannot create, change or delete
// an account, a role's permissions, an API key or an account's second
// factor on the API port. A signed-in person of the right role still can,
// and the console in this process still sets role permissions after its
// own checks (L387).
func TestAPIKeysChangeNoAccountsRolesOrKeys(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	const consoleKey = "console-bridge-key-0123456789"
	const scriptKey = "super-admin-script-key-0123"
	addTestAPIKey(t, database, consoleKey, "console-bridge", auth.RoleAdmin)
	addTestAPIKey(t, database, scriptKey, "script", auth.RoleSuperAdmin)
	vic := &db.User{Username: "vic", PasswordHash: "old-hash", Role: auth.RoleViewer}
	if err := database.CreateUser(vic); err != nil {
		t.Fatal(err)
	}
	keysBefore, err := database.ListAPIKeys()
	if err != nil {
		t.Fatal(err)
	}
	vicPath := fmt.Sprintf("/api/users/%d", vic.ID)
	changes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/users", `{"username":"mallory","password":"Strong-Pass-123!","role":"super_admin"}`},
		{http.MethodPut, vicPath, `{"role":"super_admin"}`},
		{http.MethodPut, vicPath, `{"password":"Strong-Pass-123!"}`},
		{http.MethodDelete, vicPath, ""},
		{http.MethodPost, "/api/role-permissions", `{"role":"viewer","permission":"user.create","granted":true}`},
		{http.MethodDelete, "/api/role-permissions/viewer/device.view", ""},
		{http.MethodPost, "/api/keys", `{"name":"another","role":"super_admin"}`},
		{http.MethodDelete, fmt.Sprintf("/api/keys/%d", keysBefore[0].ID), ""},
		{http.MethodPost, vicPath + "/totp/setup", ""},
		{http.MethodDelete, vicPath + "/totp", ""},
	}
	for _, key := range []string{consoleKey, scriptKey} {
		for _, c := range changes {
			status, body := listenerCall(t, srv, c.method, c.path, c.body, map[string]string{"X-API-Key": key})
			if status != http.StatusForbidden {
				t.Errorf("key %s: %s %s: %d %s, want 403", key[:8], c.method, c.path, status, body)
			}
		}
	}
	if u, _ := database.GetUser("mallory"); u != nil {
		t.Errorf("an API key created the account mallory with role %s", u.Role)
	}
	if u, _ := database.GetUserByID(vic.ID); u == nil || u.Role != auth.RoleViewer || u.PasswordHash != "old-hash" || u.TOTPSecret != "" {
		t.Errorf("an API key changed vic: %+v", u)
	}
	if overrides, _ := database.ListRolePermissions(auth.RoleViewer); len(overrides) != 0 {
		t.Errorf("an API key changed the viewer's permissions: %+v", overrides)
	}
	if keys, _ := database.ListAPIKeys(); len(keys) != len(keysBefore) {
		t.Errorf("an API key created or deleted a key: %d keys, had %d", len(keys), len(keysBefore))
	}

	people := signedIn(t, srv, database)
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/users", `{"username":"rooted","password":"Strong-Pass-123!","role":"super_admin"}`, people[auth.RoleSuperAdmin]); status != http.StatusCreated {
		t.Errorf("a signed-in super admin creates a super admin: %d %s, want 201", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPost, "/api/role-permissions", `{"role":"viewer","permission":"device.connect","granted":true}`); status != http.StatusOK {
		t.Errorf("the console sets a role permission in process: %d %s, want 200", status, body)
	}
}

// effective is the effective permission set of a role on srv, overrides
// included.
func effective(t *testing.T, srv *Server, role string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, p := range auth.AllPermissions {
		held, err := srv.RoleHasPermission(role, p)
		if err != nil {
			t.Fatal(err)
		}
		if held {
			out[p] = true
		}
	}
	return out
}

// beyond lists the permissions target holds that caller does not.
func beyond(t *testing.T, srv *Server, caller, target string) []string {
	t.Helper()
	held := effective(t, srv, caller)
	var out []string
	for p := range effective(t, srv, target) {
		if !held[p] {
			out = append(out, p)
		}
	}
	return out
}

// TestNoAccountGivesMorePermissionsThanItHolds is the rule over every role
// on the API port: whatever a role's overrides say, no signed-in account
// creates an account or an API key, or gives an account a role, that holds
// a permission the caller does not hold, and none changes an account whose
// role holds more than its own. Every role gets the permissions to create
// and edit accounts and keys, so the rule is what refuses, and operator
// gets server.config, which global_admin lacks (L387).
func TestNoAccountGivesMorePermissionsThanItHolds(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, role := range everyRole {
		if auth.IsSuperAdminRole(role) {
			continue
		}
		for _, p := range []string{auth.PermUserCreate, auth.PermUserEdit, auth.PermServerKeys} {
			if err := database.SetRolePermission(role, p, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := database.SetRolePermission(auth.RoleOperator, auth.PermServerConfig, true); err != nil {
		t.Fatal(err)
	}
	people := signedIn(t, srv, database)
	subject := &db.User{Username: "subject", PasswordHash: "x", Role: auth.RoleViewer}
	if err := database.CreateUser(subject); err != nil {
		t.Fatal(err)
	}
	subjectPath := fmt.Sprintf("/api/users/%d", subject.ID)

	allowed := 0
	for i, caller := range everyRole {
		for j, target := range everyRole {
			over := beyond(t, srv, caller, target)
			name := fmt.Sprintf("acct_%d_%d", i, j)
			status, body := listenerCall(t, srv, http.MethodPost, "/api/users",
				`{"username":"`+name+`","password":"Strong-Pass-123!","role":"`+target+`"}`, people[caller])
			if status == http.StatusCreated {
				allowed++
				if len(over) > 0 {
					t.Errorf("%s created a %s account, which also holds %v", caller, target, over)
				}
			}
			if caller == auth.RoleSuperAdmin && status != http.StatusCreated {
				t.Errorf("super_admin creates a %s account: %d %s, want 201", target, status, body)
			}

			status, body = listenerCall(t, srv, http.MethodPut, subjectPath, `{"role":"`+target+`"}`, people[caller])
			if status == http.StatusOK {
				if len(over) > 0 {
					t.Errorf("%s made an account %s, which also holds %v", caller, target, over)
				}
				if err := database.UpdateUser(&db.User{ID: subject.ID, Username: "subject", PasswordHash: "x", Role: auth.RoleViewer}); err != nil {
					t.Fatal(err)
				}
			} else if caller == auth.RoleSuperAdmin {
				t.Errorf("super_admin makes an account %s: %d %s, want 200", target, status, body)
			}

			status, body = listenerCall(t, srv, http.MethodPost, "/api/keys", `{"name":"`+name+`","role":"`+target+`"}`, people[caller])
			if status == http.StatusCreated && len(over) > 0 {
				t.Errorf("%s created a %s API key, which also holds %v", caller, target, over)
			}
			if caller == auth.RoleSuperAdmin && status != http.StatusCreated {
				t.Errorf("super_admin creates a %s API key: %d %s, want 201", target, status, body)
			}
		}
	}
	if allowed <= len(everyRole) {
		t.Errorf("only %d accounts were created: the rule must still let global_admin create viewers", allowed)
	}

	// An account whose role holds more than the caller's stays as it is
	worker := &db.User{Username: "worker", PasswordHash: "old-hash", Role: auth.RoleOperator}
	if err := database.CreateUser(worker); err != nil {
		t.Fatal(err)
	}
	workerPath := fmt.Sprintf("/api/users/%d", worker.ID)
	if status, body := listenerCall(t, srv, http.MethodPut, workerPath, `{"password":"Strong-Pass-123!"}`, people[auth.RoleGlobalAdmin]); status != http.StatusForbidden {
		t.Errorf("global_admin sets the password of an operator that holds server.config: %d %s, want 403", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodDelete, workerPath, "", people[auth.RoleGlobalAdmin]); status != http.StatusForbidden {
		t.Errorf("global_admin deletes an operator that holds server.config: %d %s, want 403", status, body)
	}
	if u, _ := database.GetUserByID(worker.ID); u == nil || u.PasswordHash != "old-hash" {
		t.Errorf("the operator changed: %+v", u)
	}
}

// TestAPIKeyRoleIsWhatItWas: the console-bridge key keeps its role, so
// what it may still do is told by the routes, not by a new role.
func TestAPIKeyRoleIsWhatItWas(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	const consoleKey = "console-bridge-key-0123456789"
	addTestAPIKey(t, database, consoleKey, "console-bridge", auth.RoleAdmin)
	status, body := listenerCall(t, srv, http.MethodGet, "/api/peers", "", map[string]string{"X-API-Key": consoleKey})
	if status != http.StatusOK {
		t.Fatalf("the console's key lists devices: %d %s, want 200", status, body)
	}
	var peers any
	if err := json.Unmarshal([]byte(body), &peers); err != nil {
		t.Fatalf("device list is no JSON: %v", err)
	}
}
