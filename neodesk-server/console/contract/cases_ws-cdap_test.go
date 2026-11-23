//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The five sockets of an IoT device (CDAP): terminal, desktop, video,
// files and audio under /api/cdap/devices/<id>/, each with its own
// permission and subprotocol, passed on to the API server's socket of
// the same name. A test instance has no CDAP device, so the API server
// answers with its error for a device that is not connected once the
// browser's first message (the init) arrives; the files socket needs no
// init. The terminal passes every message on as binary, the others keep
// text as text.
func init() {
	dev := "/api/cdap/devices/900100001/"
	sock := func(name string, s contract.Socket) *contract.Socket {
		s.Name = name
		return &s
	}
	contract.Group("ws-cdap",
		contract.Case{Name: "terminal-foreign-origin", Path: dev + "terminal",
			Socket: sock("t_foreign", contract.Socket{Origin: "https://evil.example", Protocols: []string{"cdap-terminal"}})},
		contract.Case{Name: "terminal-no-origin", Path: dev + "terminal", Socket: sock("t_bare", contract.Socket{Origin: "-", Protocols: []string{"cdap-terminal"}})},
		contract.Case{Name: "terminal-no-session", Path: dev + "terminal", NoSession: true, Socket: sock("t_nosession", contract.Socket{Protocols: []string{"cdap-terminal"}})},
		contract.Case{Name: "terminal-bad-key", Path: dev + "terminal", Headers: map[string]string{"Sec-WebSocket-Key": "short"},
			Socket: sock("t_key", contract.Socket{Protocols: []string{"cdap-terminal"}})},
		contract.Case{Name: "terminal-open", Path: dev + "terminal", Socket: sock("t", contract.Socket{Protocols: []string{"cdap-terminal"}})},
		contract.Case{Name: "terminal-init", Path: dev + "terminal", Socket: sock("t", contract.Socket{
			Send: []any{map[string]any{"cols": 80, "rows": 24}}, WaitClose: true})},
		contract.Case{Name: "terminal-bad-init", Path: dev + "terminal", Socket: sock("t2", contract.Socket{
			Protocols: []string{"cdap-terminal"}, Send: []any{"not json"}, WaitClose: true})},
		contract.Case{Name: "terminal-no-protocol", Path: dev + "terminal", Socket: sock("t3", contract.Socket{
			Send: []any{map[string]any{"cols": 80, "rows": 24}}, WaitClose: true})},

		contract.Case{Name: "desktop-open", Path: dev + "desktop", Socket: sock("d", contract.Socket{Protocols: []string{"cdap-desktop"}})},
		contract.Case{Name: "desktop-init", Path: dev + "desktop", Socket: sock("d", contract.Socket{
			Send: []any{map[string]any{"width": 800, "height": 600}}, WaitClose: true})},
		contract.Case{Name: "desktop-binary-init", Path: dev + "desktop", Socket: sock("d2", contract.Socket{
			Protocols: []string{"cdap-desktop"}, Send: []any{contract.SocketBinary(`{"width":800,"height":600}`)}, WaitClose: true})},
		contract.Case{Name: "desktop-foreign-origin", Path: dev + "desktop",
			Socket: sock("d_foreign", contract.Socket{Origin: "https://evil.example", Protocols: []string{"cdap-desktop"}})},

		contract.Case{Name: "video", Path: dev + "video", Socket: sock("v", contract.Socket{
			Protocols: []string{"cdap-video"}, Send: []any{map[string]any{"quality": 50}}, WaitClose: true})},
		contract.Case{Name: "video-bad-init", Path: dev + "video", Socket: sock("v2", contract.Socket{
			Protocols: []string{"cdap-video"}, Send: []any{"[1"}, WaitClose: true})},
		contract.Case{Name: "files", Path: dev + "files", Socket: sock("f", contract.Socket{Protocols: []string{"cdap-filebrowser"}, WaitClose: true})},
		contract.Case{Name: "audio", Path: dev + "audio", Socket: sock("a", contract.Socket{
			Protocols: []string{"cdap-audio"}, Send: []any{map[string]any{"sample_rate": 16000}}, WaitClose: true})},
		contract.Case{Name: "audio-no-session", Path: dev + "audio", NoSession: true, Socket: sock("a_nosession", contract.Socket{Protocols: []string{"cdap-audio"}})},
		contract.Case{Name: "files-bad-key", Path: dev + "files", Headers: map[string]string{"Sec-WebSocket-Version": "12"},
			Socket: sock("f_key", contract.Socket{Protocols: []string{"cdap-filebrowser"}})},

		// Device ids of 1 to 64 letters, digits, _ and -
		contract.Case{Name: "terminal-short-id", Path: "/api/cdap/devices/a/terminal", Socket: sock("t_short", contract.Socket{
			Protocols: []string{"cdap-terminal"}, Send: []any{map[string]any{"cols": 80, "rows": 24}}, WaitClose: true})},
	)
}
