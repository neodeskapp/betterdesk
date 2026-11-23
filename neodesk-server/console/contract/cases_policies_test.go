//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The policies of an organization and the attestation records. The chain
// works on a seeded organization (the third by name, Demo Northwind
// Clinic, which has all five policies): admin notes its policies, each
// role reads and writes, admin puts the noted policies back. The API
// server has no attestation, so those routes answer their fallbacks.
func init() {
	const org = "/api/panel/policies/{org}"
	connection := map[string]any{"mode": "unattended", "max_session_duration": 60, "idle_lock_timeout": 5, "require_approval": true}
	contract.Group("policies", append([]contract.Case{
		// Setup, as admin in every role's turn
		contract.Case{
			Name: "setup-organizations", Path: "/api/panel/org", As: "admin",
			Capture: map[string]string{"org": "organizations[2].id"},
		},
		contract.Case{
			Name: "setup-seeded-policies", Path: org, As: "admin",
			Capture: map[string]string{
				"connection": "connection", "features": "features", "security": "security", "network": "network", "update": "update",
			},
		},

		// Reading and writing
		contract.Case{Name: "read", Path: org},
		contract.Case{Name: "read-head", Method: "HEAD", Path: org},
		contract.Case{Name: "read-not-modified", Path: org, Headers: map[string]string{"If-None-Match": "*"}},
		contract.Case{Name: "set-connection", Method: "PUT", Path: org + "/connection", Body: connection},
		contract.Case{Name: "read-after-set", Path: org},
		contract.Case{
			Name: "set-features", Method: "PUT", Path: org + "/features",
			Body: map[string]any{"allow_clipboard": false, "allow_file_transfer": false},
		},
		contract.Case{
			Name: "set-security", Method: "PUT", Path: org + "/security",
			Body: map[string]any{"require_2fa": true, "allowed_operators": []string{"admin"}},
		},
		contract.Case{
			Name: "set-network", Method: "PUT", Path: org + "/network",
			Body: map[string]any{"block_direct_p2p": true, "ip_allowlist": []string{"192.0.2.0/24"}},
		},
		contract.Case{
			Name: "set-update", Method: "PUT", Path: org + "/update",
			Body: map[string]any{"auto_update": false, "channel": "beta"},
		},
		contract.Case{
			Name: "set-connection-list", Method: "PUT", Path: org + "/connection",
			RawBody: `["attended","unattended"]`, ContentType: "application/json",
		},
		contract.Case{Name: "set-connection-empty", Method: "PUT", Path: org + "/connection", Body: map[string]any{}},
		contract.Case{Name: "read-after-all", Path: org},
		contract.Case{Name: "effective", Path: org + "/effective/900100001"},
		contract.Case{Name: "effective-unknown-device", Path: org + "/effective/no-such-device"},
		contract.Case{
			// One entry per write; how many there are depends on the roles
			// that came before.
			Name: "audit", Path: org + "/audit", Volatile: []string{"entries[#]"},
		},

		// Writes that are refused before the route
		contract.Case{Name: "set-without-csrf-token", Method: "PUT", Path: org + "/connection", Body: connection, NoCSRF: true},
		contract.Case{
			Name: "set-as-form", Method: "PUT", Path: org + "/connection",
			RawBody: "mode=attended", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "set-malformed-json", Method: "PUT", Path: org + "/connection",
			RawBody: `{"mode":`, ContentType: "application/json",
		},

		// Cleanup, as admin in every role's turn
		contract.Case{Name: "cleanup-connection", Method: "PUT", Path: org + "/connection", Body: "{connection}", As: "admin"},
		contract.Case{Name: "cleanup-features", Method: "PUT", Path: org + "/features", Body: "{features}", As: "admin"},
		contract.Case{Name: "cleanup-security", Method: "PUT", Path: org + "/security", Body: "{security}", As: "admin"},
		contract.Case{Name: "cleanup-network", Method: "PUT", Path: org + "/network", Body: "{network}", As: "admin"},
		contract.Case{Name: "cleanup-update", Method: "PUT", Path: org + "/update", Body: "{update}", As: "admin"},
		contract.Case{Name: "cleanup-check", Path: org, As: "admin"},

		// An organization nobody has, and ids that cannot go into a path
		contract.Case{Name: "unknown-org-read", Path: "/api/panel/policies/no-such-org"},
		contract.Case{Name: "unknown-org-set", Method: "PUT", Path: "/api/panel/policies/no-such-org/connection", Body: connection},
		contract.Case{Name: "unknown-org-effective", Path: "/api/panel/policies/no-such-org/effective/900100001"},
		contract.Case{Name: "unknown-org-audit", Path: "/api/panel/policies/no-such-org/audit"},
		contract.Case{Name: "bad-org-read", Path: "/api/panel/policies/bad%20id"},
		contract.Case{Name: "bad-org-set", Method: "PUT", Path: "/api/panel/policies/bad%20id/security", Body: connection},
		contract.Case{Name: "bad-org-audit", Path: "/api/panel/policies/bad%20id/audit"},
		contract.Case{Name: "bad-org-effective", Path: "/api/panel/policies/bad%20id/effective/900100001"},
		contract.Case{Name: "bad-device-effective", Path: "/api/panel/policies/no-such-org/effective/bad%20id"},
		contract.Case{Name: "org-two-dots", Path: "/api/panel/policies/%2E%2E"},
		contract.Case{Name: "org-dots-inside", Path: "/api/panel/policies/a..b"},
		contract.Case{Name: "org-id-with-escapes", Path: "/api/panel/policies/a%3Ab%40c%2Bd"},
		contract.Case{Name: "other-spelling", Path: "/API/Panel/Policies/No-Such-Org/"},

		// Attestation
		contract.Case{Name: "attestation-list", Path: "/api/panel/attestation"},
		contract.Case{Name: "attestation-list-paged", Path: "/api/panel/attestation", Query: url.Values{"limit": {"500"}, "offset": {"20"}}},
		contract.Case{Name: "attestation-list-odd-paging", Path: "/api/panel/attestation", Query: url.Values{"limit": {"abc", "7"}, "offset[x]": {"1"}}},
		contract.Case{Name: "attestation-device", Path: "/api/panel/attestation/900100001"},
		contract.Case{Name: "attestation-device-with-escapes", Path: "/api/panel/attestation/a%3Ab%40c"},
		contract.Case{Name: "attestation-device-bad-id", Path: "/api/panel/attestation/bad%20id"},
		contract.Case{Name: "attestation-device-dots-inside", Path: "/api/panel/attestation/a..b"},

		// The second mount
		contract.Case{Name: "second-mount-read", Path: "/api/nd/api/panel/policies/no-such-org"},
		contract.Case{Name: "second-mount-set", Method: "PUT", Path: "/api/nd/api/panel/policies/no-such-org/update", Body: connection},
		contract.Case{Name: "second-mount-set-without-csrf-token", Method: "PUT", Path: "/api/nd/api/panel/policies/no-such-org/update", Body: connection, NoCSRF: true},
		contract.Case{
			Name: "second-mount-set-without-session", Method: "PUT", Path: "/api/nd/api/panel/policies/no-such-org/update", Body: connection,
			NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "second-mount-attestation", Path: "/api/nd/api/panel/attestation"},
		contract.Case{Name: "second-mount-attestation-device", Path: "/api/nd/api/panel/attestation/bad%20id"},
	}, policiesDeviceCases()...)...)
}

