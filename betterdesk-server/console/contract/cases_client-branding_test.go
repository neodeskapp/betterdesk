//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The client branding that desktop clients get from the API server
// (GET and POST /api/client-branding): read, save, the logo, what the API
// server refuses, and what the route leaves out of the request. Every
// turn ends by saving the defaults back. The settings rows stay in
// server_config with empty values, which the API server answers as its
// defaults.
func init() {
	roles := contract.Roles("signed-out", "viewer", "server_admin", "global_admin", "admin")
	admin := contract.Roles("admin")
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
	const path = "/api/client-branding"

	contract.Group("client-branding",
		contract.Case{Name: "get", Path: path, Roles: roles},
		contract.Case{
			Name: "save", Method: "POST", Path: path, Roles: roles,
			Body: map[string]any{
				"company_name": "Contract Co", "phone": "+49 30 1234", "email": "help@contract.example",
				"website": "https://contract.example", "accent_color": "#123abc", "support_contact": "Call us",
				"colors": map[string]any{"primary": "#112233"},
			},
		},
		contract.Case{Name: "get-after-save", Path: path, Roles: roles},
		contract.Case{
			Name: "save-logo", Method: "POST", Path: path, Roles: admin,
			Body: map[string]any{"logo": map[string]any{"mime": "image/png", "data_base64": png}},
		},
		contract.Case{
			Name: "save-logo-and-clear", Method: "POST", Path: path, Roles: admin,
			Body: map[string]any{"clear_logo": true, "logo": map[string]any{"mime": "image/png", "data_base64": png}},
		},
		contract.Case{Name: "save-logo-empty", Method: "POST", Path: path, Body: map[string]any{"logo": "", "clear_logo": 0}, Roles: admin},
		contract.Case{Name: "save-bad-colour", Method: "POST", Path: path, Body: map[string]any{"accent_color": "blue"}, Roles: admin},
		contract.Case{
			Name: "save-bad-logo", Method: "POST", Path: path, Roles: admin,
			Body: map[string]any{"logo": map[string]any{"mime": "image/gif", "data_base64": "AAAA"}},
		},
		contract.Case{Name: "save-colours-as-text", Method: "POST", Path: path, Body: map[string]any{"colors": "red"}, Roles: admin},
		contract.Case{Name: "save-other-keys", Method: "POST", Path: path, Body: map[string]any{"nothing": 1, "company_name": nil}, Roles: admin},
		contract.Case{Name: "save-list", Method: "POST", Path: path, Body: []any{1}, Roles: admin},
		contract.Case{Name: "save-form", Method: "POST", Path: path, RawBody: "company_name=Form", ContentType: "application/x-www-form-urlencoded", Roles: admin},
		contract.Case{
			Name: "restore", Method: "POST", Path: path, As: "admin", Roles: roles,
			Body: map[string]any{
				"company_name": "", "phone": "", "email": "", "website": "", "accent_color": "", "support_contact": "",
				"colors": map[string]any{}, "clear_logo": true,
			},
		},
		contract.Case{Name: "get-after-restore", Path: path, Roles: admin},
	)
}
