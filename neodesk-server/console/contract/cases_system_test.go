//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The system widgets of the dashboard: processes and disks, log lines,
// database statistics, containers, the command runner and the speed test.
// The command runner is on the matrix skip list. Its cases send what it
// refuses, and two commands of its fixed list that only read: whoami, and
// docker ps, which the instances do not have.
func init() {
	get := func(name, path string, query url.Values, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Path: path, Query: query, Roles: roles}
	}
	exec := func(name string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/system/exec", Body: body, Roles: roles}
	}
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	// What one process uses changes while two recordings can agree on it
	process := []string{"processes", "disks", "node.pid", "node.uptime", "node.heapUsed", "node.heapTotal", "node.rss"}

	contract.Group("system",
		contract.Case{Name: "info", Path: "/api/system/info", Volatile: process},
		contract.Case{Name: "info-head", Method: "HEAD", Path: "/api/system/info", Roles: few},
		contract.Case{Name: "info-other-spelling", Path: "/API/System/Info/", Volatile: process, Roles: few},

		get("logs", "/api/logs/recent", nil),
		get("logs-go", "/api/logs/recent", url.Values{"source": {"go"}, "limit": {"5"}}),
		get("logs-other-source", "/api/logs/recent", url.Values{"source": {"kernel"}, "limit": {"100000"}}, few...),
		get("logs-limit-not-a-number", "/api/logs/recent", url.Values{"limit": {"many"}}, few...),
		get("logs-limit-negative", "/api/logs/recent", url.Values{"limit": {"-3"}, "source": {"go"}}, few...),
		get("logs-source-twice", "/api/logs/recent", url.Values{"source": {"go", "console"}}, few...),
		get("logs-source-as-list", "/api/logs/recent", url.Values{"source[]": {"go"}}, few...),
		get("logs-source-as-object", "/api/logs/recent", url.Values{"source[name]": {"go"}}, few...),
		get("logs-source-empty", "/api/logs/recent", url.Values{"source": {""}}, few...),

		// The rows of a table and the size of the file grow with what the
		// suite did before
		contract.Case{Name: "database-stats", Path: "/api/database/stats", Volatile: []string{"tables[*].count", "~size"}},
		get("containers", "/api/docker/containers", nil),

		exec("exec-no-command", map[string]any{}),
		exec("exec-command-is-a-list", map[string]any{"command": []string{"whoami"}}, few...),
		exec("exec-command-empty", map[string]any{"command": ""}, few...),
		exec("exec-not-on-the-list", map[string]any{"command": "cat /etc/passwd"}),
		exec("exec-list-entry-with-more", map[string]any{"command": "whoami; id"}, few...),
		exec("exec-other-case", map[string]any{"command": "WHOAMI"}, few...),
		exec("exec-whoami", map[string]any{"command": "whoami"}),
		exec("exec-whoami-with-spaces", map[string]any{"command": " \twhoami\n"}, few...),
		exec("exec-command-that-is-missing", map[string]any{"command": "docker ps"}, few...),
		contract.Case{Name: "exec-without-csrf-token", Method: "POST", Path: "/api/system/exec", Body: map[string]any{"command": "whoami"}, NoCSRF: true, Roles: few},

		get("speed-test-small", "/api/speed-test", url.Values{"size": {"10"}}),
		get("speed-test-one-chunk-and-a-bit", "/api/speed-test", url.Values{"size": {"65537"}}, few...),
		get("speed-test-size-not-a-number", "/api/speed-test", url.Values{"size": {"big"}}, contract.Roles("admin")...),
		get("speed-test-size-zero", "/api/speed-test", url.Values{"size": {"0"}}, contract.Roles("admin")...),
		get("speed-test-size-with-unit", "/api/speed-test", url.Values{"size": {"2048kb"}}, few...),
		get("speed-test-over-the-limit", "/api/speed-test", url.Values{"size": {"99999999"}}, contract.Roles("viewer")...),
		contract.Case{Name: "speed-test-head", Method: "HEAD", Path: "/api/speed-test", Query: url.Values{"size": {"10"}}, Roles: few},
		contract.Case{
			Name: "speed-test-no-compression", Path: "/api/speed-test", Query: url.Values{"size": {"4096"}},
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("viewer"),
		},
	)
}
