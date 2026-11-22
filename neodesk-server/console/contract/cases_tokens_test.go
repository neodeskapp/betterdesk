//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Enrollment tokens and the enrollment mode. A token cannot be deleted,
// only revoked, so every turn leaves its tokens behind revoked. The
// enrollment mode is put back to "open" at the end of each turn.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	admins := contract.Roles("server_admin", "admin")

	contract.Group("tokens",
		contract.Case{Name: "list", Path: "/api/panel/tokens"},
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/panel/tokens", Roles: some},
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/panel/tokens",
			Body:    map[string]any{"name": "contract token", "max_uses": 3, "expires_in": 3600, "note": "made by the contract cases"},
			Capture: map[string]string{"token": "data.id"},
		},
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made"}, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/panel/tokens",
			RawBody: "name=never+made", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},
		contract.Case{
			Name: "create-with-a-list", Method: "POST", Path: "/api/panel/tokens",
			RawBody: `[{"name":"never made"}]`, ContentType: "application/json", Roles: admins,
		},
		contract.Case{
			Name: "create-with-text-for-a-number", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made", "max_uses": "many"}, Roles: admins,
		},
		// JavaScript sends 2.0 on as 2, which the API server takes
		contract.Case{
			Name: "create-with-a-fraction", Method: "POST", Path: "/api/panel/tokens",
			RawBody: `{"name":"contract fraction","max_uses":2.0,"name":"contract fraction twice"}`, ContentType: "application/json",
			Roles:   admins,
			Capture: map[string]string{"fraction": "data.id"},
		},
		contract.Case{Name: "revoke-fraction", Method: "DELETE", Path: "/api/panel/tokens/{fraction}", Roles: admins},
		contract.Case{Name: "list-after-create", Path: "/api/panel/tokens"},

		contract.Case{
			Name: "update", Method: "PUT", Path: "/api/panel/tokens/{token}",
			Body: map[string]any{"name": "contract token renamed", "max_uses": 5, "note": "changed"},
		},
		contract.Case{
			Name: "update-nothing", Method: "PUT", Path: "/api/panel/tokens/{token}",
			Body: map[string]any{}, Roles: admins,
		},
		contract.Case{
			Name: "update-missing", Method: "PUT", Path: "/api/panel/tokens/999999998",
			Body: map[string]any{"name": "nobody"}, Roles: contract.Roles("viewer", "server_admin", "admin"),
		},
		contract.Case{
			Name: "update-id-that-is-no-number", Method: "PUT", Path: "/api/panel/tokens/abc",
			Body: map[string]any{"name": "nobody"}, Roles: admins,
		},
		contract.Case{
			Name: "update-id-with-a-slash", Method: "PUT", Path: "/api/panel/tokens/1%2F2",
			Body: map[string]any{"name": "nobody"}, Roles: admins,
		},
		contract.Case{
			Name: "update-id-with-spaces-around", Method: "PUT", Path: "/api/panel/tokens/%20{token}%20",
			Body: map[string]any{"note": "trimmed"}, Roles: admins,
		},

		contract.Case{
			Name: "bind", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": "900100001"},
		},
		contract.Case{
			Name: "bind-number", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": 900100001}, Roles: admins,
		},
		contract.Case{
			Name: "bind-list-of-one", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": []any{"900100001"}}, Roles: admins,
		},
		contract.Case{
			Name: "bind-other-peer", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": "900100002"}, Roles: admins,
		},
		contract.Case{
			Name: "bind-without-peer", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{}, Roles: contract.Roles("viewer", "server_admin", "admin"),
		},
		contract.Case{
			Name: "bind-peer-false", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": false}, Roles: admins,
		},
		contract.Case{
			Name: "bind-peer-true", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": true}, Roles: admins,
		},
		contract.Case{
			Name: "bind-peer-object", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": map[string]any{"id": "900100001"}}, Roles: admins,
		},
		contract.Case{
			Name: "bind-peer-with-a-space", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": "900 100 001"}, Roles: admins,
		},
		contract.Case{
			Name: "bind-with-a-list-as-body", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			RawBody: `["900100001"]`, ContentType: "application/json", Roles: admins,
		},
		contract.Case{
			Name: "bind-short-peer", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": "abc"}, Roles: admins,
		},
		contract.Case{
			Name: "bind-missing-token", Method: "POST", Path: "/api/panel/tokens/999999998/bind",
			Body: map[string]any{"peer_id": "900100001"}, Roles: admins,
		},

		contract.Case{Name: "revoke", Method: "DELETE", Path: "/api/panel/tokens/{token}"},
		contract.Case{Name: "revoke-again", Method: "DELETE", Path: "/api/panel/tokens/{token}", Roles: admins},
		contract.Case{Name: "revoke-missing", Method: "DELETE", Path: "/api/panel/tokens/999999998", Roles: admins},
		contract.Case{Name: "revoke-id-with-a-slash", Method: "DELETE", Path: "/api/panel/tokens/1%2F2", Roles: admins},
		contract.Case{
			Name: "bind-revoked", Method: "POST", Path: "/api/panel/tokens/{token}/bind",
			Body: map[string]any{"peer_id": "900100001"}, Roles: admins,
		},
		contract.Case{Name: "list-without-revoked", Path: "/api/panel/tokens"},
		contract.Case{Name: "list-with-revoked", Path: "/api/panel/tokens", Query: url.Values{"include_revoked": {"true"}}},
		contract.Case{Name: "list-with-revoked-false", Path: "/api/panel/tokens", Query: url.Values{"include_revoked": {"false"}}, Roles: admins},
		contract.Case{Name: "list-with-revoked-one", Path: "/api/panel/tokens", Query: url.Values{"include_revoked": {"1"}}, Roles: admins},
		// A parameter sent twice is a list, and a list is not "true"
		contract.Case{Name: "list-with-revoked-twice", Path: "/api/panel/tokens", Query: url.Values{"include_revoked": {"true", "true"}}, Roles: admins},
		contract.Case{Name: "list-with-revoked-in-brackets", Path: "/api/panel/tokens", Query: url.Values{"include_revoked[]": {"true"}}, Roles: admins},

		contract.Case{
			Name: "generate-bulk", Method: "POST", Path: "/api/panel/tokens/generate-bulk",
			Body:    map[string]any{"count": 2, "name_prefix": "contract-bulk", "max_uses": 1, "expires_in": 600},
			Capture: map[string]string{"bulk1": "data.tokens[0].id", "bulk2": "data.tokens[1].id"},
		},
		contract.Case{Name: "revoke-bulk-1", Method: "DELETE", Path: "/api/panel/tokens/{bulk1}"},
		contract.Case{Name: "revoke-bulk-2", Method: "DELETE", Path: "/api/panel/tokens/{bulk2}"},
		contract.Case{
			Name: "generate-bulk-too-many", Method: "POST", Path: "/api/panel/tokens/generate-bulk",
			Body: map[string]any{"count": 101}, Roles: admins,
		},
		contract.Case{
			Name: "generate-bulk-without-count", Method: "POST", Path: "/api/panel/tokens/generate-bulk",
			Body: map[string]any{}, Roles: admins,
		},

		contract.Case{Name: "mode", Path: "/api/panel/enrollment/mode"},
		contract.Case{
			Name: "mode-set", Method: "PUT", Path: "/api/panel/enrollment/mode",
			Body: map[string]any{"mode": "managed"},
		},
		contract.Case{Name: "mode-after-set", Path: "/api/panel/enrollment/mode"},
		contract.Case{
			Name: "mode-unknown", Method: "PUT", Path: "/api/panel/enrollment/mode",
			Body: map[string]any{"mode": "closed"}, Roles: admins,
		},
		contract.Case{
			Name: "mode-number", Method: "PUT", Path: "/api/panel/enrollment/mode",
			Body: map[string]any{"mode": 1}, Roles: admins,
		},
		contract.Case{
			Name: "mode-missing", Method: "PUT", Path: "/api/panel/enrollment/mode",
			Body: map[string]any{}, Roles: admins,
		},
		contract.Case{
			Name: "mode-with-a-list-as-body", Method: "PUT", Path: "/api/panel/enrollment/mode",
			RawBody: `["locked"]`, ContentType: "application/json", Roles: admins,
		},
		contract.Case{
			Name: "mode-restore", Method: "PUT", Path: "/api/panel/enrollment/mode",
			Body: map[string]any{"mode": "open"},
		},
		contract.Case{Name: "mode-after-restore", Path: "/api/panel/enrollment/mode", Roles: admins},
	)
}
