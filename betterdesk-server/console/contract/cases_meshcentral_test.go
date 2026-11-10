//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The MeshCentral routes under /api/mesh and /api/session/recordings. They
// pass the request on to the API server and its answer back. No mesh agent
// is connected on a test instance, so every tunnel, share link, power
// action and command ends at "mesh agent not connected".
//
// What the group writes it puts back: the group list is saved as it was
// read, the device's group is set and cleared.
//
// A query whose parameter has a toString member
// (?mesh_share[toString]=x on the desktop route) is left out: it stops
// the Node console (BUGS.md MC1).
func init() {
	const device = "/api/mesh/devices/900100001"
	few := contract.Roles("signed-out", "viewer", "admin")
	ends := contract.Roles("signed-out", "admin")
	admin := contract.Roles("admin")
	empty := map[string]any{}

	contract.Group("meshcentral",
		contract.Case{Name: "status", Path: "/api/mesh/status"},
		contract.Case{Name: "status-head", Method: "HEAD", Path: "/api/mesh/status", Roles: ends},
		contract.Case{Name: "server-id", Path: "/api/mesh/server-id"},

		contract.Case{
			Name: "groups", Path: "/api/mesh/groups",
			Capture: map[string]string{"mesh": "groups[0].mesh_id"},
		},
		contract.Case{
			Name: "groups-save", Method: "POST", Path: "/api/mesh/groups",
			Body: map[string]any{"groups": []any{map[string]any{"id": "default", "name": "Default Mesh", "mesh_id": "{mesh}"}}},
		},
		contract.Case{
			Name: "groups-save-wrong-type", Method: "POST", Path: "/api/mesh/groups",
			Body: map[string]any{"groups": "x"}, Roles: contract.Roles("viewer", "server_admin", "admin"),
		},
		contract.Case{
			Name: "groups-save-list-body", Method: "POST", Path: "/api/mesh/groups",
			RawBody: `[1, 2]`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{
			Name: "groups-save-without-csrf-token", Method: "POST", Path: "/api/mesh/groups",
			Body: map[string]any{"groups": "x"}, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "groups-save-as-form", Method: "POST", Path: "/api/mesh/groups",
			RawBody: "groups=x", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},

		contract.Case{Name: "download", Path: "/api/mesh/download.msh"},
		contract.Case{
			Name: "download-named", Path: "/api/mesh/download.msh",
			Query: url.Values{"name": {"Contract Mesh"}, "mesh_server": {"wss://example.test/agent.ashx"}},
			Roles: contract.Roles("viewer", "server_admin", "admin"),
		},
		contract.Case{Name: "download-head", Method: "HEAD", Path: "/api/mesh/download.msh", Roles: ends},
		contract.Case{
			Name: "download-not-modified", Path: "/api/mesh/download.msh",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: ends,
		},
		contract.Case{
			Name: "download-query-of-lists", Path: "/api/mesh/download.msh",
			Query: url.Values{"name": {"a", "b"}, "mesh_id[x]": {"1"}}, Roles: admin,
		},
		contract.Case{
			Name: "download-query-that-cannot-be-text", Path: "/api/mesh/download.msh",
			Query: url.Values{"name[toString]": {"x"}}, Roles: few,
		},

		contract.Case{Name: "share-validate-nothing", Path: "/api/mesh/share/validate"},
		contract.Case{
			Name: "share-validate-unknown", Path: "/api/mesh/share/validate",
			Query: url.Values{"token": {"abc"}, "peer_id": {"900100001"}},
		},
		contract.Case{
			Name: "share-validate-lists", Path: "/api/mesh/share/validate",
			Query: url.Values{"token[]": {"abc"}, "peer_id[x]": {"1"}}, Roles: ends,
		},
		contract.Case{
			Name: "share-validate-token-only", Path: "/api/mesh/share/validate",
			Query: url.Values{"token": {"abc"}}, Roles: ends,
		},
		contract.Case{
			Name: "share-validate-query-that-cannot-be-text", Path: "/api/mesh/share/validate",
			Query: url.Values{"token[toString]": {"x"}}, Roles: ends,
		},
		contract.Case{
			Name: "share-validate-head", Method: "HEAD", Path: "/api/mesh/share/validate",
			Query: url.Values{"token": {"abc"}, "peer_id": {"900100001"}}, Roles: ends,
		},

		contract.Case{Name: "desktop", Method: "POST", Path: device + "/desktop", Body: empty},
		contract.Case{
			Name: "desktop-unknown-share", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"mesh_share": {"abc"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-unknown-guest", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"guest": {"abc"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-unknown-t", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"t": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-share-before-guest", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"mesh_share": {"abc"}, "guest": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-blank-tokens", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"mesh_share": {" "}, "guest": {" "}}, Body: empty, Roles: few,
		},
		contract.Case{
			Name: "desktop-share-as-list", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"mesh_share[]": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-guest-as-object", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"guest[x]": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-as-form", Method: "POST", Path: device + "/desktop",
			RawBody: "a=1", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},

		contract.Case{Name: "terminal", Method: "POST", Path: device + "/terminal", Body: empty},
		contract.Case{Name: "files", Method: "POST", Path: device + "/files", Body: empty},
		contract.Case{
			Name: "share", Method: "POST", Path: device + "/share",
			Body: map[string]any{"ttl_minutes": 5, "view_only": true},
		},
		contract.Case{
			Name: "share-wrong-type", Method: "POST", Path: device + "/share",
			Body: map[string]any{"ttl_minutes": "five"}, Roles: contract.Roles("viewer", "operator", "admin"),
		},
		contract.Case{
			Name: "tcp", Method: "POST", Path: device + "/tcp",
			Body: map[string]any{"host": "127.0.0.1", "port": 22},
		},
		contract.Case{
			Name: "udp", Method: "POST", Path: device + "/udp",
			Body: map[string]any{"host": "127.0.0.1", "port": 53},
		},
		contract.Case{
			Name: "power", Method: "POST", Path: device + "/power",
			Body: map[string]any{"action": "reboot"},
		},
		contract.Case{
			Name: "power-unknown-action", Method: "POST", Path: device + "/power",
			Body: map[string]any{"action": "dance"}, Roles: contract.Roles("viewer", "operator", "admin"),
		},
		contract.Case{
			Name: "group-assign", Method: "POST", Path: device + "/group",
			Body: map[string]any{"group_id": "default"},
		},
		contract.Case{
			Name: "group-clear", Method: "POST", Path: device + "/group",
			Body: map[string]any{"group_id": ""},
		},
		contract.Case{
			Name: "group-wrong-type", Method: "POST", Path: device + "/group",
			Body: map[string]any{"group_id": 5}, Roles: admin,
		},
		contract.Case{
			Name: "exec", Method: "POST", Path: device + "/exec",
			Body: map[string]any{"command": "whoami"},
		},
		contract.Case{
			Name: "exec-without-csrf-token", Method: "POST", Path: device + "/exec",
			Body: map[string]any{"command": "whoami"}, NoCSRF: true, Roles: few,
		},

		// What the device id of the address becomes on the way to the API
		// server
		contract.Case{
			Name: "device-id-dot", Method: "POST", Path: "/api/mesh/devices/%2e/terminal",
			Body: empty, Roles: few,
		},
		contract.Case{
			Name: "device-id-two-dots", Method: "POST", Path: "/api/mesh/devices/%2e%2e/terminal",
			Body: empty, Roles: few,
		},
		contract.Case{
			Name: "device-id-with-slash", Method: "POST", Path: "/api/mesh/devices/a%2Fb/terminal",
			Body: empty, Roles: admin,
		},
		contract.Case{
			Name: "device-id-with-space", Method: "POST", Path: "/api/mesh/devices/a%20b%3Fc/files",
			Body: empty, Roles: admin,
		},
		contract.Case{
			Name: "device-id-bad-escape", Method: "POST", Path: "/api/mesh/devices/%ff/terminal",
			Body: empty, Roles: few,
		},

		contract.Case{Name: "recordings", Path: "/api/mesh/recordings"},
		contract.Case{Name: "recording-missing", Path: "/api/mesh/recordings/nothing.webm"},
		contract.Case{Name: "recording-id-dot", Path: "/api/mesh/recordings/%2e", Roles: few},
		contract.Case{Name: "recording-id-two-dots", Path: "/api/mesh/recordings/%2e%2e", Roles: admin},
		contract.Case{Name: "session-recordings", Path: "/api/session/recordings"},
	)
}

// The desktop route with a guest link that exists. The link is made and
// deleted through the guest routes, which are another group: with
// CONTRACT_REQUIRE_GO these two steps fail for as long as Node answers
// them, which is why the cases are a group of their own.
func init() {
	const device = "/api/mesh/devices/900100001"
	ends := contract.Roles("signed-out", "admin")
	empty := map[string]any{}

	contract.Group("meshcentral-guest",
		contract.Case{
			Name: "guest-link", Method: "POST", Path: "/api/guest/access-links", As: "admin",
			Body:    map[string]any{"peer_ids": []any{"900100001"}, "ttl_minutes": 5, "view_only": true, "label": "contract mesh"},
			Capture: map[string]string{"guest": "token", "guestid": "id"},
		},
		contract.Case{
			Name: "desktop-guest", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"guest": {"{guest}"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-guest-as-t", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"t": {"{guest}"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-guest-of-another-device", Method: "POST", Path: "/api/mesh/devices/900100002/desktop",
			Query: url.Values{"guest": {"{guest}"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-guest-without-csrf-token", Method: "POST", Path: device + "/desktop",
			Query: url.Values{"guest": {"{guest}"}}, Body: empty, NoCSRF: true, Roles: ends,
		},
		contract.Case{Name: "guest-link-delete", Method: "DELETE", Path: "/api/guest/access-links/{guestid}", As: "admin"},
	)
}
