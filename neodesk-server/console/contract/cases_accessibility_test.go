//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// The signed-in account's own accessibility settings
// (console/routes/accessibility). These routes came with the Go console;
// their goldens were recorded from Go (CONTRACT_RECORD_NEW).
//
// Every role that may sign in reads and changes its own settings; signed
// out and pro get the sign-in refusal. Each role changes two settings,
// reads them back, sends a change the server refuses, and puts the
// defaults back, so the instance is left with every account at the
// defaults (a row each, which the bootstrap of the later groups shows).
func init() {
	defaults := map[string]any{
		"reduceMotion": "system", "moreContrast": "system", "underlineLinks": false,
		"readableFont": false, "noSingleKeyShortcuts": false, "pauseLiveUpdates": false,
	}
	contract.Group("accessibility",
		contract.Case{Name: "read", Path: "/api/accessibility"},
		contract.Case{Name: "change", Method: "PUT", Path: "/api/accessibility", Body: map[string]any{"moreContrast": "on", "pauseLiveUpdates": true}},
		contract.Case{Name: "read-after-change", Path: "/api/accessibility"},
		contract.Case{Name: "refused", Method: "PUT", Path: "/api/accessibility", Body: map[string]any{"fontScale": 120}},
		contract.Case{Name: "change-without-token", Method: "PUT", Path: "/api/accessibility", Body: map[string]any{"readableFont": true}, NoCSRF: true},
		contract.Case{Name: "reset", Method: "PUT", Path: "/api/accessibility", Body: defaults},
	)
}
