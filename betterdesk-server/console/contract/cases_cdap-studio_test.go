//go:build contract

package contract_test

import (
	"fmt"
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The flow routes of the CDAP studio. In each role's turn the role makes
// a flow of its own (operators and the super admin roles can) and the
// operator account makes another, so every role meets its own flow, the
// flow of somebody else and an id that names nothing. Each turn deletes
// what it made.
func init() {
	few := contract.Roles("signed-out", "viewer", "operator", "admin")
	makers := contract.Roles("operator", "super_admin", "admin")
	one := contract.Roles("operator")

	nodes := []any{}
	wires := []any{}
	for i := 1; i <= 12; i++ {
		nodes = append(nodes, map[string]any{"id": fmt.Sprintf("n%d", i), "type": "modbus-tcp", "label": fmt.Sprintf("Register %d", i), "x": 100 * i, "y": 150})
		if i > 1 {
			wires = append(wires, map[string]any{"from": fmt.Sprintf("n%d", i-1), "to": fmt.Sprintf("n%d", i), "fromPort": "out", "toPort": "in"})
		}
	}
	flow := map[string]any{"nodes": nodes, "wires": wires}
	small := map[string]any{"nodes": []any{map[string]any{"id": "n1", "type": "log"}}, "wires": []any{}}

	contract.Group("cdap-studio",
		contract.Case{Name: "templates", Path: "/api/cdap-studio/templates"},
		contract.Case{Name: "templates-head", Method: "HEAD", Path: "/api/cdap-studio/templates", Roles: few},
		contract.Case{
			Name: "templates-no-compression", Path: "/api/cdap-studio/templates",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: few,
		},
		contract.Case{Name: "list-before", Path: "/api/cdap-studio/flows"},

		contract.Case{
			Name: "create", Method: "POST", Path: "/api/cdap-studio/flows",
			Body:    map[string]any{"name": "  Contract flow  ", "description": "  made by the contract suite  ", "flow_json": flow},
			Capture: map[string]string{"flow": "id"},
		},
		contract.Case{
			Name: "create-other", Method: "POST", Path: "/api/cdap-studio/flows", As: "operator",
			Body:    map[string]any{"name": "Operator's flow", "flow_json": `{ "nodes": [ { "id": "n1", "type": "log" } ], "wires": [] }`},
			Capture: map[string]string{"other": "id"},
		},
		contract.Case{Name: "list", Path: "/api/cdap-studio/flows"},
		contract.Case{Name: "get", Path: "/api/cdap-studio/flows/{flow}"},
		contract.Case{Name: "get-other", Path: "/api/cdap-studio/flows/{other}"},
		contract.Case{Name: "get-unknown", Path: "/api/cdap-studio/flows/nothing-here", Roles: few},
		contract.Case{Name: "get-head", Method: "HEAD", Path: "/api/cdap-studio/flows/{flow}", Roles: few},

		contract.Case{
			Name: "update", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			Body: map[string]any{"name": " Contract flow v2 ", "description": "changed", "flow_json": small},
		},
		contract.Case{Name: "get-after-update", Path: "/api/cdap-studio/flows/{flow}", Roles: makers},
		contract.Case{
			Name: "update-name-only", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			Body: map[string]any{"name": "Contract flow"}, Roles: makers,
		},
		contract.Case{
			Name: "update-flow-back", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			Body: map[string]any{"flow_json": flow, "description": nil}, Roles: makers,
		},
		contract.Case{
			Name: "update-not-json", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			Body: map[string]any{"flow_json": "{nope"}, Roles: few,
		},
		contract.Case{
			Name: "update-other", Method: "PUT", Path: "/api/cdap-studio/flows/{other}",
			Body: map[string]any{"description": "seen by another account"},
		},
		contract.Case{
			Name: "update-unknown", Method: "PUT", Path: "/api/cdap-studio/flows/nothing-here",
			Body: map[string]any{"name": "x"}, Roles: few,
		},
		contract.Case{
			Name: "update-without-csrf-token", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			Body: map[string]any{"name": "x"}, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "update-as-form", Method: "PUT", Path: "/api/cdap-studio/flows/{flow}",
			RawBody: "name=x", ContentType: "application/x-www-form-urlencoded", Roles: few,
		},

		contract.Case{Name: "deploy", Method: "POST", Path: "/api/cdap-studio/flows/{flow}/deploy", Body: map[string]any{}},
		contract.Case{Name: "deploy-other", Method: "POST", Path: "/api/cdap-studio/flows/{other}/deploy", Body: map[string]any{}},
		contract.Case{Name: "deploy-unknown", Method: "POST", Path: "/api/cdap-studio/flows/nothing-here/deploy", Body: map[string]any{}, Roles: few},
		contract.Case{Name: "get-after-deploy", Path: "/api/cdap-studio/flows/{flow}", Roles: makers},
		contract.Case{Name: "test", Method: "POST", Path: "/api/cdap-studio/flows/{flow}/test", Body: map[string]any{}},
		contract.Case{Name: "test-other", Method: "POST", Path: "/api/cdap-studio/flows/{other}/test", Body: map[string]any{}},
		contract.Case{Name: "test-unknown", Method: "POST", Path: "/api/cdap-studio/flows/nothing-here/test", Body: map[string]any{}, Roles: few},
		contract.Case{Name: "export", Path: "/api/cdap-studio/flows/{flow}/export"},
		contract.Case{Name: "export-other", Path: "/api/cdap-studio/flows/{other}/export"},
		contract.Case{Name: "export-unknown", Path: "/api/cdap-studio/flows/nothing-here/export", Roles: few},
		contract.Case{Name: "export-head", Method: "HEAD", Path: "/api/cdap-studio/flows/{flow}/export", Roles: few},
		contract.Case{
			Name: "export-no-compression", Path: "/api/cdap-studio/flows/{flow}/export",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: one,
		},

		contract.Case{
			Name: "import", Method: "POST", Path: "/api/cdap-studio/flows/import",
			Body:    map[string]any{"version": "1.0", "format": "bdflow", "name": "  Imported by the contract suite  ", "description": "d", "flow": small},
			Capture: map[string]string{"imported": "id"},
		},
		contract.Case{Name: "get-imported", Path: "/api/cdap-studio/flows/{imported}", Roles: makers},
		contract.Case{Name: "delete-imported", Method: "DELETE", Path: "/api/cdap-studio/flows/{imported}", Roles: makers},
		contract.Case{
			Name: "import-without-name", Method: "POST", Path: "/api/cdap-studio/flows/import",
			Body: map[string]any{"flow": []any{}}, Roles: one,
			Capture: map[string]string{"unnamed": "id"},
		},
		contract.Case{Name: "get-import-without-name", Path: "/api/cdap-studio/flows/{unnamed}", Roles: one},
		contract.Case{Name: "delete-import-without-name", Method: "DELETE", Path: "/api/cdap-studio/flows/{unnamed}", Roles: one},
		contract.Case{
			Name: "import-without-flow", Method: "POST", Path: "/api/cdap-studio/flows/import",
			Body: map[string]any{"name": "x"}, Roles: few,
		},
		contract.Case{
			Name: "import-flow-as-text", Method: "POST", Path: "/api/cdap-studio/flows/import",
			Body: map[string]any{"name": "x", "flow": "{}"}, Roles: few,
		},
		contract.Case{
			Name: "import-as-form", Method: "POST", Path: "/api/cdap-studio/flows/import",
			RawBody: "name=x&flow=%7B%7D", ContentType: "application/x-www-form-urlencoded", Roles: few,
		},
		contract.Case{
			Name: "import-without-csrf-token", Method: "POST", Path: "/api/cdap-studio/flows/import",
			Body: map[string]any{"flow": small}, NoCSRF: true, Roles: few,
		},

		contract.Case{
			Name: "create-without-name", Method: "POST", Path: "/api/cdap-studio/flows",
			Body: map[string]any{"name": "   ", "flow_json": "{}"},
		},
		contract.Case{
			Name: "create-name-too-long", Method: "POST", Path: "/api/cdap-studio/flows",
			Body: map[string]any{"name": "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn"}, Roles: few,
		},
		contract.Case{
			Name: "create-not-json", Method: "POST", Path: "/api/cdap-studio/flows",
			Body: map[string]any{"name": "x", "flow_json": "{nope"}, Roles: few,
		},
		contract.Case{
			Name: "create-body-not-json", Method: "POST", Path: "/api/cdap-studio/flows",
			RawBody: `{"name":`, ContentType: "application/json", Roles: few,
		},
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: "/api/cdap-studio/flows",
			Body: map[string]any{"name": "x"}, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/cdap-studio/flows",
			RawBody: "name=x", ContentType: "application/x-www-form-urlencoded", Roles: few,
		},
		contract.Case{
			Name: "list-of-another-query", Path: "/api/cdap-studio/flows",
			Query: url.Values{"createdBy": {"e2e_operator"}}, Roles: contract.Roles("viewer", "admin"),
		},

		contract.Case{Name: "delete-other", Method: "DELETE", Path: "/api/cdap-studio/flows/{other}", Roles: contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "global_admin", "server_admin")},
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: "/api/cdap-studio/flows/nothing-here", Roles: few},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/cdap-studio/flows/{flow}", NoCSRF: true, Roles: few},
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/cdap-studio/flows/{flow}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/cdap-studio/flows/{flow}", Roles: makers},
		contract.Case{Name: "cleanup-other", Method: "DELETE", Path: "/api/cdap-studio/flows/{other}", As: "operator"},
		contract.Case{Name: "list-after", Path: "/api/cdap-studio/flows"},
	)
}
