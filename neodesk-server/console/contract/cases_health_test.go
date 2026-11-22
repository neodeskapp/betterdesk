//go:build contract

package contract_test

import "github.com/unitronix/betterdesk-server/console/contract"

// What the console answers before a route's own code runs: the health
// check, paths nothing serves, and writes the checks in front of the routes
// refuse. The writes go to a real route (POST /api/folders) and never reach it.
func init() {
	contract.Group("health",
		contract.Case{Name: "health", Path: "/health"},
		contract.Case{Name: "health-head", Method: "HEAD", Path: "/health"},
		contract.Case{Name: "unknown-api-path", Path: "/api/contract/no-such-route"},
		contract.Case{
			Name: "unknown-api-path-write", Method: "POST", Path: "/api/contract/no-such-route",
			Body: map[string]any{},
		},
		contract.Case{Name: "unknown-page", Path: "/contract/no-such-page"},
		contract.Case{
			Name: "unknown-page-asked-as-json", Path: "/contract/no-such-page",
			Headers: map[string]string{"Accept": "application/json"},
		},
		contract.Case{
			Name: "write-without-csrf-token", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"name": "contract"}, NoCSRF: true,
		},
		contract.Case{
			Name: "write-with-wrong-csrf-token", Method: "POST", Path: "/api/folders",
			Body: map[string]any{"name": "contract"}, NoCSRF: true,
			Headers: map[string]string{"X-CSRF-Token": "not-the-token"},
		},
		contract.Case{
			Name: "write-with-form-content-type", Method: "POST", Path: "/api/folders",
			RawBody: "name=contract", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "write-with-malformed-json", Method: "POST", Path: "/api/folders",
			RawBody: `{"name": "contract"`, ContentType: "application/json",
		},
	)
}
