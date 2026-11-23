//go:build contract

package contract_test

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The chat sockets: the agent's /ws/chat/<device> (a device token in the
// query or as a bearer header, no session) and the operators'
// /ws/chat-operator/<device> and /ws/chat-operator/panel (a session with
// chat.access). In each role's turn the device 900100001 connects as an
// agent and the role opens an operator socket on its room and on the
// panel; messages, typing, presence, reactions and history go between
// them. Roles without chat.access see their refusal and the agent's side
// alone.
//
// The relay sends what needs the API server (history, contacts, groups)
// when the call returns, so where one event brings several of those
// messages each step keeps only one type (Filter). Messages are stored on
// the API server, so each turn's history holds those of the turns before.
func init() {
	agentQuery := map[string][]string{"token": {"{secret:device_token}"}}
	only := func(typ string) map[string]string { return map[string]string{"type": typ} }
	agent := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		s.Origin = "-"
		return &s
	}
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	agentCase := func(name, socket string, s contract.Socket) contract.Case {
		return contract.Case{Name: name, Path: "/ws/chat/900100001", Query: agentQuery, NoCookies: true, Socket: agent(socket, s)}
	}
	// What an operator causes reaches the device only in the turns of the
	// roles that may open an operator socket.
	withChat := func(c contract.Case) contract.Case {
		c.Roles = contract.Roles("viewer", "operator", "global_admin", "super_admin", "admin")
		return c
	}
	withVolatile := func(c contract.Case, marks ...string) contract.Case {
		c.Volatile = append(c.Volatile, marks...)
		return c
	}
	room := "/ws/chat-operator/900100001"
	panel := "/ws/chat-operator/panel"
	contract.Group("ws-chat",
		// The checks of the agent socket
		contract.Case{Name: "agent-foreign-origin", Path: "/ws/chat/900100001", Query: agentQuery, NoCookies: true,
			Socket: sock("a_foreign", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "agent-no-origin-with-session-cookie", Path: "/ws/chat/900100001", Query: agentQuery, NoCookies: true,
			Headers: map[string]string{"Cookie": "nd.sid=s%3Anot-a-session"}, Socket: agent("a_cookie", contract.Socket{})},
		contract.Case{Name: "agent-no-token", Path: "/ws/chat/900100001", NoCookies: true, Socket: agent("a_none", contract.Socket{})},
		contract.Case{Name: "agent-token-unknown", Path: "/ws/chat/900100001", Query: map[string][]string{"token": {"contract-no-such-token"}},
			NoCookies: true, Socket: agent("a_unknown", contract.Socket{})},
		contract.Case{Name: "agent-token-of-other-device", Path: "/ws/chat/900100001", Query: map[string][]string{"token": {"{secret:device_token_other}"}},
			NoCookies: true, Socket: agent("a_other", contract.Socket{})},
		contract.Case{Name: "agent-bearer-lower-case", Path: "/ws/chat/900100001", NoCookies: true,
			Headers: map[string]string{"Authorization": "bearer {secret:device_token}"}, Socket: agent("a_lower", contract.Socket{})},
		contract.Case{Name: "agent-bad-key", Path: "/ws/chat/900100001", Query: agentQuery, NoCookies: true,
			Headers: map[string]string{"Sec-WebSocket-Key": "short"}, Socket: agent("a_key", contract.Socket{})},
		contract.Case{Name: "agent-bearer", Path: "/ws/chat/900100001", NoCookies: true,
			Headers: map[string]string{"Authorization": "Bearer {secret:device_token}"},
			Socket:  agent("a_bearer", contract.Socket{Expect: 1, Filter: only("history"), Close: true})},

		// The checks of the operator socket
		contract.Case{Name: "operator-foreign-origin", Path: room, Socket: sock("o_foreign", contract.Socket{Origin: "https://evil.example"})},
		contract.Case{Name: "operator-no-origin", Path: room, Socket: sock("o_bare", contract.Socket{Origin: "-"})},
		contract.Case{Name: "operator-no-session", Path: room, NoSession: true, Socket: sock("o_nosession", contract.Socket{})},
		contract.Case{Name: "operator-bad-key", Path: room, Headers: map[string]string{"Sec-WebSocket-Key": "short"}, Socket: sock("o_key", contract.Socket{})},

		// An operator on the device's room, then the device
		contract.Case{Name: "operator-open", Path: room, Socket: sock("op", contract.Socket{Expect: 2})},
		agentCase("agent-open", "agent", contract.Socket{Expect: 1, Filter: only("history")}),
		contract.Case{Name: "operator-sees-agent", Path: room, Socket: sock("op", contract.Socket{Expect: 2})},
		agentCase("agent-hello", "agent", contract.Socket{Send: []any{map[string]any{"type": "hello", "device_id": "900100001"}}, Expect: 1, Filter: only("welcome")}),
		agentCase("agent-message", "agent", contract.Socket{
			Send: []any{map[string]any{"type": "message", "text": "contract agent message", "conversation_id": "900100001"}}, Filter: only("message")}),
		contract.Case{Name: "operator-gets-agent-message", Path: room, Socket: sock("op", contract.Socket{Expect: 1})},
		contract.Case{Name: "operator-message", Path: room, Socket: sock("op", contract.Socket{
			Send: []any{map[string]any{"type": "message", "text": "contract operator message"}}, Expect: 1})},
		withChat(agentCase("agent-gets-operator-message", "agent", contract.Socket{Expect: 1, Filter: only("message")})),
		contract.Case{Name: "operator-typing", Path: room, Socket: sock("op", contract.Socket{Send: []any{map[string]any{"type": "typing"}}})},
		withChat(agentCase("agent-gets-typing", "agent", contract.Socket{Expect: 1, Filter: only("typing")})),
		agentCase("agent-typing-and-presence", "agent", contract.Socket{Send: []any{
			map[string]any{"type": "typing", "conversation_id": "900100001"},
			map[string]any{"type": "presence_update", "online": false, "status": "busy"},
			map[string]any{"type": "key_exchange", "public_key": "contract-key"},
			map[string]any{"type": "file_share", "file_id": "contract-file", "file_size": 12},
		}, Filter: only("typing")}),
		contract.Case{Name: "operator-gets-typing-and-presence", Path: room, Socket: sock("op", contract.Socket{Expect: 4})},
		contract.Case{Name: "operator-reaction", Path: room, Socket: sock("op", contract.Socket{
			Send: []any{map[string]any{"type": "reaction", "message_id": "1", "emoji": "+1"}}, Expect: 1})},
		withChat(agentCase("agent-gets-reaction", "agent", contract.Socket{Expect: 1, Filter: only("reaction")})),
		contract.Case{Name: "operator-history", Path: room, Socket: sock("op", contract.Socket{
			Send: []any{map[string]any{"type": "get_history", "conversation_id": "900100001"}}, Expect: 1})},
		agentCase("agent-history", "agent", contract.Socket{Send: []any{map[string]any{"type": "get_history"}}, Expect: 1, Filter: only("history")}),

		// What the relay leaves alone: binary, text that is not JSON (a
		// frame at the 8 KiB limit is answered)
		agentCase("agent-ignored", "agent", contract.Socket{Send: []any{
			contract.SocketBinary(`{"type":"hello"}`), "not json", `{"type":"nothing"}`,
		}}),
		agentCase("agent-at-the-limit", "agent", contract.Socket{Send: []any{
			`{"type":"hello","pad":"` + strings.Repeat("x", 8192-25) + `"}`,
		}, Expect: 1, Filter: only("welcome")}),
		contract.Case{Name: "operator-ignored", Path: room, Socket: sock("op", contract.Socket{Send: []any{
			contract.SocketBinary(`{"type":"hello"}`), "not json",
		}})},

		// The panel: status first, then what the API server gives
		contract.Case{Name: "panel-open", Path: panel, Socket: sock("panel", contract.Socket{Expect: 1, Filter: only("status")})},
		contract.Case{Name: "panel-groups", Path: panel, Socket: sock("panel", contract.Socket{
			Send: []any{map[string]any{"type": "get_groups"}}, Expect: 1, Filter: only("groups")})},
		contract.Case{Name: "panel-contacts", Path: panel, Socket: sock("panel", contract.Socket{
			Send: []any{map[string]any{"type": "get_contacts"}}, Expect: 1, Filter: only("contacts")})},
		agentCase("agent-message-to-everyone", "agent", contract.Socket{
			Send: []any{map[string]any{"type": "message", "text": "contract to the panel", "conversation_id": "900100001"}}, Filter: only("message")}),
		contract.Case{Name: "panel-gets-agent-message", Path: panel, Socket: sock("panel", contract.Socket{Expect: 1})},
		contract.Case{Name: "operator-gets-agent-message-again", Path: room, Socket: sock("op", contract.Socket{Expect: 1})},
		contract.Case{Name: "panel-message-to-device", Path: panel, Socket: sock("panel", contract.Socket{
			Send: []any{map[string]any{"type": "message", "text": "contract from the panel", "conversation_id": "900100001"}}, Expect: 1})},
		withChat(agentCase("agent-gets-panel-message", "agent", contract.Socket{Expect: 1, Filter: only("message")})),
		contract.Case{Name: "operator-gets-panel-message", Path: room, Socket: sock("op", contract.Socket{Expect: 1})},
		contract.Case{Name: "panel-message-to-group", Path: panel, Socket: sock("panel", contract.Socket{
			Send: []any{map[string]any{"type": "message", "text": "contract to a group", "conversation_id": "group:contract"}}, Expect: 1})},

		// A second connection of the device replaces the first; text that
		// is not UTF-8 ends it
		agentCase("agent-second", "agent2", contract.Socket{Expect: 1, Filter: only("history")}),
		agentCase("agent-replaced", "agent", contract.Socket{WaitClose: true}),
		contract.Case{Name: "operator-sees-second-agent", Path: room, Socket: sock("op", contract.Socket{Expect: 2})},
		agentCase("agent-not-utf8", "agent2", contract.Socket{Send: []any{"\xc3\x28"}, WaitClose: true}),
		contract.Case{Name: "operator-sees-agent-leave", Path: room, Socket: sock("op", contract.Socket{Expect: 2, Close: true})},
		contract.Case{Name: "panel-sees-agent-leave", Path: panel, Socket: sock("panel", contract.Socket{Expect: 4, Close: true})},

		// A frame over 8 KiB closes the socket with 1009. These come last:
		// a new agent socket would replace the turn's agent. What the
		// console sends a new agent (history, contacts) comes when the API
		// server answers, before the close or not at all, so the agent's
		// messages are free here.
		withVolatile(agentCase("agent-too-big", "a_big", contract.Socket{Send: []any{
			`{"type":"hello","pad":"` + strings.Repeat("x", 8192-24) + `"}`,
		}, WaitClose: true}), "~messages"),
		contract.Case{Name: "operator-too-big", Path: room, Socket: sock("o_big", contract.Socket{Send: []any{
			`{"type":"hello","pad":"` + strings.Repeat("x", 8192-24) + `"}`,
		}, WaitClose: true})},
	)
}
