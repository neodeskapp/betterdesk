//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The health check as the route table and the steps around it answer it:
// other spellings of the path, other methods, a conditional request, a
// client that takes no compression and a language choice in the query.
func init() {
	contract.Group("healthcheck",
		contract.Case{Name: "trailing-slash", Path: "/health/"},
		contract.Case{Name: "upper-case", Path: "/HEALTH"},
		contract.Case{Name: "options", Method: "OPTIONS", Path: "/health"},
		contract.Case{
			Name: "if-none-match-any", Path: "/health",
			Headers: map[string]string{"If-None-Match": "*"},
		},
		contract.Case{
			Name: "if-none-match-other", Path: "/health",
			Headers: map[string]string{"If-None-Match": `W/"36-notthetagofthisanswer00000"`},
		},
		contract.Case{
			Name: "no-compression", Path: "/health",
			Headers: map[string]string{"Accept-Encoding": "identity"},
		},
		contract.Case{
			Name: "language-in-query", Path: "/health", Query: url.Values{"lang": {"de"}},
			NoCookies: true, Roles: contract.Roles("signed-out", "viewer"),
		},
		contract.Case{
			Name: "unknown-language-in-query", Path: "/health", Query: url.Values{"lang": {"xx"}},
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "language-that-is-no-text", Path: "/health", Query: url.Values{"lang[toString]": {"x"}},
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "language-cookie", Path: "/health",
			Headers: map[string]string{"Cookie": "betterdesk_lang=pl"}, Roles: contract.Roles("signed-out"),
		},
	)
}
