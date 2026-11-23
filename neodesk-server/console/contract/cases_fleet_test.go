//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The fleet routes pass every call on to the API server, which has no
// fleet functions: a call that gets there comes back as 404 {"error":"not
// found"}. The cases show who gets that far, what is refused before the
// call (a parameter that cannot go into a path), and how the query of the
// two list routes is read. The same routes answer under /api/nd.
func init() {
	body := map[string]any{"name": "contract", "device_id": "900100001", "command": "echo"}
	contract.Group("fleet", append([]contract.Case{
		contract.Case{Name: "availability", Path: "/api/panel/fleet/availability"},
		contract.Case{Name: "availability-head", Method: "HEAD", Path: "/api/panel/fleet/availability"},
		contract.Case{Name: "availability-other-spelling", Path: "/API/Panel/Fleet/Availability/"},
		contract.Case{
			Name: "availability-not-modified", Path: "/api/panel/fleet/availability",
			Headers: map[string]string{"If-None-Match": "*"},
		},
		contract.Case{Name: "availability-second-mount", Path: "/api/nd/api/panel/fleet/availability"},

		// Resource mapping
		contract.Case{Name: "resources", Path: "/api/panel/fleet/resources/acme"},
		contract.Case{Name: "resources-id-with-escapes", Path: "/api/panel/fleet/resources/a%3Ab%40c%2Bd"},
		contract.Case{Name: "resources-bad-org", Path: "/api/panel/fleet/resources/bad%20id"},
		contract.Case{Name: "resources-org-with-slash", Path: "/api/panel/fleet/resources/a%2Fb"},
		contract.Case{Name: "resources-org-two-dots", Path: "/api/panel/fleet/resources/%2E%2E"},
		contract.Case{Name: "resources-org-dots-inside", Path: "/api/panel/fleet/resources/a..b"},
		contract.Case{Name: "resources-org-too-long", Path: "/api/panel/fleet/resources/" + strings.Repeat("a", 257)},
		contract.Case{Name: "resources-add", Method: "POST", Path: "/api/panel/fleet/resources/acme", Body: body},
		contract.Case{Name: "resources-add-bad-org", Method: "POST", Path: "/api/panel/fleet/resources/bad%20id", Body: body},
		contract.Case{Name: "resources-change", Method: "PUT", Path: "/api/panel/fleet/resources/acme/res-1", Body: body},
		contract.Case{Name: "resources-change-bad-org", Method: "PUT", Path: "/api/panel/fleet/resources/bad%20org/bad%20res", Body: body},
		contract.Case{Name: "resources-change-bad-resource", Method: "PUT", Path: "/api/panel/fleet/resources/acme/bad%20res", Body: body},
		contract.Case{Name: "resources-delete", Method: "DELETE", Path: "/api/panel/fleet/resources/acme/res-1"},
		contract.Case{Name: "resources-delete-bad-resource", Method: "DELETE", Path: "/api/panel/fleet/resources/acme/bad%20res"},

		// Tasks
		contract.Case{Name: "tasks", Path: "/api/panel/fleet/tasks"},
		contract.Case{
			Name: "tasks-filtered", Path: "/api/panel/fleet/tasks",
			Query: url.Values{"orgId": {"acme"}, "status": {"running"}, "deviceId": {"900100001"}},
		},
		contract.Case{Name: "tasks-empty-filters", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId": {""}, "status": {""}, "deviceId": {""}}},
		contract.Case{Name: "tasks-bad-org", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId": {"bad id"}}},
		contract.Case{Name: "tasks-bad-device", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId": {"acme"}, "deviceId": {"a/b"}}},
		contract.Case{Name: "tasks-org-twice", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId": {"acme", "other"}}},
		contract.Case{Name: "tasks-org-as-list", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId[]": {"acme"}}},
		contract.Case{Name: "tasks-org-as-empty-list", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId[]": {""}}},
		contract.Case{Name: "tasks-org-as-object", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId[name]": {"acme"}}},
		contract.Case{Name: "tasks-org-without-text", Path: "/api/panel/fleet/tasks", Query: url.Values{"orgId[toString]": {"acme"}}},
		contract.Case{Name: "tasks-status-twice", Path: "/api/panel/fleet/tasks", Query: url.Values{"status": {"running", "failed"}}},
		contract.Case{Name: "tasks-status-long", Path: "/api/panel/fleet/tasks", Query: url.Values{"status": {strings.Repeat("s", 80) + " &=?"}}},
		contract.Case{Name: "tasks-status-without-text", Path: "/api/panel/fleet/tasks", Query: url.Values{"status[toString]": {"x"}}},
		contract.Case{Name: "task", Path: "/api/panel/fleet/tasks/task-1"},
		contract.Case{Name: "task-bad-id", Path: "/api/panel/fleet/tasks/bad%20id"},
		contract.Case{Name: "task-create", Method: "POST", Path: "/api/panel/fleet/tasks", Body: body},
		contract.Case{Name: "task-create-empty", Method: "POST", Path: "/api/panel/fleet/tasks", Body: map[string]any{}},
		contract.Case{
			Name: "task-create-list", Method: "POST", Path: "/api/panel/fleet/tasks",
			RawBody: `["a","b"]`, ContentType: "application/json",
		},
		contract.Case{Name: "task-create-without-csrf-token", Method: "POST", Path: "/api/panel/fleet/tasks", Body: body, NoCSRF: true},
		contract.Case{
			Name: "task-create-as-form", Method: "POST", Path: "/api/panel/fleet/tasks",
			RawBody: "name=contract", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "task-create-malformed-json", Method: "POST", Path: "/api/panel/fleet/tasks",
			RawBody: `{"name":`, ContentType: "application/json",
		},
		contract.Case{Name: "task-change", Method: "PUT", Path: "/api/panel/fleet/tasks/task-1", Body: body},
		contract.Case{Name: "task-change-bad-id", Method: "PUT", Path: "/api/panel/fleet/tasks/bad%20id", Body: body},
		contract.Case{Name: "task-delete", Method: "DELETE", Path: "/api/panel/fleet/tasks/task-1"},
		contract.Case{Name: "task-delete-bad-id", Method: "DELETE", Path: "/api/panel/fleet/tasks/bad%20id"},
		contract.Case{Name: "task-output", Path: "/api/panel/fleet/tasks/task-1/output"},
		contract.Case{Name: "task-output-bad-id", Path: "/api/panel/fleet/tasks/bad%20id/output"},
		contract.Case{Name: "task-retry", Method: "POST", Path: "/api/panel/fleet/tasks/task-1/retry", Body: map[string]any{}},
		contract.Case{Name: "task-retry-bad-id", Method: "POST", Path: "/api/panel/fleet/tasks/bad%20id/retry", Body: map[string]any{}},

		// Workflows
		contract.Case{Name: "workflows", Path: "/api/panel/fleet/workflows"},
		contract.Case{Name: "workflow", Path: "/api/panel/fleet/workflows/wf-1"},
		contract.Case{Name: "workflow-bad-id", Path: "/api/panel/fleet/workflows/bad%20id"},
		contract.Case{Name: "workflow-create", Method: "POST", Path: "/api/panel/fleet/workflows", Body: body},
		contract.Case{Name: "workflow-change", Method: "PUT", Path: "/api/panel/fleet/workflows/wf-1", Body: body},
		contract.Case{Name: "workflow-change-bad-id", Method: "PUT", Path: "/api/panel/fleet/workflows/bad%20id", Body: body},
		contract.Case{Name: "workflow-delete", Method: "DELETE", Path: "/api/panel/fleet/workflows/wf-1"},
		contract.Case{Name: "workflow-delete-bad-id", Method: "DELETE", Path: "/api/panel/fleet/workflows/bad%20id"},
		contract.Case{Name: "workflow-execute", Method: "POST", Path: "/api/panel/fleet/workflows/wf-1/execute", Body: body},
		contract.Case{Name: "workflow-execute-bad-id", Method: "POST", Path: "/api/panel/fleet/workflows/bad%20id/execute", Body: body},
		contract.Case{Name: "workflow-history", Path: "/api/panel/fleet/workflows/wf-1/history"},
		contract.Case{Name: "workflow-history-bad-id", Path: "/api/panel/fleet/workflows/bad%20id/history"},

		// Compliance
		contract.Case{Name: "compliance", Path: "/api/panel/fleet/compliance"},
		contract.Case{Name: "compliance-of-org", Path: "/api/panel/fleet/compliance", Query: url.Values{"orgId": {"acme"}}},
		contract.Case{Name: "compliance-bad-org", Path: "/api/panel/fleet/compliance", Query: url.Values{"orgId": {"bad id"}}},
		contract.Case{Name: "compliance-org-twice", Path: "/api/panel/fleet/compliance", Query: url.Values{"orgId": {"acme", "acme"}}},
		contract.Case{Name: "compliance-org-without-text", Path: "/api/panel/fleet/compliance", Query: url.Values{"orgId[toString]": {"acme"}}},
		contract.Case{Name: "compliance-device", Path: "/api/panel/fleet/compliance/900100001"},
		contract.Case{Name: "compliance-device-bad-id", Path: "/api/panel/fleet/compliance/bad%20id"},
		contract.Case{Name: "compliance-scan", Method: "POST", Path: "/api/panel/fleet/compliance/900100001/scan", Body: map[string]any{}},
		contract.Case{Name: "compliance-scan-bad-id", Method: "POST", Path: "/api/panel/fleet/compliance/bad%20id/scan", Body: map[string]any{}},
		contract.Case{Name: "compliance-remediate", Method: "POST", Path: "/api/panel/fleet/compliance/900100001/remediate", Body: body},
		contract.Case{Name: "compliance-remediate-bad-id", Method: "POST", Path: "/api/panel/fleet/compliance/bad%20id/remediate", Body: body},

		// The second mount. Under /api/nd a write without a session is
		// not asked for a CSRF token, one with a session is.
		contract.Case{Name: "second-mount-tasks", Path: "/api/nd/api/panel/fleet/tasks", Query: url.Values{"orgId": {"bad id"}}},
		contract.Case{Name: "second-mount-task-create", Method: "POST", Path: "/api/nd/api/panel/fleet/tasks", Body: body},
		contract.Case{Name: "second-mount-task-create-without-csrf-token", Method: "POST", Path: "/api/nd/api/panel/fleet/tasks", Body: body, NoCSRF: true},
		contract.Case{
			Name: "second-mount-task-create-without-session", Method: "POST", Path: "/api/nd/api/panel/fleet/tasks", Body: body,
			NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "second-mount-resource-delete", Method: "DELETE", Path: "/api/nd/api/panel/fleet/resources/acme/bad%20res"},
		contract.Case{Name: "second-mount-workflow-history", Path: "/api/nd/api/panel/fleet/workflows/wf-1/history"},
	}, fleetDeviceCases()...)...)
}

// fleetDeviceCases are the two routes agents report to, at both mounts.
// They take a device token, which the suite has none of: the headers of a
// case are written into its golden. So the cases show what comes before
// the route: no token, a token nobody issued, another scheme, a device id
// that proves nothing, and the CSRF gate, which under /api/nd lets a write
// without a session through and checks one with a session. The answers
// with a token are pinned by routes/fleet/testdata/device_vectors.json.
func fleetDeviceCases() []contract.Case {
	report := map[string]any{"device_id": "900100001", "task_id": "contract", "status": "done", "output": "ok"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	var cases []contract.Case
	for _, mount := range []struct{ name, prefix string }{{"device", ""}, {"second-mount-device", "/api/nd"}} {
		for _, route := range []struct{ name, path string }{{"task-result", "/api/nd/fleet/task-result"}, {"software", "/api/nd/fleet/software"}} {
			at := func(what string) string { return mount.name + "-" + route.name + "-" + what }
			path := mount.prefix + route.path
			cases = append(cases,
				contract.Case{Name: at("without-token"), Method: "POST", Path: path, Body: report},
				contract.Case{Name: at("unknown-token"), Method: "POST", Path: path, Body: report, Headers: unknown},
				contract.Case{Name: at("other-scheme"), Method: "POST", Path: path, Body: report, Headers: map[string]string{"Authorization": "Basic contract"}},
				contract.Case{Name: at("device-id-alone"), Method: "POST", Path: path, Body: report, Headers: map[string]string{"X-Device-Id": "900100001"}},
				contract.Case{Name: at("without-csrf-token"), Method: "POST", Path: path, Body: report, Headers: unknown, NoCSRF: true},
				contract.Case{
					Name: at("as-a-device"), Method: "POST", Path: path, Body: report, Headers: unknown,
					NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out"),
				},
			)
		}
	}
	return cases
}
