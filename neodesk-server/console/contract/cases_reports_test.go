//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Reports: the types, every report on demand as JSON and CSV, and the
// saved reports. The seed saves four reports (devices, tickets,
// inventory, activity) with the ids 1 to 4.
//
// The device and security reports read the console's peer table, which
// only the Node console fills from the signal server's devices, inside
// its own reads of the device list (BUGS.md RP9). What they find there
// depends on which console answered last, so their device numbers, the
// banned devices and the CSV made of them are marked by hand; the tests
// of console/reports and console/routes/reports pin them to Node's. The
// CSV of the banned devices alone is no case: it is empty when the table
// is, and an empty answer is another kind of body.
//
// The seed spreads activity over working hours and the audit log grows
// with every sign-in, so the activity numbers, the audit entries and the
// payloads of the seeded reports are marked by hand too. A role's turn
// saves reports as admin, reads them as the role and deletes them as
// admin.
func init() {
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "super_admin", "admin")
	writers := contract.Roles("signed-out", "viewer", "super_admin", "admin")
	activity := []string{
		"~data.devices_with_activity", "~data.daily_reports", "~data.total_active_secs", "~data.total_idle_secs", "data.top_applications",
	}
	devices := []string{"~data.total_devices", "~data.online", "~data.offline", "~data.banned", "data.platform_distribution", "data.stats"}
	security := []string{"~data.audit_log_count", "data.recent_audit[#]", "data.banned_devices[#]"}
	generate := func(name string, body map[string]any, volatile ...string) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/reports/generate", Body: body, Roles: writers, Volatile: volatile}
	}
	csv := func(name string, body map[string]any, volatile ...string) contract.Case {
		return contract.Case{
			Name: name, Method: "POST", Path: "/api/reports/generate/csv", Body: body, Roles: writers, Volatile: volatile,
			// the file name holds the day of the export
			VolatileHeaders: []string{"content-disposition"},
		}
	}
	cases := []contract.Case{
		{Name: "types", Path: "/api/reports/types"},
		{Name: "types-other-spelling", Path: "/API/Reports/Types/", Roles: few},
		{Name: "types-head", Method: "HEAD", Path: "/api/reports/types", Roles: few},

		generate("devices", map[string]any{"type": "devices"}, devices...),
		generate("activity", map[string]any{"type": "activity"}, activity...),
		generate("security", map[string]any{"type": "security"}, security...),
		generate("tickets", map[string]any{"type": "tickets"}),
		generate("network", map[string]any{"type": "network"}),
		generate("inventory", map[string]any{"type": "inventory"}),
		generate("alerts", map[string]any{"type": "alerts"}),
		generate("type-in-a-list", map[string]any{"type": []any{"tickets"}}),
		generate("type-in-a-list-in-a-list", map[string]any{"type": []any{[]any{"inventory"}}}),
		generate("type-missing", map[string]any{"from": "2026-01-01"}),
		generate("type-empty", map[string]any{"type": ""}),
		generate("type-zero", map[string]any{"type": 0}),
		generate("type-unknown", map[string]any{"type": "nope"}),
		generate("type-other-case", map[string]any{"type": "Devices"}),
		generate("type-number", map[string]any{"type": 5}),
		generate("type-flag", map[string]any{"type": true}),
		generate("type-object", map[string]any{"type": map[string]any{"a": 1}}),
		generate("type-two-in-a-list", map[string]any{"type": []any{"devices", "tickets"}}),
		generate("type-an-object-has", map[string]any{"type": "constructor"}),
		generate("type-proto", map[string]any{"type": "__proto__"}),
		{Name: "body-a-list", Method: "POST", Path: "/api/reports/generate", RawBody: `["devices"]`, ContentType: "application/json", Roles: writers},
		{Name: "body-empty", Method: "POST", Path: "/api/reports/generate", RawBody: "", ContentType: "application/json", Roles: writers},
		{Name: "body-not-json", Method: "POST", Path: "/api/reports/generate", RawBody: "type=devices", ContentType: "application/x-www-form-urlencoded", Roles: writers},
		{Name: "generate-without-csrf-token", Method: "POST", Path: "/api/reports/generate", Body: map[string]any{"type": "tickets"}, NoCSRF: true, Roles: writers},
		generate("filters-echoed", map[string]any{
			"type": "tickets", "from": "2026-01-01", "to": "2026-12-31", "device_id": map[string]any{"b": 1, "10": "x", "2": []any{1.5, nil}}, "limit": 1e21,
		}),
		generate("filters-of-other-types", map[string]any{"type": "inventory", "from": nil, "to": false, "device_id": 900100001, "limit": "10"}),
		generate("activity-long-ago", map[string]any{"type": "activity", "from": "2000-01-01", "to": "2000-01-31"}),
		generate("activity-day-border", map[string]any{"type": "activity", "from": "2000-01-31T23:00:00.000Z", "to": "2000-02-01"}),
		generate("activity-in-the-future", map[string]any{"type": "activity", "from": "2100-01-01"}),
		generate("activity-until-long-ago", map[string]any{"type": "activity", "to": "2000-01-01T00:00:00+01:00"}),
		generate("activity-from-no-time", map[string]any{"type": "activity", "from": "garbage"}),
		generate("activity-from-a-number", map[string]any{"type": "activity", "from": 5}),
		generate("activity-from-a-flag", map[string]any{"type": "activity", "from": true}),
		generate("activity-from-a-list", map[string]any{"type": "activity", "from": []any{"2100-01-01"}}),
		generate("activity-from-two-in-a-list", map[string]any{"type": "activity", "from": []any{"2000-01-01", "2000-02-01"}}),
		generate("activity-from-an-object", map[string]any{"type": "activity", "from": map[string]any{"a": 1}}),
		generate("activity-from-falsy", map[string]any{"type": "activity", "from": 0, "to": ""}, activity...),

		csv("csv-devices", map[string]any{"type": "devices"}, "$"),
		csv("csv-tickets", map[string]any{"type": "tickets"}),
		csv("csv-inventory", map[string]any{"type": "inventory"}),
		csv("csv-recent-audit", map[string]any{"type": "security", "section": "recent_audit"}, "$"),
		csv("csv-section-in-a-list", map[string]any{"type": "network", "section": []any{"targets"}}),
		csv("csv-network-flat", map[string]any{"type": "network"}),
		csv("csv-security-flat", map[string]any{"type": "security"}, "$"),
		csv("csv-empty-section", map[string]any{"type": "network", "section": "targets"}),
		csv("csv-section-no-list", map[string]any{"type": "tickets", "section": "stats"}),
		csv("csv-section-missing", map[string]any{"type": "inventory", "section": "nope"}),
		csv("csv-section-an-object-has", map[string]any{"type": "inventory", "section": "constructor"}),
		csv("csv-alerts", map[string]any{"type": "alerts", "section": "recent_alerts"}),
		csv("csv-activity-long-ago", map[string]any{"type": "activity", "from": "2000-01-01", "to": "2000-01-02", "section": "top_applications"}),
		csv("csv-type-missing", map[string]any{"section": "targets"}),
		csv("csv-type-unknown", map[string]any{"type": "nope"}),
		csv("csv-type-in-a-list", map[string]any{"type": []any{"tickets"}}),
		csv("csv-activity-from-a-flag", map[string]any{"type": "activity", "from": true}),

		{Name: "saved", Path: "/api/reports/saved", Volatile: []string{"~[*].payload"}},
		{Name: "saved-tickets-report", Path: "/api/reports/saved/2", Roles: few},
		{Name: "saved-id-with-fraction", Path: "/api/reports/saved/2.0", Roles: few},
		{Name: "saved-id-in-hex", Path: "/api/reports/saved/0x2", Roles: few},
		{Name: "saved-id-with-spaces", Path: "/api/reports/saved/%20%202%20", Roles: few},
		{Name: "saved-id-half", Path: "/api/reports/saved/1.5", Roles: few},
		{Name: "saved-id-not-a-number", Path: "/api/reports/saved/abc", Roles: few},
		{Name: "saved-missing", Path: "/api/reports/saved/999999999", Roles: few},

		// The id of a report saved in a role's turn counts the saves of the
		// turns before it, so a fix that refuses one of them moves it.
		{Name: "save-as-the-role", Method: "POST", Path: "/api/reports/save", Body: map[string]any{"type": "network", "title": "Network as the role"}, Roles: writers, Capture: map[string]string{"own": "id"}, Volatile: []string{"id"}},
		{Name: "delete-own", Method: "DELETE", Path: "/api/reports/saved/{own}", Roles: writers},
		{Name: "save", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Volatile: []string{"id"},
			Body: map[string]any{"type": "tickets", "title": "Contract ticket report", "from": "2026-01-01", "limit": 5}, Capture: map[string]string{"report": "id"}},
		{Name: "read-saved", Path: "/api/reports/saved/{report}", Roles: few, Volatile: []string{"id"}},
		{Name: "list-with-it", Path: "/api/reports/saved", Roles: few, Volatile: []string{"~[*].payload", "[0].id"}},
		{Name: "save-default-title", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Volatile: []string{"id"},
			Body: map[string]any{"type": "inventory"}, Capture: map[string]string{"untitled": "id"}},
		{Name: "save-title-number", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Volatile: []string{"id"},
			Body: map[string]any{"type": "network", "title": 5}, Capture: map[string]string{"numbered": "id"}},
		{Name: "save-title-in-a-list", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Volatile: []string{"id"},
			Body: map[string]any{"type": "network", "title": []any{"In a list"}}, Capture: map[string]string{"listed": "id"}},
		{Name: "save-title-flag", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Body: map[string]any{"type": "network", "title": true}},
		{Name: "save-title-object", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few, Body: map[string]any{"type": "network", "title": map[string]any{"a": 1}}},
		{Name: "save-type-in-a-list", Method: "POST", Path: "/api/reports/save", As: "admin", Roles: few,
			Body: map[string]any{"type": []any{"network"}, "title": "Type in a list"}, Capture: map[string]string{"typed": "id"}},
		{Name: "save-type-missing", Method: "POST", Path: "/api/reports/save", Body: map[string]any{"title": "x"}, Roles: writers},
		{Name: "save-type-unknown", Method: "POST", Path: "/api/reports/save", Body: map[string]any{"type": "nope"}, Roles: writers},
		{Name: "save-without-csrf-token", Method: "POST", Path: "/api/reports/save", Body: map[string]any{"type": "network"}, NoCSRF: true, Roles: writers},
		{Name: "read-untitled", Path: "/api/reports/saved/{untitled}", Roles: few, Volatile: []string{"id"}},
		{Name: "read-numbered", Path: "/api/reports/saved/{numbered}", Roles: few, Volatile: []string{"id"}},

		{Name: "delete-as-the-role", Method: "DELETE", Path: "/api/reports/saved/{report}", Roles: writers},
		{Name: "delete", Method: "DELETE", Path: "/api/reports/saved/{report}", As: "admin", Roles: few},
		{Name: "delete-again", Method: "DELETE", Path: "/api/reports/saved/{report}", As: "admin", Roles: few},
		{Name: "read-deleted", Path: "/api/reports/saved/{report}", Roles: few},
		{Name: "delete-untitled", Method: "DELETE", Path: "/api/reports/saved/{untitled}", As: "admin", Roles: few},
		{Name: "delete-numbered", Method: "DELETE", Path: "/api/reports/saved/{numbered}", As: "admin", Roles: few},
		{Name: "delete-listed", Method: "DELETE", Path: "/api/reports/saved/{listed}", As: "admin", Roles: few},
		{Name: "delete-typed", Method: "DELETE", Path: "/api/reports/saved/{typed}", As: "admin", Roles: few},
		{Name: "delete-id-not-a-number", Method: "DELETE", Path: "/api/reports/saved/abc", Roles: writers},
		{Name: "delete-missing", Method: "DELETE", Path: "/api/reports/saved/999999999", Roles: writers},
		{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/reports/saved/999999999", NoCSRF: true, Roles: writers},
		{Name: "list-after-deletes", Path: "/api/reports/saved", Roles: few, Volatile: []string{"~[*].payload"}},
	}
	contract.Group("reports", cases...)
}
