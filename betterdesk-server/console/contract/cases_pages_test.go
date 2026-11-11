//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The old page address /tenants sends everyone to /organizations, which
// checks access itself. The redirect's body follows the Accept header.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	contract.Group("pages",
		contract.Case{Name: "tenants", Path: "/tenants"},
		contract.Case{Name: "tenants-head", Method: "HEAD", Path: "/tenants", Roles: some},
		contract.Case{Name: "tenants-with-query", Path: "/tenants", Query: url.Values{"tab": {"billing"}}, Roles: some},
		contract.Case{Name: "tenants-other-spelling", Path: "/TENANTS/", Roles: some},
		contract.Case{
			Name: "tenants-json", Path: "/tenants",
			Headers: map[string]string{"Accept": "application/json"}, Roles: some,
		},
		contract.Case{
			Name: "tenants-text", Path: "/tenants",
			Headers: map[string]string{"Accept": "text/plain"}, Roles: some,
		},
		contract.Case{
			Name: "tenants-no-cookies", Path: "/tenants",
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
	)
}
