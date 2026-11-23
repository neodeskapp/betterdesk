//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The remote desktop relay: the agent's socket /ws/remote-agent/<id>
// (with a device token, no session) sends screen frames, the viewers'
// sockets /ws/remote-viewer/<id> (a session with device.connect) get them
// and send input. First what the agent's socket refuses, as a device;
// then every role opens a viewer next to an agent of 900100001 and goes
// through a session: the start, a frame and a status from the agent, input
// from the viewer, a stop, a second viewer, the last viewer leaving, a
// second agent replacing the first, the agent leaving. Admin then fills a
// device with viewers up to the limit. Every socket a step opens is closed
// before the turn ends, so the next turn starts with an agent and no
// viewer.
//
// A device id whose escapes are not UTF-8 (/ws/remote-agent/ab%E0%A4cd)
// is left out: decodeURIComponent throws in the upgrade handler and the
// Node process ends.
func init() {
	signedOut := contract.Roles("signed-out")
	admin := contract.Roles("admin")
	const agentPath = "/ws/remote-agent/900100001"
	const viewerPath = "/ws/remote-viewer/900100001"
	token := func(t string) url.Values { return url.Values{"token": {t}} }
	good := token("{secret:device_token}")
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	// agent is a step on an agent socket: no session, no cookies
	agent := func(name, socket string, s contract.Socket) contract.Case {
		return contract.Case{Name: name, Path: agentPath, Query: good, NoCookies: true, Socket: sock(socket, s)}
	}
	viewer := func(name, socket string, s contract.Socket) contract.Case {
		return contract.Case{Name: name, Path: viewerPath, Socket: sock(socket, s)}
	}
	input := map[string]any{"type": "input", "event_type": "mouse_move", "x": 10, "y": 20, "buttons": []any{1}}

	cases := []contract.Case{
		// What the agent's socket refuses
		{Name: "agent-foreign-origin", Path: agentPath, Query: good, Roles: signedOut, NoCookies: true, Socket: sock("a1", contract.Socket{Origin: "https://evil.example"})},
		{
			Name: "agent-no-origin-with-session-cookie", Path: agentPath, Query: good, Roles: signedOut, NoCookies: true,
			Headers: map[string]string{"Cookie": "nd.sid=s%3Anot-a-session"}, Socket: sock("a2", contract.Socket{Origin: "-"}),
		},
		{Name: "agent-short-id", Path: "/ws/remote-agent/ab", Query: good, Roles: signedOut, NoCookies: true, Socket: sock("a3", contract.Socket{})},
		{Name: "agent-odd-id", Path: "/ws/remote-agent/abc%20def", Query: good, Roles: signedOut, NoCookies: true, Socket: sock("a4", contract.Socket{})},
		{Name: "agent-no-token", Path: agentPath, Roles: signedOut, NoCookies: true, Socket: sock("a5", contract.Socket{})},
		{Name: "agent-unknown-token", Path: agentPath, Query: token("contract-no-such-token"), Roles: signedOut, NoCookies: true, Socket: sock("a6", contract.Socket{})},
		{Name: "agent-token-of-other-device", Path: agentPath, Query: token("{secret:device_token_other}"), Roles: signedOut, NoCookies: true, Socket: sock("a7", contract.Socket{})},
		{Name: "agent-encoded-id", Path: "/ws/remote-agent/9001%300001", Query: good, Roles: signedOut, NoCookies: true, Socket: sock("a8", contract.Socket{Close: true})},
		{Name: "agent-no-origin", Path: agentPath, Query: good, Roles: signedOut, NoCookies: true, Socket: sock("a9", contract.Socket{Origin: "-", Close: true})},

		// What the viewer's socket refuses
		{Name: "viewer-foreign-origin", Path: viewerPath, Socket: sock("v1", contract.Socket{Origin: "https://evil.example"})},
		{Name: "viewer-unknown-device", Path: "/ws/remote-viewer/900199999", Socket: sock("v2", contract.Socket{Close: true})},
		{Name: "viewer-encoded-id", Path: "/ws/remote-viewer/9001%300001", Socket: sock("v3", contract.Socket{Close: true})},
		{Name: "viewer-without-session", Path: viewerPath, NoSession: true, Socket: sock("v4", contract.Socket{Close: true})},

		// A session
		agent("agent-open", "agent", contract.Socket{}),
		viewer("viewer-open", "viewer", contract.Socket{Expect: 2}),
		agent("agent-gets-start", "agent", contract.Socket{Expect: 1}),
		agent("agent-sends", "agent", contract.Socket{Send: []any{
			contract.SocketBinary{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3},
			map[string]any{"type": "status", "fps": 12, "quality": "high"},
			map[string]any{"type": "agent-ready"},
			map[string]any{"type": "pong", "ts": 1},
			"not json",
			[]any{"type", "status"},
		}}),
		viewer("viewer-gets-frames", "viewer", contract.Socket{Expect: 3}),
		viewer("viewer-sends-input", "viewer", contract.Socket{Send: []any{
			input,
			map[string]any{"type": "input", "kind": "key", "key": "a", "event_type": ""},
			map[string]any{"type": "input"},
			map[string]any{"event_type": "click", "type": "input"},
			map[string]any{"type": "input", "event_type": 5},
			map[string]any{"type": "other"},
			contract.SocketBinary{1, 2, 3},
			"not json",
		}}),
		agent("agent-gets-input", "agent", contract.Socket{Expect: 5}),
		viewer("viewer-stops", "viewer", contract.Socket{Send: []any{map[string]any{"type": "stop"}}, Expect: 1}),
		agent("agent-gets-stop", "agent", contract.Socket{Expect: 1}),
		viewer("viewer-stops-again", "viewer", contract.Socket{Send: []any{map[string]any{"type": "stop"}}}),
		viewer("second-viewer", "viewer2", contract.Socket{Expect: 2}),
		viewer("first-viewer-sees-start", "viewer", contract.Socket{Expect: 1}),
		agent("agent-started-again", "agent", contract.Socket{Expect: 1}),
		viewer("second-viewer-leaves", "viewer2", contract.Socket{Close: true}),
		agent("agent-after-one-left", "agent", contract.Socket{}),
		viewer("last-viewer-leaves", "viewer", contract.Socket{Close: true}),
		agent("agent-gets-stop-at-the-end", "agent", contract.Socket{Expect: 1}),
		viewer("viewer-again", "viewer3", contract.Socket{Expect: 2}),
		agent("agent-started-for-new-viewer", "agent", contract.Socket{Expect: 1}),
		agent("second-agent", "agent2", contract.Socket{Expect: 1}),
		agent("first-agent-replaced", "agent", contract.Socket{WaitClose: true}),
		viewer("viewer-sees-new-agent", "viewer3", contract.Socket{Expect: 2}),
		agent("second-agent-leaves", "agent2", contract.Socket{Close: true}),
		viewer("viewer-sees-agent-leave", "viewer3", contract.Socket{Expect: 1}),
		viewer("viewer-without-agent-leaves", "viewer3", contract.Socket{Close: true}),
	}
	// Admin fills the device with five viewers; a sixth is closed
	for _, name := range []string{"m1", "m2", "m3", "m4", "m5"} {
		cases = append(cases, contract.Case{Name: "viewer-" + name, Path: viewerPath, Roles: admin, Socket: sock(name, contract.Socket{Expect: 1})})
	}
	cases = append(cases, contract.Case{Name: "viewer-over-the-limit", Path: viewerPath, Roles: admin, Socket: sock("m6", contract.Socket{WaitClose: true})})
	for _, name := range []string{"m1", "m2", "m3", "m4", "m5"} {
		cases = append(cases, contract.Case{Name: "viewer-" + name + "-closed", Path: viewerPath, Roles: admin, Socket: sock(name, contract.Socket{Close: true})})
	}
	contract.Group("ws-remote", cases...)
}
