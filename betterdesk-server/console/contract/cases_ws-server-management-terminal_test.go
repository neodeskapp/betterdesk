//go:build contract

package contract_test

import (
	"github.com/unitronix/betterdesk-server/console/contract"
)

// The server terminal /ws/server-management/terminal: a shell on the
// console's machine for a session with server.config, after the origin
// check. The shell's output comes in pieces that differ from run to run,
// so after the init every step keeps only the messages it is about (ready,
// end) and drops the output; the package's tests compare the output of
// recorded conversations. What the steps show: messages before the init
// and messages that are not JSON objects are ignored, the ready message,
// "exit" ending the shell with an end message and a close, the close
// message ending the socket without one, a second init, and the refusals
// (origin, session, permission, a bad handshake).
func init() {
	ready := map[string]string{"type": "ready"}
	end := map[string]string{"type": "end"}
	const path = "/ws/server-management/terminal"
	step := func(name, socket string, send []any, filter map[string]string, expect int, waitClose bool) contract.Case {
		return contract.Case{Name: name, Path: path, Socket: &contract.Socket{
			Name: socket, Send: send, Filter: filter, Expect: expect, WaitClose: waitClose,
		}}
	}
	contract.Group("ws-server-management-terminal",
		contract.Case{Name: "open", Path: path, Socket: &contract.Socket{Name: "term"}},
		step("input-before-init", "term", []any{map[string]any{"type": "input", "data": "echo early\n"}}, nil, 0, false),
		step("resize-before-init", "term", []any{map[string]any{"type": "resize", "cols": 100, "rows": 30}}, nil, 0, false),
		step("not-objects", "term", []any{"hello", "[1]", "null", "5", `"text"`, "{"}, nil, 0, false),
		step("init", "term", []any{map[string]any{"type": "init", "cols": 1000, "rows": "x"}}, ready, 1, false),
		step("input-not-text", "term", []any{map[string]any{"type": "input", "data": 5}}, end, 0, false),
		step("resize", "term", []any{map[string]any{"type": "resize", "cols": "abc", "rows": 2}}, end, 0, false),
		step("unknown-type", "term", []any{map[string]any{"type": "ping"}, map[string]any{}}, end, 0, false),
		step("exit", "term", []any{map[string]any{"type": "input", "data": "exit\n"}}, end, 1, true),

		contract.Case{Name: "open-second", Path: path, Socket: &contract.Socket{Name: "second"}},
		step("init-second", "second", []any{map[string]any{"type": "init"}}, ready, 1, false),
		step("close-message", "second", []any{map[string]any{"type": "close"}}, end, 0, true),

		contract.Case{Name: "open-third", Path: path, Socket: &contract.Socket{Name: "third"}},
		step("init-twice", "third", []any{map[string]any{"type": "init", "cols": 80, "rows": 24}, map[string]any{"type": "init", "cols": 10, "rows": 500}}, ready, 2, false),
		step("close-third", "third", []any{map[string]any{"type": "close"}}, end, 0, true),

		contract.Case{Name: "foreign-origin", Path: path, Socket: &contract.Socket{Name: "foreign", Origin: "https://evil.example", Close: true}},
		contract.Case{Name: "no-origin", Path: path, Socket: &contract.Socket{Name: "noorigin", Origin: "-", Close: true}},
		contract.Case{Name: "no-session", Path: path, NoSession: true, Socket: &contract.Socket{Name: "nosession", Close: true}},
		contract.Case{
			Name: "bad-handshake", Path: path, Headers: map[string]string{"Sec-WebSocket-Key": "short"},
			Socket: &contract.Socket{Name: "badkey"},
		},
	)
}
