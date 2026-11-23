//go:build contract

package contract_test

import (
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The RustDesk client API on the console port (routes/rustdesk-api.routes.js).
// Clients sign in with POST /api/login and send the access token they get
// as "Authorization: Bearer". A case cannot sign in: the password would be
// written into its golden. So the cases show everything that needs no
// token: the refused sign-ins and their shapes, the second step without a
// pending sign-in, the 401 of every route with no token, an unknown token
// and other schemes, the five routes that leave a request without a
// bearer token to the console route of the same address, and the public
// routes a client calls without signing in (heartbeat, system information,
// audit uploads, keys) with made-up and seeded devices. The flows with a
// token are recorded in routes/rustdeskapi/testdata/flow_<db>.json.
//
// Writes are left to two devices: 900100031 (a seeded device nobody else
// uses) gets heartbeats, system information and the audit uploads that
// are taken, and the refused audit uploads use host ids no other group
// reads. A client report is taken only with the uuid the device registered
// with (demo-uuid-<id> for the seeded devices; BUGS RV2, RV7). Failed sign-ins run for one role only,
// because every one of them counts against the address of the suite.
func init() {
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	unknown64 := map[string]string{"Authorization": "Bearer " + strings.Repeat("0", 64)}
	basic := map[string]string{"Authorization": "Basic Y29udHJhY3Q6eA=="}
	blank := map[string]string{"Authorization": "Bearer    "}
	lower := map[string]string{"Authorization": "bearer contract-no-such-token"}
	once := contract.Roles("signed-out")
	some := contract.Roles("signed-out", "viewer", "admin")
	nobody := "rd_contract_nobody"

	cases := []contract.Case{
		// Sign-in: what is refused before the password is checked
		{Name: "login-without-body", Method: "POST", Path: "/api/login"},
		{Name: "login-empty", Method: "POST", Path: "/api/login", Body: map[string]any{}, Roles: some},
		{Name: "login-username-only", Method: "POST", Path: "/api/login", Body: map[string]any{"username": nobody}, Roles: some},
		{Name: "login-password-only", Method: "POST", Path: "/api/login", Body: map[string]any{"password": "x"}, Roles: some},
		{Name: "login-blank-username", Method: "POST", Path: "/api/login", Body: map[string]any{"username": "   ", "password": "x"}, Roles: some},
		{Name: "login-password-not-text", Method: "POST", Path: "/api/login", Body: map[string]any{"username": nobody, "password": 12345}, Roles: some},
		{Name: "login-username-not-text", Method: "POST", Path: "/api/login", Body: map[string]any{"username": []string{nobody}, "password": "x"}, Roles: some},
		{Name: "login-list-body", Method: "POST", Path: "/api/login", RawBody: `["a","b"]`, ContentType: "application/json", Roles: some},
		{Name: "login-malformed-json", Method: "POST", Path: "/api/login", RawBody: `{"username":`, ContentType: "application/json", Roles: some},
		{Name: "login-without-csrf-token", Method: "POST", Path: "/api/login", Body: map[string]any{}, NoCSRF: true},
		{
			Name: "login-as-a-device", Method: "POST", Path: "/api/login", Body: map[string]any{"username": "", "password": ""},
			NoCSRF: true, NoCookies: true, Roles: once,
		},
		// A banned device is refused before the password is checked
		{Name: "login-banned-device", Method: "POST", Path: "/api/login", Body: map[string]any{"username": nobody, "password": "x", "id": "900100028"}, Roles: some},
		{Name: "login-banned-device-other-spelling", Method: "POST", Path: "/API/Login/", Body: map[string]any{"username": nobody, "password": "x", "id": "900100029", "uuid": "u"}, Roles: once},
		// Wrong passwords
		{Name: "login-unknown-user", Method: "POST", Path: "/api/login", Body: map[string]any{"username": nobody, "password": "not-the-password"}, Roles: once},
		{
			Name: "login-unknown-user-with-client", Method: "POST", Path: "/api/login",
			Body:  map[string]any{"username": "  " + nobody + "  ", "password": "not-the-password", "id": "900100030", "uuid": "contract-uuid", "type": "account"},
			Roles: once,
		},
		{
			Name: "login-as-form", Method: "POST", Path: "/api/login",
			RawBody: "username=" + nobody + "&password=not-the-password", ContentType: "application/x-www-form-urlencoded", Roles: once,
		},
		// The second step without a pending sign-in
		{Name: "login-code-unknown-secret", Method: "POST", Path: "/api/login", Body: map[string]any{"tfaCode": "123456", "secret": "contract-unknown"}, Roles: some},
		{Name: "login-verification-code-unknown-secret", Method: "POST", Path: "/api/login", Body: map[string]any{"verificationCode": "123456", "secret": "contract-unknown", "username": nobody}, Roles: some},
		{Name: "login-code-number", Method: "POST", Path: "/api/login", Body: map[string]any{"tfaCode": 123456, "secret": 7}, Roles: some},
		{Name: "login-code-without-secret", Method: "POST", Path: "/api/login", Body: map[string]any{"tfaCode": "123456"}, Roles: some},
		{Name: "login-code-empty-secret", Method: "POST", Path: "/api/login", Body: map[string]any{"tfaCode": "123456", "secret": "", "username": nobody}, Roles: some},

		// Sign-out answers {} whatever it gets
		{Name: "logout-without-token", Method: "POST", Path: "/api/logout", Body: map[string]any{"id": "x", "uuid": "y"}, Roles: some},
		{Name: "logout-unknown-token", Method: "POST", Path: "/api/logout", Body: map[string]any{}, Headers: unknown, Roles: some},
		{Name: "logout-unknown-token-of-64", Method: "POST", Path: "/api/logout", Headers: unknown64, Roles: some},
		{Name: "logout-without-csrf-token", Method: "POST", Path: "/api/logout", Body: map[string]any{}, Headers: unknown, NoCSRF: true},

		// The account behind a token, for every method
		{Name: "current-user-without-token", Path: "/api/currentUser"},
		{Name: "current-user-unknown-token", Path: "/api/currentUser", Headers: unknown, Roles: some},
		{Name: "current-user-unknown-token-of-64", Method: "POST", Path: "/api/currentUser", Headers: unknown64, Roles: some},
		{Name: "current-user-blank-bearer", Path: "/api/currentUser", Headers: blank, Roles: some},
		{Name: "current-user-lower-case-scheme", Path: "/api/currentUser", Headers: lower, Roles: some},
		{Name: "current-user-basic", Path: "/api/currentUser", Headers: basic, Roles: some},
		{Name: "current-user-put", Method: "PUT", Path: "/api/currentUser", Headers: unknown, Roles: some},
		{Name: "current-user-delete", Method: "DELETE", Path: "/api/currentUser", Roles: some},
		{Name: "current-user-other-spelling", Path: "/API/CURRENTUSER/", Headers: unknown, Roles: some},

		{Name: "login-options", Path: "/api/login-options"},
		{Name: "login-options-head", Method: "HEAD", Path: "/api/login-options", Roles: some},
		{Name: "login-options-not-modified", Path: "/api/login-options", Headers: map[string]string{"If-None-Match": "*"}, Roles: some},

		// Address books
		{Name: "ab-without-token", Path: "/api/ab", Roles: some},
		{Name: "ab-unknown-token", Path: "/api/ab", Headers: unknown, Roles: some},
		{Name: "ab-blank-bearer", Path: "/api/ab", Headers: blank, Roles: some},
		{Name: "ab-save-without-token", Method: "POST", Path: "/api/ab", Body: map[string]any{"data": "{}"}, Roles: some},
		{Name: "ab-save-unknown-token", Method: "POST", Path: "/api/ab", Body: map[string]any{"data": "{}"}, Headers: unknown64, NoCSRF: true, Roles: some},
		{Name: "ab-personal-without-token", Path: "/api/ab/personal", Roles: some},
		{Name: "ab-personal-unknown-token", Path: "/api/ab/personal", Headers: unknown, Roles: some},
		{Name: "ab-personal-probe-without-token", Method: "POST", Path: "/api/ab/personal", Roles: some},
		{Name: "ab-personal-save-unknown-token", Method: "POST", Path: "/api/ab/personal", Body: map[string]any{"data": "{}"}, Headers: unknown, Roles: some},
		{Name: "ab-tags-without-token", Path: "/api/ab/tags", Roles: some},
		{Name: "ab-tags-unknown-token", Path: "/api/ab/tags", Headers: unknown64, Roles: some},

		// The lists. Without a bearer token GET /api/users, /api/peers,
		// /api/strategies, /api/strategies/:guid and /api/devices are the
		// console's routes; with one they are the client API's.
		{Name: "users-unknown-token", Path: "/api/users", Headers: unknown},
		{Name: "users-basic", Path: "/api/users", Headers: basic, Roles: contract.Roles("signed-out", "viewer")},
		{Name: "users-blank-bearer", Path: "/api/users", Headers: blank, Roles: contract.Roles("signed-out", "viewer")},
		{Name: "peers-without-token", Path: "/api/peers"},
		{Name: "peers-unknown-token", Path: "/api/peers", Headers: unknown},
		{Name: "peers-basic", Path: "/api/peers", Headers: basic, Roles: some},
		{Name: "peers-unknown-token-with-query", Path: "/api/peers", Query: url.Values{"folder": {"Office"}, "page": {"2"}}, Headers: unknown64, Roles: some},
		{Name: "peers-list-without-token", Path: "/api/peers/list", Roles: some},
		{Name: "peers-list-post-without-token", Method: "POST", Path: "/api/peers/list", Body: map[string]any{"folder_id": 1}, Roles: some},
		{Name: "peers-list-put-unknown-token", Method: "PUT", Path: "/api/peers/list", Headers: unknown, Roles: some},
		{Name: "device-group-accessible-without-token", Path: "/api/device-group/accessible", Roles: some},
		{Name: "group-without-token", Path: "/api/group", Roles: some},
		{Name: "group-get-without-token", Path: "/api/group/get", Roles: some},
		{Name: "group-get-post-unknown-token", Method: "POST", Path: "/api/group/get", Headers: unknown, Roles: some},
		{Name: "device-group-without-token", Path: "/api/device-group", Roles: some},
		{Name: "device-group-save-without-token", Method: "POST", Path: "/api/device-group", Body: map[string]any{"name": "contract"}, Roles: some},
		{Name: "device-group-save-unknown-token", Method: "POST", Path: "/api/device-group", Body: map[string]any{"name": "contract"}, Headers: unknown, NoCSRF: true, Roles: some},
		{Name: "user-group-without-token", Path: "/api/user/group", Roles: some},
		{Name: "user-groups-without-token", Path: "/api/user-groups", Roles: some},
		{Name: "user-groups-save-unknown-token", Method: "POST", Path: "/api/user-groups", Body: map[string]any{"name": "contract"}, Headers: unknown, Roles: some},
		{Name: "strategies-without-token", Path: "/api/strategies"},
		{Name: "strategies-unknown-token", Path: "/api/strategies", Headers: unknown},
		{Name: "strategies-basic", Path: "/api/strategies", Headers: basic, Roles: some},
		{Name: "strategies-save-without-token", Method: "POST", Path: "/api/strategies", Body: map[string]any{"name": "contract"}, Roles: some},
		{Name: "strategy-without-token", Path: "/api/strategies/contract-guid"},
		{Name: "strategy-unknown-token", Path: "/api/strategies/contract-guid", Headers: unknown},
		{Name: "strategy-delete-without-token", Method: "DELETE", Path: "/api/strategies/contract-guid", Roles: some},
		{Name: "strategies-assign-without-token", Method: "POST", Path: "/api/strategies/assign", Body: map[string]any{"strategy": "x", "peers": []string{"900100001"}}, Roles: some},
		{Name: "strategy-status-without-token", Method: "PUT", Path: "/api/strategies/contract-guid/status", Body: map[string]any{}, Roles: some},
		{Name: "devices-unknown-token", Path: "/api/devices", Headers: unknown},
		{Name: "devices-basic", Path: "/api/devices", Headers: basic, Roles: once},
		{Name: "devices-blank-bearer", Path: "/api/devices", Headers: blank, Roles: once},
		{Name: "peer-key-without-token", Path: "/api/peer-key/900100001", Roles: some},
		{Name: "peer-key-unknown-token", Path: "/api/peer-key/900100001", Headers: unknown64, Roles: some},

		// Audit summary and the reads of the uploads
		{Name: "audit-without-token", Path: "/api/audit", Roles: some},
		{Name: "audit-unknown-token", Path: "/api/audit", Headers: unknown, Roles: some},
		{Name: "audit-file-without-token", Path: "/api/audit/file", Roles: some},
		{Name: "audit-alarm-without-token", Path: "/api/audit/alarm", Query: url.Values{"alarm_type": {"1"}}, Roles: some},
		{Name: "audit-conn-unknown-token", Path: "/api/audit/conn", Headers: unknown, Roles: some},

		// Audit uploads: connections
		{Name: "audit-conn-empty", Method: "POST", Path: "/api/audit/conn", Body: map[string]any{}, Roles: some},
		{Name: "audit-conn-host-null", Method: "POST", Path: "/api/audit/conn", Body: map[string]any{"host_id": nil}, Roles: some},
		{Name: "audit-conn-bad-type", Method: "POST", Path: "/api/audit/conn", Body: map[string]any{"host_id": "rdc-host-1", "conn_type": 9}, Roles: some},
		{Name: "audit-conn-type-fraction", Method: "POST", Path: "/api/audit/conn", Body: map[string]any{"host_id": "rdc-host-1", "conn_type": 1.5}, Roles: some},
		{
			Name: "audit-conn", Method: "POST", Path: "/api/audit/conn",
			Body: map[string]any{
				"host_id": "rdc-host-1", "host_uuid": "rdc-uuid-1", "peer_id": "rdc-peer-1", "peer_name": "Contract peer",
				"action": "new", "conn_type": 1, "session_id": "s-1", "ip": "198.51.100.7",
			},
		},
		{
			Name: "audit-conn-numbers-and-defaults", Method: "POST", Path: "/api/audit/conn", NoCSRF: true,
			Body:  map[string]any{"host_id": 12345, "host_uuid": 6, "peer_id": 7, "conn_type": "2", "peer_name": strings.Repeat("n", 300)},
			Roles: some,
		},
		{
			Name: "audit-conn-from-the-device", Method: "POST", Path: "/api/audit/conn",
			Body: map[string]any{
				"host_id": "900100031", "uuid": base64.StdEncoding.EncodeToString([]byte("demo-uuid-900100031")), "peer_id": "rdc-peer-1",
				"action": "new", "conn_type": 1, "session_id": "s-31", "ip": "198.51.100.7",
			},
			Roles: some,
		},
		{
			Name: "audit-conn-other-uuid", Method: "POST", Path: "/api/audit/conn",
			Body:  map[string]any{"host_id": "900100031", "uuid": "demo-uuid-900100030", "conn_type": 1},
			Roles: some,
		},
		{Name: "audit-conn-list", Path: "/api/audit/conn", Query: url.Values{"host_id": {"rdc-host-1"}}},
		{Name: "audit-conn-list-paged", Path: "/api/audit/conn", Query: url.Values{"host_id": {"12345"}, "limit": {"1"}, "offset": {"1"}}, Roles: some},
		{Name: "audit-conn-list-odd-numbers", Path: "/api/audit/conn", Query: url.Values{"peer_id": {"rdc-peer-1"}, "action": {"new"}, "limit": {"abc"}, "offset": {"-5"}}, Roles: some},

		// Audit uploads: file transfers
		{Name: "audit-file-empty", Method: "POST", Path: "/api/audit/file", Body: map[string]any{}, Roles: some},
		{Name: "audit-file-host-number", Method: "POST", Path: "/api/audit/file", Body: map[string]any{"host_id": 5}, Roles: some},
		{
			Name: "audit-file", Method: "POST", Path: "/api/audit/file",
			Body: map[string]any{
				"host_id": "rdc-host-2", "host_uuid": "rdc-uuid-2", "peer_id": "rdc-peer-2", "direction": 1, "path": "C:/data",
				"is_file": false, "num_files": 20000, "files": []any{"a.txt", map[string]any{"name": "b.txt", "size": 12}, nil, 7, map[string]any{"size": -1}},
				"ip": "198.51.100.8", "peer_name": "Contract peer 2",
			},
			Roles: some,
		},
		{Name: "audit-file-odd-values", Method: "POST", Path: "/api/audit/file", Body: map[string]any{"host_id": "rdc-host-2", "direction": "1", "is_file": 0, "num_files": "3", "files": "x"}, Roles: some},
		{
			Name: "audit-file-from-the-device", Method: "POST", Path: "/api/audit/file",
			Body:  map[string]any{"host_id": "900100031", "host_uuid": "demo-uuid-900100031", "peer_id": "rdc-peer-2", "direction": 1, "path": "C:/data", "files": []any{"a.txt"}},
			Roles: some,
		},

		// Audit uploads: alarms
		{Name: "audit-alarm-bad-type", Method: "POST", Path: "/api/audit/alarm", Body: map[string]any{"alarm_type": 7}, Roles: some},
		{Name: "audit-alarm-type-text", Method: "POST", Path: "/api/audit/alarm", Body: map[string]any{"alarm_type": "9", "host_id": "rdc-host-3"}, Roles: some},
		{
			Name: "audit-alarm", Method: "POST", Path: "/api/audit/alarm",
			Body:  map[string]any{"alarm_type": 2, "alarm_name": "IP violation", "host_id": "rdc-host-3", "peer_id": "rdc-peer-3", "ip": "198.51.100.9", "details": map[string]any{"b": 1, "a": []int{1, 2}}},
			Roles: some,
		},
		{Name: "audit-alarm-details-text", Method: "POST", Path: "/api/audit/alarm", Body: map[string]any{"alarm_type": 6, "host_id": "rdc-host-3", "details": "plain"}, Roles: some},
		{
			Name: "audit-alarm-from-the-device", Method: "POST", Path: "/api/audit/alarm",
			Body:  map[string]any{"alarm_type": 2, "host_id": "900100031", "uuid": "demo-uuid-900100031", "details": map[string]any{"a": 1}},
			Roles: some,
		},

		// Heartbeats: an unknown device, a banned one, a seeded one
		{Name: "heartbeat-empty", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{}, Roles: some},
		{Name: "heartbeat-bad-id", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "bad id!"}, Roles: some},
		{Name: "heartbeat-id-number", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": 900100031}, Roles: some},
		{Name: "heartbeat-unknown-device", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "rdcontract-unknown", "cpu": 5}, Roles: some},
		{Name: "heartbeat-long-id", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": strings.Repeat("a", 40)}, Roles: some},
		{Name: "heartbeat-banned-device", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "900100028", "cpu": 5}, Roles: some},
		{Name: "heartbeat-banned-device-with-uuid", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "900100028", "uuid": "demo-uuid-900100028", "cpu": 5}, Roles: some},
		{Name: "heartbeat-without-uuid", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "900100031", "cpu": 5}, Roles: some},
		{Name: "heartbeat-other-uuid", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "900100031", "uuid": "demo-uuid-900100030", "cpu": 5}, Roles: some},
		{Name: "heartbeat-deleted-device", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"id": "900100030"}, Roles: some},
		{
			Name: "heartbeat", Method: "POST", Path: "/api/heartbeat", NoCSRF: true,
			Body: map[string]any{"id": "900100031", "uuid": "demo-uuid-900100031", "cpu": 150, "memory": "50", "disk": -3},
		},

		// System information
		{Name: "sysinfo-empty", Method: "POST", Path: "/api/sysinfo", Body: map[string]any{}, Roles: some},
		{Name: "sysinfo-unknown-device", Method: "POST", Path: "/api/sysinfo", Body: map[string]any{"id": "rdcontract-unknown", "hostname": "x"}, Roles: some},
		{Name: "sysinfo-banned-device", Method: "POST", Path: "/api/sysinfo", Body: map[string]any{"uuid": "900100029"}, Roles: some},
		{Name: "sysinfo-id-number", Method: "POST", Path: "/api/sysinfo", Body: map[string]any{"id": 900100031}, Roles: some},
		{Name: "sysinfo-without-uuid", Method: "POST", Path: "/api/sysinfo", Body: map[string]any{"id": "900100031", "hostname": "x"}, Roles: some},
		{
			Name: "sysinfo", Method: "POST", Path: "/api/sysinfo",
			Body: map[string]any{
				"id": "900100031", "uuid": base64.StdEncoding.EncodeToString([]byte("demo-uuid-900100031")),
				"cpu": "Intel Core i7-12700K, 5.2GHz, 24/16 cores", "memory": "31.87GB",
				"hostname": "demo-noted-pc", "username": "demo.front", "os": "Windows 11 Pro 23H2", "version": "1.4.7",
				"displays": []any{map[string]any{"w": 1920, "h": 1080}}, "encoding": []string{"vp9", "h264"},
				"features": map[string]any{"privacy_mode": true}, "platform_additions": map[string]any{"is_installed": true},
			},
		},
		{Name: "heartbeat-after-sysinfo", Method: "POST", Path: "/api/heartbeat", Body: map[string]any{"uuid": "900100031", "cpu": 12.5, "memory": 40, "disk": 70}, Roles: some},
		{Name: "sysinfo-version", Method: "POST", Path: "/api/sysinfo_ver", Body: map[string]any{"id": "900100031"}, Roles: some},
		{Name: "sysinfo-version-empty", Method: "POST", Path: "/api/sysinfo_ver", Body: map[string]any{}, Roles: some},
		{Name: "sysinfo-version-bad-id", Method: "POST", Path: "/api/sysinfo_ver", Body: map[string]any{"uuid": "a/b"}, Roles: some},

		// Keys, software, telemetry
		{Name: "server-key", Path: "/api/server-key"},
		{Name: "server-key-fingerprint", Path: "/api/server-key/fingerprint", Roles: some},
		{Name: "software", Path: "/api/software", Roles: some},
		{Name: "software-other-spelling", Path: "/Api/Software/", Roles: some},
		{Name: "software-client-download-link", Path: "/api/software/client-download-link", Roles: some},
		{Name: "telemetry-key", Path: "/api/telemetry/key", Roles: some},
		{Name: "telemetry-key-with-query", Path: "/api/telemetry/key", Query: url.Values{"v": {"1"}}, Roles: once},
	}
	contract.Group("rustdesk-api", cases...)
}
