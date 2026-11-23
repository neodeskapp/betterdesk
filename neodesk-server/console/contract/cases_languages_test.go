//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// The language overview of the settings: every language file with its
// coverage, the keys one lacks, and the switched-off automatic fix.
func init() {
	contract.Group("languages",
		contract.Case{Name: "overview", Path: "/api/panel/languages"},
		contract.Case{Name: "overview-head", Method: "HEAD", Path: "/api/panel/languages", Roles: contract.Roles("signed-out", "viewer", "admin")},
		contract.Case{
			Name: "overview-no-compression", Path: "/api/panel/languages",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("admin"),
		},
		contract.Case{
			Name: "overview-not-modified", Path: "/api/panel/languages",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "missing-de", Path: "/api/panel/languages/de/missing"},
		contract.Case{Name: "missing-en", Path: "/api/panel/languages/en/missing", Roles: contract.Roles("server_admin", "admin")},
		contract.Case{Name: "missing-unknown", Path: "/api/panel/languages/xx/missing", Roles: contract.Roles("viewer", "server_admin", "admin")},
		contract.Case{Name: "missing-code-with-other-characters", Path: "/api/panel/languages/d.e9/missing", Roles: contract.Roles("server_admin", "admin")},
		contract.Case{Name: "missing-code-that-leaves-the-folder", Path: "/api/panel/languages/..%2F..%2Fpackage/missing", Roles: contract.Roles("server_admin", "admin")},
		contract.Case{
			Name: "fix", Method: "POST", Path: "/api/panel/languages/de/fix",
			Body: map[string]any{},
		},
		contract.Case{
			Name: "fix-without-csrf-token", Method: "POST", Path: "/api/panel/languages/de/fix",
			Body: map[string]any{}, NoCSRF: true, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{
			Name: "fix-as-form", Method: "POST", Path: "/api/panel/languages/de/fix",
			RawBody: "a=1", ContentType: "application/x-www-form-urlencoded", Roles: contract.Roles("signed-out", "admin"),
		},
	)
}
