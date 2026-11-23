//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The network monitoring routes: targets, checks, the monitor and the
// ad-hoc tools.
//
// Checks and tools only reach addresses inside the instance. The guard in
// front of every outbound check refuses loopback and 0.0.0.0, so the cases
// that need an open port use the instance's own host name ({console_host},
// a private address the guard lets through): port 5000 is the console,
// port 1 is closed. The instance has no ping command, so a ping that
// passes the guard fails at starting it.
//
// The monitor is started and stopped while there are no targets, so its
// poll finds nothing to check. Each role's turn deletes the targets it
// made and leaves the monitor stopped with the default interval.
func init() {
	few := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	one := contract.Roles("admin")
	post := func(name, path string, body any, roles []contract.Role, volatile ...string) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: path, Body: body, Roles: roles, Volatile: volatile}
	}
	// A target row: its id is a counter and its times are the clock.
	row := []string{"id", "~last_rtt_ms"}
	rows := []string{"[*].id", "~[*].last_rtt_ms"}

	contract.Group("network",
		contract.Case{Name: "targets-empty", Path: "/api/network/targets"},
		contract.Case{Name: "targets-empty-head", Method: "HEAD", Path: "/api/network/targets", Roles: few},
		contract.Case{Name: "stats-empty", Path: "/api/network/stats"},

		// The monitor, with nothing to check
		contract.Case{Name: "monitor-status", Path: "/api/network/monitor/status"},
		post("monitor-start", "/api/network/monitor/start", map[string]any{}, nil),
		contract.Case{Name: "monitor-status-running", Path: "/api/network/monitor/status", Roles: few},
		post("monitor-start-while-running", "/api/network/monitor/start", map[string]any{"interval_ms": 5000}, few),
		post("monitor-stop", "/api/network/monitor/stop", map[string]any{}, nil),
		contract.Case{Name: "monitor-status-stopped", Path: "/api/network/monitor/status", Roles: few},
		post("monitor-start-interval", "/api/network/monitor/start", map[string]any{"interval_ms": "120000ms"}, few),
		contract.Case{Name: "monitor-status-interval", Path: "/api/network/monitor/status", Roles: few},
		post("monitor-stop-again", "/api/network/monitor/stop", map[string]any{}, few),
		post("monitor-stop-while-stopped", "/api/network/monitor/stop", map[string]any{}, few),
		post("monitor-start-bad-interval", "/api/network/monitor/start", map[string]any{"interval_ms": "soon"}, few),
		post("monitor-stop-last", "/api/network/monitor/stop", map[string]any{}, few),
		contract.Case{
			Name: "monitor-start-without-csrf-token", Method: "POST", Path: "/api/network/monitor/start",
			Body: map[string]any{}, NoCSRF: true, Roles: few,
		},
		contract.Case{Name: "monitor-status-last", Path: "/api/network/monitor/status", Roles: few},

		// Targets
		contract.Case{
			Name: "create-tcp", Method: "POST", Path: "/api/network/targets",
			Body:    map[string]any{"name": "Contract tcp", "host": "{console_host}", "port": 5000, "check_type": "tcp", "timeout_ms": 3000, "interval_ms": 30000},
			Capture: map[string]string{"tcp": "id"}, Volatile: row,
		},
		contract.Case{
			Name: "create-http", Method: "POST", Path: "/api/network/targets",
			Body:    map[string]any{"name": "Contract http", "url": "http://{console_host}:5000/health", "check_type": "http", "enabled": true},
			Capture: map[string]string{"http": "id"}, Volatile: row,
		},
		contract.Case{
			Name: "create-ping", Method: "POST", Path: "/api/network/targets",
			Body:    map[string]any{"name": "Contract ping", "host": "{console_host}", "check_type": "ping", "enabled": false},
			Capture: map[string]string{"ping": "id"}, Volatile: row,
		},
		contract.Case{
			Name: "create-odd-types", Method: "POST", Path: "/api/network/targets",
			Body: map[string]any{
				"name": 42, "host": "{console_host}", "port": "1x", "url": 0, "check_type": "tcp",
				"timeout_ms": "abc", "interval_ms": "1500.9", "enabled": "no",
			},
			Capture: map[string]string{"odd": "id"}, Volatile: row,
		},
		post("create-without-name", "/api/network/targets", map[string]any{"check_type": "ping", "host": "{console_host}"}, few),
		post("create-without-type", "/api/network/targets", map[string]any{"name": "x", "host": "{console_host}"}, few),
		post("create-unknown-type", "/api/network/targets", map[string]any{"name": "x", "host": "{console_host}", "check_type": "icmp"}, few),
		post("create-type-in-capitals", "/api/network/targets", map[string]any{"name": "x", "host": "{console_host}", "check_type": "TCP"}, few),
		post("create-without-host", "/api/network/targets", map[string]any{"name": "x", "check_type": "ping", "host": "", "url": nil}, few),
		post("create-name-as-object", "/api/network/targets", map[string]any{"name": map[string]any{"a": 1}, "check_type": "ping", "host": "{console_host}"}, few),
		post("create-name-as-true", "/api/network/targets", map[string]any{"name": true, "check_type": "ping", "host": "{console_host}"}, few),
		contract.Case{
			Name: "create-host-as-list", Method: "POST", Path: "/api/network/targets",
			Body:    map[string]any{"name": "x", "check_type": "ping", "host": []any{"{console_host}"}},
			Capture: map[string]string{"listed": "id"}, Roles: few, Volatile: row,
		},
		post("create-host-as-empty-list", "/api/network/targets", map[string]any{"name": "x", "check_type": "ping", "host": []any{}}, few),
		post("create-host-as-list-of-lists", "/api/network/targets", map[string]any{"name": "x", "check_type": "ping", "host": []any{[]any{"{console_host}"}}}, few),
		contract.Case{
			Name: "create-body-is-a-list", Method: "POST", Path: "/api/network/targets",
			RawBody: `[{"name":"x","check_type":"ping","host":"{console_host}"}]`, ContentType: "application/json", Roles: few,
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: "/api/network/targets",
			RawBody: "name=x&check_type=ping&host={console_host}", ContentType: "application/x-www-form-urlencoded", Roles: few,
		},
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: "/api/network/targets",
			Body: map[string]any{"name": "x", "check_type": "ping", "host": "{console_host}"}, NoCSRF: true, Roles: few,
		},

		contract.Case{Name: "list", Path: "/api/network/targets", Volatile: rows},
		contract.Case{Name: "list-enabled", Path: "/api/network/targets", Query: url.Values{"enabled": {"true"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-disabled", Path: "/api/network/targets", Query: url.Values{"enabled": {"false"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-enabled-as-number", Path: "/api/network/targets", Query: url.Values{"enabled": {"1"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-enabled-twice", Path: "/api/network/targets", Query: url.Values{"enabled": {"true", "true"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-by-type", Path: "/api/network/targets", Query: url.Values{"check_type": {"tcp"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-by-type-and-state", Path: "/api/network/targets", Query: url.Values{"check_type": {"tcp"}, "enabled": {"true"}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-by-unknown-type", Path: "/api/network/targets", Query: url.Values{"check_type": {"icmp"}}, Roles: few},
		contract.Case{Name: "list-by-empty-type", Path: "/api/network/targets", Query: url.Values{"check_type": {""}}, Roles: few, Volatile: rows},
		contract.Case{Name: "list-by-two-types", Path: "/api/network/targets", Query: url.Values{"check_type": {"tcp", "http"}}, Roles: few},
		contract.Case{Name: "list-by-type-in-brackets", Path: "/api/network/targets", Query: url.Values{"check_type[]": {"tcp"}}, Roles: few},
		contract.Case{Name: "list-by-type-as-object", Path: "/api/network/targets", Query: url.Values{"check_type[is]": {"tcp"}}, Roles: few},

		contract.Case{Name: "get", Path: "/api/network/targets/{tcp}", Volatile: row},
		contract.Case{Name: "get-head", Method: "HEAD", Path: "/api/network/targets/{tcp}", Roles: few},
		contract.Case{Name: "get-http", Path: "/api/network/targets/{http}", Roles: few, Volatile: row},
		contract.Case{Name: "get-odd-types", Path: "/api/network/targets/{odd}", Roles: few, Volatile: row},
		contract.Case{Name: "get-unknown", Path: "/api/network/targets/999999999", Roles: few},
		contract.Case{Name: "get-not-a-number", Path: "/api/network/targets/first", Roles: few},
		contract.Case{Name: "get-with-fraction", Path: "/api/network/targets/{tcp}.0", Roles: few, Volatile: row},
		contract.Case{Name: "get-with-spaces", Path: "/api/network/targets/%20{tcp}%20", Roles: few, Volatile: row},
		contract.Case{Name: "get-with-letters", Path: "/api/network/targets/{tcp}abc", Roles: few},

		contract.Case{
			Name: "update", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body:     map[string]any{"name": "Contract tcp renamed", "port": "5000", "timeout_ms": 2500, "interval_ms": "45000", "enabled": false},
			Volatile: row,
		},
		contract.Case{Name: "get-after-update", Path: "/api/network/targets/{tcp}", Roles: few, Volatile: row},
		contract.Case{
			Name: "update-enable", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"enabled": "yes"}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-nothing", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{}, Roles: few,
		},
		contract.Case{
			Name: "update-unknown-field", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"last_status": "up", "note": "x"}, Roles: few,
		},
		contract.Case{
			Name: "update-unknown-type", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"check_type": "icmp"}, Roles: few,
		},
		contract.Case{
			Name: "update-empty-type", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"check_type": ""}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-type-back", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"check_type": "tcp"}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-name-to-null", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"name": nil}, Roles: few,
		},
		contract.Case{
			Name: "update-name-to-list", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"name": []any{"a", "b"}}, Roles: few,
		},
		contract.Case{
			Name: "update-enabled-to-object", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"enabled": map[string]any{}}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-numbers-that-are-none", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"port": "none", "timeout_ms": "none", "interval_ms": nil, "url": nil, "host": 7.5}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-large-numbers", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"port": 5000000000, "timeout_ms": "99999999999999999999999", "name": 5000000000}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-odd-back", Method: "PATCH", Path: "/api/network/targets/{odd}",
			Body: map[string]any{"name": "42", "host": "{console_host}", "port": 1, "timeout_ms": 5000, "interval_ms": 1500, "enabled": true}, Roles: few, Volatile: row,
		},
		contract.Case{
			Name: "update-unknown", Method: "PATCH", Path: "/api/network/targets/999999999",
			Body: map[string]any{"name": "x"}, Roles: few,
		},
		contract.Case{
			Name: "update-not-a-number", Method: "PATCH", Path: "/api/network/targets/first",
			Body: map[string]any{"name": "x"}, Roles: few,
		},
		contract.Case{
			Name: "update-without-csrf-token", Method: "PATCH", Path: "/api/network/targets/{tcp}",
			Body: map[string]any{"name": "x"}, NoCSRF: true, Roles: few,
		},

		// Checks and their history
		contract.Case{Name: "history-empty", Path: "/api/network/targets/{tcp}/history", Roles: few},
		post("check-tcp", "/api/network/targets/{tcp}/check", map[string]any{}, nil, "target_id", "~rtt_ms"),
		post("check-http", "/api/network/targets/{http}/check", map[string]any{}, nil, "target_id", "~rtt_ms"),
		post("check-ping", "/api/network/targets/{ping}/check", map[string]any{}, nil, "target_id"),
		post("check-closed-port", "/api/network/targets/{odd}/check", map[string]any{}, few, "target_id"),
		post("check-unknown", "/api/network/targets/999999999/check", map[string]any{}, few),
		post("check-not-a-number", "/api/network/targets/first/check", map[string]any{}, few),
		contract.Case{
			Name: "check-without-csrf-token", Method: "POST", Path: "/api/network/targets/{tcp}/check",
			Body: map[string]any{}, NoCSRF: true, Roles: few,
		},
		contract.Case{Name: "get-after-check", Path: "/api/network/targets/{tcp}", Volatile: row},
		contract.Case{Name: "get-http-after-check", Path: "/api/network/targets/{http}", Roles: few, Volatile: row},
		contract.Case{Name: "get-ping-after-check", Path: "/api/network/targets/{ping}", Roles: few, Volatile: row},
		contract.Case{Name: "list-after-checks", Path: "/api/network/targets", Roles: few, Volatile: rows},
		// The mean round trip of the checks above, which the clock decides
		contract.Case{Name: "stats", Path: "/api/network/stats", Volatile: []string{"~avg_latency"}},
		contract.Case{
			Name: "history", Path: "/api/network/targets/{tcp}/history",
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{
			Name: "history-http", Path: "/api/network/targets/{http}/history", Roles: few,
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{
			Name: "history-ping", Path: "/api/network/targets/{ping}/history", Roles: few,
			Volatile: []string{"[*].id", "[*].target_id"},
		},
		post("check-tcp-again", "/api/network/targets/{tcp}/check", map[string]any{}, few, "target_id", "~rtt_ms"),
		contract.Case{
			Name: "history-limit", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"limit": {"1"}}, Roles: few,
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{
			Name: "history-limit-not-a-number", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"limit": {"all"}}, Roles: few,
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{
			Name: "history-limit-below-zero", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"limit": {"-1"}}, Roles: few,
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{
			Name: "history-from-long-ago", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"from": {"2000-01-01"}, "to": {"2999-01-01"}}, Roles: few,
			Volatile: []string{"[*].id", "[*].target_id", "~[*].rtt_ms"},
		},
		contract.Case{Name: "history-from-far-ahead", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"from": {"2999-01-01"}}, Roles: few},
		contract.Case{Name: "history-to-long-ago", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"to": {"2000-01-01"}}, Roles: few},
		contract.Case{Name: "history-from-twice", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"from": {"2000-01-01", "2001-01-01"}}, Roles: few},
		contract.Case{Name: "history-to-twice", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"to": {"2999-01-01", "2999-01-02"}}, Roles: few},
		contract.Case{Name: "history-from-not-a-time", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"from": {"never"}}, Roles: few},
		contract.Case{Name: "history-to-not-a-time", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"to": {"never"}}, Roles: few},
		contract.Case{Name: "history-from-in-brackets", Path: "/api/network/targets/{tcp}/history", Query: url.Values{"from[x]": {"1"}}, Roles: few},
		contract.Case{Name: "history-unknown", Path: "/api/network/targets/999999999/history", Roles: few},
		contract.Case{Name: "history-not-a-number", Path: "/api/network/targets/first/history", Roles: few},

		// The tools
		post("ping-without-host", "/api/network/ping", map[string]any{}, few),
		post("ping", "/api/network/ping", map[string]any{"host": "{console_host}"}, nil),
		post("ping-loopback", "/api/network/ping", map[string]any{"host": "127.0.0.1", "timeout": 1000}, nil),
		post("ping-localhost", "/api/network/ping", map[string]any{"host": "LocalHost"}, few),
		post("ping-shell-characters", "/api/network/ping", map[string]any{"host": "{console_host}; id"}, few),
		post("ping-option-as-host", "/api/network/ping", map[string]any{"host": "-c5"}, few),
		post("ping-host-as-number", "/api/network/ping", map[string]any{"host": 2130706433}, few),
		post("ping-host-as-list", "/api/network/ping", map[string]any{"host": []any{"{console_host}"}}, few),
		post("ping-timeout-below-zero", "/api/network/ping", map[string]any{"host": "{console_host}", "timeout": -5000}, few),
		post("ping-timeout-as-text", "/api/network/ping", map[string]any{"host": "{console_host}", "timeout": "2500ms"}, few),

		post("tcp-without-host", "/api/network/tcp", map[string]any{"port": 5000}, few),
		post("tcp-without-port", "/api/network/tcp", map[string]any{"host": "{console_host}"}, few),
		post("tcp-port-zero", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": 0}, few),
		post("tcp", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": 5000}, nil, "~rtt_ms"),
		post("tcp-port-as-text", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": "5000/tcp", "timeout": "1000"}, few, "~rtt_ms"),
		post("tcp-closed-port", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": 1}, nil),
		post("tcp-loopback", "/api/network/tcp", map[string]any{"host": "127.0.0.1", "port": 5000}, nil),
		post("tcp-loopback-other-address", "/api/network/tcp", map[string]any{"host": "127.8.9.10", "port": 5000}, few),
		post("tcp-loopback-v6", "/api/network/tcp", map[string]any{"host": "::1", "port": 5000}, few),
		post("tcp-loopback-mapped", "/api/network/tcp", map[string]any{"host": "::ffff:127.0.0.1", "port": 5000}, few),
		post("tcp-link-local", "/api/network/tcp", map[string]any{"host": "169.254.169.254", "port": 80}, few),
		post("tcp-link-local-v6", "/api/network/tcp", map[string]any{"host": "fe80::1", "port": 80}, few),
		post("tcp-localhost", "/api/network/tcp", map[string]any{"host": "localhost", "port": 5000}, few),
		post("tcp-metadata-name", "/api/network/tcp", map[string]any{"host": "metadata.google.internal", "port": 80}, few),
		post("tcp-loopback-as-number", "/api/network/tcp", map[string]any{"host": "2130706433", "port": 5000}, few),
		post("tcp-loopback-in-hex", "/api/network/tcp", map[string]any{"host": "0x7f.1", "port": 5000}, few),
		post("tcp-loopback-with-zeros", "/api/network/tcp", map[string]any{"host": "127.000.000.001", "port": 5000}, few),
		post("tcp-host-with-space", "/api/network/tcp", map[string]any{"host": " {console_host}", "port": 5000}, few),
		post("tcp-host-in-brackets", "/api/network/tcp", map[string]any{"host": "[::1]", "port": 5000}, few),
		post("tcp-port-too-large", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": 70000}, few),
		post("tcp-port-below-zero", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": -1}, few),
		post("tcp-port-not-a-number", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": "http"}, few),
		post("tcp-port-text-zero", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": "0"}, few),
		post("tcp-timeout-below-zero", "/api/network/tcp", map[string]any{"host": "{console_host}", "port": 5000, "timeout": -1}, few),

		post("http-without-url", "/api/network/http", map[string]any{}, few),
		post("http", "/api/network/http", map[string]any{"url": "http://{console_host}:5000/health"}, nil, "~rtt_ms"),
		post("http-with-query", "/api/network/http", map[string]any{"url": "  HTTP://{console_host}:5000/health?probe=1#top  ", "timeout": 2000}, few, "~rtt_ms"),
		post("http-redirect", "/api/network/http", map[string]any{"url": "http://{console_host}:5000/"}, few, "~rtt_ms"),
		post("http-refused-by-console", "/api/network/http", map[string]any{"url": "http://{console_host}:5000/api/network/targets"}, few, "~rtt_ms"),
		post("http-not-found", "/api/network/http", map[string]any{"url": "http://{console_host}:5000/api/nothing/here"}, few, "~rtt_ms"),
		post("http-closed-port", "/api/network/http", map[string]any{"url": "http://{console_host}:1/"}, nil),
		post("http-loopback", "/api/network/http", map[string]any{"url": "http://127.0.0.1:5000/health"}, nil),
		post("http-localhost", "/api/network/http", map[string]any{"url": "http://localhost:5000/health"}, few),
		post("http-loopback-v6", "/api/network/http", map[string]any{"url": "http://[::1]:5000/health"}, few),
		post("http-loopback-as-number", "/api/network/http", map[string]any{"url": "http://2130706433:5000/health"}, few),
		post("http-loopback-in-hex", "/api/network/http", map[string]any{"url": "http://0x7f.0.0.1:5000/health"}, few),
		post("http-loopback-short", "/api/network/http", map[string]any{"url": "http://127.1:5000/health"}, few),
		post("http-loopback-with-backslashes", "/api/network/http", map[string]any{"url": `http:\\127.0.0.1:5000\health`}, few),
		post("http-link-local", "/api/network/http", map[string]any{"url": "http://169.254.169.254/latest/meta-data/"}, few),
		post("http-credentials", "/api/network/http", map[string]any{"url": "http://user:secret@{console_host}:5000/health"}, few),
		post("http-user-only", "/api/network/http", map[string]any{"url": "http://user@{console_host}:5000/health"}, few),
		post("http-other-scheme", "/api/network/http", map[string]any{"url": "ftp://{console_host}/file"}, few),
		post("http-file", "/api/network/http", map[string]any{"url": "file:///etc/passwd"}, few),
		post("http-not-a-url", "/api/network/http", map[string]any{"url": "0.0.0.0:5000/health"}, few),
		post("http-no-scheme", "/api/network/http", map[string]any{"url": "//0.0.0.0:5000/health"}, few),
		post("http-bad-port", "/api/network/http", map[string]any{"url": "http://{console_host}:99999/"}, few),
		post("http-url-as-number", "/api/network/http", map[string]any{"url": 5}, few),
		contract.Case{
			Name: "http-url-as-object", Method: "POST", Path: "/api/network/http", Roles: few, ContentType: "application/json",
			RawBody: `{"url":{"href":"http://{console_host}:5000/health","b":1.50,"a":"xé","10":true,"2":null}}`,
		},
		post("http-timeout-below-zero", "/api/network/http", map[string]any{"url": "http://{console_host}:5000/health", "timeout": -1}, few),

		post("resolve-without-host", "/api/network/resolve", map[string]any{}, few),
		post("resolve", "/api/network/resolve", map[string]any{"host": "{console_host}"}, nil),
		post("resolve-private-address", "/api/network/resolve", map[string]any{"host": "10.255.255.1"}, few),
		post("resolve-v6", "/api/network/resolve", map[string]any{"host": "FD00::1"}, few),
		post("resolve-loopback", "/api/network/resolve", map[string]any{"host": "127.0.0.1"}, nil),
		post("resolve-localhost", "/api/network/resolve", map[string]any{"host": "localhost"}, few),
		post("resolve-loopback-as-number", "/api/network/resolve", map[string]any{"host": "2130706433"}, few),
		post("resolve-bad-characters", "/api/network/resolve", map[string]any{"host": "bad host"}, few),
		post("resolve-host-as-number", "/api/network/resolve", map[string]any{"host": 5}, few),
		contract.Case{
			Name: "resolve-without-csrf-token", Method: "POST", Path: "/api/network/resolve",
			Body: map[string]any{"host": "{console_host}"}, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "resolve-as-form", Method: "POST", Path: "/api/network/resolve",
			RawBody: "host={console_host}", ContentType: "application/x-www-form-urlencoded", Roles: few,
		},

		// Clean up
		contract.Case{Name: "delete", Method: "DELETE", Path: "/api/network/targets/{tcp}"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: "/api/network/targets/{tcp}", Roles: few},
		contract.Case{Name: "history-after-delete", Path: "/api/network/targets/{tcp}/history", Roles: one},
		contract.Case{Name: "delete-not-a-number", Method: "DELETE", Path: "/api/network/targets/first", Roles: few},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/network/targets/{http}", NoCSRF: true, Roles: few},
		contract.Case{Name: "delete-http", Method: "DELETE", Path: "/api/network/targets/{http}"},
		contract.Case{Name: "delete-ping", Method: "DELETE", Path: "/api/network/targets/{ping}"},
		contract.Case{Name: "delete-odd-types", Method: "DELETE", Path: "/api/network/targets/{odd}"},
		contract.Case{Name: "delete-host-as-list", Method: "DELETE", Path: "/api/network/targets/{listed}", Roles: few},
		contract.Case{Name: "targets-after-delete", Path: "/api/network/targets", Roles: few},
	)
}
