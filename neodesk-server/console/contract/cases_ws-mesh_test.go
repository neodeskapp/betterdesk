//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The MeshCentral sockets /agent.ashx, /meshrelay.ashx and /control.ashx,
// passed on to the API server's MeshCentral gateway with their query.
// The agent and relay sockets need no session (the gateway checks the
// agent and the relay cookie itself), the control socket a signed-in
// session of any role. The gateway answers what it gets as it would on
// its own port: a relay without an id or with a bad cookie and a control
// socket without a token are refused there, so the browser's socket is
// opened and then closed.
//
// The gateway lets one address open 30 sockets a minute, and every
// socket the console passes on comes from the console's address. So the
// sockets that reach the gateway run in the signed-out turn and one more,
// and the control socket in the turns of the roles that sign in; the
// refusals in front of the gateway run for every role.
func init() {
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	few := contract.Roles("signed-out", "admin")
	contract.Group("ws-mesh",
		contract.Case{Name: "agent-foreign-origin", Path: "/agent.ashx", NoCookies: true, Socket: sock("a_foreign", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "agent-no-origin-with-session-cookie", Path: "/agent.ashx", Socket: sock("a_cookie", contract.Socket{Origin: "-"})},
		contract.Case{Name: "agent-bad-key", Path: "/agent.ashx", NoCookies: true, Headers: map[string]string{"Sec-WebSocket-Key": "short"},
			Socket: sock("a_key", contract.Socket{Origin: "-"})},
		contract.Case{Name: "agent-junk", Path: "/agent.ashx", NoCookies: true, Roles: few, Socket: sock("a", contract.Socket{
			Origin: "-", Send: []any{contract.SocketBinary{0, 1}}, Close: true})},
		contract.Case{Name: "agent-own-origin", Path: "/agent.ashx", Roles: few, Socket: sock("a2", contract.Socket{Close: true})},
		contract.Case{Name: "relay-no-id", Path: "/meshrelay.ashx", NoCookies: true, Roles: few, Socket: sock("r", contract.Socket{Origin: "-", WaitClose: true})},
		contract.Case{Name: "relay-bad-cookie", Path: "/meshrelay.ashx", Query: map[string][]string{"id": {"contract"}, "auth": {"bad"}},
			NoCookies: true, Roles: few, Socket: sock("r2", contract.Socket{Origin: "-", WaitClose: true})},
		contract.Case{Name: "relay-foreign-origin", Path: "/meshrelay.ashx", Socket: sock("r_foreign", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "control", Path: "/control.ashx", Socket: sock("c", contract.Socket{WaitClose: true})},
		contract.Case{Name: "control-no-session", Path: "/control.ashx", NoSession: true, Socket: sock("c_nosession", contract.Socket{})},
		contract.Case{Name: "control-foreign-origin", Path: "/control.ashx", Socket: sock("c_foreign", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "control-bad-key", Path: "/control.ashx", Headers: map[string]string{"Sec-WebSocket-Key": "short"}, Socket: sock("c_key", contract.Socket{})},
	)
}
