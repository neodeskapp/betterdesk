//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The server attestation: the state of the capacity test, its last result,
// the texts of the capacity badge and the public badge the sign-in page
// shows.
//
// A capacity test puts the server under load for minutes, so no case
// starts one: POST /api/server-attestation/run goes only to the roles the
// guards refuse, and to server_admin and admin without a CSRF token or
// with a body that is not JSON, and every role's turn finds no test
// running. A test instance has run no capacity test, so the public badge
// has no tier here; the tests of console/attestation pin a run, the
// result it stores and the badge made from it against the Node code.
//
// The badge texts are set by admin at the start of every role's turn, so
// each role reads them back from the badge routes and the public route;
// then each role tries to save them itself. The group ends by saving the
// empty texts again, which is what an instance without a saved row shows.
//
// The public badge in German is asked for by signed-in roles only: in a
// run of all groups the signed-out browser still has the language cookie
// the i18n group set last, which wins over Accept-Language.
func init() {
	some := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	signedIn := contract.Roles("viewer", "server_admin", "admin")
	refusedRun := contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "operator")
	// Refused by the guards (viewer, global_admin) or by the route itself
	// (server_admin, admin)
	refusedLater := contract.Roles("viewer", "global_admin", "server_admin", "admin")
	labels := map[string]any{
		"badgeTitle": "Measured capacity",
		"tierLabels": map[string]any{"bronze": "Starter", "iron": "", "obsidian": "Top"},
	}
	contract.Group("server-attestation",
		contract.Case{Name: "status", Path: "/api/server-attestation/status"},
		contract.Case{Name: "status-head", Method: "HEAD", Path: "/api/server-attestation/status", Roles: some},
		contract.Case{
			Name: "status-not-modified", Path: "/api/server-attestation/status",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{Name: "status-other-spelling", Path: "/API/Server-Attestation/Status/", Roles: some},
		contract.Case{Name: "result", Path: "/api/server-attestation/result"},
		contract.Case{Name: "result-head", Method: "HEAD", Path: "/api/server-attestation/result", Roles: some},

		// Abort with no capacity test running
		contract.Case{Name: "abort-without-run", Method: "POST", Path: "/api/server-attestation/abort", Body: map[string]any{}},
		contract.Case{
			Name: "abort-without-csrf", Method: "POST", Path: "/api/server-attestation/abort",
			Body: map[string]any{}, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "abort-text-body", Method: "POST", Path: "/api/server-attestation/abort",
			RawBody: "abort", ContentType: "text/plain", Roles: some,
		},
		contract.Case{Name: "status-after-abort", Path: "/api/server-attestation/status", Roles: some},

		// Starting a capacity test, only where the request is refused
		contract.Case{Name: "run-refused", Method: "POST", Path: "/api/server-attestation/run", Body: map[string]any{}, Roles: refusedRun},
		contract.Case{
			Name: "run-without-csrf", Method: "POST", Path: "/api/server-attestation/run",
			Body: map[string]any{}, NoCSRF: true, Roles: refusedLater,
		},
		contract.Case{
			Name: "run-text-body", Method: "POST", Path: "/api/server-attestation/run",
			RawBody: "run", ContentType: "text/plain", Roles: refusedLater,
		},
		contract.Case{
			Name: "run-form-body", Method: "POST", Path: "/api/server-attestation/run",
			RawBody: "start=1", ContentType: "application/x-www-form-urlencoded", Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "status-after-refused-run", Path: "/api/server-attestation/status", Roles: some},

		// The badge texts as an install without saved texts has them
		contract.Case{Name: "badge-config", Path: "/api/server-attestation/badge-config"},
		contract.Case{
			Name: "badge-config-german", Path: "/api/server-attestation/badge-config",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: some,
		},
		contract.Case{
			Name: "badge-config-polish", Path: "/api/server-attestation/badge-config",
			Headers: map[string]string{"Accept-Language": "pl"}, Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "public", Path: "/api/public/server-attestation"},
		contract.Case{Name: "public-head", Method: "HEAD", Path: "/api/public/server-attestation", Roles: some},
		contract.Case{
			Name: "public-german", Path: "/api/public/server-attestation",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: signedIn,
		},
		contract.Case{
			Name: "public-not-modified", Path: "/api/public/server-attestation",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{
			Name: "public-no-cookies", Path: "/api/public/server-attestation",
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		contract.Case{Name: "public-other-spelling", Path: "/API/Public/Server-Attestation/", Roles: some},

		// Saved texts, read back by every role
		contract.Case{Name: "put-labels-as-admin", Method: "PUT", Path: "/api/server-attestation/badge-config", Body: labels, As: "admin"},
		contract.Case{Name: "badge-config-after-put", Path: "/api/server-attestation/badge-config"},
		contract.Case{Name: "public-after-put", Path: "/api/public/server-attestation"},
		contract.Case{
			Name: "public-after-put-german", Path: "/api/public/server-attestation",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: signedIn,
		},
		contract.Case{Name: "status-after-put", Path: "/api/server-attestation/status", Roles: some},

		// Who may save them
		contract.Case{
			Name: "put-labels", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"badgeTitle": "Saved by the role", "tierLabels": map[string]any{"iron": "Eisen"}},
		},
		contract.Case{Name: "badge-config-after-own-put", Path: "/api/server-attestation/badge-config", Roles: some},
		contract.Case{
			Name: "put-without-csrf", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: labels, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "put-text-body", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody: "badgeTitle=x", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},

		// How texts are cleaned
		contract.Case{
			Name: "put-control-characters-and-limits", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{
				"badgeTitle": "  Custom\u0000 title\u007f " + strings.Repeat("x", 120) + "  ",
				"tierLabels": map[string]any{
					"bronze":   "  Bronze\u0007 " + strings.Repeat("y", 60) + "  ",
					"iron":     "<b>Iron</b>",
					"platinum": "\tTabbed\nlines\r",
					"titanium": strings.Repeat("z", 31) + " w",
					"obsidian": string(rune(0xa0)) + string(rune(0xfeff)) + "spaced" + string(rune(0x2003)),
				},
				"unexpected": "ignored",
			},
			Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-other-types", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{
				"badgeTitle": 12345,
				"tierLabels": map[string]any{
					"bronze": true, "iron": 7.5, "platinum": nil,
					"titanium": []any{"a", []any{"b", "c"}, nil}, "obsidian": map[string]any{},
				},
			},
			Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-labels-as-list", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"badgeTitle": []any{"t", 1}, "tierLabels": []any{"a", "b"}}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-labels-as-text", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"badgeTitle": "", "tierLabels": "iron"}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-escaped-characters", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody:     `{"badgeTitle":"half \ud83d of an emoji \ud83d\ude00 and \u2028","tierLabels":{"iron":"` + strings.Repeat("\\u00e9", 40) + `"}}`,
			ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-repeated-key", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody:     `{"badgeTitle":"first","tierLabels":{"iron":"a"},"badgeTitle":"second"}`,
			ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "badge-config-after-cleaning", Path: "/api/server-attestation/badge-config", Roles: some},

		// Refused bodies; the saved texts stay
		contract.Case{
			Name: "put-array", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: []any{map[string]any{"badgeTitle": "x"}}, Roles: some,
		},
		contract.Case{
			Name: "put-empty-array", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: []any{}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-null", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody: "null", ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-text-json", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody: `"badge"`, ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-broken-json", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody: `{"badgeTitle":`, ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-title-without-text", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"badgeTitle": map[string]any{"toString": 1}}, Roles: some,
		},
		contract.Case{
			Name: "put-label-without-text", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"tierLabels": map[string]any{"bronze": "fine", "iron": map[string]any{"toString": "x"}}}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "put-label-list-without-text", Method: "PUT", Path: "/api/server-attestation/badge-config",
			Body: map[string]any{"tierLabels": map[string]any{"titanium": []any{"a", map[string]any{"toString": 1}}}}, Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "badge-config-after-refusals", Path: "/api/server-attestation/badge-config", Roles: some},
		contract.Case{Name: "public-after-refusals", Path: "/api/public/server-attestation", Roles: some},

		// No body is the empty texts; then every role's turn ends with them
		contract.Case{
			Name: "put-no-body", Method: "PUT", Path: "/api/server-attestation/badge-config",
			RawBody: "", ContentType: "application/json", Roles: contract.Roles("admin"),
		},
		contract.Case{Name: "put-reset", Method: "PUT", Path: "/api/server-attestation/badge-config", Body: map[string]any{}, As: "admin"},
		contract.Case{Name: "badge-config-after-reset", Path: "/api/server-attestation/badge-config", Roles: some},
		contract.Case{Name: "public-after-reset", Path: "/api/public/server-attestation", Roles: some},
	)
}
