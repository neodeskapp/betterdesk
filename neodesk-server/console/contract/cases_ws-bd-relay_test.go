//go:build contract

package contract_test

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The relay between two devices: /ws/nd-relay with the session and the
// tokens that POST /api/nd/connect (initiator) and the signal socket's
// relay_ready (target) hand out. 900100002 asks for 900100001, which is
// connected to /ws/nd-signal, accepts, and both sides are paired and pass
// a text and a binary message; then the initiator leaves and the target
// is told. A second request is refused by the target. Around it: the
// checks after the handshake (each a close code and reason), the origin
// check before it, and the session routes of the device API, which read
// the same sessions. Devices connect without a session, so the group runs
// signed out only.
func init() {
	signedOut := contract.Roles("signed-out")
	target := map[string][]string{"device_id": {"900100001"}, "token": {"{secret:device_token}"}}
	initiator := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	relay := func(session, token, role string) map[string][]string {
		out := map[string][]string{}
		if session != "" {
			out["session"] = []string{session}
		}
		if token != "" {
			out["token"] = []string{token}
		}
		if role != "" {
			out["role"] = []string{role}
		}
		return out
	}
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	contract.Group("ws-bd-relay",
		contract.Case{Name: "no-parameters", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("a", contract.Socket{WaitClose: true})},
		contract.Case{Name: "role-unknown", Path: "/ws/nd-relay", Query: relay("s", "t", "viewer"), Roles: signedOut, Socket: sock("b", contract.Socket{WaitClose: true})},
		contract.Case{Name: "session-unknown", Path: "/ws/nd-relay", Query: relay("contract-none", "t", "initiator"), Roles: signedOut, Socket: sock("c", contract.Socket{WaitClose: true})},
		contract.Case{Name: "foreign-origin", Path: "/ws/nd-relay", Query: relay("contract-none", "t", "initiator"), Roles: signedOut, Socket: sock("d", contract.Socket{Origin: "https://evil.example"})},

		contract.Case{Name: "target-online", Path: "/ws/nd-signal", Query: target, Roles: signedOut, Socket: sock("target", contract.Socket{Expect: 1})},
		contract.Case{
			Name: "connect", Method: "POST", Path: "/api/nd/connect", Roles: signedOut, Headers: initiator, NoCSRF: true, NoCookies: true,
			Body:          map[string]any{"target_id": "900100001", "public_key": "contract-pk"},
			Capture:       map[string]string{"relay": "session_id"},
			CaptureSecret: map[string]string{"initiator_token": "token"},
		},
		contract.Case{Name: "incoming-connection", Path: "/ws/nd-signal", Query: target, Roles: signedOut, Socket: sock("target", contract.Socket{Expect: 1})},
		contract.Case{
			Name: "session-pending", Path: "/api/nd/session/{relay}", Roles: signedOut, Headers: initiator, NoCookies: true,
		},
		contract.Case{
			Name: "heartbeat-shows-pending", Method: "POST", Path: "/api/nd/heartbeat", Roles: signedOut, NoCSRF: true, NoCookies: true,
			Headers: map[string]string{"Authorization": "Bearer {secret:device_token}"}, Body: map[string]any{"device_id": "900100001"},
			Volatile: []string{"pending_connections[0].created_at"},
		},
		contract.Case{
			Name: "token-wrong", Path: "/ws/nd-relay", Query: relay("{relay}", strings.Repeat("0", 64), "initiator"), Roles: signedOut,
			Socket: sock("wrong", contract.Socket{WaitClose: true}),
		},
		contract.Case{
			Name: "initiator-joins", Path: "/ws/nd-relay", Query: relay("{relay}", "{secret:initiator_token}", "initiator"), Roles: signedOut,
			Socket: sock("init", contract.Socket{}),
		},
		contract.Case{
			Name: "target-accepts", Path: "/ws/nd-signal", Query: target, Roles: signedOut,
			Socket:        sock("target", contract.Socket{Send: []any{map[string]any{"type": "relay_accept", "session_id": "{relay}"}}, Expect: 1}),
			CaptureSecret: map[string]string{"target_token": "messages[0].json.token"},
		},
		contract.Case{
			Name: "target-joins", Path: "/ws/nd-relay", Query: relay("{relay}", "{secret:target_token}", "target"), Roles: signedOut,
			Socket: sock("tgt", contract.Socket{Expect: 1}),
		},
		contract.Case{Name: "initiator-paired", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("init", contract.Socket{Expect: 1})},
		contract.Case{
			Name: "slot-taken", Path: "/ws/nd-relay", Query: relay("{relay}", "{secret:initiator_token}", "initiator"), Roles: signedOut,
			Socket: sock("init2", contract.Socket{WaitClose: true}),
		},
		contract.Case{Name: "session-active", Path: "/api/nd/session/{relay}", Roles: signedOut, Headers: initiator, NoCookies: true},
		contract.Case{Name: "initiator-sends", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("init", contract.Socket{Send: []any{"hello target", contract.SocketBinary{0, 1, 2, 255}}})},
		contract.Case{Name: "target-receives", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("tgt", contract.Socket{Expect: 2})},
		contract.Case{Name: "target-sends", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("tgt", contract.Socket{Send: []any{map[string]any{"type": "e2e", "n": 1}}})},
		contract.Case{Name: "initiator-receives", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("init", contract.Socket{Expect: 1})},
		contract.Case{Name: "initiator-leaves", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("init", contract.Socket{Close: true})},
		contract.Case{Name: "target-told", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("tgt", contract.Socket{WaitClose: true})},
		contract.Case{Name: "session-gone", Path: "/api/nd/session/{relay}", Roles: signedOut, Headers: initiator, NoCookies: true},

		contract.Case{
			Name: "connect-again", Method: "POST", Path: "/api/nd/connect", Roles: signedOut, Headers: initiator, NoCSRF: true, NoCookies: true,
			Body:          map[string]any{"target_id": "900100001"},
			Capture:       map[string]string{"relay2": "session_id"},
			CaptureSecret: map[string]string{"initiator_token2": "token"},
		},
		contract.Case{Name: "incoming-again", Path: "/ws/nd-signal", Query: target, Roles: signedOut, Socket: sock("target", contract.Socket{Expect: 1})},
		contract.Case{
			Name: "initiator-waits", Path: "/ws/nd-relay", Query: relay("{relay2}", "{secret:initiator_token2}", "initiator"), Roles: signedOut,
			Socket: sock("init3", contract.Socket{}),
		},
		contract.Case{
			Name: "target-rejects", Path: "/ws/nd-signal", Query: target, Roles: signedOut,
			Socket: sock("target", contract.Socket{Send: []any{map[string]any{"type": "relay_reject", "session_id": "{relay2}"}}}),
		},
		contract.Case{Name: "initiator-rejected", Path: "/ws/nd-relay", Roles: signedOut, Socket: sock("init3", contract.Socket{WaitClose: true})},

		contract.Case{
			Name: "connect-delete", Method: "POST", Path: "/api/nd/connect", Roles: signedOut, Headers: initiator, NoCSRF: true, NoCookies: true,
			Body:          map[string]any{"target_id": "900100001"},
			Capture:       map[string]string{"relay3": "session_id"},
			CaptureSecret: map[string]string{"initiator_token3": "token"},
		},
		contract.Case{Name: "incoming-third", Path: "/ws/nd-signal", Query: target, Roles: signedOut, Socket: sock("target", contract.Socket{Expect: 1})},
		contract.Case{Name: "session-delete", Method: "DELETE", Path: "/api/nd/session/{relay3}", Roles: signedOut, Headers: initiator, NoCSRF: true, NoCookies: true},
		contract.Case{
			Name: "initiator-after-delete", Path: "/ws/nd-relay", Query: relay("{relay3}", "{secret:initiator_token3}", "initiator"), Roles: signedOut,
			Socket: sock("init4", contract.Socket{WaitClose: true}),
		},
		contract.Case{Name: "peers-online", Path: "/api/nd/peers", Roles: signedOut, Headers: initiator, NoCookies: true},
		contract.Case{Name: "peer-online", Path: "/api/nd/peer/900100001", Roles: signedOut, Headers: initiator, NoCookies: true},
		contract.Case{Name: "target-offline", Path: "/ws/nd-signal", Query: target, Roles: signedOut, Socket: sock("target", contract.Socket{Close: true})},
		contract.Case{Name: "peer-offline", Path: "/api/nd/peer/900100001", Roles: signedOut, Headers: initiator, NoCookies: true},

		// A browser's session cookie without Origin is refused before the
		// handshake; with its own Origin the relay's checks answer, the
		// same for every role. Added after the socket moved and recorded
		// with CONTRACT_RECORD_NEW=1; the answers are those of the Node
		// recording bdrelay/testdata/node_relay.json ("relay checks").
		contract.Case{Name: "browser", Path: "/ws/nd-relay", Socket: sock("browser", contract.Socket{WaitClose: true})},
		contract.Case{Name: "browser-without-origin", Path: "/ws/nd-relay", Socket: sock("bare", contract.Socket{Origin: "-", WaitClose: true})},
	)
}
