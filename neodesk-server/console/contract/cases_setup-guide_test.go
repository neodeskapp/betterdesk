//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// The setup guide on the overview (console/routes/setupguide). These
// routes came with the Go console; their goldens were recorded from Go
// (CONTRACT_RECORD_NEW).
//
// The test instance opens the guide for its first account, admin, which
// the suite never signs in as: every role here finds it closed, as a
// second admin of a new install does. The routes need server.config, so
// the roles without it get the guard's refusal and none of the steps.
// Each role that may brings the guide up and hides it again, so the
// instance is left with the guide closed for every account the suite
// uses. Which steps are done follows what the groups before this one
// left on the instance (seeded devices and sessions, the backup the
// settings-backup group downloaded).
func init() {
	contract.Group("setup-guide",
		contract.Case{Name: "status", Path: "/api/setup-guide"},
		contract.Case{Name: "show", Method: "POST", Path: "/api/setup-guide/show", Body: map[string]any{}},
		contract.Case{Name: "status-shown", Path: "/api/setup-guide"},
		contract.Case{Name: "hide", Method: "POST", Path: "/api/setup-guide/hide", Body: map[string]any{}},
		contract.Case{Name: "status-hidden", Path: "/api/setup-guide"},
		contract.Case{Name: "show-without-token", Method: "POST", Path: "/api/setup-guide/show", Body: map[string]any{}, NoCSRF: true},
	)
}
