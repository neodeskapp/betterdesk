//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The RDP and VNC gateway /ws/remote-target/<id>: a session with
// remote_target.connect and the target in the account's device scope; the
// console then opens the API server's tunnel for the target and passes the
// messages on. The test instance has no guacd and the target's host does
// not resolve, so the API server refuses the tunnel and the console closes
// the browser's socket. Admin makes the target in each turn and deletes
// it; the two steps go to routes the Go console answers (cases/devices
// pins them as Node answered) and were recorded with CONTRACT_RECORD_NEW=1.
func init() {
	const targets = "/api/remote-targets"
	ends := contract.Roles("signed-out", "admin")
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}

	contract.Group("ws-remote-target",
		contract.Case{
			Name: "target", Method: "POST", Path: targets, As: "admin",
			Body: map[string]any{
				"name": "Contract gateway", "protocol": "vnc", "host": "vnc.example.invalid", "port": 5900,
				"username": "contract-user", "enabled": true,
			},
			Capture: map[string]string{"target": "data.id"},
		},
		contract.Case{Name: "open", Path: "/ws/remote-target/{target}", Socket: sock("t", contract.Socket{WaitClose: true, Wait: 15e9})},
		contract.Case{Name: "foreign-origin", Path: "/ws/remote-target/{target}", Socket: sock("o", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "without-session", Path: "/ws/remote-target/{target}", NoSession: true, Socket: sock("w", contract.Socket{})},
		contract.Case{Name: "unknown-target", Path: "/ws/remote-target/rt_contract-none-0000", Socket: sock("u", contract.Socket{})},
		contract.Case{Name: "viewer-data", Path: "/api/remote/viewer/{target}"},
		contract.Case{Name: "viewer-data-cdap", Path: "/api/remote/viewer/{target}", Query: map[string][]string{"transport": {"cdap"}}, Roles: ends},
		contract.Case{Name: "target-deleted", Method: "DELETE", Path: targets + "/{target}", As: "admin"},
		contract.Case{Name: "after-delete", Path: "/ws/remote-target/{target}", Roles: ends, Socket: sock("d", contract.Socket{})},
	)
}
