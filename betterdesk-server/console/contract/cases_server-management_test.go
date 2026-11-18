//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Server management (beta): the machine the console runs on, its files and
// BetterDesk's system services, all behind server.config.
//
// The file cases work in a folder of the instance below the server's
// folder (/opt/rustdesk, one of the two folders the file browser may
// change), which admin makes at the start of each turn and removes at the
// end. Nothing is written anywhere else: the refused writes name folders
// the browser may only read (/tmp) or not see at all (/etc), and the
// console's own folder, the other writable one, is the worktree, so it is
// only read, and only a name that is not there. Relative paths are taken
// from the console's folder.
//
// The service cases send only what is refused before a command runs: a
// name outside the allow list or the pattern, an action the list does not
// have. Starting, stopping or reading the status of a listed service runs
// systemctl; the package's tests cover that with a stand-in.
func init() {
	const base = "/opt/rustdesk/sm-contract"
	few := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	post := func(name, path string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/server-management/" + path, Body: body, Roles: roles}
	}
	get := func(name, path string, query url.Values, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/server-management/" + path, Query: query, Roles: roles}
	}
	pathQuery := func(p string) url.Values { return url.Values{"path": {p}} }

	contract.Group("server-management",
		// The runtime and how long the processes have been up. nodeVersion
		// names the runtime of the console that answers: Node's version,
		// Go's in the Go console (spec updater-installers 4.6).
		contract.Case{Name: "info", Path: "/api/server-management/info", Volatile: []string{"pid", "server.uptimeSeconds", "~nodeVersion"}},
		contract.Case{
			Name: "resources", Path: "/api/server-management/resources",
			Volatile: []string{
				"~snapshot.arch", "snapshot.cpuCount", "~snapshot.cpuModel", "~snapshot.release", "~snapshot.platform",
				"snapshot.mem.total", "snapshot.mem.free", "snapshot.mem.used", "snapshot.mem.percent",
				"snapshot.swap.free", "snapshot.swap.total", "snapshot.swap.used",
				"snapshot.cpu", "snapshot.load", "snapshot.uptime", "snapshot.nodeUptime", "snapshot.ts",
				"snapshot.disks[#]", "snapshot.network[#]", "snapshot.history[#]", "~snapshot.nodeVersion",
			},
		},
		get("services", "services", nil),

		// The instance's folder for the file cases
		contract.Case{Name: "setup", Method: "POST", Path: "/api/server-management/files/mkdir", Body: map[string]any{"path": base}, As: "admin"},

		post("mkdir", "files/mkdir", map[string]any{"path": base + "/sub"}),
		post("mkdir-exists", "files/mkdir", map[string]any{"path": base + "/sub"}),
		post("mkdir-missing-parent", "files/mkdir", map[string]any{"path": base + "/no/such"}, few...),
		post("mkdir-not-text", "files/mkdir", map[string]any{"path": 5}, few...),
		post("mkdir-empty", "files/mkdir", map[string]any{"path": ""}, few...),
		post("mkdir-no-body", "files/mkdir", map[string]any{}, few...),
		post("mkdir-only-readable", "files/mkdir", map[string]any{"path": "/tmp/sm-contract"}, few...),
		post("mkdir-dot-segments", "files/mkdir", map[string]any{"path": base + "/sub/../sub2/."}, few...),

		post("write", "files/write", map[string]any{"path": base + "/note.txt", "content": "hello\nworld\n"}),
		post("write-unicode", "files/write", map[string]any{"path": base + "/sub/grüße ñ.txt", "content": "Grüße – 🙂\r\n"}, few...),
		post("write-nul-content", "files/write", map[string]any{"path": base + "/bin.dat", "content": "a\u0000b"}, few...),
		post("write-empty-content", "files/write", map[string]any{"path": base + "/empty.txt", "content": ""}, few...),
		post("write-no-content", "files/write", map[string]any{"path": base + "/x.txt"}, few...),
		post("write-content-number", "files/write", map[string]any{"path": base + "/x.txt", "content": 5}, few...),
		post("write-path-list", "files/write", map[string]any{"path": []string{base + "/x.txt"}, "content": "x"}, few...),
		post("write-empty-path", "files/write", map[string]any{"path": "", "content": "x"}, few...),
		post("write-nul-in-path", "files/write", map[string]any{"path": base + "/a\u0000b", "content": "x"}, few...),
		post("write-only-readable", "files/write", map[string]any{"path": "/tmp/sm-contract.txt", "content": "x"}, few...),
		post("write-outside", "files/write", map[string]any{"path": "/etc/sm-contract", "content": "x"}, few...),
		post("write-dot-dot", "files/write", map[string]any{"path": base + "/../../../etc/sm-contract", "content": "x"}, few...),
		post("write-relative-dot-dot", "files/write", map[string]any{"path": "../../../etc/sm-contract", "content": "x"}, few...),
		post("write-onto-folder", "files/write", map[string]any{"path": base + "/sub", "content": "x"}, few...),
		post("write-missing-parent", "files/write", map[string]any{"path": base + "/no/such.txt", "content": "x"}, few...),

		get("read", "files/read", pathQuery(base+"/note.txt")),
		get("read-unicode", "files/read", pathQuery(base+"/sub/grüße ñ.txt"), few...),
		get("read-binary", "files/read", pathQuery(base+"/bin.dat"), few...),
		get("read-empty", "files/read", pathQuery(base+"/empty.txt"), few...),
		get("read-folder", "files/read", pathQuery(base), few...),
		get("read-missing", "files/read", pathQuery(base+"/missing.txt"), few...),
		get("read-no-path", "files/read", nil, few...),
		get("read-empty-path", "files/read", pathQuery(""), few...),
		get("read-outside", "files/read", pathQuery("/etc/hostname"), few...),
		get("read-dot-dot", "files/read", pathQuery(base+"/../../../etc/hostname"), few...),
		get("read-relative-missing", "files/read", pathQuery("no-such-sm-file"), few...),
		get("read-path-twice", "files/read", url.Values{"path": {"no-such-a", "no-such-b"}}, few...),
		get("read-path-as-object", "files/read", url.Values{"path[x]": {"1"}}, few...),
		get("read-nul-in-path", "files/read", pathQuery(base+"/a\x00b"), few...),

		get("list", "files", pathQuery(base)),
		get("list-sub", "files", pathQuery(base+"/sub"), few...),
		get("list-trailing-slash", "files", pathQuery(base+"/sub/"), few...),
		get("list-file", "files", pathQuery(base+"/note.txt"), few...),
		get("list-missing", "files", pathQuery(base+"/missing"), few...),
		get("list-outside", "files", pathQuery("/etc"), few...),
		get("list-no-path", "files", nil, few...),
		get("list-empty-path", "files", pathQuery(""), few...),

		post("rename", "files/rename", map[string]any{"from": base + "/note.txt", "to": base + "/sub/moved.txt"}),
		post("rename-missing", "files/rename", map[string]any{"from": base + "/note.txt", "to": base + "/sub/again.txt"}, few...),
		post("rename-outside", "files/rename", map[string]any{"from": base + "/sub/moved.txt", "to": "/tmp/sm-moved.txt"}, few...),
		post("rename-from-outside", "files/rename", map[string]any{"from": "/etc/hostname", "to": base + "/hostname"}, few...),
		post("rename-folder-onto-file", "files/rename", map[string]any{"from": base + "/sub", "to": base + "/bin.dat"}, few...),
		post("rename-not-text", "files/rename", map[string]any{"from": 1, "to": base + "/x"}, few...),
		post("rename-no-body", "files/rename", map[string]any{}, few...),
		get("list-after-rename", "files", pathQuery(base+"/sub"), few...),

		post("delete-file", "files/delete", map[string]any{"path": base + "/sub/moved.txt"}),
		post("delete-missing", "files/delete", map[string]any{"path": base + "/sub/moved.txt"}, few...),
		post("delete-only-readable", "files/delete", map[string]any{"path": "/tmp"}, few...),
		post("delete-filesystem-root", "files/delete", map[string]any{"path": "/"}, few...),
		post("delete-not-text", "files/delete", map[string]any{"path": 7}, few...),
		post("delete-empty", "files/delete", map[string]any{"path": ""}, few...),
		post("delete-folder", "files/delete", map[string]any{"path": base + "/sub"}),
		get("list-after-delete", "files", pathQuery(base), few...),

		contract.Case{Name: "cleanup", Method: "POST", Path: "/api/server-management/files/delete", Body: map[string]any{"path": base}, As: "admin"},

		// Services: only refusals, nothing runs
		post("service-not-listed", "services/sshd/status", map[string]any{}),
		post("service-name-with-space", "services/bad%20name/status", map[string]any{}, few...),
		post("service-name-too-long", "services/"+strings.Repeat("a", 129)+"/status", map[string]any{}, few...),
		post("service-name-longest", "services/"+strings.Repeat("a", 128)+"/status", map[string]any{}, few...),
		post("service-name-with-colon", "services/a:b/start", map[string]any{}, few...),
		post("service-action-not-listed", "services/betterdesk-server/kill", map[string]any{}, few...),
		post("service-action-other-case", "services/hbbs/START", map[string]any{}, few...),
		post("service-name-other-case", "services/HBBS/status", map[string]any{}, few...),
	)
}
