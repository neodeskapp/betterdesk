//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Roles and permission overrides. The overrides written here are for the
// role "pro", which cannot sign in to the console, and each turn deletes
// what it set. The API server lists a role's permissions in no fixed
// order, so those lists are compared in any order.
func init() {
	some := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	admins := contract.Roles("server_admin", "super_admin", "admin")
	writers := contract.Roles("super_admin", "admin")
	set := func(name string, body any, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/panel/role-permissions", Body: body, Roles: roles}
	}

	contract.Group("permissions",
		contract.Case{Name: "roles", Path: "/api/panel/roles", Volatile: []string{contract.AnyOrder + "data.roles[*].permissions"}},
		contract.Case{Name: "roles-head", Method: "HEAD", Path: "/api/panel/roles", Roles: some},
		contract.Case{Name: "role-operator", Path: "/api/panel/roles/operator/permissions", Volatile: []string{contract.AnyOrder + "data.permissions"}},
		contract.Case{Name: "role-super-admin", Path: "/api/panel/roles/super_admin/permissions", Volatile: []string{contract.AnyOrder + "data.permissions"}, Roles: admins},
		contract.Case{Name: "role-with-spaces-around", Path: "/api/panel/roles/%20viewer%20/permissions", Volatile: []string{contract.AnyOrder + "data.permissions"}, Roles: admins},
		contract.Case{Name: "role-unknown", Path: "/api/panel/roles/nobody/permissions", Roles: some},
		contract.Case{Name: "role-in-upper-case", Path: "/api/panel/roles/OPERATOR/permissions", Roles: admins},
		contract.Case{Name: "role-with-a-space", Path: "/api/panel/roles/oper%20ator/permissions", Roles: some},
		contract.Case{Name: "role-with-a-slash", Path: "/api/panel/roles/oper%2Fator/permissions", Roles: admins},
		contract.Case{Name: "role-of-dots", Path: "/api/panel/roles/%2E%2E/permissions", Roles: admins},

		contract.Case{Name: "overrides", Path: "/api/panel/role-permissions"},
		contract.Case{Name: "overrides-of-pro", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"pro"}}, Roles: some},

		set("set", map[string]any{"role": "pro", "permission": "device.view", "granted": true}, nil),
		contract.Case{Name: "overrides-after-set", Path: "/api/panel/role-permissions", Roles: admins},
		contract.Case{Name: "overrides-of-pro-after-set", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"pro"}}, Roles: admins},
		contract.Case{Name: "overrides-of-viewer-after-set", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"viewer"}}, Roles: admins},
		contract.Case{Name: "overrides-of-unknown-role", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"nobody"}}, Roles: admins},
		contract.Case{Name: "overrides-of-empty-role", Path: "/api/panel/role-permissions", Query: url.Values{"role": {""}}, Roles: admins},
		// A parameter sent twice is a list and reads as "pro,viewer", which is no role
		contract.Case{Name: "overrides-of-two-roles", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"pro", "viewer"}}, Roles: admins},
		contract.Case{Name: "overrides-of-role-in-brackets", Path: "/api/panel/role-permissions", Query: url.Values{"role[]": {"pro"}}, Roles: admins},
		contract.Case{Name: "overrides-of-role-as-object", Path: "/api/panel/role-permissions", Query: url.Values{"role[name]": {"pro"}}, Roles: admins},
		contract.Case{Name: "role-pro-after-set", Path: "/api/panel/roles/pro/permissions", Volatile: []string{contract.AnyOrder + "data.permissions"}, Roles: admins},
		set("set-again-to-denied", map[string]any{"role": "pro", "permission": "device.view", "granted": false}, writers),
		contract.Case{Name: "overrides-of-pro-after-denied", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"pro"}}, Roles: admins},
		set("set-second", map[string]any{"role": []any{"pro"}, "permission": []any{"audit.view"}, "granted": true}, writers),
		contract.Case{Name: "overrides-of-pro-with-two", Path: "/api/panel/role-permissions", Query: url.Values{"role": {"pro"}}, Roles: admins},

		set("set-without-fields", map[string]any{}, some),
		set("set-without-granted", map[string]any{"role": "pro", "permission": "device.view"}, writers),
		set("set-granted-as-text", map[string]any{"role": "pro", "permission": "device.view", "granted": "true"}, writers),
		set("set-granted-as-number", map[string]any{"role": "pro", "permission": "device.view", "granted": 1}, writers),
		set("set-granted-null", map[string]any{"role": "pro", "permission": "device.view", "granted": nil}, writers),
		set("set-empty-role", map[string]any{"role": "", "permission": "device.view", "granted": true}, writers),
		set("set-role-zero", map[string]any{"role": 0, "permission": "device.view", "granted": true}, writers),
		set("set-role-number", map[string]any{"role": 5, "permission": "device.view", "granted": true}, writers),
		set("set-role-object", map[string]any{"role": map[string]any{"name": "pro"}, "permission": "device.view", "granted": true}, writers),
		set("set-role-with-a-space", map[string]any{"role": "pro fessional", "permission": "device.view", "granted": true}, writers),
		set("set-role-of-spaces", map[string]any{"role": "   ", "permission": "device.view", "granted": true}, writers),
		set("set-permission-with-a-space", map[string]any{"role": "pro", "permission": "device view", "granted": true}, writers),
		set("set-both-names-bad", map[string]any{"role": "p ro", "permission": "device view", "granted": true}, writers),
		set("set-unknown-role", map[string]any{"role": "nobody", "permission": "device.view", "granted": true}, writers),
		set("set-unknown-permission", map[string]any{"role": "pro", "permission": "nothing.at.all", "granted": true}, writers),
		set("set-for-super-admin", map[string]any{"role": "super_admin", "permission": "device.view", "granted": false}, writers),
		set("set-for-admin", map[string]any{"role": "admin", "permission": "device.view", "granted": false}, writers),
		contract.Case{
			Name: "set-with-a-list-as-body", Method: "POST", Path: "/api/panel/role-permissions",
			RawBody: `[{"role":"pro","permission":"device.view","granted":true}]`, ContentType: "application/json", Roles: writers,
		},
		contract.Case{
			Name: "set-without-csrf-token", Method: "POST", Path: "/api/panel/role-permissions",
			Body: map[string]any{"role": "pro", "permission": "device.view", "granted": true}, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "set-as-form", Method: "POST", Path: "/api/panel/role-permissions",
			RawBody: "role=pro&permission=device.view&granted=true", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},

		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/panel/role-permissions/pro/device.view"},
		contract.Case{Name: "delete-second", Method: "DELETE", Path: "/api/panel/role-permissions/%20pro%20/%20audit.view%20", Roles: writers},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/panel/role-permissions/pro/device.view", Roles: writers},
		contract.Case{Name: "delete-unknown-role", Method: "DELETE", Path: "/api/panel/role-permissions/nobody/device.view", Roles: writers},
		contract.Case{Name: "delete-unknown-permission", Method: "DELETE", Path: "/api/panel/role-permissions/pro/nothing.at.all", Roles: writers},
		contract.Case{Name: "delete-role-with-a-space", Method: "DELETE", Path: "/api/panel/role-permissions/p%20ro/device.view", Roles: some},
		contract.Case{Name: "delete-permission-with-a-slash", Method: "DELETE", Path: "/api/panel/role-permissions/pro/device%2Fview", Roles: writers},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/panel/role-permissions/pro/device.view", NoCSRF: true, Roles: some},
		contract.Case{Name: "overrides-after-delete", Path: "/api/panel/role-permissions", Roles: admins},
		contract.Case{Name: "role-pro-after-delete", Path: "/api/panel/roles/pro/permissions", Volatile: []string{contract.AnyOrder + "data.permissions"}, Roles: admins},
	)
}
