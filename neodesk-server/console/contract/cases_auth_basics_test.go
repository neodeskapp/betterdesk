//go:build contract

package contract_test

import "github.com/unitronix/betterdesk-server/console/contract"

// What the web app asks before and right after sign-in: the sign-in page's
// data, the console's bootstrap for every role, and a sign-in that fails.
func init() {
	contract.Group("auth-basics",
		contract.Case{Name: "login-page", Path: "/api/auth/login-page"},
		contract.Case{Name: "bootstrap", Path: "/api/console/bootstrap"},
		contract.Case{
			Name: "bootstrap-without-session", Path: "/api/console/bootstrap",
			NoSession: true, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "login-unknown-user", Method: "POST", Path: "/api/auth/login",
			Body:  map[string]any{"username": "contract-nobody", "password": "not-a-password"},
			Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "login-without-password", Method: "POST", Path: "/api/auth/login",
			Body:  map[string]any{"username": "contract-nobody"},
			Roles: contract.Roles("signed-out"),
		},
	)
}
