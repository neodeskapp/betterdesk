//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The socket /ws/panel-events: after the origin check, a session with
// enrollment.approve may open it. Its messages here come from a LAN
// registration request and its approval. Each turn removes the
// registration it made.
// The messages the API server's event bus brings (enrollment, help
// requests) cannot be caused through the console; the package's tests
// replay what Node made of them.
//
// The goldens were recorded from Node behind the Go front door. The
// registration's id counts the registrations earlier groups made, and the
// approval's console_url says the port of the process that answered (5001
// for Node there, 5000 for Go and for a console on its own), so the marks
// with ~ compare only their type.
func init() {
	pending := map[string]string{"type": "registration_pending"}
	changed := map[string]string{"type": "registration_changed"}
	messageID := []string{"~messages[*].json.registration.id"}
	contract.Group("ws-panel-events",
		contract.Case{Name: "open", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel"}},
		contract.Case{
			Name: "register-request", Method: "POST", Path: "/api/nd/register-request", As: "signed-out",
			Body:     map[string]any{"device_id": "contract-ws-lan", "hostname": "lan-box", "platform": "linux", "version": "1.0.0"},
			Capture:  map[string]string{"registration": "id"},
			Volatile: []string{"~id"},
		},
		contract.Case{Name: "registration-pending", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: pending}, Volatile: messageID},
		contract.Case{
			Name: "registration-approve", Method: "PUT", Path: "/api/nd/api/registrations/{registration}/approve", As: "admin",
			Body:     map[string]any{},
			Volatile: []string{"~data.id", "~data.console_url"},
		},
		contract.Case{Name: "registration-changed", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Expect: 1, Filter: changed}, Volatile: messageID},
		contract.Case{Name: "registration-delete", Method: "DELETE", Path: "/api/nd/api/registrations/{registration}", As: "admin"},
		contract.Case{Name: "nothing-else", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "panel", Filter: pending, Close: true}},
		contract.Case{Name: "foreign-origin", Path: "/ws/panel-events", Socket: &contract.Socket{Name: "foreign", Origin: "https://evil.example", Close: true}},
		contract.Case{Name: "no-session", Path: "/ws/panel-events", NoSession: true, Socket: &contract.Socket{Name: "nosession"}},
		contract.Case{
			Name: "method-check-after-permission", Path: "/ws/panel-events", Headers: map[string]string{"Sec-WebSocket-Key": "short"},
			Socket: &contract.Socket{Name: "badkey"},
		},
	)
}
