//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Devices: the device list and one device, remote targets, tags, device
// groups and their members, the writes on one device (notes, names, tags,
// ban, delete and restore, a new id, policies), bulk delete and the rules
// for RustDesk clients.
//
// Each role's turn starts with admin putting 900100019 into a device
// group that only e2e_viewer may see, so the lists and checks of operator
// and guest_issuer see a limited scope; the turn ends by taking it out
// and deleting the group. The writes work on 900100018 and 900100019,
// which are in no folder and no manual group in the seed, have no metrics
// and are offline, and every chain puts them back as it found them: tags
// ["laptop","sales"], no note, no display name, user demo.seller, not
// banned, not deleted, the same id.
//
// The routes that reach into a connected agent (services, processes,
// files, input, terminal and the others behind the agent's signal
// socket) are near the end, with the answers for an agent that is not
// connected; they were added with the move of the agent socket.
func init() {
	long := strings.Repeat("x", 600)
	tags21 := make([]any, 21)
	for i := range tags21 {
		tags21[i] = "t" + strings.Repeat("x", i)
	}
	guids101 := make([]any, 101)
	for i := range guids101 {
		guids101[i] = "g"
	}
	// The ids of the device groups a turn makes are counters, which depend
	// on what ran before
	list := func(name string, query string) contract.Case {
		values, err := url.ParseQuery(query)
		if err != nil {
			panic(err)
		}
		return contract.Case{Name: name, Path: "/api/devices", Query: values, Volatile: []string{"data.devices[*].groups[*].id"}}
	}
	contract.Group("devices",
		// Setup: a group only e2e_viewer may see, with 900100019 in it
		contract.Case{
			Name: "setup-restricted", Method: "POST", Path: "/api/device-groups", As: "admin",
			Body:     map[string]any{"name": "Contract restricted", "allowed_users": []any{"e2e_viewer"}},
			Capture:  map[string]string{"restricted": "data.guid"},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "setup-restricted-member", Method: "PUT", Path: "/api/devices/900100019/groups", As: "admin",
			Body: map[string]any{"groupGuids": []any{"{restricted}"}},
		},

		// The list, in a fixed order: the default order is by last contact,
		// which changes while devices report in
		list("list", "sortBy=id&sortOrder=asc"),
		list("list-by-hostname-desc", "sortBy=hostname&sortOrder=DESC"),
		list("list-search", "search=DEMO-LAB&sortBy=id&sortOrder=asc"),
		list("list-search-note", "search=fan%20is&sortBy=id&sortOrder=asc"),
		list("list-search-nothing", "search=no-such-device-anywhere"),
		list("list-online", "status=online&sortBy=id&sortOrder=asc"),
		list("list-offline", "status=offline&sortBy=id&sortOrder=asc"),
		list("list-banned", "status=banned"),
		list("list-unknown-status", "status=sleeping&sortBy=id&sortOrder=asc"),
		list("list-has-notes", "hasNotes=true&sortBy=id&sortOrder=asc"),
		list("list-has-notes-not-true", "hasNotes=TRUE&status=banned"),
		list("list-include-deleted", "includeDeleted=true&sortBy=id&sortOrder=asc"),
		list("list-unknown-sort", "status=banned&sortBy=password&sortOrder=sideways"),
		list("list-sort-by-version", "sortBy=version&sortOrder=asc"),
		list("list-sort-by-note", "hasNotes=true&sortBy=note"),
		list("list-sort-list", "status=banned&sortBy[]=id&sortOrder[]=asc"),
		list("list-search-list", "search[]=demo&sortBy=id&sortOrder=asc"),
		list("list-status-list", "status[]=banned&sortBy=id&sortOrder=asc"),
		list("list-pages-ignored", "page=2&limit=3&offset=5&sortBy=id&sortOrder=asc"),
		// A request with a bearer token belongs to the RustDesk client API
		// in front of the console's route; without one, or with a blank
		// one, the console answers
		contract.Case{
			Name: "list-bearer-unknown-token", Path: "/api/devices",
			Headers: map[string]string{"Authorization": "Bearer contract-no-such-token"},
		},
		contract.Case{
			Name: "list-bearer-blank", Path: "/api/devices", Query: map[string][]string{"status": {"banned"}},
			Headers: map[string]string{"Authorization": "Bearer    "},
		},
		contract.Case{
			Name: "list-basic-auth", Path: "/api/devices", Query: map[string][]string{"status": {"banned"}},
			Headers: map[string]string{"Authorization": "Basic Y29udHJhY3Q6bm90LWEtc2VjcmV0"},
		},
		contract.Case{
			Name: "list-bearer-without-session", Path: "/api/devices", NoSession: true,
			Headers: map[string]string{"Authorization": "Bearer contract-no-such-token"},
		},

		// One device
		// The seeded metrics follow the time of day and the minute the
		// instance started
		contract.Case{Name: "one-online", Path: "/api/devices/900100001", Volatile: []string{"data.metrics", "data.metrics_history"}},
		contract.Case{Name: "one-offline", Path: "/api/devices/900100018"},
		contract.Case{Name: "one-restricted", Path: "/api/devices/900100019", Volatile: []string{"data.groups[*].id"}},
		contract.Case{Name: "one-banned", Path: "/api/devices/900100028"},
		contract.Case{Name: "one-deleted", Path: "/api/devices/900100030"},
		contract.Case{Name: "one-with-note", Path: "/api/devices/900100031"},
		contract.Case{Name: "one-unknown", Path: "/api/devices/999999999"},
		contract.Case{Name: "one-unknown-remote-target", Path: "/api/devices/rt_contract_none"},
		contract.Case{Name: "one-other-spelling", Path: "/API/Devices/900100018/"},
		contract.Case{Name: "availability-empty-day", Path: "/api/devices/900100001/availability", Query: map[string][]string{"day": {"2000-01-01"}, "tz_offset": {"-60"}}},
		contract.Case{Name: "availability-today", Path: "/api/devices/900100018/availability", Query: map[string][]string{"tz_offset": {"0"}}},
		contract.Case{Name: "availability-bad-day", Path: "/api/devices/900100018/availability", Query: map[string][]string{"day": {"yesterday"}}},
		contract.Case{Name: "availability-bad-offset", Path: "/api/devices/900100018/availability", Query: map[string][]string{"day": {"2000-01-01"}, "tz_offset": {"abc"}}},
		contract.Case{Name: "availability-day-list", Path: "/api/devices/900100018/availability", Query: map[string][]string{"day[]": {"2000-01-01"}, "tz_offset": {"0"}}},
		contract.Case{Name: "availability-restricted", Path: "/api/devices/900100019/availability", Query: map[string][]string{"day": {"2000-01-01"}, "tz_offset": {"0"}}},
		contract.Case{Name: "availability-unknown", Path: "/api/devices/999999999/availability"},
		contract.Case{
			Name: "connection-sessions", Path: "/api/devices/900100001/connection-sessions", Query: map[string][]string{"limit": {"3"}},
			// Which sessions the seed gives a device, and how many, depends
			// on the hour it runs at: it puts them where the device was
			// online. The replay of routes/devices pins the list itself.
			// Without compression, so the length of the list decides no
			// header.
			Headers:  map[string]string{"Accept-Encoding": "identity"},
			Volatile: []string{"data.sessions[*].duration_seconds", "~data.sessions", "~data.count"},
		},
		contract.Case{Name: "connection-sessions-none", Path: "/api/devices/900100018/connection-sessions"},
		contract.Case{Name: "connection-sessions-limit-text", Path: "/api/devices/900100018/connection-sessions", Query: map[string][]string{"limit": {"abc"}}},
		contract.Case{Name: "connection-sessions-limit-zero", Path: "/api/devices/900100018/connection-sessions", Query: map[string][]string{"limit": {"0"}}},
		contract.Case{Name: "connection-sessions-unknown", Path: "/api/devices/999999999/connection-sessions"},
		contract.Case{
			Name: "telemetry-refresh", Method: "POST", Path: "/api/devices/900100016/telemetry/refresh", Body: map[string]any{},
			Volatile: []string{"data.command_id"},
		},
		contract.Case{Name: "telemetry-refresh-unknown", Method: "POST", Path: "/api/devices/999999999/telemetry/refresh", Body: map[string]any{}},
		contract.Case{Name: "telemetry-refresh-restricted", Method: "POST", Path: "/api/devices/900100019/telemetry/refresh", Body: map[string]any{}, Roles: contract.Roles("operator")},
		contract.Case{Name: "telemetry-command-unknown-command", Method: "POST", Path: "/api/devices/900100016/telemetry/command", Body: map[string]any{"command": "format.disk"}},
		contract.Case{Name: "telemetry-command-not-text", Method: "POST", Path: "/api/devices/900100016/telemetry/command", Body: map[string]any{"command": 5, "args": "x"}},
		contract.Case{
			Name: "telemetry-command", Method: "POST", Path: "/api/devices/900100016/telemetry/command",
			Body:     map[string]any{"command": "collect.services", "args": map[string]any{"limit": 5}},
			Volatile: []string{"data.command_id"},
		},
		contract.Case{Name: "telemetry-command-unknown", Method: "POST", Path: "/api/devices/999999999/telemetry/command", Body: map[string]any{"command": "collect.services"}},

		// Remote targets: one of the role's own, with made-up values. The
		// credential vault is not set up on a test instance, so no
		// credential is stored
		contract.Case{Name: "remote-targets", Path: "/api/remote-targets"},
		contract.Case{Name: "remote-targets-with-disabled", Path: "/api/remote-targets", Query: map[string][]string{"include_disabled": {"true"}}},
		// L83: a route of the Go console only, recorded from Go: whether the
		// vault has a key and guacd answers (neither on a test instance)
		contract.Case{Name: "remote-target-readiness", Path: "/api/remote-targets/readiness"},
		contract.Case{Name: "remote-target-create-invalid", Method: "POST", Path: "/api/remote-targets", Body: map[string]any{"name": "", "protocol": "vnc"}},
		contract.Case{Name: "remote-target-create-list-body", Method: "POST", Path: "/api/remote-targets", RawBody: `[{"name":"In a list"}]`, ContentType: "application/json"},
		contract.Case{
			Name: "remote-target-create", Method: "POST", Path: "/api/remote-targets",
			Body: map[string]any{
				"name": "Contract VNC", "protocol": "vnc", "host": "vnc.example.invalid", "port": 5900,
				"username": "contract-user", "enabled": false,
			},
			Capture: map[string]string{"target": "data.id"},
		},
		contract.Case{Name: "remote-targets-after-create", Path: "/api/remote-targets", Query: map[string][]string{"include_disabled": {"true"}}},
		contract.Case{Name: "remote-target", Path: "/api/remote-targets/{target}"},
		contract.Case{Name: "remote-target-as-device", Path: "/api/devices/{target}"},
		contract.Case{Name: "remote-target-unknown", Path: "/api/remote-targets/rt_contract_none"},
		contract.Case{Name: "remote-target-update", Method: "PATCH", Path: "/api/remote-targets/{target}", Body: map[string]any{"name": "Contract VNC renamed", "port": 5901}},
		contract.Case{Name: "remote-target-update-invalid", Method: "PATCH", Path: "/api/remote-targets/{target}", Body: map[string]any{"protocol": "telnet"}},
		contract.Case{Name: "remote-target-update-unknown", Method: "PATCH", Path: "/api/remote-targets/rt_contract_none", Body: map[string]any{"name": "x"}},
		contract.Case{Name: "remote-target-credentials", Path: "/api/remote-targets/{target}/credentials"},
		contract.Case{Name: "remote-target-credentials-unknown", Path: "/api/remote-targets/rt_contract_none/credentials"},
		contract.Case{
			Name: "remote-target-credentials-set", Method: "PUT", Path: "/api/remote-targets/{target}/credentials",
			Body: map[string]any{"username": "contract-user", "password": "contract-made-up-value"},
		},
		contract.Case{Name: "remote-target-credentials-set-unknown", Method: "PUT", Path: "/api/remote-targets/rt_contract_none/credentials", Body: map[string]any{}},
		contract.Case{Name: "remote-target-credentials-clear", Method: "DELETE", Path: "/api/remote-targets/{target}/credentials"},
		contract.Case{Name: "remote-target-credentials-clear-unknown", Method: "DELETE", Path: "/api/remote-targets/rt_contract_none/credentials"},
		contract.Case{Name: "remote-target-certificate-invalid", Method: "POST", Path: "/api/remote-targets/{target}/certificate/accept", Body: map[string]any{"fingerprint": "not hex"}},
		contract.Case{Name: "remote-target-certificate-not-text", Method: "POST", Path: "/api/remote-targets/{target}/certificate/accept", Body: map[string]any{"fingerprint": []any{"ab"}}},
		contract.Case{
			Name: "remote-target-certificate", Method: "POST", Path: "/api/remote-targets/{target}/certificate/accept",
			Body: map[string]any{"fingerprint": "AB:CD:" + strings.Repeat("ef", 30)},
		},
		contract.Case{Name: "remote-target-certificate-unknown", Method: "POST", Path: "/api/remote-targets/rt_contract_none/certificate/accept", Body: map[string]any{"fingerprint": "ab"}},
		contract.Case{Name: "remote-target-test-blocked", Method: "POST", Path: "/api/remote-targets/test", Body: map[string]any{"name": "Contract", "protocol": "vnc", "host": "localhost", "port": 5900}},
		contract.Case{Name: "remote-target-test-invalid", Method: "POST", Path: "/api/remote-targets/test", Body: map[string]any{"protocol": "vnc"}},
		contract.Case{Name: "remote-target-delete", Method: "DELETE", Path: "/api/remote-targets/{target}"},
		contract.Case{Name: "remote-target-after-delete", Path: "/api/remote-targets/{target}"},
		contract.Case{Name: "remote-target-delete-unknown", Method: "DELETE", Path: "/api/remote-targets/rt_contract_none"},

		// Tags: a tag only 900100018 and a tag group of the turn carry
		contract.Case{Name: "tags", Path: "/api/tags"},
		contract.Case{
			Name: "setup-tag-group", Method: "POST", Path: "/api/device-groups", As: "admin",
			Body:     map[string]any{"name": "Contract tag group", "source_type": "tag", "tag_filter": "contract-tag"},
			Capture:  map[string]string{"taggroup": "data.guid"},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "setup-tag", Method: "PUT", Path: "/api/devices/900100018/tags", As: "admin",
			Body: map[string]any{"tags": []any{"laptop", "sales", "contract-tag"}},
		},
		contract.Case{Name: "tags-with-the-tag", Path: "/api/tags"},
		contract.Case{Name: "tag-rename-without-names", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{}},
		contract.Case{Name: "tag-rename-blank", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": "  ", "to": "x"}},
		contract.Case{Name: "tag-rename-identical", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": " contract-tag ", "to": "contract-tag"}},
		contract.Case{Name: "tag-rename-list", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": []any{"contract-tag"}, "to": "x"}},
		contract.Case{Name: "tag-rename-unknown", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": "no-device-has-this", "to": "nor-this"}},
		contract.Case{Name: "tag-rename", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": "CONTRACT-TAG", "to": "contract-tag-2"}},
		contract.Case{Name: "groups-after-rename", Path: "/api/device-groups", Volatile: []string{"data.groups[*].id"}},
		contract.Case{Name: "tag-delete-without-tag", Method: "POST", Path: "/api/tags/delete", Body: map[string]any{"tag": ""}},
		contract.Case{Name: "tag-delete-number", Method: "POST", Path: "/api/tags/delete", Body: map[string]any{"tag": 42}},
		contract.Case{Name: "tag-delete", Method: "POST", Path: "/api/tags/delete", Body: map[string]any{"tag": "contract-tag-2"}},
		contract.Case{Name: "one-after-tag-delete", Path: "/api/devices/900100018"},
		contract.Case{Name: "set-tags-not-a-list", Method: "PUT", Path: "/api/devices/900100018/tags", Body: map[string]any{"tags": "laptop,sales"}},
		contract.Case{Name: "set-tags-too-many", Method: "PUT", Path: "/api/devices/900100018/tags", Body: map[string]any{"tags": tags21}},
		contract.Case{Name: "set-tags-unknown", Method: "PUT", Path: "/api/devices/999999999/tags", Body: map[string]any{"tags": []any{"x"}}},
		contract.Case{Name: "set-tags-restricted", Method: "PUT", Path: "/api/devices/900100019/tags", Body: map[string]any{"tags": []any{"laptop", "sales"}}},
		contract.Case{
			Name: "set-tags", Method: "PUT", Path: "/api/devices/900100018/tags",
			Body: map[string]any{"tags": []any{" laptop ", "", 5, nil, "sales", "  ", "contract-" + strings.Repeat("y", 60)}},
		},
		contract.Case{Name: "set-tags-back", Method: "PUT", Path: "/api/devices/900100018/tags", Body: map[string]any{"tags": []any{"laptop", "sales"}}},
		contract.Case{
			Name: "cleanup-tag", Method: "PUT", Path: "/api/devices/900100018/tags", As: "admin",
			Body: map[string]any{"tags": []any{"laptop", "sales"}},
		},
		contract.Case{Name: "cleanup-tag-group", Method: "DELETE", Path: "/api/device-groups/{taggroup}", As: "admin"},

		// Device groups
		contract.Case{Name: "groups", Path: "/api/device-groups", Volatile: []string{"data.groups[*].id"}},
		contract.Case{Name: "group-create-without-name", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"note": "nameless"}},
		contract.Case{Name: "group-create-tag-without-filter", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "dynamic": true}},
		contract.Case{Name: "group-create-bad-user-group", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "allowed_groups": []any{"bad guid!"}}},
		contract.Case{Name: "group-create-folder-guid", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "guid": "folder_1"}},
		contract.Case{Name: "group-update-bad-guid", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "guid": "bad guid!"}},
		contract.Case{Name: "group-update-unknown", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "guid": "contract-no-such-group"}},
		contract.Case{Name: "group-create-list-body", Method: "POST", Path: "/api/device-groups", RawBody: `[{"name":"In a list"}]`, ContentType: "application/json"},
		contract.Case{
			Name: "group-create", Method: "POST", Path: "/api/device-groups",
			Body: map[string]any{
				"name": "  Contract group ", "note": " A note ", "team_id": "contract-team",
				"allowed_users":  []any{"e2e_operator", "nobody-has-this-name", " e2e_operator "},
				"allowed_groups": []any{"contract-no-such-user-group"},
			},
			Capture:  map[string]string{"group": "data.guid"},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "group-update", Method: "POST", Path: "/api/device-groups",
			Body:     map[string]any{"guid": "{group}", "name": "Contract group renamed", "source_type": "tag", "tag_filter": " kiosk "},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "group-update-access", Method: "POST", Path: "/api/device-groups",
			Body:     map[string]any{"guid": "{group}", "name": "Contract group renamed", "allowed_users": "e2e_viewer, e2e_operator", "allowed_groups": []any{}},
			Volatile: []string{"data.id"},
		},
		contract.Case{
			Name: "group-create-many-user-groups", Method: "POST", Path: "/api/device-groups",
			Body:     map[string]any{"name": "Contract many", "allowed_groups": guids101},
			Capture:  map[string]string{"many": "data.guid"},
			Volatile: []string{"data.id"},
		},
		contract.Case{Name: "groups-after-create", Path: "/api/device-groups", Volatile: []string{"data.groups[*].id"}},
		contract.Case{Name: "group-delete-bad-guid", Method: "DELETE", Path: "/api/device-groups/bad$guid"},
		contract.Case{Name: "group-delete-unknown", Method: "DELETE", Path: "/api/device-groups/contract-no-such-group"},
		contract.Case{Name: "group-delete-many", Method: "DELETE", Path: "/api/device-groups/{many}"},

		// Members of groups
		contract.Case{Name: "memberships-manual", Path: "/api/devices/900100022/groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "memberships-tag", Path: "/api/devices/900100018/groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "memberships-restricted", Path: "/api/devices/900100019/groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "memberships-unknown", Path: "/api/devices/999999999/groups"},
		contract.Case{Name: "set-groups-bad-guid", Method: "PUT", Path: "/api/devices/900100018/groups", Body: map[string]any{"groupGuids": []any{"bad guid!"}}},
		contract.Case{Name: "set-groups-too-many", Method: "PUT", Path: "/api/devices/900100018/groups", Body: map[string]any{"groupGuids": guids101}},
		contract.Case{Name: "set-groups-unknown-device", Method: "PUT", Path: "/api/devices/999999999/groups", Body: map[string]any{"groupGuids": []any{}}},
		contract.Case{Name: "set-groups-restricted", Method: "PUT", Path: "/api/devices/900100019/groups", Body: map[string]any{"groupGuids": []any{}}, Roles: contract.Roles("operator")},
		contract.Case{
			Name: "set-groups-two-manual", Method: "PUT", Path: "/api/devices/900100018/groups",
			Body: map[string]any{"groupGuids": []any{"{group}", "{restricted}"}},
		},
		contract.Case{
			Name: "set-groups", Method: "PUT", Path: "/api/devices/900100018/groups",
			Body: map[string]any{"groupGuids": []any{"{group}", "{group}", "contract-no-such-group", "{taggroup}"}},
		},
		contract.Case{Name: "memberships-after-set", Path: "/api/devices/900100018/groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "one-in-a-group", Path: "/api/devices/900100018", Volatile: []string{"data.groups[*].id"}},
		contract.Case{Name: "set-groups-not-a-list", Method: "PUT", Path: "/api/devices/900100018/groups", Body: map[string]any{"groupGuids": "{group}"}},
		contract.Case{Name: "memberships-after-clear", Path: "/api/devices/900100018/groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "group-delete", Method: "DELETE", Path: "/api/device-groups/{group}"},
		contract.Case{Name: "group-delete-again", Method: "DELETE", Path: "/api/device-groups/{group}"},
		contract.Case{Name: "cleanup-group", Method: "DELETE", Path: "/api/device-groups/{group}", As: "admin"},
		contract.Case{Name: "cleanup-many", Method: "DELETE", Path: "/api/device-groups/{many}", As: "admin"},

		// A folder's group cannot be changed here
		contract.Case{Name: "group-update-folder-group", Method: "POST", Path: "/api/device-groups", Body: map[string]any{"name": "Contract", "guid": "folder_7"}},

		// Changing a device
		contract.Case{Name: "update-unknown", Method: "PATCH", Path: "/api/devices/999999999", Body: map[string]any{"note": "x"}},
		contract.Case{Name: "update-restricted", Method: "PATCH", Path: "/api/devices/900100019", Body: map[string]any{"note": "x"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "update-nothing", Method: "PATCH", Path: "/api/devices/900100018", Body: map[string]any{}},
		contract.Case{
			Name: "update", Method: "PATCH", Path: "/api/devices/900100018",
			Body: map[string]any{"note": "  Contract note " + long, "display_name": " Contract name ", "user": "contract-user"},
		},
		contract.Case{Name: "one-after-update", Path: "/api/devices/900100018"},
		contract.Case{Name: "list-after-update", Path: "/api/devices", Query: map[string][]string{"hasNotes": {"true"}, "sortBy": {"id"}, "sortOrder": {"asc"}}, Volatile: []string{"data.devices[*].groups[*].id"}},
		contract.Case{Name: "update-odd-types", Method: "PATCH", Path: "/api/devices/900100018", Body: map[string]any{"note": nil, "display_name": 5}},
		contract.Case{Name: "one-after-odd-types", Path: "/api/devices/900100018"},
		contract.Case{Name: "update-back", Method: "PATCH", Path: "/api/devices/900100018", Body: map[string]any{"note": "", "display_name": "", "user": "demo.seller"}},
		contract.Case{Name: "rename-without-name", Method: "POST", Path: "/api/devices/999999999/rename", Body: map[string]any{"display_name": "  "}},
		contract.Case{Name: "rename-unknown", Method: "POST", Path: "/api/devices/999999999/rename", Body: map[string]any{"display_name": "x"}},
		contract.Case{Name: "rename", Method: "POST", Path: "/api/devices/900100018/rename", Body: map[string]any{"display_name": " Contract renamed "}},
		contract.Case{Name: "one-after-rename", Path: "/api/devices/900100018"},
		contract.Case{Name: "rename-back", Method: "PATCH", Path: "/api/devices/900100018", Body: map[string]any{"display_name": ""}},

		// Ban
		contract.Case{Name: "ban-unknown", Method: "POST", Path: "/api/devices/999999999/ban", Body: map[string]any{"reason": "x"}},
		contract.Case{Name: "ban-restricted", Method: "POST", Path: "/api/devices/900100019/ban", Body: map[string]any{}, Roles: contract.Roles("operator")},
		contract.Case{Name: "ban", Method: "POST", Path: "/api/devices/900100018/ban", Body: map[string]any{"reason": "  Contract ban  "}},
		contract.Case{Name: "one-banned-by-the-console", Path: "/api/devices/900100018"},
		contract.Case{Name: "list-banned-after-ban", Path: "/api/devices", Query: map[string][]string{"status": {"banned"}, "sortBy": {"id"}, "sortOrder": {"asc"}}, Volatile: []string{"data.devices[*].groups[*].id"}},
		contract.Case{Name: "unban", Method: "POST", Path: "/api/devices/900100018/unban", Body: map[string]any{}},
		contract.Case{Name: "unban-unknown", Method: "POST", Path: "/api/devices/999999999/unban", Body: map[string]any{}},
		contract.Case{Name: "ban-reason-not-text", Method: "POST", Path: "/api/devices/900100018/ban", Body: map[string]any{"reason": []any{"x"}}},
		contract.Case{Name: "unban-again", Method: "POST", Path: "/api/devices/900100018/unban", Body: map[string]any{}},
		// "Issue a new token" (L45): the device's tokens end and a new one
		// is due. 900100018 holds none of the stack's device tokens, which
		// other groups use. Recorded from Go (CONTRACT_RECORD_NEW): the
		// route is new.
		contract.Case{Name: "issue-device-token", Method: "POST", Path: "/api/devices/900100018/device-token", Body: map[string]any{}},
		contract.Case{Name: "issue-device-token-unknown", Method: "POST", Path: "/api/devices/999999999/device-token", Body: map[string]any{}},
		contract.Case{Name: "issue-device-token-restricted", Method: "POST", Path: "/api/devices/900100019/device-token", Body: map[string]any{}, Roles: contract.Roles("operator")},

		// Delete and restore
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: "/api/devices/999999999", Query: map[string][]string{"revoke": {"true"}}},
		contract.Case{Name: "delete-unknown-hard", Method: "DELETE", Path: "/api/devices/999999999", Query: map[string][]string{"hard": {"true"}}},
		contract.Case{Name: "delete-restricted", Method: "DELETE", Path: "/api/devices/900100019", Roles: contract.Roles("operator")},
		contract.Case{Name: "restore-active", Method: "POST", Path: "/api/devices/900100018/restore", Body: map[string]any{}},
		contract.Case{Name: "restore-unknown", Method: "POST", Path: "/api/devices/999999999/restore", Body: map[string]any{}},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/devices/900100018", Query: map[string][]string{"cascade": {"true"}}},
		contract.Case{Name: "one-after-delete", Path: "/api/devices/900100018"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/devices/900100018"},
		contract.Case{Name: "restore", Method: "POST", Path: "/api/devices/900100018/restore", Body: map[string]any{}},
		contract.Case{Name: "bulk-delete-not-a-list", Method: "POST", Path: "/api/devices/bulk-delete", Body: map[string]any{"ids": "900100018"}},
		contract.Case{Name: "bulk-delete-nothing", Method: "POST", Path: "/api/devices/bulk-delete", Body: map[string]any{"ids": []any{}}},
		contract.Case{Name: "bulk-delete-unknown", Method: "POST", Path: "/api/devices/bulk-delete", Body: map[string]any{"ids": []any{"900100018", "999999998"}}},
		contract.Case{Name: "bulk-delete-restricted", Method: "POST", Path: "/api/devices/bulk-delete", Body: map[string]any{"ids": []any{"900100018", "900100019"}}, Roles: contract.Roles("operator")},
		contract.Case{Name: "bulk-delete", Method: "POST", Path: "/api/devices/bulk-delete", Body: map[string]any{"ids": []any{"900100018", "900100019"}}},
		contract.Case{Name: "list-after-bulk-delete", Path: "/api/devices", Query: map[string][]string{"search": {"demo-sales"}, "sortBy": {"id"}, "sortOrder": {"asc"}}, Volatile: []string{"data.devices[*].groups[*].id"}},
		contract.Case{Name: "restore-first", Method: "POST", Path: "/api/devices/900100018/restore", Body: map[string]any{}},
		contract.Case{Name: "restore-second", Method: "POST", Path: "/api/devices/900100019/restore", Body: map[string]any{}},

		// A new id, and back
		contract.Case{Name: "change-id-missing", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{}},
		contract.Case{Name: "change-id-short", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "12345"}},
		contract.Case{Name: "change-id-long", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "12345678901234567"}},
		contract.Case{Name: "change-id-bad-characters", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "contract id"}},
		contract.Case{Name: "change-id-number", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": 1234567}},
		contract.Case{Name: "change-id-unknown-device", Method: "POST", Path: "/api/devices/999999999/change-id", Body: map[string]any{"newId": "contract-new-1"}},
		contract.Case{Name: "change-id-taken", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "900100018"}},
		contract.Case{Name: "change-id-deleted", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "900100030"}},
		contract.Case{Name: "change-id", Method: "POST", Path: "/api/devices/900100019/change-id", Body: map[string]any{"newId": "900199019"}},
		contract.Case{Name: "one-after-change-id", Path: "/api/devices/900199019"},
		contract.Case{Name: "change-id-back", Method: "POST", Path: "/api/devices/900199019/change-id", Body: map[string]any{"newId": "900100019"}},
		contract.Case{Name: "one-with-the-old-id", Path: "/api/devices/900199019"},

		// Access policy, connect password, connection mode
		contract.Case{Name: "access-policy", Path: "/api/devices/900100018/access-policy"},
		contract.Case{Name: "access-policy-unknown", Path: "/api/devices/999999999/access-policy"},
		contract.Case{Name: "access-policy-restricted", Path: "/api/devices/900100019/access-policy", Roles: contract.Roles("operator", "viewer")},
		contract.Case{
			Name: "access-policy-save", Method: "PUT", Path: "/api/devices/900100018/access-policy",
			Body: map[string]any{"unattended_enabled": true, "allowed_operators": "e2e_admin", "schedule_enabled": false},
		},
		contract.Case{Name: "access-policy-after-save", Path: "/api/devices/900100018/access-policy"},
		contract.Case{Name: "access-policy-save-unknown", Method: "PUT", Path: "/api/devices/999999999/access-policy", Body: map[string]any{}},
		contract.Case{Name: "access-policy-delete", Method: "DELETE", Path: "/api/devices/900100018/access-policy"},
		contract.Case{Name: "access-policy-delete-unknown", Method: "DELETE", Path: "/api/devices/999999999/access-policy"},
		contract.Case{Name: "access-policy-after-delete", Path: "/api/devices/900100018/access-policy"},
		contract.Case{Name: "connect-password", Path: "/api/devices/900100018/connect-password"},
		contract.Case{Name: "connect-password-unknown", Path: "/api/devices/999999999/connect-password"},
		contract.Case{Name: "connection-mode-bad-mode", Method: "POST", Path: "/api/devices/900100018/connection-mode", Body: map[string]any{"mode": "outgoing-only", "reason": "x"}},
		contract.Case{Name: "connection-mode-no-reason", Method: "POST", Path: "/api/devices/900100018/connection-mode", Body: map[string]any{"mode": " incoming-only ", "reason": "   "}},
		contract.Case{Name: "connection-mode-unknown", Method: "POST", Path: "/api/devices/999999999/connection-mode", Body: map[string]any{"mode": "normal", "reason": "x"}},
		contract.Case{Name: "connection-mode-without-agent", Method: "POST", Path: "/api/devices/900100018/connection-mode", Body: map[string]any{"mode": "incoming-only", "reason": "Contract check"}},

		// Rules for RustDesk clients
		contract.Case{Name: "strategies", Path: "/api/panel/strategies"},
		contract.Case{Name: "strategy-create-without-name", Method: "POST", Path: "/api/panel/strategies", Body: map[string]any{"name": "  "}},
		contract.Case{Name: "strategy-create-bad-user-group", Method: "POST", Path: "/api/panel/strategies", Body: map[string]any{"name": "Contract", "user_group_guid": "bad guid!"}},
		contract.Case{Name: "strategy-create-bad-device-group", Method: "POST", Path: "/api/panel/strategies", Body: map[string]any{"name": "Contract", "device_group_guid": "bad guid!"}},
		contract.Case{
			Name: "strategy-create", Method: "POST", Path: "/api/panel/strategies",
			Body: map[string]any{
				"name": " Contract rule " + strings.Repeat("r", 90), "enabled": 0,
				"permissions": map[string]any{"enable-file-transfer": true, "disable-clipboard": "n", "enable-audio": " custom ", "enable-tunnel": 5, "enable-camera": ""},
			},
			Capture: map[string]string{"rule": "data.strategy.guid"},
		},
		contract.Case{Name: "strategies-after-create", Path: "/api/panel/strategies"},
		contract.Case{Name: "strategy", Path: "/api/panel/strategies/{rule}"},
		contract.Case{Name: "strategy-bad-guid", Path: "/api/panel/strategies/bad$guid"},
		contract.Case{Name: "strategy-unknown", Path: "/api/panel/strategies/contract-no-such-rule"},
		contract.Case{Name: "strategy-update", Method: "PATCH", Path: "/api/panel/strategies/{rule}", Body: map[string]any{"name": "Contract rule renamed", "enabled": true, "permissions": map[string]any{"enable-clipboard": "Y"}}},
		contract.Case{Name: "strategy-update-blank-name", Method: "PATCH", Path: "/api/panel/strategies/{rule}", Body: map[string]any{"name": ""}},
		contract.Case{Name: "strategy-update-unknown", Method: "PATCH", Path: "/api/panel/strategies/contract-no-such-rule", Body: map[string]any{"name": "x"}},
		contract.Case{Name: "strategy-update-bad-guid", Method: "PATCH", Path: "/api/panel/strategies/bad$guid", Body: map[string]any{"name": "x"}},
		contract.Case{Name: "strategy-assign-nothing", Method: "POST", Path: "/api/panel/strategies/{rule}/assign", Body: map[string]any{"peers": []any{}}},
		contract.Case{Name: "strategy-assign-unknown-peer", Method: "POST", Path: "/api/panel/strategies/{rule}/assign", Body: map[string]any{"peers": []any{"999999999"}}},
		contract.Case{Name: "strategy-assign-unknown-rule", Method: "POST", Path: "/api/panel/strategies/contract-no-such-rule/assign", Body: map[string]any{"peers": []any{"900100018"}}},
		contract.Case{
			Name: "strategy-assign", Method: "POST", Path: "/api/panel/strategies/{rule}/assign",
			Body:     map[string]any{"peers": []any{"900100018"}, "users": []any{"e2e_viewer"}, "groups": []any{"{restricted}"}},
			Volatile: []string{"data.summary.peers[*]", "data.summary.users[*]", "data.summary.groups[*]"},
		},
		contract.Case{
			Name: "strategy-after-assign", Path: "/api/panel/strategies/{rule}",
			Volatile: []string{"data.strategy.peers[*]", "data.strategy.users[*]", "data.strategy.groups[*]", "data.strategy.group_guids[*]"},
		},
		contract.Case{Name: "strategy-unassign-nothing", Method: "POST", Path: "/api/panel/strategies/unassign", Body: map[string]any{}},
		contract.Case{Name: "strategy-unassign-unknown-user", Method: "POST", Path: "/api/panel/strategies/unassign", Body: map[string]any{"users": []any{"contract-nobody"}}},
		contract.Case{
			Name: "strategy-unassign", Method: "POST", Path: "/api/panel/strategies/unassign",
			Body: map[string]any{"peers": []any{"900100018"}, "users": []any{"e2e_viewer"}, "groups": []any{"{restricted}"}},
		},
		contract.Case{Name: "strategy-after-unassign", Path: "/api/panel/strategies/{rule}"},
		contract.Case{Name: "strategy-delete-bad-guid", Method: "DELETE", Path: "/api/panel/strategies/bad$guid"},
		contract.Case{Name: "strategy-delete-unknown", Method: "DELETE", Path: "/api/panel/strategies/contract-no-such-rule"},
		contract.Case{Name: "strategy-delete", Method: "DELETE", Path: "/api/panel/strategies/{rule}"},
		contract.Case{Name: "strategies-at-the-end", Path: "/api/panel/strategies"},
		contract.Case{Name: "cleanup-rule", Method: "DELETE", Path: "/api/panel/strategies/{rule}", As: "admin"},

		// The routes that reach into a running agent over its signal
		// socket. No agent is connected to a test instance, so these are
		// the answers for an agent that is offline: the snapshot the server
		// has, a collection queued for later, or 503. The answers of a
		// connected agent are pinned by the Node recordings the package
		// replays (routes/devices/testdata/node_agent.json).
		contract.Case{Name: "agent-services", Path: "/api/devices/900100001/services"},
		contract.Case{Name: "agent-services-offline", Path: "/api/devices/900100018/services"},
		contract.Case{Name: "agent-services-restricted", Path: "/api/devices/900100019/services"},
		contract.Case{Name: "agent-services-unknown", Path: "/api/devices/999999999/services"},
		contract.Case{Name: "agent-processes", Path: "/api/devices/900100001/processes"},
		contract.Case{Name: "agent-events", Path: "/api/devices/900100001/events", Query: map[string][]string{"limit": {"5"}}},
		contract.Case{Name: "agent-events-limit-text", Path: "/api/devices/900100018/events", Query: map[string][]string{"limit": {"abc"}}},
		contract.Case{Name: "agent-activity", Path: "/api/devices/900100001/activity"},
		contract.Case{Name: "agent-clipboard", Path: "/api/devices/900100001/clipboard"},
		contract.Case{Name: "agent-clipboard-unknown", Path: "/api/devices/999999999/clipboard"},
		contract.Case{Name: "agent-files-browse", Method: "POST", Path: "/api/devices/900100018/files/browse", Body: map[string]any{"path": "/tmp", "show_hidden": true}},
		contract.Case{Name: "agent-files-browse-restricted", Method: "POST", Path: "/api/devices/900100019/files/browse", Body: map[string]any{}},
		contract.Case{Name: "agent-files-read", Method: "POST", Path: "/api/devices/900100018/files/read", Body: map[string]any{"path": "/etc/hostname", "offset": 3, "length": 10}},
		contract.Case{Name: "agent-files-write-without-path", Method: "POST", Path: "/api/devices/900100018/files/write", Body: map[string]any{"data": "eA=="}},
		contract.Case{Name: "agent-files-write", Method: "POST", Path: "/api/devices/900100018/files/write", Body: map[string]any{"path": "/tmp/contract.txt", "data": "eA==", "mode": "append"}},
		contract.Case{Name: "agent-files-delete-without-path", Method: "POST", Path: "/api/devices/900100018/files/delete", Body: map[string]any{}},
		contract.Case{Name: "agent-files-delete", Method: "POST", Path: "/api/devices/900100018/files/delete", Body: map[string]any{"path": "/tmp/contract.txt", "recursive": true}},
		contract.Case{Name: "agent-files-rename-without-paths", Method: "POST", Path: "/api/devices/900100018/files/rename", Body: map[string]any{"from": "/tmp/a"}},
		contract.Case{Name: "agent-files-rename", Method: "POST", Path: "/api/devices/900100018/files/rename", Body: map[string]any{"from": "/tmp/a", "to": "/tmp/b"}},
		contract.Case{Name: "agent-files-mkdir-without-path", Method: "POST", Path: "/api/devices/900100018/files/mkdir", Body: map[string]any{"recursive": false}},
		contract.Case{Name: "agent-files-mkdir", Method: "POST", Path: "/api/devices/900100018/files/mkdir", Body: map[string]any{"path": "/tmp/new"}},
		contract.Case{Name: "agent-clipboard-set", Method: "POST", Path: "/api/devices/900100018/clipboard", Body: map[string]any{"text": "contract"}},
		contract.Case{Name: "agent-clipboard-set-not-text", Method: "POST", Path: "/api/devices/900100018/clipboard", Body: map[string]any{"text": 5}},
		contract.Case{Name: "agent-clipboard-set-too-long", Method: "POST", Path: "/api/devices/900100018/clipboard", Body: map[string]any{"text": strings.Repeat("c", 1024*1024+1)}},
		contract.Case{Name: "agent-screenshot", Method: "POST", Path: "/api/devices/900100018/screenshot", Body: map[string]any{}},
		contract.Case{Name: "agent-input-mouse", Method: "POST", Path: "/api/devices/900100018/input/mouse", Body: map[string]any{"action": "click", "x": 10.7, "y": 20, "button": "left"}},
		contract.Case{Name: "agent-input-key-without-key", Method: "POST", Path: "/api/devices/900100018/input/key", Body: map[string]any{}},
		contract.Case{Name: "agent-input-key", Method: "POST", Path: "/api/devices/900100018/input/key", Body: map[string]any{"key": "Enter"}},
		contract.Case{Name: "agent-input-text-without-text", Method: "POST", Path: "/api/devices/900100018/input/text", Body: map[string]any{"text": ""}},
		contract.Case{Name: "agent-input-text", Method: "POST", Path: "/api/devices/900100018/input/text", Body: map[string]any{"text": "hello"}},
		contract.Case{Name: "agent-terminal-blank", Method: "POST", Path: "/api/devices/900100018/terminal/execute", Body: map[string]any{"command": "   "}},
		contract.Case{Name: "agent-terminal", Method: "POST", Path: "/api/devices/900100018/terminal/execute", Body: map[string]any{"command": "uptime"}},
		contract.Case{Name: "agent-terminal-unknown", Method: "POST", Path: "/api/devices/999999999/terminal/execute", Body: map[string]any{"command": "uptime"}},

		// The device scope check in front of every address below
		// /api/devices/{id} (audit H9), for the routes that had no case of
		// a device the account may not see: it answers before the route's
		// sign-in and permission checks, so operator, who may not see
		// 900100019, gets the scope refusal also where it lacks the
		// permission. Recorded from the specification tree's Node console
		// (upstream 3.5.264); the writes run as operator only.
		contract.Case{Name: "connection-sessions-restricted", Path: "/api/devices/900100019/connection-sessions", Query: map[string][]string{"limit": {"5"}}},
		contract.Case{Name: "telemetry-command-restricted", Method: "POST", Path: "/api/devices/900100019/telemetry/command", Body: map[string]any{"command": "collect.services"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "connection-mode-restricted", Method: "POST", Path: "/api/devices/900100019/connection-mode", Body: map[string]any{"mode": "normal", "reason": "x"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "unban-restricted", Method: "POST", Path: "/api/devices/900100019/unban", Body: map[string]any{}, Roles: contract.Roles("operator")},
		contract.Case{Name: "access-policy-save-restricted", Method: "PUT", Path: "/api/devices/900100019/access-policy", Body: map[string]any{}, Roles: contract.Roles("operator")},
		contract.Case{Name: "access-policy-delete-restricted", Method: "DELETE", Path: "/api/devices/900100019/access-policy", Roles: contract.Roles("operator")},
		contract.Case{Name: "connect-password-restricted", Path: "/api/devices/900100019/connect-password"},
		contract.Case{Name: "rename-restricted", Method: "POST", Path: "/api/devices/900100019/rename", Body: map[string]any{"display_name": "x"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "memberships-other-spelling-restricted", Path: "/API/Devices/900100019/Groups", Volatile: []string{"data.groups[*].id", "data.memberships[*].id"}},
		contract.Case{Name: "agent-processes-restricted", Path: "/api/devices/900100019/processes"},
		contract.Case{Name: "agent-events-restricted", Path: "/api/devices/900100019/events"},
		contract.Case{Name: "agent-activity-restricted", Path: "/api/devices/900100019/activity"},
		contract.Case{Name: "agent-clipboard-restricted", Path: "/api/devices/900100019/clipboard"},
		contract.Case{Name: "agent-screenshot-restricted", Method: "POST", Path: "/api/devices/900100019/screenshot", Body: map[string]any{}, Roles: contract.Roles("operator")},
		contract.Case{Name: "agent-clipboard-set-restricted", Method: "POST", Path: "/api/devices/900100019/clipboard", Body: map[string]any{"text": "x"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "agent-input-key-restricted", Method: "POST", Path: "/api/devices/900100019/input/key", Body: map[string]any{"key": "Enter"}, Roles: contract.Roles("operator")},
		contract.Case{Name: "agent-terminal-restricted", Method: "POST", Path: "/api/devices/900100019/terminal/execute", Body: map[string]any{"command": "uptime"}, Roles: contract.Roles("operator")},
		// The number of sessions the server is asked for is held to 1..200
		// (audit M5); the routes/devices replay pins the limit it sends.
		// 900100018 has no sessions: up to 200 sessions of a device the
		// seed gives some would make the answer's size, and so its
		// compression, depend on the hour
		contract.Case{
			Name: "connection-sessions-limit-negative", Path: "/api/devices/900100001/connection-sessions", Query: map[string][]string{"limit": {"-5"}},
			Volatile: []string{"data.sessions[*].duration_seconds", "~data.sessions", "~data.count"},
		},
		contract.Case{Name: "connection-sessions-limit-huge", Path: "/api/devices/900100018/connection-sessions", Query: map[string][]string{"limit": {"100000"}}},

		// Cleanup
		contract.Case{
			Name: "cleanup-restricted-member", Method: "PUT", Path: "/api/devices/900100019/groups", As: "admin",
			Body: map[string]any{"groupGuids": []any{}},
		},
		contract.Case{Name: "cleanup-restricted", Method: "DELETE", Path: "/api/device-groups/{restricted}", As: "admin"},
		list("list-at-the-end", "sortBy=id&sortOrder=asc"),
	)
}
