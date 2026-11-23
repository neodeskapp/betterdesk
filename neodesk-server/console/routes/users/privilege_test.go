package users_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// consoleRoles are the roles of the console's test accounts.
var consoleRoles = []string{"super_admin", "admin", "server_admin", "global_admin", "operator", "viewer", "guest_issuer", "pro"}

// TestNoAccountGivesMorePermissionsThanItHoldsHere is the rule over every
// role on the console's port: whatever a role's overrides say, no account
// creates an account or gives one a role that holds a permission the caller
// does not hold, and none changes an account whose role holds more than its
// own. Every role may create and edit accounts here, so the rule is what
// refuses, and operator holds server.config, which global_admin lacks
// (L387).
func TestNoAccountGivesMorePermissionsThanItHoldsHere(t *testing.T) {
	roles := routetest.Overrides{"operator": {"server.config": true}}
	for _, role := range consoleRoles {
		if !auth.IsSuperAdminRole(role) {
			if roles[role] == nil {
				roles[role] = map[string]bool{}
			}
			roles[role]["user.create"] = true
			roles[role]["user.edit"] = true
			roles[role]["user.delete"] = true
		}
	}
	w := world(t, nil, routetest.Options{Roles: roles, Env: map[string]string{"SESSION_MAX_PER_USER": "1000"}})
	ctx := context.Background()
	holds := func(role string) map[string]bool {
		t.Helper()
		out := map[string]bool{}
		for _, p := range auth.AllPermissions {
			has, err := w.Server.Deps().Access.RoleHasEffectivePermission(ctx, role, p)
			if err != nil {
				t.Fatal(err)
			}
			if has {
				out[p] = true
			}
		}
		return out
	}
	beyond := func(caller, target string) []string {
		mine := holds(caller)
		var out []string
		for p := range holds(target) {
			if !mine[p] {
				out = append(out, p)
			}
		}
		return out
	}
	root := w.As("admin")
	subject := createdID(t, root.Do("POST", "/api/users", `{"username":"subject","password":"Strong-Pass-1!","role":"viewer"}`))
	created := 0
	for i, caller := range consoleRoles {
		agent := w.As(caller)
		for j, target := range consoleRoles {
			over := beyond(caller, target)
			got := agent.Do("POST", "/api/users", fmt.Sprintf(`{"username":"acct_%d_%d","password":"Strong-Pass-1!","role":"%s"}`, i, j, target),
				"X-Forwarded-For", fmt.Sprintf("10.7.%d.%d", i, j))
			if got.Status == 200 {
				created++
				if len(over) > 0 {
					t.Errorf("%s created a %s account, which also holds %v", caller, target, over)
				}
			} else if auth.IsSuperAdminRole(caller) {
				t.Errorf("%s creates a %s account: %d %s", caller, target, got.Status, got.Body)
			}
			got = agent.Do("PATCH", "/api/users/"+subject, `{"role":"`+target+`"}`)
			if got.Status == 200 {
				if len(over) > 0 {
					t.Errorf("%s made an account %s, which also holds %v", caller, target, over)
				}
				if back := root.Do("PATCH", "/api/users/"+subject, `{"role":"viewer"}`); back.Status != 200 {
					t.Fatalf("reset the subject: %d %s", back.Status, back.Body)
				}
			} else if auth.IsSuperAdminRole(caller) {
				t.Errorf("%s makes an account %s: %d %s", caller, target, got.Status, got.Body)
			}
		}
	}
	if created <= 2*len(consoleRoles) {
		t.Errorf("only %d accounts were created: global_admin must still create viewers", created)
	}

	// An operator holds server.config: global_admin may not change or delete it
	worker := createdID(t, root.Do("POST", "/api/users", `{"username":"worker","password":"Strong-Pass-1!","role":"operator"}`))
	global := w.As("global_admin")
	for _, c := range []struct{ method, body string }{
		{"PATCH", `{"password":"Other-Pass-1!"}`},
		{"PATCH", `{"email":"w@example.org"}`},
		{"DELETE", ""},
	} {
		if got := global.Do(c.method, "/api/users/"+worker, c.body); got.Status != 403 {
			t.Errorf("global_admin %s %s the operator that holds server.config: %d %s, want 403", c.method, c.body, got.Status, got.Body)
		}
	}
	if got := global.Do("POST", "/api/users/"+worker+"/reset-password", `{"newPassword":"Other-Pass-1!"}`); got.Status != 403 {
		t.Errorf("global_admin resets the password of the operator that holds server.config: %d %s, want 403", got.Status, got.Body)
	}
	if got := root.Do("GET", "/api/users", ""); !strings.Contains(got.Body, `"worker"`) {
		t.Errorf("the operator is gone: %d", got.Status)
	}
}
