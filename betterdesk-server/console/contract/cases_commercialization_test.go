//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Commercialization: the server's clock and its time sync, billing
// packages, contracts, sessions, work reports and their exports,
// currencies, the help request mail settings and the SMTP status of the
// billing page. Most routes pass the call on to the API server.
//
// Who gets through: the panel access guard keeps everyone without
// billing.view out of /api/panel/billing (server_admin too, although the
// clock routes would let server.config in), and writes there need
// billing.manage. So global_admin, super_admin and admin write, operator
// reads.
//
// What the cases leave out, and why:
//   - A clock save that the validation accepts rewrites web-nodejs/.env,
//     which every instance of a worktree shares and which a fresh instance
//     does not have; other groups' answers change with that file. Only
//     saves that are refused before anything is written are sent. The
//     accepted saves and their restart transaction are pinned by the Node
//     vectors of console/routes/commercialization and by the console's
//     own test of a cancelled change.
//   - POST /api/panel/billing/timesync/check makes the server ask the
//     public NTP servers: only roles that are turned away send it.
//   - A currency or a work report cannot be deleted once saved: only
//     requests the API server refuses are sent.
//
// Packages and contracts are created and deleted in each turn. The email
// settings are put back to their defaults at the end of a turn. The SMTP
// settings are set at the start of a turn to what the automation cases
// leave behind (they cannot be removed), so the SMTP status reads the same
// whether those cases ran before or not.
func init() {
	few := contract.Roles("signed-out", "viewer", "operator", "admin")
	// The roles the panel access guard lets write, and one it turns away
	writers := contract.Roles("operator", "global_admin", "super_admin", "admin")
	// The roles that pass the guard and may save the clock, and the two the
	// route itself turns away (global_admin has billing.manage but not
	// server.config)
	clock := contract.Roles("signed-out", "operator", "global_admin", "server_admin", "super_admin", "admin")
	admin := contract.Roles("admin")
	// The seeded data is dated from the time the instance started
	period := []string{"available_periods"}
	// The automation cases' last SMTP settings
	smtp := map[string]any{
		"host": "127.0.0.1", "port": 1, "secure": false, "user": "contract", "pass": "contract-smtp-pass",
		"from": "console@example.com", "alert_email": "ops@example.com", "tlsVerify": false,
	}
	clockSave := func(name string, body map[string]any) contract.Case {
		return contract.Case{Name: name, Method: "PUT", Path: "/api/panel/billing/clock/settings", Body: body, Roles: clock}
	}

	contract.Group("commercialization",
		// Setup, as admin in every role's turn: a seeded package that has
		// a contract
		contract.Case{
			Name: "setup-packages", Path: "/api/panel/billing/packages", As: "admin",
			Capture: map[string]string{"busy": "packages[0].id"},
		},

		// Time sync and the clock settings
		contract.Case{
			Name: "timesync-status", Path: "/api/panel/billing/timesync/status",
			// The measured offset and the server's stratum can agree in two
			// recordings
			Volatile: []string{"offset_ms", "stratum", "last_check_at"},
		},
		contract.Case{
			Name: "timesync-status-head", Method: "HEAD", Path: "/api/panel/billing/timesync/status", Roles: few,
		},
		contract.Case{
			Name: "timesync-check", Method: "POST", Path: "/api/panel/billing/timesync/check", Body: map[string]any{},
			Roles: contract.Roles("signed-out", "viewer", "operator", "global_admin", "server_admin"),
		},
		contract.Case{
			Name: "timesync-check-without-csrf-token", Method: "POST", Path: "/api/panel/billing/timesync/check",
			Body: map[string]any{}, NoCSRF: true, Roles: admin,
		},
		contract.Case{
			Name: "timesync-check-as-form", Method: "POST", Path: "/api/panel/billing/timesync/check",
			RawBody: "check=1", ContentType: "application/x-www-form-urlencoded", Roles: admin,
		},
		contract.Case{Name: "clock-settings", Path: "/api/panel/billing/clock/settings"},
		contract.Case{Name: "clock-settings-head", Method: "HEAD", Path: "/api/panel/billing/clock/settings", Roles: few},
		contract.Case{
			Name: "clock-settings-not-modified", Path: "/api/panel/billing/clock/settings",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: few,
		},
		contract.Case{Name: "clock-detect-timezone", Path: "/api/panel/billing/clock/detect-timezone"},

		// Clock saves the validation refuses: nothing is written
		clockSave("clock-save-invalid-servers", map[string]any{"ntp_servers": "bad host name"}),
		clockSave("clock-save-empty-servers", map[string]any{"ntp_servers": " , ,"}),
		clockSave("clock-save-server-too-long", map[string]any{"ntp_servers": strings.Repeat("a", 254)}),
		clockSave("clock-save-server-dash-first", map[string]any{"ntp_servers": "ntp.example.com,-bad.example"}),
		clockSave("clock-save-last-server-bad", map[string]any{"ntp_servers": "fe80::1%eth0, ::ffff:1.2.3.4 ,300.1.1.1,bad_host"}),
		clockSave("clock-save-servers-list", map[string]any{"ntp_servers": []any{"ntp.example.com", "bad host"}}),
		clockSave("clock-save-servers-object", map[string]any{"ntp_servers": map[string]any{"toString": "x"}}),
		clockSave("clock-save-skew-too-large", map[string]any{"max_skew_ms": 600001}),
		clockSave("clock-save-skew-text", map[string]any{"max_skew_ms": "700000ms"}),
		clockSave("clock-save-skew-many-digits", map[string]any{"max_skew_ms": "99999999999999999999"}),
		clockSave("clock-save-skew-object", map[string]any{"max_skew_ms": map[string]any{"toString": 1}}),
		clockSave("clock-save-zone-unknown", map[string]any{"billing_timezone": "Mars/Base"}),
		clockSave("clock-save-zone-too-long", map[string]any{"billing_timezone": "Europe/" + strings.Repeat("x", 60)}),
		clockSave("clock-save-zone-space-inside", map[string]any{"billing_timezone": "Europe/ Berlin"}),
		clockSave("clock-save-zone-gmt-offset", map[string]any{"billing_timezone": "GMT+1"}),
		clockSave("clock-save-zone-beyond-etc", map[string]any{"billing_timezone": "Etc/GMT+13"}),
		clockSave("clock-save-zone-list", map[string]any{"billing_timezone": []any{"Europe/Berlin", "UTC"}}),
		clockSave("clock-save-all-bad", map[string]any{
			"ntp_servers": "bad host", "max_skew_ms": 0.5, "require_synced_clock": "no", "billing_timezone": "Nowhere/Town",
		}),
		// A body with none of the settings would save the defaults
		clockSave("clock-save-empty", map[string]any{}),
		contract.Case{
			Name: "clock-save-without-csrf-token", Method: "PUT", Path: "/api/panel/billing/clock/settings",
			Body: map[string]any{"ntp_servers": "pool.ntp.org"}, NoCSRF: true, Roles: admin,
		},
		contract.Case{
			Name: "clock-save-as-form", Method: "PUT", Path: "/api/panel/billing/clock/settings",
			RawBody: "ntp_servers=pool.ntp.org", ContentType: "application/x-www-form-urlencoded", Roles: admin,
		},
		contract.Case{
			Name: "clock-save-malformed-json", Method: "PUT", Path: "/api/panel/billing/clock/settings",
			RawBody: `{"ntp_servers":`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{Name: "clock-settings-after-refused-saves", Path: "/api/panel/billing/clock/settings", Roles: few},

		// Packages
		contract.Case{Name: "packages", Path: "/api/panel/billing/packages"},
		contract.Case{Name: "packages-other-spelling", Path: "/API/Panel/Billing/Packages/", Roles: few},
		contract.Case{
			Name: "package-create", Method: "POST", Path: "/api/panel/billing/packages",
			Body: map[string]any{
				"name": "  Contract test package ", "description": "Created by the contract cases", "included_minutes": 90,
				"rounding_minutes": 15, "overage_rate": 12.345, "currency": "EUR",
			},
			Roles: writers, Capture: map[string]string{"package": "id"},
		},
		contract.Case{
			Name: "package-create-without-name", Method: "POST", Path: "/api/panel/billing/packages",
			Body: map[string]any{"description": "no name"}, Roles: writers,
		},
		contract.Case{
			Name: "package-create-blank-name", Method: "POST", Path: "/api/panel/billing/packages",
			Body: map[string]any{"name": "   "}, Roles: admin,
		},
		contract.Case{
			Name: "package-create-list-body", Method: "POST", Path: "/api/panel/billing/packages",
			RawBody: `[{"name":"listed"}]`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{Name: "packages-after-create", Path: "/api/panel/billing/packages", Roles: writers},
		contract.Case{
			Name: "package-preview", Path: "/api/panel/billing/packages/{package}/preview",
			Query: url.Values{"period": {"2026-03"}}, Roles: writers, Volatile: period,
		},
		contract.Case{
			Name: "package-preview-seeded", Path: "/api/panel/billing/packages/{busy}/preview",
			Query: url.Values{"period": {"2026-10"}}, Volatile: period,
		},
		contract.Case{
			Name: "package-preview-year-border", Path: "/api/panel/billing/packages/{busy}/preview",
			Query: url.Values{"period": {"2025-12"}}, Roles: few, Volatile: period,
		},
		contract.Case{
			Name: "package-preview-two-periods", Path: "/api/panel/billing/packages/{busy}/preview",
			Query: url.Values{"period": {"2026-03", "2026-04"}}, Roles: few, Volatile: period,
		},
		// The period written with brackets is left out (BUGS CM7); the
		// period beside it gives the answer a month that does not move.
		contract.Case{
			Name: "package-preview-period-object", Path: "/api/panel/billing/packages/{busy}/preview",
			Query: url.Values{"period[toString]": {"x"}, "period": {"2026-10"}}, Roles: few, Volatile: period,
		},
		contract.Case{
			Name: "package-preview-extra-query", Path: "/api/panel/billing/packages/{busy}/preview",
			Query: url.Values{"period": {"2026-02"}, "note": {"a b&c=d+e"}}, Roles: few, Volatile: period,
		},
		contract.Case{Name: "package-preview-unknown", Path: "/api/panel/billing/packages/no-such-package/preview", Roles: few},
		contract.Case{Name: "package-preview-bad-id", Path: "/api/panel/billing/packages/a%20b/preview", Roles: few},
		contract.Case{
			Name: "package-update", Method: "PUT", Path: "/api/panel/billing/packages/{package}",
			Body: map[string]any{
				"name": "Contract test package (renamed)", "included_minutes": 120, "rounding_minutes": 30,
				"overage_rate": 0.1, "currency": "USD",
			},
			Roles: writers,
		},
		contract.Case{
			Name: "package-update-list-body", Method: "PUT", Path: "/api/panel/billing/packages/{package}",
			RawBody: `[]`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{
			Name: "package-update-bad-id", Method: "PUT", Path: "/api/panel/billing/packages/a:b@c%2Bd%20e",
			Body: map[string]any{"name": "x"}, Roles: admin,
		},
		contract.Case{Name: "package-delete-in-use", Method: "DELETE", Path: "/api/panel/billing/packages/{busy}", Roles: writers},

		// Contracts, on a device that has none, with the package created above
		contract.Case{Name: "contracts", Path: "/api/panel/billing/contracts", Volatile: []string{"contracts[*].current_period_ym"}},
		contract.Case{
			Name: "contracts-active", Path: "/api/panel/billing/contracts", Query: url.Values{"status": {"active"}},
			Roles: few, Volatile: []string{"contracts[*].current_period_ym"},
		},
		contract.Case{
			Name: "contracts-org-target", Path: "/api/panel/billing/contracts", Query: url.Values{"target_type": {"org"}},
			Roles: few, Volatile: []string{"contracts[*].current_period_ym"},
		},
		contract.Case{
			Name: "contracts-two-statuses", Path: "/api/panel/billing/contracts", Query: url.Values{"status": {"active", "suspended"}},
			Roles: few,
		},
		contract.Case{
			Name: "contracts-query-without-text", Path: "/api/panel/billing/contracts", Query: url.Values{"status[toString]": {"x"}},
			Volatile: []string{"contracts[*].current_period_ym"},
			Roles: few,
		},
		contract.Case{
			Name: "contract-create", Method: "POST", Path: "/api/panel/billing/contracts",
			Body: map[string]any{
				"target_type": "device", "target_key": "900100010", "package_id": "{package}", "currency": "EUR",
				"hourly_rate": 19.99, "overage_rate": 2.5,
			},
			Roles: writers, Capture: map[string]string{"contract": "id"},
		},
		contract.Case{
			Name: "contract-create-again", Method: "POST", Path: "/api/panel/billing/contracts",
			Body: map[string]any{"target_type": "device", "target_key": "900100010", "package_id": "{package}"}, Roles: writers,
		},
		contract.Case{
			Name: "contract-create-folder", Method: "POST", Path: "/api/panel/billing/contracts",
			Body: map[string]any{"target_type": "folder", "target_key": "1", "package_id": "{package}"}, Roles: admin,
		},
		contract.Case{
			Name: "contract-create-without-package", Method: "POST", Path: "/api/panel/billing/contracts",
			Body: map[string]any{"target_type": "device", "target_key": "900100011"}, Roles: admin,
		},
		contract.Case{
			Name: "contract-create-without-target", Method: "POST", Path: "/api/panel/billing/contracts",
			Body: map[string]any{}, Roles: writers,
		},
		contract.Case{
			Name: "contract-update", Method: "PUT", Path: "/api/panel/billing/contracts/{contract}",
			Body: map[string]any{
				"status": "suspended", "remaining_minutes": 30, "overage_rate": 1.5, "hourly_rate": 0.333,
				"currency": "PLN", "valid_until": "2027-03-28T01:30:00Z",
			},
			Roles: writers,
		},
		contract.Case{
			Name: "contract-update-unknown", Method: "PUT", Path: "/api/panel/billing/contracts/no-such-contract",
			Body: map[string]any{"status": "active"}, Roles: writers,
		},
		contract.Case{
			Name: "contract-update-bad-id", Method: "PUT", Path: "/api/panel/billing/contracts/a%2Fb",
			Body: map[string]any{"status": "active"}, Roles: admin,
		},
		contract.Case{Name: "contract-delete", Method: "DELETE", Path: "/api/panel/billing/contracts/{contract}", Roles: writers},
		contract.Case{Name: "package-delete", Method: "DELETE", Path: "/api/panel/billing/packages/{package}", Roles: writers},
		contract.Case{Name: "package-delete-again", Method: "DELETE", Path: "/api/panel/billing/packages/{package}", Roles: admin},
		contract.Case{Name: "packages-after-delete", Path: "/api/panel/billing/packages", Roles: writers},

		// Stats, sessions and the connection check
		contract.Case{Name: "stats", Path: "/api/panel/billing/stats"},
		contract.Case{Name: "sessions", Path: "/api/panel/billing/sessions", Query: url.Values{"limit": {"3"}}},
		contract.Case{
			Name: "sessions-closed", Path: "/api/panel/billing/sessions",
			Query: url.Values{"status": {"closed"}, "limit": {"2"}}, Roles: few,
		},
		contract.Case{Name: "sessions-other-org", Path: "/api/panel/billing/sessions", Query: url.Values{"org_id": {"no-such-org"}}, Roles: few},
		contract.Case{
			Name: "sessions-query-without-text", Path: "/api/panel/billing/sessions", Query: url.Values{"limit[toString]": {"1"}},
			Roles: few,
		},
		contract.Case{
			Name: "check", Path: "/api/panel/billing/check", Query: url.Values{"device_id": {"900100001"}},
			Roles: contract.Roles("signed-out", "guest_issuer", "viewer", "operator", "global_admin", "server_admin", "admin"),
		},
		contract.Case{Name: "check-without-device", Path: "/api/panel/billing/check", Roles: few},
		contract.Case{Name: "check-unknown-device", Path: "/api/panel/billing/check", Query: url.Values{"device_id": {"no-such-device"}}, Roles: few},
		contract.Case{
			Name: "sessions-pending", Path: "/api/panel/billing/sessions/pending", Query: url.Values{"device_id": {"900100017"}},
		},
		contract.Case{
			Name: "sessions-pending-none", Path: "/api/panel/billing/sessions/pending", Query: url.Values{"device_id": {"900100003"}},
			Roles: few,
		},
		contract.Case{Name: "sessions-pending-without-device", Path: "/api/panel/billing/sessions/pending", Roles: few},

		// Exports. The file name holds the day of the API server's clock.
		contract.Case{
			Name: "sessions-export", Path: "/api/panel/billing/sessions/export", Query: url.Values{"format": {"csv"}},
			VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "sessions-export-other-org", Path: "/api/panel/billing/sessions/export",
			Query: url.Values{"format": {"csv"}, "org_id": {"no-such-org"}}, Roles: few, VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "sessions-export-pdf", Path: "/api/panel/billing/sessions/export", Query: url.Values{"format": {"pdf"}}, Roles: few,
		},
		contract.Case{Name: "sessions-export-without-format", Path: "/api/panel/billing/sessions/export", Roles: few},
		contract.Case{
			Name: "sessions-export-query-without-text", Path: "/api/panel/billing/sessions/export",
			Query: url.Values{"format[toString]": {"csv"}}, Roles: few,
		},
		contract.Case{Name: "reports", Path: "/api/panel/billing/reports", Query: url.Values{"limit": {"2"}}},
		contract.Case{Name: "reports-other-org", Path: "/api/panel/billing/reports", Query: url.Values{"org_id": {"no-such-org"}}, Roles: few},
		contract.Case{
			Name: "reports-export", Path: "/api/panel/billing/reports/export", Query: url.Values{"format": {"csv"}},
			VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "reports-export-default-format", Path: "/api/panel/billing/reports/export",
			Query: url.Values{"org_id": {"no-such-org"}}, Roles: few, VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "reports-export-pdf", Path: "/api/panel/billing/reports/export", Query: url.Values{"format": {"pdf"}},
			Roles: few, VolatileHeaders: []string{"content-disposition"},
		},
		contract.Case{
			Name: "reports-export-xml", Path: "/api/panel/billing/reports/export", Query: url.Values{"format": {"xml"}}, Roles: few,
		},

		// Work reports the API server refuses: nothing is written
		contract.Case{
			Name: "report-closed-session", Method: "POST", Path: "/api/panel/billing/sessions/demo-bs-001/report",
			Body: map[string]any{"summary": "Done", "category": "software"},
		},
		contract.Case{
			Name: "report-without-summary", Method: "POST", Path: "/api/panel/billing/sessions/demo-bs-009/report",
			Body: map[string]any{"summary": "   ", "ticket_ref": "#1"}, Roles: writers,
		},
		contract.Case{
			Name: "report-unknown-session", Method: "POST", Path: "/api/panel/billing/sessions/no-such-session/report",
			Body: map[string]any{"summary": "Done"}, Roles: few,
		},
		contract.Case{
			Name: "report-bad-id", Method: "POST", Path: "/api/panel/billing/sessions/a%2Fb/report",
			Body: map[string]any{"summary": "Done"}, Roles: admin,
		},
		contract.Case{
			Name: "report-without-csrf-token", Method: "POST", Path: "/api/panel/billing/sessions/demo-bs-009/report",
			Body: map[string]any{"summary": "Done"}, NoCSRF: true, Roles: admin,
		},

		// Currencies: only saves the API server refuses
		contract.Case{Name: "currencies", Path: "/api/panel/billing/currencies"},
		contract.Case{
			Name: "currency-list-body", Method: "PUT", Path: "/api/panel/billing/currencies/xts",
			RawBody: `[]`, ContentType: "application/json", Roles: writers,
		},
		contract.Case{
			Name: "currency-bad-code", Method: "PUT", Path: "/api/panel/billing/currencies/a%2Fb",
			Body: map[string]any{"code": "XTS"}, Roles: admin,
		},
		contract.Case{
			Name: "currency-without-csrf-token", Method: "PUT", Path: "/api/panel/billing/currencies/xts",
			Body: map[string]any{"name": "Test"}, NoCSRF: true, Roles: admin,
		},

		// Help request mail settings
		contract.Case{Name: "email-config", Path: "/api/panel/commercialization/email-config"},
		contract.Case{Name: "email-config-head", Method: "HEAD", Path: "/api/panel/commercialization/email-config", Roles: few},
		contract.Case{
			Name: "email-config-save", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			Body: map[string]any{
				"help_requests_enabled": false, "notify_assigned_operators": 0, "fallback_alert_email": nil,
				"include_folder_in_subject": "no", "other": true,
			},
			Roles: writers,
		},
		contract.Case{Name: "email-config-after-save", Path: "/api/panel/commercialization/email-config", Roles: writers},
		contract.Case{
			Name: "email-config-save-all-off", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			Body: map[string]any{
				"help_requests_enabled": false, "notify_assigned_operators": false, "fallback_alert_email": false,
				"include_folder_in_subject": false,
			},
			Roles: writers,
		},
		contract.Case{Name: "email-config-after-all-off", Path: "/api/panel/commercialization/email-config", Roles: writers},
		contract.Case{
			Name: "email-config-save-list-body", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			RawBody: `[false,false]`, ContentType: "application/json", Roles: admin,
		},
		contract.Case{
			Name: "email-config-without-csrf-token", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			Body: map[string]any{"help_requests_enabled": false}, NoCSRF: true, Roles: admin,
		},
		contract.Case{
			Name: "email-config-as-form", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			RawBody: "help_requests_enabled=false", ContentType: "application/x-www-form-urlencoded", Roles: admin,
		},
		contract.Case{
			Name: "email-config-save-empty", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			Body: map[string]any{}, Roles: admin,
		},
		contract.Case{
			Name: "cleanup-email-config", Method: "PUT", Path: "/api/panel/commercialization/email-config",
			Body: map[string]any{
				"help_requests_enabled": true, "notify_assigned_operators": true, "fallback_alert_email": true,
				"include_folder_in_subject": true,
			},
			As: "admin",
		},
		contract.Case{Name: "email-config-after-cleanup", Path: "/api/panel/commercialization/email-config", Roles: few},

		// The SMTP status of the billing page
		contract.Case{Name: "setup-smtp", Method: "PUT", Path: "/api/settings/email/smtp", Body: smtp, As: "admin"},
		contract.Case{Name: "smtp-status", Path: "/api/panel/commercialization/smtp-status"},
		contract.Case{Name: "smtp-status-head", Method: "HEAD", Path: "/api/panel/commercialization/smtp-status", Roles: few},
	)
}
