//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The shell choice: reading it, setting it in every spelling the route
// takes, and every way a write is refused before or in the route.
func init() {
	contract.Group("ui-shell",
		contract.Case{Name: "read", Path: "/api/ui-shell"},
		contract.Case{Name: "read-head", Method: "HEAD", Path: "/api/ui-shell"},
		contract.Case{Name: "read-other-spelling", Path: "/API/UI-Shell/"},
		contract.Case{
			Name: "read-preflight", Method: "OPTIONS", Path: "/api/ui-shell",
			Headers: map[string]string{"Origin": "tauri://localhost"}, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{
			Name: "read-not-modified", Path: "/api/ui-shell",
			Headers: map[string]string{"If-None-Match": "*"},
		},
		contract.Case{
			Name: "set-ux36", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": "ux36"},
		},
		contract.Case{Name: "read-after-set", Path: "/api/ui-shell"},
		contract.Case{
			Name: "set-back-in-other-spelling", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": "  UX35\n"},
		},
		contract.Case{Name: "read-after-set-back", Path: "/api/ui-shell"},
		contract.Case{
			Name: "set-unknown-shell", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": "classic"},
		},
		contract.Case{
			Name: "set-number", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": 36},
		},
		contract.Case{
			Name: "set-list", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": []string{"ux36"}},
		},
		contract.Case{
			Name: "set-nothing", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{},
		},
		contract.Case{
			Name: "set-array-body", Method: "POST", Path: "/api/ui-shell",
			RawBody: `["ux36"]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "set-without-csrf-token", Method: "POST", Path: "/api/ui-shell",
			Body: map[string]any{"shell": "ux36"}, NoCSRF: true,
		},
		contract.Case{
			Name: "set-as-form", Method: "POST", Path: "/api/ui-shell",
			RawBody: "shell=ux36", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "set-as-text", Method: "POST", Path: "/api/ui-shell",
			RawBody: `{"shell":"ux36"}`, ContentType: "text/plain",
		},
		contract.Case{
			Name: "set-json-with-charset", Method: "POST", Path: "/api/ui-shell",
			RawBody: `{"shell":"ux35"}`, ContentType: "application/json; charset=utf-8",
		},
		contract.Case{
			Name: "set-malformed-json", Method: "POST", Path: "/api/ui-shell",
			RawBody: `{"shell":"ux36"`, ContentType: "application/json",
		},
		contract.Case{
			Name: "set-body-over-the-limit", Method: "POST", Path: "/api/ui-shell",
			RawBody: `{"shell":"ux36","pad":"` + strings.Repeat("x", 2<<20) + `"}`, ContentType: "application/json",
			Roles: contract.Roles("signed-out", "viewer", "admin"),
		},
		contract.Case{
			Name: "set-body-over-the-route-limit", Method: "POST", Path: "/api/ui-shell",
			RawBody: `{"shell":"ux35","pad":"` + strings.Repeat("x", 4096) + `"}`, ContentType: "application/json",
			Roles: contract.Roles("signed-out", "viewer", "admin"),
		},
	)
}
