//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The socket /ws/device-status: who may open it (the origin check, then
// a session with device.view), what the handshake checks of the websocket
// library answer behind the session check, and the device_id_changed
// message a new device id pushes to every open socket of an account that
// sees the device (every role here). The id change goes through
// 900100018 (offline, in no folder, not kept online by the fake
// heartbeats) and back, and is filtered by type, because the socket also
// carries the status changes the signal server reports.
//
// The group's name starts with "ws-" so it runs after the route groups:
// it writes audit entries and changes a device's id history.
func init() {
	changed := map[string]string{"type": "device_id_changed"}
	contract.Group("ws-device-status",
		contract.Case{Name: "open", Path: "/ws/device-status", Socket: &contract.Socket{Name: "status"}},
		contract.Case{
			Name: "change-id", Method: "POST", Path: "/api/devices/900100018/change-id", As: "admin",
			Body: map[string]any{"newId": "900100098"},
		},
		contract.Case{Name: "id-changed", Path: "/ws/device-status", Socket: &contract.Socket{Name: "status", Expect: 1, Filter: changed}},
		contract.Case{
			Name: "change-id-back", Method: "POST", Path: "/api/devices/900100098/change-id", As: "admin",
			Body: map[string]any{"newId": "900100018"},
		},
		contract.Case{Name: "id-changed-back", Path: "/ws/device-status", Socket: &contract.Socket{Name: "status", Expect: 1, Filter: changed, Close: true}},
		contract.Case{Name: "browser-sends", Path: "/ws/device-status", Socket: &contract.Socket{Name: "talk", Send: []any{"hello", map[string]any{"type": "subscribe"}}, Close: true}},
		contract.Case{Name: "foreign-origin", Path: "/ws/device-status", Socket: &contract.Socket{Name: "foreign", Origin: "https://evil.example", Close: true}},
		contract.Case{Name: "no-origin", Path: "/ws/device-status", Socket: &contract.Socket{Name: "noorigin", Origin: "-", Close: true}},
		contract.Case{Name: "no-session", Path: "/ws/device-status", NoSession: true, Socket: &contract.Socket{Name: "nosession"}},
		contract.Case{Name: "subprotocols", Path: "/ws/device-status", Socket: &contract.Socket{Name: "proto", Protocols: []string{"second", "first"}, Close: true}},
		contract.Case{
			Name: "subprotocols-malformed", Path: "/ws/device-status", Headers: map[string]string{"Sec-WebSocket-Protocol": "a,,b"},
			Socket: &contract.Socket{Name: "badproto"},
		},
		contract.Case{
			Name: "version-12", Path: "/ws/device-status", Headers: map[string]string{"Sec-WebSocket-Version": "12"},
			Socket: &contract.Socket{Name: "badversion"},
		},
		contract.Case{
			Name: "version-8", Path: "/ws/device-status", Headers: map[string]string{"Sec-WebSocket-Version": "8"},
			Socket: &contract.Socket{Name: "version8", Close: true},
		},
		contract.Case{
			Name: "key-malformed", Path: "/ws/device-status", Headers: map[string]string{"Sec-WebSocket-Key": "not-a-key"},
			Socket: &contract.Socket{Name: "badkey"},
		},
		contract.Case{
			Name: "upgrade-other", Path: "/ws/device-status", Headers: map[string]string{"Upgrade": "h2c"},
			Socket: &contract.Socket{Name: "h2c"},
		},
		contract.Case{
			Name: "query-and-dots", Path: "/ws/./x/../device-status", Query: map[string][]string{"a": {"1"}},
			Socket: &contract.Socket{Name: "dots", Close: true},
		},
	)
}
