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
// The file browser only lists and reads (A194): a request to write, make a
// folder, rename or delete finds no route. The read and list cases name
// paths below the server's folder (/opt/rustdesk) that are not there, and
// folders the browser may not see (/etc); nothing of the server's own
// files is read, since that folder holds its keys. Relative paths are
// taken from the console's folder.
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

		// Requests that would change files: no route answers them
		post("mkdir", "files/mkdir", map[string]any{"path": base + "/sub"}),
		post("write", "files/write", map[string]any{"path": base + "/note.txt", "content": "hello\nworld\n"}),
		post("rename", "files/rename", map[string]any{"from": base + "/note.txt", "to": base + "/sub/moved.txt"}),
		post("delete-file", "files/delete", map[string]any{"path": base + "/sub/moved.txt"}),

		get("read-missing", "files/read", pathQuery(base+"/missing.txt"), few...),
		get("read-no-path", "files/read", nil, few...),
		get("read-empty-path", "files/read", pathQuery(""), few...),
		get("read-outside", "files/read", pathQuery("/etc/hostname"), few...),
		get("read-dot-dot", "files/read", pathQuery(base+"/../../../etc/hostname"), few...),
		get("read-relative-missing", "files/read", pathQuery("no-such-sm-file"), few...),
		get("read-path-twice", "files/read", url.Values{"path": {"no-such-a", "no-such-b"}}, few...),
		get("read-path-as-object", "files/read", url.Values{"path[x]": {"1"}}, few...),
		get("read-nul-in-path", "files/read", pathQuery(base+"/a\x00b"), few...),

		get("list-missing", "files", pathQuery(base+"/missing"), few...),
		get("list-outside", "files", pathQuery("/etc"), few...),
		get("list-no-path", "files", nil, few...),
		get("list-empty-path", "files", pathQuery(""), few...),

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
