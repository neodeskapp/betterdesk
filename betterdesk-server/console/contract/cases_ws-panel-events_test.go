//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The socket /ws/panel-events: a session with enrollment.approve may open
// it, no origin check. Its messages here are made by Node routes that
// stay in Node (a LAN registration request and its approval), so the
// group also shows that what Node publishes still reaches the socket
// wherever it lives. Each turn removes the registration it made. The
// messages the API server's event bus brings (enrollment, help requests)
// cannot be caused through the console; the package's tests replay what
// Node made of them.
func init() {
	pending := map[string]string{"type": "registration_pending"}
	changed := map[string]string{"type": "registration_changed"}
	contract.Group("ws-panel-events",
		contract.Case{Name: "open", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel"}},
		contract.Case{
			Name: "register-request", Method: "POST", Path: "/api/bd/register-request", As: "signed-out",
			Body:    map[string]any{"device_id": "contract-ws-lan", "hostname": "lan-box", "platform": "linux", "version": "1.0.0"},
			Capture: map[string]string{"registration": "id"},
		},
		contract.Case{Name: "registration-pending", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: pending}},
		contract.Case{
			Name: "registration-approve", Method: "PUT", Path: "/api/bd/api/registrations/{registration}/approve", As: "admin",
			Body: map[string]any{},
		},
		contract.Case{Name: "registration-changed", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: changed}},
		contract.Case{Name: "registration-delete", Method: "DELETE", Path: "/api/bd/api/registrations/{registration}", As: "admin"},
		contract.Case{Name: "nothing-else", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Filter: pending, Close: true}},
		contract.Case{Name: "foreign-origin", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "foreign", Origin: "https://evil.example", Close: true}},
		contract.Case{Name: "no-session", Path: "/ws/panel-events", NoSession: true, Socket: &contract.Socket{Name: "nosession"}},
		contract.Case{
			Name: "method-check-after-permission", Path: "/ws/panel-events", Headers: map[string]string{"Sec-WebSocket-Key": "short"},
			Socket: &contract.Socket{Name: "badkey"},
		},
	)
}
