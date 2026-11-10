//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The device gateway routes under /api/cdap. They ask the API server and
// hand its answer on; no gateway device is connected on a test instance,
// so a device route ends at the API server's "not found", which these
// routes answer with status 200.
func init() {
	const device = "/api/cdap/devices/900100001"
	few := contract.Roles("signed-out", "viewer", "admin")
	ends := contract.Roles("signed-out", "admin")
	admin := contract.Roles("admin")
	writers := contract.Roles("viewer", "operator", "admin")
	command := map[string]any{"widget_id": "power", "action": "set", "value": true, "reason": "contract"}

	contract.Group("cdap",
		contract.Case{Name: "status", Path: "/api/cdap/status"},
		contract.Case{Name: "status-head", Method: "HEAD", Path: "/api/cdap/status", Roles: ends},
		contract.Case{Name: "status-other-spelling", Path: "/API/Cdap/Status/", Roles: few},
		contract.Case{Name: "devices", Path: "/api/cdap/devices"},
		contract.Case{Name: "device", Path: device},
		contract.Case{Name: "device-manifest", Path: device + "/manifest"},
		contract.Case{Name: "device-state", Path: device + "/state"},
		contract.Case{Name: "device-linked", Path: device + "/linked"},
		contract.Case{Name: "device-linked-unknown", Path: "/api/cdap/devices/no-such-device/linked", Roles: few},
		contract.Case{Name: "alerts", Path: "/api/cdap/alerts"},
		contract.Case{
			Name: "alerts-of-a-device", Path: "/api/cdap/alerts",
			Query: url.Values{"device_id": {"900100001"}},
		},
		contract.Case{
			Name: "alerts-of-two-devices", Path: "/api/cdap/alerts",
			Query: url.Values{"device_id": {"900100001", "900100002"}}, Roles: few,
		},
		contract.Case{
			Name: "alerts-device-as-object", Path: "/api/cdap/alerts",
			Query: url.Values{"device_id[x]": {"1"}}, Roles: ends,
		},
		contract.Case{
			Name: "alerts-empty-device", Path: "/api/cdap/alerts",
			Query: url.Values{"device_id": {""}}, Roles: ends,
		},

		// What the device id of the address becomes on the way to the API
		// server
		contract.Case{Name: "device-id-dot", Path: "/api/cdap/devices/%2e", Roles: few},
		contract.Case{Name: "device-id-two-dots", Path: "/api/cdap/devices/%2e%2e/state", Roles: few},
		contract.Case{Name: "device-id-with-slash", Path: "/api/cdap/devices/a%2Fb/manifest", Roles: admin},
		contract.Case{Name: "device-id-with-space", Path: "/api/cdap/devices/a%20b%3Fc", Roles: admin},
		contract.Case{Name: "device-id-broken-escape", Path: "/api/cdap/devices/%ff/state", Roles: ends},

		contract.Case{Name: "command", Method: "POST", Path: device + "/command", Body: command},
		contract.Case{
			Name: "command-without-widget", Method: "POST", Path: device + "/command",
			Body: map[string]any{"action": "set"}, Roles: writers,
		},
		contract.Case{
			Name: "command-without-action", Method: "POST", Path: device + "/command",
			Body: map[string]any{"widget_id": "power", "action": ""}, Roles: writers,
		},
		contract.Case{
			Name: "command-without-value", Method: "POST", Path: device + "/command",
			Body: map[string]any{"widget_id": "power", "action": "press"}, Roles: writers,
		},
		contract.Case{
			Name: "command-odd-types", Method: "POST", Path: device + "/command",
			Body: map[string]any{"widget_id": 5, "action": []any{"a"}, "value": map[string]any{"n": 1.5}, "reason": 0}, Roles: admin,
		},
		contract.Case{
			Name: "command-list-body", Method: "POST", Path: device + "/command",
			RawBody: `[1, 2]`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{
			Name: "command-without-csrf-token", Method: "POST", Path: device + "/command",
			Body: command, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "command-as-form", Method: "POST", Path: device + "/command",
			RawBody: "widget_id=power&action=set", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},
		contract.Case{
			Name: "command-broken-json", Method: "POST", Path: device + "/command",
			RawBody: `{"widget_id":`, ContentType: "application/json", Roles: ends,
		},

		contract.Case{
			Name: "link", Method: "POST", Path: device + "/link",
			Body: map[string]any{"linked_peer_id": "900100002"},
		},
		contract.Case{
			Name: "link-clear", Method: "POST", Path: device + "/link",
			Body: map[string]any{"linked_peer_id": ""}, Roles: writers,
		},
		contract.Case{
			Name: "link-null", Method: "POST", Path: device + "/link",
			Body: map[string]any{"linked_peer_id": nil}, Roles: writers,
		},
		contract.Case{
			Name: "link-without-peer", Method: "POST", Path: device + "/link",
			Body: map[string]any{"peer": "900100002"}, Roles: writers,
		},
		contract.Case{
			Name: "link-odd-type", Method: "POST", Path: device + "/link",
			Body: map[string]any{"linked_peer_id": []any{1, "a"}}, Roles: admin,
		},

		// The gateway switch
		contract.Case{
			Name: "toggle-without-value", Method: "POST", Path: "/api/cdap/toggle",
			Body: map[string]any{}, Roles: writers,
		},
		contract.Case{
			Name: "toggle-text-value", Method: "POST", Path: "/api/cdap/toggle",
			Body: map[string]any{"enabled": "true"}, Roles: contract.Roles("server_admin", "admin"),
		},
		contract.Case{
			Name: "toggle-number-value", Method: "POST", Path: "/api/cdap/toggle",
			Body: map[string]any{"enabled": 1}, Roles: admin,
		},
		contract.Case{
			Name: "toggle-as-form", Method: "POST", Path: "/api/cdap/toggle",
			RawBody: "enabled=true", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},
		contract.Case{
			Name: "toggle-without-csrf-token", Method: "POST", Path: "/api/cdap/toggle",
			Body: map[string]any{"enabled": true}, NoCSRF: true, Roles: few,
		},
	)
}

// The gateway switch with a value: it saves the setting and opens a
// restart transaction. Each turn cancels the transaction it opened, which
// also puts the setting back. Nothing is restarted.
//
// Reading and cancelling a transaction are routes of the settings group:
// with CONTRACT_REQUIRE_GO these steps fail for as long as Node answers
// them, which is why the cases are a group of their own. Their goldens
// also show that the old console reads and undoes a transaction the Go
// console wrote.
func init() {
	contract.Group("cdap-toggle",
		contract.Case{
			Name: "toggle-off", Method: "POST", Path: "/api/cdap/toggle",
			Body:    map[string]any{"enabled": false},
			Capture: map[string]string{"tx": "restartRequired.id"},
		},
		contract.Case{Name: "toggle-pending", Path: "/api/settings/restart/pending"},
		contract.Case{
			Name: "toggle-on-again", Method: "POST", Path: "/api/cdap/toggle",
			Body:    map[string]any{"enabled": true},
			Headers: map[string]string{"Accept-Language": "de"},
		},
		// Another operator's switch while a transaction is open: admin
		// opens its own when the role of the turn could not
		contract.Case{
			Name: "toggle-by-admin", Method: "POST", Path: "/api/cdap/toggle",
			Body: map[string]any{"enabled": true}, As: "admin",
			Capture: map[string]string{"adminTx": "restartRequired.id"},
		},
		contract.Case{
			Name: "toggle-cancel-admin", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "{adminTx}"}, As: "admin",
		},
		contract.Case{
			Name: "toggle-cancel", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "{tx}"},
		},
		contract.Case{Name: "toggle-pending-after", Path: "/api/settings/restart/pending"},
	)
}
