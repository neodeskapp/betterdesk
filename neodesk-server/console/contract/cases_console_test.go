//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// What the web app loads before it renders: the boot script that runs in
// the page's head, the bootstrap with the account, its permissions, panel
// access, navigation, languages and branding, and the supporters list of
// the Help panel.
//
// A bootstrap under an account policy is pinned by the tests of
// console/routes/console: writing the policy is a route of the Go console,
// and a case answered by it at recording time has no golden.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	signedIn := contract.Roles("viewer", "operator", "global_admin", "admin")

	contract.Group("console",
		contract.Case{Name: "boot-script", Path: "/console-boot.js"},
		contract.Case{Name: "boot-script-head", Method: "HEAD", Path: "/console-boot.js", Roles: some},
		contract.Case{
			Name: "boot-script-german", Path: "/console-boot.js",
			Headers: map[string]string{"Accept-Language": "de-DE,de;q=0.9"}, Roles: some,
		},
		contract.Case{
			Name: "boot-script-right-to-left", Path: "/console-boot.js",
			Headers: map[string]string{"Accept-Language": "ar"}, Roles: some,
		},
		contract.Case{
			Name: "boot-script-language-in-query", Path: "/console-boot.js", Query: url.Values{"lang": {"pl"}},
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "boot-script-not-modified", Path: "/console-boot.js",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{Name: "boot-script-other-spelling", Path: "/Console-Boot.js/", Roles: some},

		contract.Case{Name: "bootstrap", Path: "/api/console/bootstrap"},
		contract.Case{Name: "bootstrap-head", Method: "HEAD", Path: "/api/console/bootstrap", Roles: some},
		contract.Case{
			Name: "bootstrap-german", Path: "/api/console/bootstrap",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: signedIn,
		},
		contract.Case{
			Name: "bootstrap-right-to-left", Path: "/api/console/bootstrap",
			Headers: map[string]string{"Accept-Language": "ar"}, Roles: contract.Roles("viewer", "admin"),
		},
		contract.Case{
			Name: "bootstrap-not-modified", Path: "/api/console/bootstrap",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{
			Name: "bootstrap-no-compression", Path: "/api/console/bootstrap",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: some,
		},
		contract.Case{Name: "bootstrap-with-a-query", Path: "/api/console/bootstrap", Query: url.Values{"page": {"devices"}}, Roles: some},
		contract.Case{Name: "bootstrap-other-spelling", Path: "/API/Console/Bootstrap/", Roles: some},

		contract.Case{Name: "supporters", Path: "/api/console/supporters"},
		contract.Case{Name: "supporters-head", Method: "HEAD", Path: "/api/console/supporters", Roles: some},
		contract.Case{
			Name: "supporters-not-modified", Path: "/api/console/supporters",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
	)
}
