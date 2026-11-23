//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The overview: device counts and the server's health, the server status
// with its ports, the client configuration with the hosts a caller can ask
// for, the relay counters, the status sync that does nothing, and the
// activity feed.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	host := func(value string) url.Values { return url.Values{"host": {value}} }

	// The feed shows the server's ten newest audit entries. Five pairing
	// codes made and revoked are ten entries, so the feed reads the same
	// whatever ran before.
	var feed []contract.Case
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		feed = append(feed, contract.Case{
			Name: "activity-make-entry-" + n, Method: "POST", Path: "/api/panel/pairing/codes",
			Body: map[string]any{"label": "feed " + n}, As: "admin",
			Capture: map[string]string{"feed" + n: "id"},
		})
	}
	for _, n := range []string{"1", "2", "3", "4", "5"} {
		feed = append(feed, contract.Case{
			Name: "activity-make-entry-" + n + "-revoked", Method: "DELETE", Path: "/api/panel/pairing/codes/{feed" + n + "}", As: "admin",
		})
	}

	cases := []contract.Case{
		{Name: "stats", Path: "/api/stats", Volatile: []string{"~data.hbbs.uptime"}},
		{Name: "stats-head", Method: "HEAD", Path: "/api/stats", Roles: some},
		{Name: "stats-other-spelling", Path: "/API/Stats/", Volatile: []string{"~data.hbbs.uptime"}, Roles: some},

		// console_port is the port of the process that answers: the Node
		// console listens on its own port behind the Go console in a test
		// instance (DECISIONS A18). The matrix golden, recorded from the
		// Node console alone, holds the value.
		{Name: "server-status", Path: "/api/server/status", Volatile: []string{"~data.uptime", "~data.console_port"}},
		{
			Name: "server-status-not-modified", Path: "/api/server/status",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},

		{Name: "client-config", Path: "/api/dashboard/client-config"},
		{Name: "client-config-for-a-host", Path: "/api/dashboard/client-config", Query: host("console.example.org")},
		{Name: "client-config-for-a-host-with-scheme-and-port", Path: "/api/dashboard/client-config", Query: host("HTTP://Console.Example.ORG:5000/x?y=1"), Roles: some},
		{Name: "client-config-for-a-local-name", Path: "/api/dashboard/client-config", Query: host("nas.local"), Roles: some},
		{Name: "client-config-for-an-address", Path: "/api/dashboard/client-config", Query: host("192.0.2.7"), Roles: some},
		{Name: "client-config-for-an-invalid-host", Path: "/api/dashboard/client-config", Query: host("bad host"), Roles: some},
		{Name: "client-config-for-two-hosts", Path: "/api/dashboard/client-config", Query: url.Values{"host": {"a.example.org", "b.example.org"}}, Roles: some},
		{Name: "client-config-for-a-host-list", Path: "/api/dashboard/client-config", Query: url.Values{"host[]": {"a.example.org"}}, Roles: some},
		{
			Name: "client-config-no-compression", Path: "/api/dashboard/client-config",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: some,
		},
		{Name: "client-config-head", Method: "HEAD", Path: "/api/dashboard/client-config", Roles: some},

		{Name: "bandwidth", Path: "/api/server/bandwidth"},
		{Name: "bandwidth-head", Method: "HEAD", Path: "/api/server/bandwidth", Roles: some},

		{Name: "sync-status", Method: "POST", Path: "/api/sync-status", Body: map[string]any{}},
		{Name: "sync-status-with-fields", Method: "POST", Path: "/api/sync-status", Body: map[string]any{"force": true}, Roles: some},
		{Name: "sync-status-without-a-body", Method: "POST", Path: "/api/sync-status", Roles: some},
		{Name: "sync-status-without-csrf-token", Method: "POST", Path: "/api/sync-status", Body: map[string]any{}, NoCSRF: true, Roles: some},
		{
			Name: "sync-status-as-form", Method: "POST", Path: "/api/sync-status",
			RawBody: "a=1", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},
		{
			Name: "sync-status-malformed-json", Method: "POST", Path: "/api/sync-status",
			RawBody: `{"a":`, ContentType: "application/json", Roles: some,
		},
	}
	// The picture of a QR code is some bytes longer or shorter from one
	// key to the next, so its text is free; the matrix golden holds the
	// form of the data URL.
	for i := range cases {
		if cases[i].Method == "" && cases[i].Path == "/api/dashboard/client-config" {
			cases[i].Volatile = []string{"~data.qr"}
		}
	}
	cases = append(cases, feed...)
	cases = append(cases,
		contract.Case{Name: "activity", Path: "/api/dashboard/activity"},
		contract.Case{Name: "activity-head", Method: "HEAD", Path: "/api/dashboard/activity", Roles: contract.Roles("signed-out", "guest_issuer", "viewer", "admin")},
		contract.Case{
			Name: "activity-no-compression", Path: "/api/dashboard/activity",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("guest_issuer", "admin"),
		},
	)
	contract.Group("dashboard", cases...)
}
