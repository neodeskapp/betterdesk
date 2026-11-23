//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The sign-in page's data: the CSRF token, name and version of the
// console, its branding, the languages and the capacity badge. A test
// instance has run no capacity test, so the badge is null here; the tests
// of console/routes/auth pin the badge against the Node code.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	contract.Group("login-page",
		contract.Case{Name: "login-page", Path: "/api/auth/login-page"},
		contract.Case{Name: "login-page-head", Method: "HEAD", Path: "/api/auth/login-page", Roles: some},
		contract.Case{
			Name: "login-page-german", Path: "/api/auth/login-page",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: some,
		},
		contract.Case{
			Name: "login-page-language-in-query", Path: "/api/auth/login-page", Query: url.Values{"lang": {"fr"}},
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "login-page-desktop-mode", Path: "/api/auth/login-page",
			Headers: map[string]string{"Cookie": "neodesk_desktop_mode=true"}, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "login-page-desktop-mode-other-value", Path: "/api/auth/login-page",
			Headers: map[string]string{"Cookie": "neodesk_desktop_mode=TRUE"}, Roles: contract.Roles("signed-out"),
		},
		contract.Case{Name: "login-page-without-session", Path: "/api/auth/login-page", NoSession: true, Roles: contract.Roles("viewer")},
		contract.Case{
			Name: "login-page-not-modified", Path: "/api/auth/login-page",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{
			Name: "login-page-no-compression", Path: "/api/auth/login-page",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: some,
		},
		contract.Case{Name: "login-page-other-spelling", Path: "/API/Auth/Login-Page/", Roles: some},
	)
}