// policiesDeviceCases are the two routes devices call, at both mounts.
// They take a device token, which the suite has none of: the headers of a
// case are written into its golden. So the cases show what comes before
// the route: no token, a token nobody issued, a device id that proves
// nothing, and the CSRF gate, which under /api/nd lets a write without a
// session through and checks one with a session. The answers with a token
// are pinned by routes/policies/testdata/device_vectors.json.
func policiesDeviceCases() []contract.Case {
	attestation := map[string]any{"device_id": "900100001", "fingerprint": "contract"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	var cases []contract.Case
	for _, mount := range []struct{ name, prefix string }{{"device", ""}, {"second-mount-device", "/api/nd"}} {
		at := func(what string) string { return mount.name + "-" + what }
		policy, report := mount.prefix+"/api/nd/device-policy", mount.prefix+"/api/nd/attestation"
		cases = append(cases,
			contract.Case{Name: at("policy-without-token"), Path: policy},
			contract.Case{Name: at("policy-unknown-token"), Path: policy, Headers: unknown},
			contract.Case{Name: at("policy-device-id-alone"), Path: policy, Query: url.Values{"device_id": {"900100001"}}, Headers: map[string]string{"X-Device-Id": "900100001"}},
			contract.Case{Name: at("policy-head"), Method: "HEAD", Path: policy, Headers: unknown},
			contract.Case{Name: at("attestation-without-token"), Method: "POST", Path: report, Body: attestation},
			contract.Case{Name: at("attestation-unknown-token"), Method: "POST", Path: report, Body: attestation, Headers: unknown},
			contract.Case{Name: at("attestation-without-csrf-token"), Method: "POST", Path: report, Body: attestation, Headers: unknown, NoCSRF: true},
			contract.Case{
				Name: at("attestation-as-a-device"), Method: "POST", Path: report, Body: attestation, Headers: unknown,
				NoCSRF: true, NoCookies: true, Roles: contract.Roles("signed-out"),
			},
		)
	}
	// Spelled in capitals only at the second mount. At the first,
	// GET /API/ND/DEVICE-POLICY/ is answered by the inventory group's
	// GET /api/nd/:id, whose check for the device mount compares the
	// spelling of the address (BUGS DV8); that answer comes from Go only
	// once the inventory group has moved.
	return append(cases, contract.Case{Name: "second-mount-device-policy-other-spelling", Path: "/API/ND/API/ND/DEVICE-POLICY/", Headers: unknown})
}
