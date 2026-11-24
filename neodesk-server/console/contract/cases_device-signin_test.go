//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// Saved sign-in of a device (console/routes/devices/signin.go, BUGS L469)
// and the answer to a device's password question from the organization's
// stored device password (BUGS L454). These routes came with the Go
// console; their goldens were recorded from Go (CONTRACT_RECORD_NEW).
//
// Only the super admin roles hold Use and Manage saved sign-in, so every
// other role gets the refusal. Each role that may sets the details of
// device 900100018, turns the switch on, tries to use them without an open
// viewer session (the suite has none: the use is refused) and clears them
// again, so the instance is left without saved details. The passwords are
// made up and open nothing.
func init() {
	contract.Group("device-signin",
		contract.Case{Name: "status", Path: "/api/devices/900100018/signin"},
		contract.Case{Name: "status-unknown", Path: "/api/devices/999999999/signin"},
		contract.Case{
			Name: "set-without-password", Method: "PUT", Path: "/api/devices/900100018/signin",
			Body: map[string]any{"username": "LAB\\admin", "password": ""},
		},
		contract.Case{
			Name: "set-with-a-tab", Method: "PUT", Path: "/api/devices/900100018/signin",
			Body: map[string]any{"username": "admin", "password": "made\tup"},
		},
		contract.Case{
			Name: "set", Method: "PUT", Path: "/api/devices/900100018/signin",
			Body: map[string]any{"username": "LAB\\admin", "password": "Made-Up-Contract-Pass"},
		},
		contract.Case{Name: "status-after-set", Path: "/api/devices/900100018/signin"},
		contract.Case{Name: "switch-on", Method: "PATCH", Path: "/api/devices/900100018/signin", Body: map[string]any{"auto": true}},
		contract.Case{Name: "switch-bad-form", Method: "PATCH", Path: "/api/devices/900100018/signin", Body: map[string]any{"form": "pin"}},
		contract.Case{Name: "use-without-session", Method: "POST", Path: "/api/devices/900100018/signin/use", Body: map[string]any{"session": "00000000-0000-4000-8000-000000000000"}},
		contract.Case{Name: "use-no-session-given", Method: "POST", Path: "/api/devices/900100018/signin/use", Body: map[string]any{}},
		contract.Case{Name: "clear", Method: "DELETE", Path: "/api/devices/900100018/signin"},
		contract.Case{Name: "status-after-clear", Path: "/api/devices/900100018/signin"},
		contract.Case{Name: "switch-without-details", Method: "PATCH", Path: "/api/devices/900100018/signin", Body: map[string]any{"auto": true}},
		contract.Case{
			Name: "answer-without-stored-password", Method: "POST", Path: "/api/devices/900100018/connect-password/answer",
			Body: map[string]any{"salt": "contract-salt", "challenge": "contract-challenge"},
		},
		contract.Case{
			Name: "answer-unknown-device", Method: "POST", Path: "/api/devices/999999999/connect-password/answer",
			Body: map[string]any{"salt": "contract-salt", "challenge": "contract-challenge"},
		},
	)
}
