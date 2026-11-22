//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The device API under /api/bd (routes/bd-api.routes.js): the routes the
// BetterDesk agent and the desktop client call with a device token
// (device_token is issued for 900100001, device_token_other for 900100002,
// both by the instance's admin account), the operator routes that take a
// live account token of an admin or operator (operator_token, the admin's
// own) and refuse a device token, the public identity and branding routes,
// and the notification bell of the web app, which takes the session.
//
// Device requests carry no cookies and run signed out. The relay flows
// with an agent on its signal socket are in ws-bd-relay; here are the
// refusals and the answers without one. GET /api/bd/chat/history past its
// checks ended the old console's process (BUGS BD), so no case goes
// there. Successes that would change what later groups read (a
// registration of a demo device writes its address, an operator session
// adds to the connection audit, accepting a seeded help request) are left
// to the replays of package bdapi. Registrations are limited to 20 a minute per
// address, so the group sends fewer than that.
func init() {
	signedOut := contract.Roles("signed-out")
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	operator := map[string]string{"Authorization": "Bearer {secret:operator_token}"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	basic := map[string]string{"Authorization": "Basic ZTJlX2FkbWluOmNvbnRyYWN0"}
	header := func(id string) map[string]string { return map[string]string{"X-Device-Id": id} }
	with := func(a, b map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range a {
			out[k] = v
		}
		for k, v := range b {
			out[k] = v
		}
		return out
	}
	// dev is a request of a device: no session, no cookies, no CSRF token.
	dev := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, signedOut
		return c
	}
	// A valid Ed25519 public key (32 bytes) and proof headers that do not
	// verify.
	publicKey := "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo="
	proof := map[string]string{
		"X-BD-Enrollment-Timestamp": "2001-01-01T00:00:00Z",
		"X-BD-Enrollment-Nonce":     "contract-nonce",
		"X-BD-Enrollment-Signature": strings.Repeat("A", 86) + "==",
	}
	long := strings.Repeat("x", 4097)

	contract.Group("bd-api",
		// Public: who the panel is and how it looks
		contract.Case{Name: "server-info", Path: "/api/bd/server-info"},
		contract.Case{Name: "branding", Path: "/api/bd/branding"},
		contract.Case{Name: "appearance", Path: "/api/bd/appearance"},
		dev(contract.Case{Name: "server-info-as-a-device", Path: "/api/bd/server-info", Headers: device}),

		// Registration: who may register, and how a known device is refreshed
		dev(contract.Case{Name: "register-without-identification", Method: "POST", Path: "/api/bd/register", Body: map[string]any{"device_id": "contract-bd-new"}}),
		dev(contract.Case{Name: "register-device-id-too-short", Method: "POST", Path: "/api/bd/register", Headers: header("ab"), Body: map[string]any{}}),
		dev(contract.Case{Name: "register-device-id-with-a-dot", Method: "POST", Path: "/api/bd/register", Headers: header("contract.bd"), Body: map[string]any{}}),
		dev(contract.Case{Name: "register-unknown-token", Method: "POST", Path: "/api/bd/register", Headers: unknown, Body: map[string]any{"device_id": "contract-bd-new"}}),
		dev(contract.Case{
			Name: "register-basic-auth-and-header", Method: "POST", Path: "/api/bd/register", Headers: with(basic, header("contract-bd-new")),
			Body: map[string]any{"device_id": "contract-bd-new"},
		}),
		dev(contract.Case{
			Name: "register-new-device-without-proof", Method: "POST", Path: "/api/bd/register", Headers: header("contract-bd-new"),
			Body: map[string]any{"device_id": "contract-bd-new", "uuid": "u-1", "public_key": "AAAA1111"},
		}),
		dev(contract.Case{
			Name: "register-new-device-proof-not-verified", Method: "POST", Path: "/api/bd/register", Headers: with(header("contract-bd-new"), proof),
			Body: map[string]any{"device_id": "contract-bd-new", "uuid": "u-1", "public_key": publicKey},
		}),
		dev(contract.Case{
			Name: "register-new-device-unknown-enrollment-token", Method: "POST", Path: "/api/bd/register", Headers: header("contract-bd-new"),
			Body: map[string]any{"device_id": "contract-bd-new", "enrollment_token": "contract-no-such-enrollment-token"},
		}),
		dev(contract.Case{
			Name: "register-claim-differs-from-header", Method: "POST", Path: "/api/bd/register", Headers: header("contract-bd-new"),
			Body: map[string]any{"device_id": "contract-bd-other"},
		}),
		dev(contract.Case{
			Name: "register-claim-differs-from-token", Method: "POST", Path: "/api/bd/register", Headers: device,
			Body: map[string]any{"device_id": "900100002"},
		}),
		dev(contract.Case{
			Name: "register-known-device-other-uuid", Method: "POST", Path: "/api/bd/register", Headers: device,
			Body: map[string]any{"device_id": "900100001", "uuid": "contract-other-uuid"},
		}),
		dev(contract.Case{
			Name: "register-banned-device", Method: "POST", Path: "/api/bd/register", Headers: header("900100028"),
			Body: map[string]any{"device_id": "900100028", "uuid": "demo-uuid-900100028"},
		}),
		contract.Case{
			Name: "register-with-a-session", Method: "POST", Path: "/api/bd/register", Headers: header("contract-bd-new"), NoCSRF: true,
			Body: map[string]any{"device_id": "contract-bd-new"}, Roles: contract.Roles("viewer", "admin"),
		},

		// Heartbeat
		dev(contract.Case{Name: "heartbeat-without-token", Method: "POST", Path: "/api/bd/heartbeat", Body: map[string]any{"device_id": "900100001"}}),
		dev(contract.Case{Name: "heartbeat-unknown-token", Method: "POST", Path: "/api/bd/heartbeat", Headers: unknown, Body: map[string]any{"device_id": "900100001"}}),
		dev(contract.Case{Name: "heartbeat-device-id-header-alone", Method: "POST", Path: "/api/bd/heartbeat", Headers: header("900100001"), Body: map[string]any{}}),
		dev(contract.Case{Name: "heartbeat-other-device", Method: "POST", Path: "/api/bd/heartbeat", Headers: device, Body: map[string]any{"device_id": "900100002"}}),
		dev(contract.Case{Name: "heartbeat", Method: "POST", Path: "/api/bd/heartbeat", Headers: device, Body: map[string]any{"device_id": "900100001"}}),
		dev(contract.Case{Name: "heartbeat-without-claim", Method: "POST", Path: "/api/bd/heartbeat", Headers: other, Body: map[string]any{}}),
		contract.Case{Name: "heartbeat-with-a-session", Method: "POST", Path: "/api/bd/heartbeat", Headers: device, NoCSRF: true, Body: map[string]any{"device_id": "900100001"}},

		// Relay sessions without an agent online
		dev(contract.Case{Name: "connect-without-token", Method: "POST", Path: "/api/bd/connect", Body: map[string]any{"target_id": "900100002"}}),
		dev(contract.Case{Name: "connect-unknown-token", Method: "POST", Path: "/api/bd/connect", Headers: unknown, Body: map[string]any{"target_id": "900100002"}}),
		dev(contract.Case{Name: "connect-without-target", Method: "POST", Path: "/api/bd/connect", Headers: device, Body: map[string]any{}}),
		dev(contract.Case{
			Name: "connect-initiator-mismatch", Method: "POST", Path: "/api/bd/connect", Headers: device,
			Body: map[string]any{"target_id": "900100002", "initiator_id": "900100003"},
		}),
		dev(contract.Case{Name: "connect-unknown-target", Method: "POST", Path: "/api/bd/connect", Headers: device, Body: map[string]any{"target_id": "contract-none"}}),
		dev(contract.Case{
			Name: "connect-offline-target", Method: "POST", Path: "/api/bd/connect", Headers: device,
			Body:          map[string]any{"target_id": "900100002", "initiator_id": "900100001", "public_key": "contract-pk"},
			Capture:       map[string]string{"session": "session_id"},
			CaptureSecret: map[string]string{"bd_initiator_token": "token"},
		}),
		dev(contract.Case{Name: "session-as-initiator", Path: "/api/bd/session/{session}", Headers: device}),
		dev(contract.Case{Name: "session-as-target", Path: "/api/bd/session/{session}", Headers: other}),
		dev(contract.Case{Name: "session-without-token", Path: "/api/bd/session/{session}"}),
		dev(contract.Case{Name: "session-unknown", Path: "/api/bd/session/contract-none", Headers: device}),
		dev(contract.Case{Name: "heartbeat-of-the-target", Method: "POST", Path: "/api/bd/heartbeat", Headers: other, Body: map[string]any{"device_id": "900100002"},
			Volatile: []string{"pending_connections[0].created_at"}}),
		dev(contract.Case{Name: "session-delete-unknown", Method: "DELETE", Path: "/api/bd/session/contract-none", Headers: device}),
		dev(contract.Case{Name: "session-delete", Method: "DELETE", Path: "/api/bd/session/{session}", Headers: device}),
		dev(contract.Case{Name: "session-after-delete", Path: "/api/bd/session/{session}", Headers: device}),
		dev(contract.Case{Name: "peers", Path: "/api/bd/peers", Headers: device}),
		dev(contract.Case{Name: "peers-without-token", Path: "/api/bd/peers"}),
		dev(contract.Case{Name: "peer", Path: "/api/bd/peer/900100002", Headers: device}),
		dev(contract.Case{Name: "peer-banned", Path: "/api/bd/peer/900100028", Headers: device}),
		dev(contract.Case{Name: "peer-unknown", Path: "/api/bd/peer/contract-none", Headers: device}),
		dev(contract.Case{Name: "peer-unknown-token", Path: "/api/bd/peer/900100002", Headers: unknown}),

		// Help requests and chat of the agent
		dev(contract.Case{Name: "help-request-without-token", Method: "POST", Path: "/api/bd/help-request", Body: map[string]any{"device_id": "900100001"}}),
		dev(contract.Case{Name: "help-request-without-device-id", Method: "POST", Path: "/api/bd/help-request", Headers: device, Body: map[string]any{"message": "x"}}),
		dev(contract.Case{Name: "help-request-device-id-number", Method: "POST", Path: "/api/bd/help-request", Headers: device, Body: map[string]any{"device_id": 900100001}}),
		dev(contract.Case{Name: "help-request-other-device", Method: "POST", Path: "/api/bd/help-request", Headers: device, Body: map[string]any{"device_id": "900100002"}}),
		dev(contract.Case{
			Name: "help-request", Method: "POST", Path: "/api/bd/help-request", Headers: device,
			Body: map[string]any{"device_id": "900100001", "hostname": "demo-reception-01", "message": "Contract: the printer"},
		}),
		dev(contract.Case{Name: "chat-send-without-token", Method: "POST", Path: "/api/bd/chat/send", Body: map[string]any{"device_id": "900100001", "content": "hi"}}),
		dev(contract.Case{Name: "chat-send-without-content", Method: "POST", Path: "/api/bd/chat/send", Headers: device, Body: map[string]any{"device_id": "900100001"}}),
		dev(contract.Case{Name: "chat-send-blank", Method: "POST", Path: "/api/bd/chat/send", Headers: device, Body: map[string]any{"device_id": "900100001", "content": " \t "}}),
		dev(contract.Case{Name: "chat-send-too-long", Method: "POST", Path: "/api/bd/chat/send", Headers: device, Body: map[string]any{"device_id": "900100001", "content": long}}),
		dev(contract.Case{Name: "chat-send-without-device-id", Method: "POST", Path: "/api/bd/chat/send", Headers: device, Body: map[string]any{"content": "hi"}}),
		dev(contract.Case{Name: "chat-send", Method: "POST", Path: "/api/bd/chat/send", Headers: device, Body: map[string]any{"device_id": "900100001", "content": "hi", "sender": "Front desk"}}),
		dev(contract.Case{Name: "chat-history-without-token", Path: "/api/bd/chat/history"}),
		dev(contract.Case{Name: "chat-history-without-device-id", Path: "/api/bd/chat/history", Headers: device}),
		dev(contract.Case{Name: "chat-history-other-device", Path: "/api/bd/chat/history", Query: map[string][]string{"device_id": {"900100002"}}, Headers: device}),

		// Operator mode of the desktop client
		dev(contract.Case{Name: "operator-login-without-body", Method: "POST", Path: "/api/bd/operator/login"}),
		dev(contract.Case{Name: "operator-login-password-number", Method: "POST", Path: "/api/bd/operator/login", Body: map[string]any{"username": "e2e_admin", "password": 5}}),
		dev(contract.Case{Name: "operator-login", Method: "POST", Path: "/api/bd/operator/login", Body: map[string]any{"username": "e2e_admin", "password": "contract-wrong", "device_id": "900100001"}}),
		dev(contract.Case{Name: "operator-devices-without-token", Path: "/api/bd/operator/devices"}),
		dev(contract.Case{Name: "operator-devices-basic", Path: "/api/bd/operator/devices", Headers: basic}),
		dev(contract.Case{
			Name: "operator-devices", Path: "/api/bd/operator/devices", Headers: operator,
			Volatile: []string{"~devices[*].online", "~devices[*].last_online"},
		}),
		dev(contract.Case{Name: "operator-devices-with-a-device-token", Path: "/api/bd/operator/devices", Headers: device}),
		dev(contract.Case{Name: "operator-help-requests", Path: "/api/bd/help-requests", Headers: operator}),
		dev(contract.Case{Name: "operator-help-requests-pending", Path: "/api/bd/help-requests", Query: map[string][]string{"status": {"pending"}}, Headers: operator}),
		dev(contract.Case{Name: "operator-help-requests-without-token", Path: "/api/bd/help-requests"}),
		dev(contract.Case{Name: "operator-help-request-accept-unknown", Method: "POST", Path: "/api/bd/help-requests/999999999/accept", Headers: operator}),
		dev(contract.Case{Name: "operator-help-request-resolve-unknown", Method: "POST", Path: "/api/bd/help-requests/999999999/resolve", Headers: operator}),
		dev(contract.Case{Name: "operator-help-request-delete-unknown", Method: "DELETE", Path: "/api/bd/help-requests/999999999", Headers: operator}),
		dev(contract.Case{Name: "operator-help-request-accept-without-token", Method: "POST", Path: "/api/bd/help-requests/1/accept"}),
		dev(contract.Case{Name: "operator-sessions-without-token", Path: "/api/bd/operator/sessions"}),
		dev(contract.Case{Name: "operator-session-record-bad-action", Method: "POST", Path: "/api/bd/operator/sessions", Headers: operator, Body: map[string]any{"action": "pause", "device_id": "900100001"}}),
		dev(contract.Case{Name: "operator-session-record-bad-device", Method: "POST", Path: "/api/bd/operator/sessions", Headers: operator, Body: map[string]any{"action": "start", "device_id": "9 0"}}),
		dev(contract.Case{
			Name: "operator-sessions", Path: "/api/bd/operator/sessions", Query: map[string][]string{"limit": {"5"}}, Headers: operator,
			Volatile: []string{"~sessions[*].duration_secs"},
		}),

		// The notification bell of the web app
		contract.Case{Name: "notifications", Path: "/api/bd/notifications"},
		contract.Case{Name: "notifications-unread-limit", Path: "/api/bd/notifications", Query: map[string][]string{"unread_only": {"TRUE"}, "limit": {"2"}}},
		contract.Case{Name: "notification-read", Method: "POST", Path: "/api/bd/notifications/contract-n-1/read"},
		contract.Case{Name: "notification-read-without-csrf-token", Method: "POST", Path: "/api/bd/notifications/contract-n-1/read", NoCSRF: true},
		contract.Case{Name: "notifications-read-all", Method: "POST", Path: "/api/bd/notifications/read-all"},
		contract.Case{Name: "notifications-after-read-all", Path: "/api/bd/notifications"},
		dev(contract.Case{Name: "notifications-as-a-device", Path: "/api/bd/notifications", Headers: device}),
	)
}
