//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The update and management routes of the settings: the update check,
// the install, the update backups, the update channel, and the report and
// configuration of the install.
//
// No case installs, restores or rebuilds anything, and none reaches
// GitHub: the routes that would (install with a valid commit, restore of
// a backup, rebuild, install-go, check, preflight, changes with a valid
// commit, last-result) are sent by roles the guards turn away or with
// values the validation refuses. The real work is covered by the unit
// tests of console/updater and console/routes/settingsupdates against a
// fake release server, and end to end on an instance with a fake GitHub.
// A test instance has no update backups, so the list is empty and the
// deletes find nothing.
//
// The update channel is switched and switched back; the file .env of
// web-nodejs keeps UPDATE_GITHUB_BRANCH=main afterwards, which is the
// default.
//
// Paths marked "A29" are fields the Go console fills differently on
// purpose (DECISIONS A29, spec updater-installers.md 4.6): updates install
// release binaries, so the Go toolchain facts of server-info describe no
// toolchain; the install has one service, a restart is the process's exit
// under a supervisor, and an update needs the binary's folder to be
// writable instead of Node and npm.
func init() {
	few := contract.Roles("signed-out", "operator", "admin")
	refused := contract.Roles("signed-out", "operator", "global_admin")
	writers := contract.Roles("server_admin", "super_admin", "admin")
	admin := contract.Roles("admin")

	// A29: the report of management/capabilities as the Go console makes it.
	capabilities := func(prefix string) []string {
		return []string{
			prefix + "services",                             // A29: one service instead of the console and the server
			prefix + "groups.update",                        // A29: the update check is the binary's folder (method release)
			"~" + prefix + "groups.restart.checks[0].error", // A29: a restart needs BETTERDESK_SUPERVISOR
			"~" + prefix + "checks[7].error",                // A29: the restart check
			prefix + "checks[9]",                            // A29: the update check
		}
	}

	contract.Group("settings-updates",
		contract.Case{
			Name: "server-info", Path: "/api/settings/updates/server-info", Roles: few,
			// A29: updates install release binaries; no Go toolchain is
			// looked for, found or installed.
			Volatile: []string{
				"~data.goVersion", "~data.goVersionNumber", "~data.goPath", "~data.goMinimumVersion",
				"data.canInstallGo", "data.sourcePresent", "~data.buildMethod",
			},
		},
		contract.Case{Name: "install-go-refused", Method: "POST", Path: "/api/settings/updates/install-go", Body: map[string]any{}, Roles: refused},
		contract.Case{Name: "preflight-refused", Path: "/api/settings/updates/preflight", Query: url.Values{"serverUpdate": {"1"}, "sha": {"abc1234"}}, Roles: refused},

		contract.Case{
			Name: "capabilities", Path: "/api/settings/management/capabilities", Roles: few,
			Volatile: capabilities("data."),
		},
		// The keys are every key the template .env.example names, which
		// changes with the release (upstream added eight in 3.5.264); the
		// managedconfig test pins the list against the template.
		contract.Case{Name: "config", Path: "/api/settings/management/config", Roles: few, Volatile: []string{"~data.keys"}},
		contract.Case{Name: "config-save-without-changes", Method: "PUT", Path: "/api/settings/management/config", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "config-save-a-list", Method: "PUT", Path: "/api/settings/management/config", Body: map[string]any{"changes": []any{"PORT"}}, Roles: admin},
		contract.Case{Name: "config-save-nothing", Method: "PUT", Path: "/api/settings/management/config", Body: map[string]any{"changes": map[string]any{}}, Roles: admin},
		contract.Case{
			Name: "config-save-unknown-key", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"NOT_A_SETTING": "1"}}, Roles: writers,
		},
		contract.Case{
			Name: "config-save-bad-port", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"PORT": "70000"}}, Roles: admin,
		},
		contract.Case{
			Name: "config-save-newline", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"LOG_LEVEL": "info\nPORT=1"}}, Roles: admin,
		},
		contract.Case{
			Name: "config-save-path-outside", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"DB_PATH": "/etc/passwd"}}, Roles: admin,
		},
		contract.Case{
			Name: "config-save-bad-value", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"LOG_LEVEL": "loud"}}, Roles: admin,
		},
		contract.Case{
			Name: "config-dry-run", Method: "PUT", Path: "/api/settings/management/config",
			Body:     map[string]any{"changes": map[string]any{"SESSION_SECRET": "not-saved", "PORT": 5001}, "dryRun": true},
			Roles:    contract.Roles("operator", "admin"),
			Volatile: capabilities("data.capabilities."),
		},
		contract.Case{
			Name: "config-dry-run-as-text", Method: "PUT", Path: "/api/settings/management/config",
			Body:     map[string]any{"changes": map[string]any{"LOG_LEVEL": "debug"}, "dryRun": "true"},
			Roles:    admin,
			Volatile: capabilities("data.capabilities."),
		},
		contract.Case{
			Name: "config-save-refused", Method: "PUT", Path: "/api/settings/management/config",
			Body: map[string]any{"changes": map[string]any{"LOG_LEVEL": "debug"}}, Roles: refused,
		},

		contract.Case{Name: "channel", Path: "/api/settings/updates/channel", Roles: few},
		contract.Case{Name: "channel-missing", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "channel-unknown", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": "nightly"}, Roles: admin},
		contract.Case{Name: "channel-number", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": 1}, Roles: admin},
		contract.Case{Name: "channel-development", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": "development"}, Roles: writers},
		contract.Case{Name: "channel-after-the-switch", Path: "/api/settings/updates/channel", Roles: admin},
		// The old route looked the name up in a plain object, where
		// "toString" exists too (BUGS UPD2).
		contract.Case{Name: "channel-prototype-name", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": "toString"}, Roles: admin},
		contract.Case{Name: "channel-list-of-one", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": []any{"development"}}, Roles: admin},
		contract.Case{Name: "channel-back-to-stable", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": "stable"}, Roles: writers},
		contract.Case{Name: "channel-refused", Method: "POST", Path: "/api/settings/updates/channel", Body: map[string]any{"channel": "development"}, Roles: refused},

		contract.Case{Name: "check-refused", Path: "/api/settings/updates/check", Roles: refused},
		contract.Case{Name: "last-result-refused", Path: "/api/settings/updates/last-result", Roles: refused},
		contract.Case{Name: "install-status", Path: "/api/settings/updates/install-status", Roles: few},
		contract.Case{Name: "changes-without-sha", Path: "/api/settings/updates/changes", Roles: few},
		contract.Case{Name: "changes-bad-sha", Path: "/api/settings/updates/changes", Query: url.Values{"sha": {"not-a-sha"}}, Roles: admin},
		contract.Case{Name: "changes-short-sha", Path: "/api/settings/updates/changes", Query: url.Values{"sha": {"abc12"}}, Roles: admin},

		contract.Case{Name: "install-without-sha", Method: "POST", Path: "/api/settings/updates/install", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "install-bad-sha", Method: "POST", Path: "/api/settings/updates/install", Body: map[string]any{"remoteSHA": "main"}, Roles: admin},
		contract.Case{Name: "install-sha-number", Method: "POST", Path: "/api/settings/updates/install", Body: map[string]any{"remoteSHA": 1234567}, Roles: admin},
		contract.Case{Name: "install-refused", Method: "POST", Path: "/api/settings/updates/install", Body: map[string]any{"remoteSHA": "abc1234"}, Roles: refused},

		contract.Case{Name: "binary-status", Path: "/api/settings/updates/server-binary/status", Roles: few},
		contract.Case{Name: "rebuild-refused", Method: "POST", Path: "/api/settings/updates/server-binary/rebuild", Body: map[string]any{}, Roles: refused},

		contract.Case{Name: "backups", Path: "/api/settings/updates/backups", Roles: few},
		contract.Case{Name: "restore-without-name", Method: "POST", Path: "/api/settings/updates/restore", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "restore-name-not-text", Method: "POST", Path: "/api/settings/updates/restore", Body: map[string]any{"backupName": 7}, Roles: admin},
		contract.Case{Name: "restore-invalid-name", Method: "POST", Path: "/api/settings/updates/restore", Body: map[string]any{"backupName": "../../etc"}, Roles: admin},
		contract.Case{Name: "restore-missing-backup", Method: "POST", Path: "/api/settings/updates/restore", Body: map[string]any{"backupName": "pre-update-2001-01-01T00-00-00"}, Roles: admin},
		contract.Case{Name: "restore-refused", Method: "POST", Path: "/api/settings/updates/restore", Body: map[string]any{"backupName": "pre-update-2001-01-01T00-00-00"}, Roles: refused},
		contract.Case{Name: "delete-invalid-name", Method: "DELETE", Path: "/api/settings/updates/backups/pre-update-x", Roles: writers},
		contract.Case{Name: "delete-missing-backup", Method: "DELETE", Path: "/api/settings/updates/backups/pre-update-2001-01-01T00-00-00", Roles: few},
		contract.Case{Name: "prune-without-keep", Method: "POST", Path: "/api/settings/updates/backups/prune", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "prune-zero", Method: "POST", Path: "/api/settings/updates/backups/prune", Body: map[string]any{"keep": 0}, Roles: admin},
		contract.Case{Name: "prune-text", Method: "POST", Path: "/api/settings/updates/backups/prune", Body: map[string]any{"keep": "3 backups"}, Roles: admin},
		contract.Case{Name: "prune", Method: "POST", Path: "/api/settings/updates/backups/prune", Body: map[string]any{"keep": 5}, Roles: few},
	)
}
