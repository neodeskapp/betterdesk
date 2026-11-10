//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// Tenants: every route as the roles that may manage tenants (super_admin,
// admin, global_admin) and as the ones that may not, which get the
// group's own 403 also when signed out. A role's turn creates four
// tenants, works on the first one and deletes all four, so the next turn
// starts from an empty list.
//
// Lists that are ordered by the second of the assignment are read while
// they hold one row: two rows written in the same second have no order
// to rely on.
func init() {
	contract.Group("tenants",
		contract.Case{Name: "list-empty", Path: "/api/tenants"},
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/tenants"},

		// Create
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{
				"name": "  Acme Corp ", "contact_name": "Ada", "contact_email": "ada@example.com",
				"max_devices": 25, "notes": "first",
			},
			Capture: map[string]string{"tenant": "id"},
		},
		contract.Case{
			Name: "create-with-defaults", Method: "POST", Path: "/api/tenants",
			Body:    map[string]any{"name": "Nord Süd GmbH & Co."},
			Capture: map[string]string{"second": "id"},
		},
		contract.Case{
			Name: "create-other-types", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{
				"name": "Types", "slug": 77, "contact_name": 5, "contact_email": 0,
				"max_devices": "12", "notes": 1.5, "active": false,
			},
			Capture: map[string]string{"third": "id"},
		},
		contract.Case{
			Name: "create-slug-in-a-list", Method: "POST", Path: "/api/tenants",
			Body:    map[string]any{"name": "List", "slug": []string{"from-a-list"}},
			Capture: map[string]string{"fourth": "id"},
		},
		contract.Case{
			Name: "create-slug-taken", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": "ACME   corp!"},
		},
		contract.Case{
			Name: "create-without-name", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"slug": "nameless"},
		},
		contract.Case{
			Name: "create-blank-name", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": " \t\u00a0 "},
		},
		contract.Case{
			Name: "create-name-number", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": 5},
		},
		contract.Case{
			Name: "create-flag-in-a-text-field", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": "Flag", "contact_email": true},
		},
		contract.Case{
			Name: "create-object-in-a-text-field", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": "Object", "notes": map[string]any{"a": 1}},
		},
		contract.Case{
			Name: "create-two-values-in-a-list", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": "Two", "slug": []string{"a", "b"}},
		},
		contract.Case{
			Name: "create-array-body", Method: "POST", Path: "/api/tenants",
			RawBody: `[{"name":"In a list"}]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/tenants",
			RawBody: "name=Form+Tenant", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: "/api/tenants",
			Body: map[string]any{"name": "No token"}, NoCSRF: true,
		},

		// Read
		contract.Case{Name: "read", Path: "/api/tenants/{tenant}"},
		contract.Case{Name: "read-id-with-fraction", Path: "/api/tenants/{tenant}.0"},
		contract.Case{Name: "read-id-with-exponent", Path: "/api/tenants/%20{tenant}e0"},
		contract.Case{Name: "read-id-not-a-number", Path: "/api/tenants/abc"},
		contract.Case{Name: "read-id-half", Path: "/api/tenants/{tenant}.5"},
		contract.Case{Name: "read-id-infinite", Path: "/api/tenants/Infinity"},
		contract.Case{Name: "read-missing", Path: "/api/tenants/999999999"},
		contract.Case{Name: "read-other-spelling", Path: "/API/Tenants/{tenant}/"},
		contract.Case{Name: "list", Path: "/api/tenants"},

		// Update
		contract.Case{
			Name: "update", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{
				"name": "Acme Corporation", "contact_name": "Grace", "contact_email": "",
				"max_devices": 30, "notes": nil, "active": false,
			},
		},
		contract.Case{
			Name: "update-nothing", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{"unknown": "field"},
		},
		contract.Case{
			Name: "update-active-by-truth", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{"active": "no", "max_devices": "40"},
		},
		contract.Case{
			Name: "update-slug-taken", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{"slug": "from-a-list"},
		},
		contract.Case{
			Name: "update-name-null", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{"name": nil},
		},
		contract.Case{
			Name: "update-flag-in-a-text-field", Method: "PATCH", Path: "/api/tenants/{tenant}",
			Body: map[string]any{"notes": true},
		},
		contract.Case{
			Name: "update-missing", Method: "PATCH", Path: "/api/tenants/999999999",
			Body: map[string]any{"name": "Nobody"},
		},
		contract.Case{
			Name: "update-id-not-a-number", Method: "PATCH", Path: "/api/tenants/abc",
			Body: map[string]any{"name": "Nobody"},
		},
		contract.Case{
			Name: "update-as-text", Method: "PATCH", Path: "/api/tenants/{tenant}",
			RawBody: `{"name":"As text"}`, ContentType: "text/plain",
		},
		contract.Case{Name: "read-after-updates", Path: "/api/tenants/{tenant}"},

		// Devices
		contract.Case{Name: "devices-empty", Path: "/api/tenants/{tenant}/devices"},
		contract.Case{
			Name: "assign-device", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_id": "900100001"},
		},
		// Whether the device counts as online depends on when the console's
		// copy of the devices was last brought up to date: the copy says
		// online, and within 15 seconds a job says offline again because
		// the last contact in it is old. So online is checked by its type.
		contract.Case{Name: "devices-one", Path: "/api/tenants/{tenant}/devices", Volatile: []string{"~[*].online"}},
		contract.Case{
			Name: "assign-devices", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": []string{"900100002", "900100001", "no-such-device"}, "device_id": "ignored"},
		},
		contract.Case{
			Name: "assign-devices-of-other-types", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": []any{7, nil, true, []string{"nested"}, map[string]any{}, []string{}}},
		},
		contract.Case{
			Name: "assign-devices-text", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": "ab"},
		},
		contract.Case{
			Name: "assign-devices-number", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": 5},
		},
		contract.Case{
			Name: "assign-devices-object", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": map[string]any{"length": 0}},
		},
		contract.Case{
			Name: "assign-devices-empty-list", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_ids": []string{}, "device_id": "ignored"},
		},
		contract.Case{
			Name: "assign-no-device", Method: "POST", Path: "/api/tenants/{tenant}/devices",
			Body: map[string]any{"device_id": ""},
		},
		contract.Case{
			Name: "assign-device-missing-tenant", Method: "POST", Path: "/api/tenants/999999999/devices",
			Body: map[string]any{"device_id": "900100001"},
		},
		contract.Case{Name: "remove-device", Method: "DELETE", Path: "/api/tenants/{tenant}/devices/900100002"},
		contract.Case{Name: "remove-device-again", Method: "DELETE", Path: "/api/tenants/{tenant}/devices/900100002"},
		contract.Case{Name: "remove-device-number-as-stored", Method: "DELETE", Path: "/api/tenants/{tenant}/devices/7.0"},
		contract.Case{Name: "remove-device-id-not-a-number", Method: "DELETE", Path: "/api/tenants/abc/devices/900100001"},
		contract.Case{Name: "devices-of-missing-tenant", Path: "/api/tenants/999999999/devices"},
		contract.Case{Name: "devices-id-not-a-number", Path: "/api/tenants/abc/devices"},

		// Users
		contract.Case{Name: "users-empty", Path: "/api/tenants/{tenant}/users"},
		contract.Case{
			Name: "assign-user", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": 2},
		},
		contract.Case{Name: "users-one", Path: "/api/tenants/{tenant}/users"},
		contract.Case{
			Name: "assign-user-again", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": "2"},
		},
		contract.Case{
			Name: "assign-user-in-a-list", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": []string{" 3 "}},
		},
		contract.Case{
			Name: "assign-user-half", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": "1.5"},
		},
		contract.Case{
			Name: "assign-user-flag", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": true},
		},
		contract.Case{
			Name: "assign-user-not-a-number", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": "abc"},
		},
		contract.Case{
			Name: "assign-user-zero", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{"user_id": 0},
		},
		contract.Case{
			Name: "assign-no-user", Method: "POST", Path: "/api/tenants/{tenant}/users",
			Body: map[string]any{},
		},
		contract.Case{
			Name: "assign-user-missing-tenant", Method: "POST", Path: "/api/tenants/999999999/users",
			Body: map[string]any{"user_id": 2},
		},
		contract.Case{Name: "remove-user", Method: "DELETE", Path: "/api/tenants/{tenant}/users/3"},
		contract.Case{Name: "remove-user-again", Method: "DELETE", Path: "/api/tenants/{tenant}/users/3"},
		contract.Case{Name: "remove-user-half", Method: "DELETE", Path: "/api/tenants/{tenant}/users/1.5"},
		contract.Case{Name: "remove-user-id-not-a-number", Method: "DELETE", Path: "/api/tenants/{tenant}/users/abc"},
		contract.Case{Name: "users-of-missing-tenant", Path: "/api/tenants/999999999/users"},

		// Stats
		contract.Case{Name: "stats", Path: "/api/tenants/{tenant}/stats", Volatile: []string{"~online_devices"}},
		contract.Case{Name: "stats-id-with-fraction", Path: "/api/tenants/{tenant}.0/stats", Volatile: []string{"~online_devices"}},
		contract.Case{Name: "stats-of-tenant-with-defaults", Path: "/api/tenants/{second}/stats"},
		contract.Case{Name: "stats-missing", Path: "/api/tenants/999999999/stats"},

		// Delete: the assignments go with their tenant
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/tenants/{tenant}", NoCSRF: true},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/tenants/{tenant}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/tenants/{tenant}"},
		contract.Case{Name: "delete-id-not-a-number", Method: "DELETE", Path: "/api/tenants/abc"},
		contract.Case{Name: "devices-after-delete", Path: "/api/tenants/{tenant}/devices"},
		contract.Case{Name: "users-after-delete", Path: "/api/tenants/{tenant}/users"},
		contract.Case{Name: "delete-second", Method: "DELETE", Path: "/api/tenants/{second}"},
		contract.Case{Name: "delete-third", Method: "DELETE", Path: "/api/tenants/{third}"},
		contract.Case{Name: "delete-fourth", Method: "DELETE", Path: "/api/tenants/{fourth}"},
		contract.Case{Name: "list-after-deletes", Path: "/api/tenants"},
	)
}
