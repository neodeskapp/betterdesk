//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The agent's signal socket /ws/bd-signal: the origin check before the
// handshake, the checks after it (each a close code and reason), the
// messages a device sends and gets, a second connection of the same
// device replacing the first, and the 64 KiB message limit. Devices
// connect without a session, so the group runs signed out only.
func init() {
	signedOut := contract.Roles("signed-out")
	q := func(id, token string) map[string][]string {
		out := map[string][]string{}
		if id != "" {
			out["device_id"] = []string{id}
		}
		if token != "" {
			out["token"] = []string{token}
		}
		return out
	}
	good := q("900100001", "{secret:device_token}")
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	contract.Group("ws-bd-signal",
		contract.Case{Name: "no-parameters", Path: "/ws/bd-signal", Roles: signedOut, Socket: sock("a", contract.Socket{WaitClose: true})},
		contract.Case{Name: "no-token", Path: "/ws/bd-signal", Query: q("900100001", ""), Roles: signedOut, Socket: sock("b", contract.Socket{WaitClose: true})},
		contract.Case{Name: "empty-token", Path: "/ws/bd-signal", Query: map[string][]string{"device_id": {"900100001"}, "token": {""}}, Roles: signedOut, Socket: sock("c", contract.Socket{WaitClose: true})},
		contract.Case{Name: "device-id-short", Path: "/ws/bd-signal", Query: q("ab", "{secret:device_token}"), Roles: signedOut, Socket: sock("d", contract.Socket{WaitClose: true})},
		contract.Case{Name: "device-id-odd", Path: "/ws/bd-signal", Query: q("9001 00001", "{secret:device_token}"), Roles: signedOut, Socket: sock("e", contract.Socket{WaitClose: true})},
		contract.Case{Name: "token-unknown", Path: "/ws/bd-signal", Query: q("900100001", "contract-no-such-token"), Roles: signedOut, Socket: sock("f", contract.Socket{WaitClose: true})},
		contract.Case{Name: "token-short", Path: "/ws/bd-signal", Query: q("900100001", "short"), Roles: signedOut, Socket: sock("g", contract.Socket{WaitClose: true})},
		contract.Case{Name: "token-of-other-device", Path: "/ws/bd-signal", Query: q("900100001", "{secret:device_token_other}"), Roles: signedOut, Socket: sock("h", contract.Socket{WaitClose: true})},
		contract.Case{Name: "foreign-origin", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("i", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{
			Name: "no-origin-with-session-cookie", Path: "/ws/bd-signal", Query: good, Roles: signedOut, NoCookies: true,
			Headers: map[string]string{"Cookie": "bd.sid=s%3Anot-a-session"}, Socket: sock("j", contract.Socket{Origin: "-"}),
		},
		contract.Case{Name: "no-origin", Path: "/ws/bd-signal", Query: good, Roles: signedOut, NoCookies: true, Socket: sock("k", contract.Socket{Origin: "-", Expect: 1, Close: true})},
		contract.Case{Name: "welcome", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent", contract.Socket{Expect: 1})},
		contract.Case{Name: "heartbeat", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent", contract.Socket{Send: []any{map[string]any{"type": "heartbeat"}}, Expect: 1})},
		contract.Case{Name: "not-json", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent", contract.Socket{Send: []any{"not json", contract.SocketBinary{1, 2, 3}}})},
		contract.Case{Name: "unknown-type", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent", contract.Socket{Send: []any{map[string]any{"type": "nope"}, []any{1}, nil}})},
		contract.Case{
			Name: "relay-accept-unknown", Path: "/ws/bd-signal", Query: good, Roles: signedOut,
			Socket: sock("agent", contract.Socket{Send: []any{map[string]any{"type": "relay_accept", "session_id": "contract-none"}, map[string]any{"type": "relay_reject", "session_id": "contract-none"}}}),
		},
		contract.Case{
			Name: "command-response-unknown", Path: "/ws/bd-signal", Query: good, Roles: signedOut,
			Socket: sock("agent", contract.Socket{Send: []any{map[string]any{"type": "command_response", "request_id": "contract-none", "ok": true, "data": 1}}}),
		},
		contract.Case{Name: "second-connection", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent2", contract.Socket{Expect: 1})},
		contract.Case{Name: "first-replaced", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent", contract.Socket{WaitClose: true})},
		contract.Case{Name: "message-at-the-limit", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent2", contract.Socket{Send: []any{strings.Repeat("x", 64*1024)}})},
		contract.Case{Name: "message-over-the-limit", Path: "/ws/bd-signal", Query: good, Roles: signedOut, Socket: sock("agent2", contract.Socket{Send: []any{strings.Repeat("x", 64*1024+1)}, WaitClose: true})},
		contract.Case{Name: "other-device", Path: "/ws/bd-signal", Query: q("900100002", "{secret:device_token_other}"), Roles: signedOut, Socket: sock("other", contract.Socket{Expect: 1, Close: true})},

		// A browser's cookies count only for the origin check: with its
		// own Origin every role gets the device's socket, without one a
		// session cookie is refused (403). Added after the socket moved and
		// recorded with CONTRACT_RECORD_NEW=1; the answers are those of
		// the Node recording bdrelay/testdata/node_relay.json ("signal
		// checks", sockets m and n).
		contract.Case{Name: "browser", Path: "/ws/bd-signal", Query: good, Socket: sock("browser", contract.Socket{Expect: 1, Close: true})},
		contract.Case{Name: "browser-without-origin", Path: "/ws/bd-signal", Query: good, Socket: sock("bare", contract.Socket{Origin: "-", Expect: 1, Close: true})},
	)
}
