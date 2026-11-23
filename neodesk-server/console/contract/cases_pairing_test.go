//go:build contract

package contract_test

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Short-code pairing: the suggested address, the list of open codes, and
// codes made with every kind of label and revoked again. Each code a turn
// makes is revoked in the same turn, by the role or by admin.
func init() {
	some := contract.Roles("signed-out", "operator", "global_admin", "admin")
	revoke := func(name, variable string) contract.Case {
		return contract.Case{Name: name, Method: "DELETE", Path: "/api/panel/pairing/codes/{" + variable + "}", As: "admin"}
	}

	contract.Group("pairing",
		contract.Case{Name: "host", Path: "/api/panel/pairing/host"},
		contract.Case{Name: "host-head", Method: "HEAD", Path: "/api/panel/pairing/host", Roles: some},
		contract.Case{Name: "list-empty", Path: "/api/panel/pairing/codes"},
		contract.Case{Name: "list-other-spelling", Path: "/API/Panel/Pairing/Codes/", Roles: some},

		// make, read back, revoke, read back, revoke again
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": "  Front desk  "},
			Capture: map[string]string{"code": "id"},
		},
		contract.Case{Name: "list-with-one", Path: "/api/panel/pairing/codes"},
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/panel/pairing/codes", Roles: some},
		contract.Case{Name: "revoke", Method: "DELETE", Path: "/api/panel/pairing/codes/{code}"},
		contract.Case{Name: "list-after-revoke", Path: "/api/panel/pairing/codes"},
		contract.Case{Name: "revoke-again", Method: "DELETE", Path: "/api/panel/pairing/codes/{code}"},

		// labels
		contract.Case{
			Name: "create-without-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body: map[string]any{}, Capture: map[string]string{"plain": "id"},
		},
		revoke("create-without-label-revoked", "plain"),
		contract.Case{
			Name: "create-without-body", Method: "POST", Path: "/api/panel/pairing/codes",
			Capture: map[string]string{"bare": "id"}, Roles: some,
		},
		revoke("create-without-body-revoked", "bare"),
		contract.Case{
			Name: "create-long-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": " " + strings.Repeat("0123456789", 7) + " "},
			Capture: map[string]string{"long": "id"}, Roles: some,
		},
		revoke("create-long-label-revoked", "long"),
		contract.Case{
			Name: "create-label-outside-ascii", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": strings.Repeat("\U0001F5A5", 40) + "\u00a0"},
			Capture: map[string]string{"wide": "id"}, Roles: some,
		},
		revoke("create-label-outside-ascii-revoked", "wide"),
		// 64 characters as JavaScript counts them end inside the last sign
		contract.Case{
			Name: "create-label-cut-inside-a-character", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": "a" + strings.Repeat("\U0001F5A5", 40)},
			Capture: map[string]string{"cut": "id"}, Roles: some,
		},
		contract.Case{Name: "list-with-a-cut-label", Path: "/api/panel/pairing/codes", Roles: some},
		revoke("create-label-cut-inside-a-character-revoked", "cut"),
		contract.Case{
			Name: "create-number-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": 12.5},
			Capture: map[string]string{"number": "id"}, Roles: some,
		},
		revoke("create-number-label-revoked", "number"),
		contract.Case{
			Name: "create-zero-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": 0},
			Capture: map[string]string{"zero": "id"}, Roles: some,
		},
		revoke("create-zero-label-revoked", "zero"),
		contract.Case{
			Name: "create-true-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": true},
			Capture: map[string]string{"flag": "id"}, Roles: some,
		},
		revoke("create-true-label-revoked", "flag"),
		contract.Case{
			Name: "create-list-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": []any{"a", 1, nil, []any{"b"}}},
			Capture: map[string]string{"list": "id"}, Roles: some,
		},
		revoke("create-list-label-revoked", "list"),
		contract.Case{
			Name: "create-object-label", Method: "POST", Path: "/api/panel/pairing/codes",
			Body:    map[string]any{"label": map[string]any{"a": 1}},
			Capture: map[string]string{"object": "id"}, Roles: some,
		},
		revoke("create-object-label-revoked", "object"),
		contract.Case{
			Name: "create-array-body", Method: "POST", Path: "/api/panel/pairing/codes",
			RawBody: `["label"]`, ContentType: "application/json",
			Capture: map[string]string{"array": "id"}, Roles: some,
		},
		revoke("create-array-body-revoked", "array"),
		contract.Case{
			Name: "create-body-over-the-route-limit", Method: "POST", Path: "/api/panel/pairing/codes",
			RawBody: `{"label":"big","pad":"` + strings.Repeat("x", 5000) + `"}`, ContentType: "application/json",
			Capture: map[string]string{"big": "id"}, Roles: some,
		},
		revoke("create-body-over-the-route-limit-revoked", "big"),

		// refused before the route
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: "/api/panel/pairing/codes",
			Body: map[string]any{"label": "x"}, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/panel/pairing/codes",
			RawBody: "label=form", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},
		contract.Case{
			Name: "create-as-text", Method: "POST", Path: "/api/panel/pairing/codes",
			RawBody: `{"label":"text"}`, ContentType: "text/plain", Roles: some,
		},
		contract.Case{
			Name: "create-malformed-json", Method: "POST", Path: "/api/panel/pairing/codes",
			RawBody: `{"label":`, ContentType: "application/json", Roles: some,
		},

		// ids
		contract.Case{Name: "revoke-unknown", Method: "DELETE", Path: "/api/panel/pairing/codes/0123456789abcdef"},
		contract.Case{Name: "revoke-unknown-in-capitals", Method: "DELETE", Path: "/api/panel/pairing/codes/ABCDEF0123456789", Roles: some},
		contract.Case{Name: "revoke-id-with-spaces-around", Method: "DELETE", Path: "/api/panel/pairing/codes/%20abcdef01%09", Roles: some},
		contract.Case{Name: "revoke-id-too-short", Method: "DELETE", Path: "/api/panel/pairing/codes/abcdef0", Roles: some},
		contract.Case{Name: "revoke-id-too-long", Method: "DELETE", Path: "/api/panel/pairing/codes/" + strings.Repeat("a", 33), Roles: some},
		contract.Case{Name: "revoke-id-longest", Method: "DELETE", Path: "/api/panel/pairing/codes/" + strings.Repeat("a", 32), Roles: some},
		contract.Case{Name: "revoke-id-not-hex", Method: "DELETE", Path: "/api/panel/pairing/codes/ghijklmn"},
		contract.Case{Name: "revoke-id-with-a-line-break", Method: "DELETE", Path: "/api/panel/pairing/codes/abcdef01%0Aabcdef01", Roles: some},
		contract.Case{Name: "revoke-without-csrf-token", Method: "DELETE", Path: "/api/panel/pairing/codes/0123456789abcdef", NoCSRF: true, Roles: some},
	)
}
