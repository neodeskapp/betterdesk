//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The receiver app: the switch "Offer to install after quick support"
// (console/routes/onboard, server.config) and Devices › Add › Receiver app
// (console/routes/receiverapp, enrollment.manage). These routes came with
// the Go console; their goldens were recorded from Go. A test instance has
// no imported Support Agent release, so the setup names none and a
// download with a working token is 409 no_release. The switch is put back
// on at the end of each turn; the token the turn makes is revoked. The
// group runs after "tokens" (groups run by name), so the token it leaves
// behind does not move the ids the tokens group's answers carry.
func init() {
	admins := contract.Roles("server_admin", "admin")
	contract.Group("tokens-receiver-app",
		contract.Case{Name: "offer", Path: "/api/support/receiver-offer"},
		contract.Case{Name: "offer-off", Method: "PUT", Path: "/api/support/receiver-offer", Body: map[string]any{"enabled": false}},
		contract.Case{Name: "offer-after-off", Path: "/api/support/receiver-offer", Roles: admins},
		contract.Case{Name: "offer-not-a-switch", Method: "PUT", Path: "/api/support/receiver-offer", Body: map[string]any{"enabled": "yes"}, Roles: admins},
		contract.Case{Name: "offer-on", Method: "PUT", Path: "/api/support/receiver-offer", Body: map[string]any{"enabled": true}, Roles: admins},

		contract.Case{Name: "setup", Path: "/api/panel/receiver/setup"},
		contract.Case{Name: "installer-no-token", Method: "POST", Path: "/api/panel/receiver/installer", Body: map[string]any{}},
		contract.Case{Name: "installer-short-token", Method: "POST", Path: "/api/panel/receiver/installer", Body: map[string]any{"token": "short"}, Roles: admins},
		contract.Case{Name: "installer-unknown-token", Method: "POST", Path: "/api/panel/receiver/installer", Body: map[string]any{"token": "0123456789abcdef0123456789abcdef"}, Roles: admins},
		contract.Case{
			Name: "token-for-installer", Method: "POST", Path: "/api/panel/tokens",
			Body:          map[string]any{"name": "contract receiver", "max_uses": 1, "expires_in": 3600},
			Capture:       map[string]string{"receiver_token": "data.id"},
			CaptureSecret: map[string]string{"receiver_token_value": "data.token"},
			Roles:         admins,
		},
		contract.Case{Name: "installer-unknown-folder", Method: "POST", Path: "/api/panel/receiver/installer", Body: map[string]any{"token": "{secret:receiver_token_value}", "group": 999999999}, Roles: admins},
		contract.Case{Name: "installer-no-release", Method: "POST", Path: "/api/panel/receiver/installer", Body: map[string]any{"token": "{secret:receiver_token_value}"}, Roles: admins},
		contract.Case{Name: "token-for-installer-revoke", Method: "DELETE", Path: "/api/panel/tokens/{receiver_token}", Roles: admins},
		// A rollout token (A278) needs a limit and an expiry, and a kind that
		// exists. Only refusals here: a token made here would move the ids
		// of the tokens made after it; the Go tests cover the rest.
		contract.Case{
			Name: "rollout-token-no-limit", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made", "kind": "rollout", "expires_in": 3600}, Roles: admins,
		},
		contract.Case{
			Name: "rollout-token-no-expiry", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made", "kind": "rollout", "max_uses": 10}, Roles: admins,
		},
		contract.Case{
			Name: "rollout-token-unknown-kind", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made", "kind": "many", "max_uses": 10, "expires_in": 3600}, Roles: admins,
		},
		contract.Case{
			Name: "token-unknown-folder", Method: "POST", Path: "/api/panel/tokens",
			Body: map[string]any{"name": "never made", "max_uses": 1, "folder_id": 999999999}, Roles: admins,
		},
	)
}
