//go:build contract

package contract_test

import "github.com/unitronix/betterdesk-server/console/contract"

// Panel access policies. The account policy is written for the seeded
// account demo_viewer (id 3), which no role of the suite signs in as, and
// is emptied again at the end of each turn. The group policy is written
// for a user group that admin creates and deletes in each turn.
func init() {
	some := contract.Roles("signed-out", "viewer", "operator", "admin")
	editors := contract.Roles("server_admin", "admin")
	policy := map[string]any{
		"sections": []any{
			map[string]any{"resource_key": "devices", "access_level": "write"},
			map[string]any{"key": "group:operations", "access": "read"},
			map[string]any{"resource_key": "devices", "access_level": "none"},
			map[string]any{"resource_key": "no-such-section", "access_level": "read"},
			map[string]any{"resource_key": " users ", "access_level": 2},
			map[string]any{"resource_key": "tickets"},
			map[string]any{"resource_key": "reports", "access_level": "WRITE"},
			map[string]any{"resource_key": "chat", "access_level": nil, "access": "read"},
			map[string]any{"resource_key": "settings", "access_level": "everything"},
			"not a row",
			nil,
		},
		"features": []any{
			map[string]any{"feature_key": "file_transfer", "enabled": false},
			map[string]any{"key": "clipboard", "enabled": "true"},
			map[string]any{"feature_key": "audio", "enabled": 1},
			map[string]any{"feature_key": "tunnel", "enabled": "yes"},
			map[string]any{"feature_key": "client_actions", "enabled": true},
			map[string]any{"feature_key": "no-such-feature", "enabled": true},
			map[string]any{"feature_key": "file_transfer", "enabled": true},
		},
	}
	empty := map[string]any{"sections": []any{}, "features": []any{}}
	userPut := func(name string, body any, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "PUT", Path: "/api/panel/access/users/3", Body: body, Roles: roles}
	}
	userGet := func(name, id string, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/panel/access/users/" + id, Roles: roles}
	}

	contract.Group("panel-access",
		contract.Case{Name: "catalog", Path: "/api/panel/access/catalog"},
		contract.Case{Name: "catalog-head", Method: "HEAD", Path: "/api/panel/access/catalog", Roles: some},
		contract.Case{Name: "effective", Path: "/api/panel/access/effective"},

		userGet("user", "3", nil),
		userGet("user-of-an-admin", "1", editors),
		userGet("user-number-with-letters-behind", "3x", editors),
		userGet("user-number-with-a-space-in-front", "%203", editors),
		userGet("user-fraction", "3.9", editors),
		userGet("user-with-a-plus", "%2B3", editors),
		userGet("user-unknown", "999999999", some),
		userGet("user-no-number", "abc", some),
		userGet("user-negative", "-3", editors),
		userGet("user-hexadecimal", "0x3", editors),
		userGet("user-too-large", "99999999999999999999999", editors),
		userGet("user-zero", "0", editors),

		userPut("user-save", policy, nil),
		userGet("user-after-save", "3", editors),
		userPut("user-save-sections-only", map[string]any{"sections": []any{map[string]any{"resource_key": "group:core", "access_level": "none"}}}, editors),
		userGet("user-after-sections-only", "3", editors),
		userPut("user-save-lists-of-the-wrong-kind", map[string]any{"sections": "devices", "features": map[string]any{"audio": false}}, editors),
		contract.Case{
			Name: "user-save-with-a-list-as-body", Method: "PUT", Path: "/api/panel/access/users/3",
			RawBody: `[{"sections":[{"resource_key":"devices","access_level":"write"}]}]`, ContentType: "application/json", Roles: editors,
		},
		contract.Case{
			Name: "user-save-number-with-letters-behind", Method: "PUT", Path: "/api/panel/access/users/3x",
			Body: map[string]any{"sections": []any{map[string]any{"resource_key": "devices", "access_level": "read"}}}, Roles: editors,
		},
		userGet("user-after-letters-behind", "3", editors),
		contract.Case{Name: "user-save-unknown", Method: "PUT", Path: "/api/panel/access/users/999999999", Body: policy, Roles: some},
		contract.Case{Name: "user-save-no-number", Method: "PUT", Path: "/api/panel/access/users/abc", Body: policy, Roles: editors},
		contract.Case{Name: "user-save-without-csrf-token", Method: "PUT", Path: "/api/panel/access/users/3", Body: policy, NoCSRF: true, Roles: some},
		contract.Case{
			Name: "user-save-as-form", Method: "PUT", Path: "/api/panel/access/users/3",
			RawBody: "sections=devices", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},
		userPut("user-empty", empty, nil),
		userGet("user-after-empty", "3", editors),

		contract.Case{
			Name: "group-create", Method: "POST", Path: "/api/panel/user-groups", As: "admin",
			Body:    map[string]any{"name": "contract policy group"},
			Capture: map[string]string{"group": "data.group.guid"},
		},
		contract.Case{Name: "group", Path: "/api/panel/access/groups/{group}"},
		contract.Case{Name: "group-save", Method: "PUT", Path: "/api/panel/access/groups/{group}", Body: policy},
		contract.Case{Name: "group-after-save", Path: "/api/panel/access/groups/{group}", Roles: editors},
		contract.Case{
			Name: "group-save-nothing", Method: "PUT", Path: "/api/panel/access/groups/{group}",
			Body: map[string]any{}, Roles: editors,
		},
		contract.Case{Name: "group-after-nothing", Path: "/api/panel/access/groups/{group}", Roles: editors},
		contract.Case{Name: "group-unknown", Path: "/api/panel/access/groups/00000000-0000-4000-8000-000000000000", Roles: some},
		contract.Case{Name: "group-unknown-with-odd-letters", Path: "/api/panel/access/groups/no%20such%2Fgroup", Roles: editors},
		contract.Case{
			Name: "group-save-unknown", Method: "PUT", Path: "/api/panel/access/groups/00000000-0000-4000-8000-000000000000",
			Body: policy, Roles: some,
		},
		contract.Case{
			Name: "group-save-without-csrf-token", Method: "PUT", Path: "/api/panel/access/groups/{group}",
			Body: policy, NoCSRF: true, Roles: some,
		},
		contract.Case{Name: "group-empty", Method: "PUT", Path: "/api/panel/access/groups/{group}", Body: empty, As: "admin"},
		contract.Case{Name: "group-delete", Method: "DELETE", Path: "/api/panel/user-groups/{group}", As: "admin"},
		contract.Case{Name: "group-after-delete", Path: "/api/panel/access/groups/{group}", Roles: editors},
	)
}
