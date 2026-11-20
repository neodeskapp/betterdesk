//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The general settings: the restart transaction, the server's facts and
// addresses, device scope, availability history, the audit log,
// enrollment, the directory and single sign-on settings, client sessions,
// the configuration file editor, the connection mode and the public
// endpoints.
//
// Every write puts back what it changed, as far as a route allows it. Two
// things stay: the LDAP bind password (a save without one keeps the
// stored one) and the file data/.session_secret, which the file editor
// creates in the instance's own data folder (the instance takes its
// session secret from the environment, so the file is never read).
//
// Some routes are only sent where they are safe:
//   - Confirming a restart transaction restarts the server, and
//     POST /api/settings/advanced/restart and
//     POST /api/settings/connection-mode/restart confirm the caller's
//     pending transaction. They are sent with ids nobody owns, or while
//     the caller has no transaction.
//   - PUT /api/settings/connection-mode rewrites docker-compose.yml in the
//     repository and PUT /api/settings/public-endpoints rewrites the .env
//     of web-nodejs, both in the folder every instance of a worktree
//     shares. They are sent by roles the guards turn away, and with values
//     the validation refuses before anything is written.
//   - The tests of the directory and single sign-on settings connect to
//     127.0.0.1:1, where nothing listens.
func init() {
	few := contract.Roles("signed-out", "viewer", "admin")
	admin := contract.Roles("admin")
	ends := contract.Roles("signed-out", "admin")
	// The roles with server.config, and operator for a refusal
	writers := contract.Roles("server_admin", "super_admin", "admin")
	readers := contract.Roles("operator", "server_admin", "super_admin", "admin")
	refused := contract.Roles("signed-out", "operator", "global_admin")

	const secretFile = "/api/settings/advanced/files/console-session-secret"
	// The content the file editor writes. It is no secret: the instance
	// never reads the file.
	const fileContent = "contract-throwaway-file\n"

	contract.Group("settings",
		// Liveness and the public status of a restart
		contract.Case{
			Name: "restart-status", Path: "/api/settings/restart-status", Roles: few,
			// The seconds since the start can agree in two recordings
			Volatile: []string{"data.uptime"},
		},
		contract.Case{
			Name: "restart-status-unknown-job", Path: "/api/settings/restart-status",
			Query: url.Values{"job": {"no-such-job"}, "token": {"no-such-token"}}, Roles: few,
		},
		contract.Case{
			Name: "restart-status-job-without-token", Path: "/api/settings/restart-status",
			Query: url.Values{"job": {"no-such-job"}}, Roles: ends,
			Volatile: []string{"data.uptime"},
		},
		contract.Case{
			Name: "restart-status-two-jobs", Path: "/api/settings/restart-status",
			Query: url.Values{"job": {"a", "b"}, "token": {"t"}}, Roles: admin,
		},
		contract.Case{
			Name: "restart-status-empty-token", Path: "/api/settings/restart-status",
			Query: url.Values{"job": {"a"}, "token": {""}}, Roles: admin,
			Volatile: []string{"data.uptime"},
		},

		// The restart transaction, with ids nobody owns
		contract.Case{Name: "restart-pending", Path: "/api/settings/restart/pending"},
		contract.Case{
			Name: "restart-cancel-unknown", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "no-such-transaction"}, Roles: contract.Roles("signed-out", "operator", "admin"),
		},
		contract.Case{
			Name: "restart-cancel-without-id", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "restart-cancel-number-id", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": 12345}, Roles: admin,
		},
		contract.Case{
			Name: "restart-confirm-unknown", Method: "POST", Path: "/api/settings/restart/confirm",
			Body: map[string]any{"id": "no-such-transaction"}, Roles: contract.Roles("signed-out", "operator", "server_admin", "admin"),
		},
		contract.Case{
			Name: "restart-confirm-without-id", Method: "POST", Path: "/api/settings/restart/confirm",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "restart-complete-unknown", Method: "POST", Path: "/api/settings/restart/complete",
			Body: map[string]any{"id": "no-such-transaction"}, Roles: few,
		},
		contract.Case{
			Name: "restart-complete-as-form", Method: "POST", Path: "/api/settings/restart/complete",
			RawBody: "id=no-such-transaction", ContentType: "application/x-www-form-urlencoded", Roles: admin,
		},

		// What the server is
		contract.Case{
			Name: "info", Path: "/api/settings/info",
			// nodeVersion names the runtime of the process that answers;
			// the uptimes and the memory change from second to second
			Volatile: []string{"~data.app.nodeVersion", "~data.goServer.uptime", "data.server.uptime", "data.server.memoryUsage"},
		},
		contract.Case{Name: "server-info", Path: "/api/settings/server-info"},
		contract.Case{
			Name: "server-info-ignores-host", Path: "/api/settings/server-info",
			Query: url.Values{"host": {"console.example.org"}}, Roles: admin,
		},

		// Device scope
		contract.Case{Name: "device-scope", Path: "/api/settings/device-scope", Roles: readers},
		contract.Case{
			Name: "device-scope-restricted", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{"mode": "RESTRICTED"}, Roles: contract.Roles("signed-out", "operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{Name: "device-scope-after", Path: "/api/settings/device-scope", Roles: writers},
		contract.Case{
			// The newest entry of the audit log is the change just made.
			// Its id counts what the instance logged before.
			Name: "audit-newest", Path: "/api/settings/audit", Query: url.Values{"limit": {"1"}},
			Roles: writers, Volatile: []string{"data[*].id"},
		},
		contract.Case{
			Name: "device-scope-invalid", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{"mode": "closed"}, Roles: writers,
		},
		contract.Case{
			Name: "device-scope-number", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{"mode": 5}, Roles: admin,
		},
		contract.Case{
			Name: "device-scope-list", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{"mode": []any{"restricted"}}, Roles: admin,
		},
		// a body without the mode is refused (BUGS C6)
		contract.Case{
			Name: "device-scope-empty", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "device-scope-default", Method: "POST", Path: "/api/settings/device-scope",
			Body: map[string]any{"mode": "open"}, Roles: writers,
		},
		contract.Case{Name: "device-scope-restored", Path: "/api/settings/device-scope", Roles: admin},

		// Availability history (the server keeps the values)
		contract.Case{Name: "availability-history", Path: "/api/settings/availability-history", Roles: readers},
		contract.Case{
			Name: "availability-history-on", Method: "POST", Path: "/api/settings/availability-history",
			Body: map[string]any{"enabled": true, "retention_days": 90}, Roles: contract.Roles("operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{Name: "availability-history-after", Path: "/api/settings/availability-history", Roles: writers},
		contract.Case{
			Name: "availability-history-odd-types", Method: "POST", Path: "/api/settings/availability-history",
			Body: map[string]any{"enabled": "no", "retention_days": "12abc"}, Roles: admin,
		},
		contract.Case{
			Name: "availability-history-clamped", Method: "POST", Path: "/api/settings/availability-history",
			Body: map[string]any{"enabled": 0, "retention_days": 99999}, Roles: admin,
		},
		contract.Case{
			Name: "availability-history-negative", Method: "POST", Path: "/api/settings/availability-history",
			Body: map[string]any{"retention_days": -4}, Roles: admin,
		},
		contract.Case{Name: "availability-history-read-again", Path: "/api/settings/availability-history", Roles: admin},
		contract.Case{
			Name: "availability-history-restore", Method: "POST", Path: "/api/settings/availability-history",
			Body: map[string]any{"enabled": false, "retention_days": 30}, Roles: writers,
		},

		// The audit log
		contract.Case{
			Name: "audit-limit-list", Path: "/api/settings/audit", Query: url.Values{"limit": {"1", "5"}},
			Roles: admin, Volatile: []string{"data[*].id"},
		},
		contract.Case{
			Name: "audit-limit-object", Path: "/api/settings/audit", Query: url.Values{"limit[x]": {"1"}},
			Roles: contract.Roles("signed-out", "viewer"), Volatile: []string{"data[*].id"},
		},

		// Enrollment
		contract.Case{Name: "enrollment", Path: "/api/settings/enrollment", Roles: readers},
		contract.Case{
			Name: "enrollment-toggles", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"rich_approve": false, "tag_picker": 0}, Roles: contract.Roles("operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{Name: "enrollment-after", Path: "/api/settings/enrollment", Roles: writers},
		contract.Case{
			Name: "enrollment-require-approval", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"require_approval": true, "rich_approve": "yes"}, Roles: admin,
		},
		contract.Case{
			Name: "enrollment-locked", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"mode": "locked", "require_approval": false}, Roles: admin,
		},
		contract.Case{
			Name: "enrollment-invalid-mode", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"mode": "closed"}, Roles: writers,
		},
		contract.Case{
			Name: "enrollment-null-mode", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"mode": nil}, Roles: admin,
		},
		contract.Case{
			Name: "enrollment-approval-off", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"require_approval": false, "mode": "managed"}, Roles: admin,
		},
		contract.Case{
			Name: "enrollment-restore", Method: "PUT", Path: "/api/settings/enrollment",
			Body: map[string]any{"mode": "open", "rich_approve": true, "tag_picker": true}, Roles: writers,
		},

		// Directory (LDAP): the server masks the bind password
		contract.Case{Name: "ldap", Path: "/api/settings/ldap", Roles: readers},
		contract.Case{
			Name: "ldap-save", Method: "PUT", Path: "/api/settings/ldap",
			Body: map[string]any{
				"enabled": false, "host": "ldap.invalid", "port": 389,
				"bind_dn": "cn=contract,dc=example,dc=org", "bind_password": "throwaway-bind-pw-1",
				"base_dn": "dc=example,dc=org",
			},
			Roles: contract.Roles("operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{Name: "ldap-after", Path: "/api/settings/ldap", Roles: writers},
		contract.Case{
			Name: "ldap-enable-without-host", Method: "PUT", Path: "/api/settings/ldap",
			Body: map[string]any{"enabled": true}, Roles: admin,
		},
		contract.Case{
			Name: "ldap-test-empty", Method: "POST", Path: "/api/settings/ldap/test",
			Body: map[string]any{}, Roles: contract.Roles("operator", "admin"),
		},
		contract.Case{
			Name: "ldap-test-nothing-listens", Method: "POST", Path: "/api/settings/ldap/test",
			Body: map[string]any{"host": "127.0.0.1", "port": 1, "conn_timeout_sec": 2}, Roles: admin,
		},
		contract.Case{
			Name: "ldap-empty", Method: "PUT", Path: "/api/settings/ldap",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "ldap-restore", Method: "PUT", Path: "/api/settings/ldap",
			Body:  map[string]any{"enabled": false, "host": "", "port": 0, "bind_dn": "", "base_dn": ""},
			Roles: writers,
		},

		// Client sessions (the server keeps the values; the .env of
		// web-nodejs is only written when it exists, which it does not on
		// a fresh instance)
		contract.Case{Name: "client-sessions", Path: "/api/settings/client-sessions", Roles: readers},
		contract.Case{
			Name: "client-sessions-save", Method: "PUT", Path: "/api/settings/client-sessions",
			Body: map[string]any{"expiry_days": 9, "sliding": false, "max_days": 40}, Roles: contract.Roles("operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{Name: "client-sessions-after", Path: "/api/settings/client-sessions", Roles: writers},
		contract.Case{
			Name: "client-sessions-odd-types", Method: "PUT", Path: "/api/settings/client-sessions",
			Body: map[string]any{"expiry_days": "12.9days", "sliding": "false", "max_days": 1000}, Roles: admin,
		},
		contract.Case{Name: "client-sessions-after-odd", Path: "/api/settings/client-sessions", Roles: admin},
		contract.Case{
			Name: "client-sessions-empty", Method: "PUT", Path: "/api/settings/client-sessions",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "client-sessions-restore", Method: "PUT", Path: "/api/settings/client-sessions",
			Body: map[string]any{"expiry_days": 7, "sliding": true, "max_days": 30}, Roles: writers,
		},

		// Single sign-on (OIDC): the server masks the client secret
		contract.Case{Name: "oidc", Path: "/api/settings/oidc", Roles: readers},
		contract.Case{
			Name: "oidc-wrong-redirect-path", Method: "PUT", Path: "/api/settings/oidc",
			Body:  map[string]any{"enabled": true, "redirect_url": "https://console.example.org/login"},
			Roles: contract.Roles("operator", "server_admin", "super_admin", "admin"),
		},
		contract.Case{
			Name: "oidc-redirect-number", Method: "PUT", Path: "/api/settings/oidc",
			Body: map[string]any{"enabled": 1, "redirect_url": 5}, Roles: admin,
		},
		contract.Case{
			Name: "oidc-redirect-other-language", Method: "PUT", Path: "/api/settings/oidc",
			Body:    map[string]any{"enabled": true, "redirect_url": "ftp://console.example.org/api/auth/oidc/callback"},
			Headers: map[string]string{"Accept-Language": "de"}, Roles: admin,
		},
		contract.Case{
			Name: "oidc-enable-without-client", Method: "PUT", Path: "/api/settings/oidc",
			Body:  map[string]any{"enabled": true, "redirect_url": " https://console.example.org/api/auth/oidc/callback/ "},
			Roles: admin,
		},
		contract.Case{
			Name: "oidc-save", Method: "PUT", Path: "/api/settings/oidc",
			Body: map[string]any{
				"enabled": false, "client_id": "contract-client", "client_secret": "throwaway-oidc-secret-1",
				"issuer_url": "https://idp.example.org", "redirect_url": "not checked while off",
			},
			Roles: writers,
		},
		contract.Case{Name: "oidc-after", Path: "/api/settings/oidc", Roles: writers},
		contract.Case{
			Name: "oidc-test-empty", Method: "POST", Path: "/api/settings/oidc/test",
			Body: map[string]any{}, Roles: contract.Roles("operator", "admin"),
		},
		contract.Case{
			Name: "oidc-test-nothing-listens", Method: "POST", Path: "/api/settings/oidc/test",
			Body: map[string]any{"issuer_url": "http://127.0.0.1:1"}, Roles: admin,
		},
		contract.Case{
			Name: "oidc-empty", Method: "PUT", Path: "/api/settings/oidc",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "oidc-restore", Method: "PUT", Path: "/api/settings/oidc",
			Body: map[string]any{
				"enabled": false, "scopes": "openid profile email", "claim_username": "preferred_username",
				"claim_email": "email", "claim_name": "name", "claim_groups": "groups", "default_role": "viewer",
				"allow_signup": true, "auto_discovery": true,
			},
			Roles: writers,
		},

		// The configuration file editor. The chain writes
		// data/.session_secret: in the first writer's turn the file is new,
		// so cancelling the transaction cannot undo it (the rollback gets
		// no backup) and the transaction fails; it is completed. The next
		// save makes a backup, and cancelling puts it back.
		contract.Case{Name: "advanced-files", Path: "/api/settings/advanced/files", Roles: readers},
		contract.Case{Name: "advanced-file-unknown", Path: "/api/settings/advanced/files/no-such-file", Roles: few},
		contract.Case{Name: "advanced-file-missing", Path: "/api/settings/advanced/files/console-env", Roles: admin},
		contract.Case{
			Name: "advanced-file-of-the-repository", Path: "/api/settings/advanced/files/docker-supervisord", Roles: admin,
			// A file the editor lists in the checkout that the one image of
			// A32 removed: a listed file that is missing and cannot be made
			// answers 404
			Volatile: []string{"data.content", "data.size", "data.mtime"},
		},
		contract.Case{
			Name: "advanced-write-unknown", Method: "PUT", Path: "/api/settings/advanced/files/no-such-file",
			Body: map[string]any{"content": "x"}, Roles: contract.Roles("operator", "admin"),
		},
		contract.Case{
			Name: "advanced-write-not-text", Method: "PUT", Path: secretFile,
			Body: map[string]any{"content": 5}, Roles: admin,
		},
		contract.Case{
			Name: "advanced-write-without-content", Method: "PUT", Path: secretFile,
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "advanced-write-missing", Method: "PUT", Path: "/api/settings/advanced/files/console-env",
			Body: map[string]any{"content": "X=1\n"}, Roles: admin,
		},
		contract.Case{
			Name: "advanced-write-create", Method: "PUT", Path: secretFile,
			Body:    map[string]any{"content": fileContent},
			Capture: map[string]string{"tx": "data.restartRequired.id"},
			Roles:   readers,
		},
		contract.Case{Name: "advanced-pending", Path: "/api/settings/restart/pending", Roles: writers},
		contract.Case{Name: "advanced-read-back", Path: secretFile, Roles: writers},
		contract.Case{
			Name: "advanced-restart-without-file", Method: "POST", Path: "/api/settings/advanced/restart",
			Body: map[string]any{}, Roles: writers,
		},
		contract.Case{
			Name: "advanced-restart-number-file", Method: "POST", Path: "/api/settings/advanced/restart",
			Body: map[string]any{"fileId": 7}, Roles: admin,
		},
		// Another operator acts on the transaction: admin, in the turns of
		// the two other writers
		contract.Case{
			Name: "advanced-confirm-by-another", Method: "POST", Path: "/api/settings/restart/confirm",
			Body: map[string]any{"id": "{tx}"}, As: "admin", Roles: contract.Roles("server_admin", "super_admin"),
		},
		contract.Case{
			Name: "advanced-cancel-by-another", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "{tx}"}, As: "admin", Roles: contract.Roles("server_admin"),
		},
		contract.Case{
			Name: "advanced-write-by-another", Method: "PUT", Path: secretFile,
			Body: map[string]any{"content": fileContent}, As: "admin", Roles: contract.Roles("server_admin"),
		},
		contract.Case{
			Name: "advanced-cancel", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "{tx}"}, Roles: writers,
		},
		contract.Case{Name: "advanced-pending-after-cancel", Path: "/api/settings/restart/pending", Roles: writers},
		contract.Case{
			Name: "advanced-complete", Method: "POST", Path: "/api/settings/restart/complete",
			Body: map[string]any{"id": "{tx}"}, Roles: writers,
		},
		contract.Case{
			Name: "advanced-write-again", Method: "PUT", Path: secretFile,
			Body:    map[string]any{"content": "contract-throwaway-file, second\n"},
			Capture: map[string]string{"tx2": "data.restartRequired.id"},
			Roles:   writers,
		},
		contract.Case{
			Name: "advanced-cancel-again", Method: "POST", Path: "/api/settings/restart/cancel",
			Body: map[string]any{"id": "{tx2}"}, Roles: writers,
		},
		contract.Case{Name: "advanced-read-after-cancel", Path: secretFile, Roles: writers},
		contract.Case{Name: "advanced-files-after", Path: "/api/settings/advanced/files", Roles: writers},
		contract.Case{Name: "restart-pending-at-the-end", Path: "/api/settings/restart/pending", Roles: writers},
		// The caller has no transaction now: the two restart routes refuse
		contract.Case{
			Name: "advanced-restart-nothing-pending", Method: "POST", Path: "/api/settings/advanced/restart",
			Body: map[string]any{"fileId": "console-env"}, Roles: readers,
		},
		contract.Case{
			Name: "connection-mode-restart-nothing-pending", Method: "POST", Path: "/api/settings/connection-mode/restart",
			Body: map[string]any{}, Roles: contract.Roles("signed-out", "operator", "server_admin", "super_admin", "admin"),
		},

		// Connection mode: read; saved only by roles that are refused
		contract.Case{Name: "connection-mode", Path: "/api/settings/connection-mode", Roles: readers},
		contract.Case{
			Name: "connection-mode-save-refused", Method: "PUT", Path: "/api/settings/connection-mode",
			Body: map[string]any{"mode": "relay_only"}, Roles: refused,
		},

		// Public endpoints: read; saved only with values the validation
		// refuses
		contract.Case{Name: "public-endpoints", Path: "/api/settings/public-endpoints", Roles: readers},
		contract.Case{
			Name: "public-endpoints-invalid-host", Method: "PUT", Path: "/api/settings/public-endpoints",
			Body: map[string]any{"public_server_id": "bad host!"}, Roles: contract.Roles("signed-out", "operator", "server_admin", "admin"),
		},
		contract.Case{
			Name: "public-endpoints-empty", Method: "PUT", Path: "/api/settings/public-endpoints",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "public-endpoints-line-break", Method: "PUT", Path: "/api/settings/public-endpoints",
			Body: map[string]any{"public_relay_server": "relay.example.org\nX=1"}, Roles: admin,
		},
		contract.Case{
			Name: "public-endpoints-invalid-api-url", Method: "PUT", Path: "/api/settings/public-endpoints",
			Body: map[string]any{"public_server_id": "id.example.org", "public_api_url": "ftp://api.example.org"}, Roles: admin,
		},
		contract.Case{
			Name: "public-endpoints-api-url-without-scheme", Method: "PUT", Path: "/api/settings/public-endpoints",
			Body:    map[string]any{"public_api_url": "api.example.org:21114"},
			Headers: map[string]string{"Accept-Language": "de"}, Roles: admin,
		},
	)
}
