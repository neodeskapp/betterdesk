//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The strings of a language for the web app: plain JSON without an
// envelope, compressed by the route itself and cached under its revision.
func init() {
	contract.Group("console-strings",
		contract.Case{Name: "strings-de", Path: "/api/console/strings/de"},
		contract.Case{Name: "strings-en-head", Method: "HEAD", Path: "/api/console/strings/en", Roles: contract.Roles("signed-out", "admin")},
		contract.Case{
			Name: "strings-no-compression", Path: "/api/console/strings/de",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{
			Name: "strings-other-revision", Path: "/api/console/strings/de", Query: url.Values{"v": {"0000000000000000"}},
			Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{
			Name: "strings-other-tag", Path: "/api/console/strings/de",
			Headers: map[string]string{"If-None-Match": `"0000000000000000"`}, Roles: contract.Roles("signed-out"),
		},
		contract.Case{
			Name: "strings-any-tag", Path: "/api/console/strings/de",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: contract.Roles("signed-out"),
		},
		contract.Case{Name: "strings-zh-tw", Path: "/api/console/strings/zh-TW", Roles: contract.Roles("signed-out")},
		contract.Case{Name: "strings-unknown", Path: "/api/console/strings/xx", Roles: contract.Roles("signed-out", "viewer", "admin")},
		contract.Case{Name: "strings-upper-case", Path: "/api/console/strings/DE", Roles: contract.Roles("signed-out")},
		contract.Case{Name: "strings-not-a-code", Path: "/api/console/strings/de.json", Roles: contract.Roles("signed-out")},
		contract.Case{Name: "strings-inherited-name", Path: "/api/console/strings/constructor", Roles: contract.Roles("signed-out")},
		contract.Case{
			Name: "strings-language-in-query", Path: "/api/console/strings/de", Query: url.Values{"lang": {"pl"}},
			NoCookies: true, Roles: contract.Roles("signed-out"),
		},
	)
}
