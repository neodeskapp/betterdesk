//go:build contract

package contract_test

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The SMTP settings under /api/settings/email: the same handlers as the
// SMTP routes of the automation group. Every route needs server.config
// (server_admin, super_admin, admin).
//
// Saved SMTP settings cannot be removed. Each turn starts by saving what
// the automation cases leave behind and ends by saving it again, so every
// role sees the same settings and the instance ends as those cases leave
// it. The settings point at 127.0.0.1 port 1, where nothing listens, so
// the test button fails at once and no mail can leave.
func init() {
	few := contract.Roles("signed-out", "viewer", "global_admin", "admin")
	writers := contract.Roles("operator", "server_admin", "super_admin", "admin")
	baseline := map[string]any{
		"host": "127.0.0.1", "port": 1, "secure": false, "user": "contract", "pass": "contract-smtp-pass",
		"from": "console@example.com", "alert_email": "ops@example.com", "tlsVerify": false,
	}
	const smtp = "/api/settings/email/smtp"

	contract.Group("settings-email",
		contract.Case{Name: "setup", Method: "PUT", Path: smtp, Body: baseline, As: "admin"},
		contract.Case{Name: "read", Path: smtp},
		contract.Case{Name: "read-head", Method: "HEAD", Path: smtp, Roles: few},
		contract.Case{Name: "read-other-spelling", Path: "/API/Settings/Email/SMTP/", Roles: few},
		contract.Case{Name: "read-not-modified", Path: smtp, Headers: map[string]string{"If-None-Match": "*"}, Roles: few},

		// Saves that are refused
		contract.Case{Name: "save-without-host", Method: "PUT", Path: smtp, Body: map[string]any{"port": 1, "user": "contract"}},
		contract.Case{Name: "save-empty-host", Method: "PUT", Path: smtp, Body: map[string]any{"host": "", "port": 1}, Roles: writers},
		contract.Case{Name: "save-host-zero", Method: "PUT", Path: smtp, Body: map[string]any{"host": 0}, Roles: writers},
		contract.Case{Name: "save-host-false", Method: "PUT", Path: smtp, Body: map[string]any{"host": false}, Roles: writers},
		contract.Case{
			Name: "save-list-body", Method: "PUT", Path: smtp,
			RawBody: `[{"host":"127.0.0.1","port":1}]`, ContentType: "application/json", Roles: writers,
		},
		contract.Case{
			Name: "save-as-form", Method: "PUT", Path: smtp,
			RawBody: "host=127.0.0.1&port=1", ContentType: "application/x-www-form-urlencoded", Roles: writers,
		},
		contract.Case{
			Name: "save-malformed-json", Method: "PUT", Path: smtp,
			RawBody: `{"host":`, ContentType: "application/json", Roles: writers,
		},
		contract.Case{Name: "save-without-csrf-token", Method: "PUT", Path: smtp, Body: baseline, NoCSRF: true, Roles: writers},
		contract.Case{Name: "read-after-refused-saves", Path: smtp, Roles: writers},

		// Saves that are taken. An empty or masked password keeps the
		// stored one.
		contract.Case{Name: "save-host-only", Method: "PUT", Path: smtp, Body: map[string]any{"host": "127.0.0.1", "pass": ""}, Roles: writers},
		contract.Case{Name: "read-host-only", Path: smtp, Roles: writers},
		contract.Case{
			Name: "save-other-types", Method: "PUT", Path: smtp,
			Body: map[string]any{
				"host": []any{"127.0.0.1"}, "port": "1abc", "secure": "false", "user": 5, "pass": "********",
				"from": 7, "alert_email": 8, "tlsVerify": nil,
			},
			Roles: writers,
		},
		contract.Case{Name: "read-other-types", Path: smtp, Roles: writers},
		contract.Case{
			Name: "save-spaces-and-fractions", Method: "PUT", Path: smtp,
			Body: map[string]any{
				"host": "  127.0.0.1  ", "port": 25.9, "secure": 1, "user": "", "pass": "new-contract-pass",
				"from": "", "alert_email": "  ops@example.com  ", "tlsVerify": "yes",
			},
			Roles: writers,
		},
		contract.Case{Name: "read-spaces-and-fractions", Path: smtp, Roles: writers},

		// The connection test, against the baseline
		contract.Case{Name: "save-baseline", Method: "PUT", Path: smtp, Body: baseline, Roles: writers},
		contract.Case{Name: "test", Method: "POST", Path: smtp + "/test", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "test-without-body", Method: "POST", Path: smtp + "/test", Roles: writers},
		contract.Case{Name: "test-without-csrf-token", Method: "POST", Path: smtp + "/test", Body: map[string]any{}, NoCSRF: true, Roles: writers},

		contract.Case{Name: "cleanup", Method: "PUT", Path: smtp, Body: baseline, As: "admin"},
		contract.Case{Name: "read-at-the-end", Path: smtp, Roles: few},
	)
}
