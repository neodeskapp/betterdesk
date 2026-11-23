//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// API tokens (console/routes/apitokens, console/admintoken). These routes
// came with the Go console; their goldens were recorded from Go
// (CONTRACT_RECORD_NEW).
//
// Every role and signed out asks for the list, the permissions and the
// token of a browser session (none). The four admin roles each make a
// read-only token with audit.view, use it as a client would (the Bearer
// header, no cookie): to read who it is, to try a write, to try a route no
// token reaches; then revoke it, and the next use is refused. A made-up
// token is refused the same way. What a turn makes it revokes again.
func init() {
	admins := contract.Roles("global_admin", "server_admin", "super_admin", "admin")
	token := func(c contract.Case) contract.Case {
		c.NoCookies, c.NoCSRF, c.Roles = true, true, admins
		c.Headers = map[string]string{"Authorization": "Bearer {secret:api_token}"}
		return c
	}
	contract.Group("api-tokens",
		contract.Case{Name: "list-empty", Path: "/api/panel/api-tokens"},
		contract.Case{Name: "list-all", Path: "/api/panel/api-tokens", Query: map[string][]string{"all": {"true"}}},
		contract.Case{Name: "permissions", Path: "/api/panel/api-tokens/permissions"},
		contract.Case{Name: "current-in-a-browser", Path: "/api/panel/api-tokens/current"},
		contract.Case{Name: "create-empty", Method: "POST", Path: "/api/panel/api-tokens", Body: map[string]any{}},
		contract.Case{Name: "create-no-expiry", Method: "POST", Path: "/api/panel/api-tokens", Body: map[string]any{"name": "Contract", "permissions": []string{"audit.view"}}},
		contract.Case{Name: "create-too-long", Method: "POST", Path: "/api/panel/api-tokens", Body: map[string]any{"name": "Contract", "permissions": []string{"audit.view"}, "expires_in_days": 400}},
		contract.Case{Name: "create-unknown-permission", Method: "POST", Path: "/api/panel/api-tokens", Body: map[string]any{"name": "Contract", "permissions": []string{"no.such"}, "expires_in_days": 1}},
		contract.Case{
			Name: "create", Method: "POST", Path: "/api/panel/api-tokens",
			Body:          map[string]any{"name": "Contract token", "permissions": []string{"audit.view"}, "expires_in_days": 1, "allow_list": []string{}},
			Capture:       map[string]string{"token_id": "data.api_token.id"},
			CaptureSecret: map[string]string{"api_token": "data.token"},
			Roles:         admins,
		},
		token(contract.Case{Name: "use-current", Path: "/api/panel/api-tokens/current"}),
		token(contract.Case{Name: "use-read-only-write", Method: "POST", Path: "/api/tags/rename", Body: map[string]any{"from": "a", "to": "b"}}),
		token(contract.Case{Name: "use-never-for-tokens", Path: "/api/auth/sessions"}),
		token(contract.Case{Name: "use-whole-api-key", Path: "/api/keys/api", Query: map[string][]string{"show": {"true"}}}),
		contract.Case{Name: "list-one", Path: "/api/panel/api-tokens", Roles: admins},
		contract.Case{Name: "revoke", Method: "DELETE", Path: "/api/panel/api-tokens/{token_id}"},
		token(contract.Case{Name: "use-revoked", Path: "/api/panel/api-tokens/current"}),
		contract.Case{
			Name: "use-made-up", Path: "/api/stats", NoCookies: true, Roles: contract.Roles("admin"),
			Headers: map[string]string{"Authorization": "Bearer ndt_made_up_token_of_the_wrong_length"},
		},
	)
}
