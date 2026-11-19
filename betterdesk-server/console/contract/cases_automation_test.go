//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Automation: alert rules, the alert history, remote commands and the
// SMTP settings under /api/automation. Every route needs the permission
// server.config (server_admin, super_admin, admin). The routes for device
// agents (GET /api/bd/commands and the two result routes) take a device
// token; they are the group automation-device below, which runs after
// this one, so the commands it completes do not reach the lists here.
//
// A role's turn creates five rules, works on the first one and deletes all
// five. The rule list is read while it holds one row: rules written in the
// same second have no order to rely on.
//
// Nothing writes alerts on a test instance, so the alert history stays
// empty here. Remote commands cannot be deleted: every turn of a role that
// may send them leaves one (the only command for a seeded device), and
// the command lists mark what depends on the turns before.
//
// The SMTP cases come last, because saved SMTP settings cannot be removed
// either. They point at 127.0.0.1 port 1, where nothing listens, so the
// test button fails at once and no mail can leave. The first role that may
// save (server_admin) still sees an instance without settings; the turns
// after it see what the turn before left.
func init() {
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "global_admin", "admin")
	// The newest command of device 900100001: the turns of two roles can
	// fall into one second, and then either one's command is the newest
	newest := []string{"commands[*].id", "commands[*].created_by", "commands[*].created_at"}
	smtp := map[string]any{
		"host": " 127.0.0.1 ", "port": "1", "secure": false, "user": "contract", "pass": "contract-smtp-pass",
		"from": "console@example.com", "alert_email": " ops@example.com ", "tlsVerify": false,
	}

	contract.Group("automation",
		// Alert rules
		contract.Case{Name: "rules-empty", Path: "/api/automation/rules"},
		contract.Case{Name: "rules-head", Method: "HEAD", Path: "/api/automation/rules"},
		contract.Case{
			Name: "rule-create", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{
				"name": "  CPU high ", "description": "CPU over 90 percent", "condition_type": "cpu_usage", "condition_op": "gte",
				"condition_value": 90, "severity": "critical", "scope_device_id": "900100001", "cooldown_secs": 600,
				"notify_emails": "ops@example.com", "enabled": false,
			},
			Capture: map[string]string{"rule": "rule.id"},
		},
		contract.Case{Name: "rules-one", Path: "/api/automation/rules"},
		contract.Case{Name: "rule-read", Path: "/api/automation/rules/{rule}"},
		contract.Case{Name: "rule-read-head", Method: "HEAD", Path: "/api/automation/rules/{rule}", Roles: few},
		contract.Case{Name: "rule-read-leading-zeros", Path: "/api/automation/rules/00{rule}"},
		contract.Case{Name: "rule-read-other-spelling", Path: "/API/Automation/Rules/{rule}/", Roles: few},
		contract.Case{Name: "rule-read-missing", Path: "/api/automation/rules/999999999"},
		contract.Case{Name: "rule-read-id-too-long", Path: "/api/automation/rules/99999999999999999999"},
		contract.Case{Name: "rule-read-id-not-digits", Path: "/api/automation/rules/abc", Roles: few},
		contract.Case{Name: "rule-read-id-with-fraction", Path: "/api/automation/rules/{rule}.0", Roles: few},
		contract.Case{
			Name: "rule-create-with-defaults", Method: "POST", Path: "/api/automation/rules",
			Body:    map[string]any{"name": "Defaults", "condition_type": "custom"},
			Capture: map[string]string{"second": "rule.id"},
		},
		contract.Case{Name: "rule-read-defaults", Path: "/api/automation/rules/{second}"},
		contract.Case{
			Name: "rule-create-other-types", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{
				"name": "Types", "description": 5, "condition_type": "disk_usage", "condition_op": "", "condition_value": "12.5",
				"severity": "fatal", "scope_device_id": 7, "cooldown_secs": "900", "notify_emails": 0, "enabled": "false",
			},
			Capture: map[string]string{"third": "rule.id"},
		},
		contract.Case{Name: "rule-read-other-types", Path: "/api/automation/rules/{third}"},
		contract.Case{
			Name: "rule-create-value-not-a-number", Method: "POST", Path: "/api/automation/rules",
			Body:    map[string]any{"name": "Text value", "condition_type": "memory_usage", "condition_value": "high", "cooldown_secs": 1.5, "enabled": 0},
			Capture: map[string]string{"fourth": "rule.id"},
		},
		contract.Case{Name: "rule-read-value-not-a-number", Path: "/api/automation/rules/{fourth}"},
		contract.Case{
			Name: "rule-create-text-in-a-list", Method: "POST", Path: "/api/automation/rules",
			Body:    map[string]any{"name": "List", "condition_type": "offline_duration", "description": []string{"from a list"}, "enabled": "0"},
			Capture: map[string]string{"fifth": "rule.id"},
		},
		contract.Case{Name: "rule-read-text-in-a-list", Path: "/api/automation/rules/{fifth}"},
		contract.Case{
			Name: "rule-create-two-values-in-a-list", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Two", "condition_type": "custom", "description": []string{"a", "b"}},
		},
		contract.Case{
			Name: "rule-create-flag-in-a-text-field", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Flag", "condition_type": "custom", "description": true},
		},
		contract.Case{
			Name: "rule-create-object-in-a-text-field", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Object", "condition_type": "custom", "notify_emails": map[string]any{"to": "ops@example.com"}},
		},
		contract.Case{
			Name: "rule-create-without-name", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"condition_type": "custom"},
		},
		contract.Case{
			Name: "rule-create-blank-name", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": " \t\u00a0 ", "condition_type": "custom"},
		},
		contract.Case{
			Name: "rule-create-name-number", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": 5, "condition_type": "custom"},
		},
		contract.Case{
			Name: "rule-create-name-in-a-list", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": []string{"List"}, "condition_type": "custom"},
		},
		contract.Case{
			Name: "rule-create-without-type", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "No type"},
		},
		contract.Case{
			Name: "rule-create-unknown-type", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Unknown type", "condition_type": "CPU_USAGE"},
		},
		contract.Case{
			Name: "rule-create-type-in-a-list", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Type list", "condition_type": []string{"custom"}},
		},
		contract.Case{
			Name: "rule-create-unknown-op", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "Unknown op", "condition_type": "custom", "condition_op": ">"},
		},
		contract.Case{
			Name: "rule-create-array-body", Method: "POST", Path: "/api/automation/rules",
			RawBody: `[{"name":"In a list","condition_type":"custom"}]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "rule-create-as-form", Method: "POST", Path: "/api/automation/rules",
			RawBody: "name=Form+rule&condition_type=custom", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "rule-create-without-csrf-token", Method: "POST", Path: "/api/automation/rules",
			Body: map[string]any{"name": "No token", "condition_type": "custom"}, NoCSRF: true,
		},

		contract.Case{
			Name: "rule-update", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{
				"name": "  CPU very high ", "description": "", "enabled": true, "condition_type": "memory_usage", "condition_op": "lt",
				"condition_value": 12.5, "severity": "info", "scope_device_id": nil, "cooldown_secs": 60, "notify_emails": "a@example.com,b@example.com",
			},
		},
		contract.Case{Name: "rule-read-after-update", Path: "/api/automation/rules/{rule}"},
		contract.Case{
			Name: "rule-update-nothing", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"unknown": "field"},
		},
		contract.Case{
			Name: "rule-update-enabled-by-truth", Method: "PATCH", Path: "/api/automation/rules/{second}",
			Body: map[string]any{"enabled": "false", "severity": "anything goes", "cooldown_secs": "120", "condition_value": "7"},
		},
		contract.Case{Name: "rule-read-enabled-by-truth", Path: "/api/automation/rules/{second}"},
		contract.Case{
			Name: "rule-update-disable", Method: "PATCH", Path: "/api/automation/rules/{second}",
			Body: map[string]any{"enabled": 0},
		},
		contract.Case{
			Name: "rule-update-name-null", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"name": nil},
		},
		contract.Case{
			Name: "rule-update-name-number", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"name": 5, "condition_type": "nonsense"},
		},
		contract.Case{
			Name: "rule-update-unknown-type", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"description": "not written", "condition_type": "nonsense"},
		},
		contract.Case{
			Name: "rule-update-type-null", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"condition_type": nil},
		},
		contract.Case{
			Name: "rule-update-unknown-op", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"condition_op": ""},
		},
		contract.Case{
			Name: "rule-update-value-null", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"condition_value": nil},
		},
		contract.Case{
			Name: "rule-update-flag-in-a-text-field", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			Body: map[string]any{"description": true},
		},
		contract.Case{
			Name: "rule-update-missing", Method: "PATCH", Path: "/api/automation/rules/999999999",
			Body: map[string]any{"name": "Nobody"},
		},
		contract.Case{
			Name: "rule-update-missing-name-number", Method: "PATCH", Path: "/api/automation/rules/999999999",
			Body: map[string]any{"name": 5},
		},
		contract.Case{
			Name: "rule-update-id-not-digits", Method: "PATCH", Path: "/api/automation/rules/abc",
			Body: map[string]any{"name": "Nobody"}, Roles: few,
		},
		contract.Case{
			Name: "rule-update-as-text", Method: "PATCH", Path: "/api/automation/rules/{rule}",
			RawBody: `{"name":"As text"}`, ContentType: "text/plain",
		},
		contract.Case{Name: "rule-read-after-updates", Path: "/api/automation/rules/{rule}"},

		contract.Case{Name: "rule-delete-without-csrf-token", Method: "DELETE", Path: "/api/automation/rules/{rule}", NoCSRF: true},
		contract.Case{Name: "rule-delete-second", Method: "DELETE", Path: "/api/automation/rules/{second}"},
		contract.Case{Name: "rule-delete-third", Method: "DELETE", Path: "/api/automation/rules/{third}"},
		contract.Case{Name: "rule-delete-fourth", Method: "DELETE", Path: "/api/automation/rules/{fourth}"},
		contract.Case{Name: "rule-delete-fifth", Method: "DELETE", Path: "/api/automation/rules/{fifth}"},
		contract.Case{Name: "rules-one-after-updates", Path: "/api/automation/rules"},
		contract.Case{Name: "rule-delete", Method: "DELETE", Path: "/api/automation/rules/{rule}"},
		contract.Case{Name: "rule-delete-again", Method: "DELETE", Path: "/api/automation/rules/{rule}"},
		contract.Case{Name: "rule-delete-id-not-digits", Method: "DELETE", Path: "/api/automation/rules/abc", Roles: few},
		contract.Case{Name: "rule-read-after-delete", Path: "/api/automation/rules/{rule}"},
		contract.Case{Name: "rules-after-deletes", Path: "/api/automation/rules"},

		// Alert history
		contract.Case{Name: "alerts-empty", Path: "/api/automation/alerts"},
		contract.Case{Name: "alerts-head", Method: "HEAD", Path: "/api/automation/alerts", Roles: few},
		contract.Case{Name: "alerts-other-spelling", Path: "/API/AUTOMATION/Alerts/", Roles: few},
		contract.Case{
			Name: "alerts-filtered", Path: "/api/automation/alerts",
			Query: url.Values{"device_id": {"900100001"}, "severity": {"critical"}, "acknowledged": {"true"}, "limit": {"5"}},
		},
		contract.Case{Name: "alerts-not-acknowledged", Path: "/api/automation/alerts", Query: url.Values{"acknowledged": {"false"}}},
		contract.Case{Name: "alerts-acknowledged-other-word", Path: "/api/automation/alerts", Query: url.Values{"acknowledged": {"yes"}}},
		contract.Case{Name: "alerts-acknowledged-twice", Path: "/api/automation/alerts", Query: url.Values{"acknowledged": {"true", "true"}}},
		contract.Case{Name: "alerts-limit-with-text", Path: "/api/automation/alerts", Query: url.Values{"limit": {" 3 rows"}}},
		contract.Case{Name: "alerts-limit-not-a-number", Path: "/api/automation/alerts", Query: url.Values{"limit": {"abc"}}},
		contract.Case{Name: "alerts-limit-zero", Path: "/api/automation/alerts", Query: url.Values{"limit": {"0"}}},
		contract.Case{Name: "alerts-limit-negative", Path: "/api/automation/alerts", Query: url.Values{"limit": {"-1"}}},
		contract.Case{Name: "alerts-limit-too-large", Path: "/api/automation/alerts", Query: url.Values{"limit": {"99999999999999999999"}}},
		contract.Case{Name: "alerts-limit-twice", Path: "/api/automation/alerts", Query: url.Values{"limit": {"1", "2"}}},
		contract.Case{Name: "alerts-limit-object", Path: "/api/automation/alerts", Query: url.Values{"limit[rows]": {"1"}}},
		contract.Case{Name: "alerts-device-twice", Path: "/api/automation/alerts", Query: url.Values{"device_id": {"900100001", "900100002"}}},
		contract.Case{Name: "alerts-device-in-brackets", Path: "/api/automation/alerts", Query: url.Values{"device_id[]": {"900100001"}}},
		contract.Case{Name: "alerts-device-object", Path: "/api/automation/alerts", Query: url.Values{"device_id[id]": {"900100001"}}},
		contract.Case{Name: "alerts-severity-empty", Path: "/api/automation/alerts", Query: url.Values{"severity": {""}, "device_id": {""}}},
		contract.Case{Name: "alert-ack-missing", Method: "POST", Path: "/api/automation/alerts/999999999/ack", Body: map[string]any{}},
		contract.Case{Name: "alert-ack-without-body", Method: "POST", Path: "/api/automation/alerts/1/ack"},
		contract.Case{Name: "alert-ack-id-too-long", Method: "POST", Path: "/api/automation/alerts/99999999999999999999/ack", Body: map[string]any{}},
		contract.Case{Name: "alert-ack-id-not-digits", Method: "POST", Path: "/api/automation/alerts/abc/ack", Body: map[string]any{}, Roles: few},
		contract.Case{Name: "alert-ack-without-csrf-token", Method: "POST", Path: "/api/automation/alerts/1/ack", Body: map[string]any{}, NoCSRF: true},

		// Remote commands
		contract.Case{
			Name: "command-create", Method: "POST", Path: "/api/automation/commands",
			Body:    map[string]any{"device_id": "900100001", "command_type": "powershell", "payload": "  Get-Process  "},
			Capture: map[string]string{"command": "command.id"},
		},
		contract.Case{Name: "command-read", Path: "/api/automation/commands/{command}"},
		contract.Case{Name: "command-read-head", Method: "HEAD", Path: "/api/automation/commands/{command}", Roles: few},
		contract.Case{Name: "command-read-leading-zeros", Path: "/api/automation/commands/0{command}", Roles: few},
		contract.Case{Name: "command-read-other-spelling", Path: "/API/Automation/Commands/{command}/", Roles: few},
		contract.Case{Name: "command-read-missing", Path: "/api/automation/commands/999999999"},
		contract.Case{Name: "command-read-id-too-long", Path: "/api/automation/commands/99999999999999999999"},
		contract.Case{Name: "command-read-id-not-digits", Path: "/api/automation/commands/abc", Roles: few},
		contract.Case{
			Name: "command-create-with-defaults", Method: "POST", Path: "/api/automation/commands",
			Body:    map[string]any{"device_id": "contract-defaults", "payload": "uptime"},
			Capture: map[string]string{"second_command": "command.id"},
		},
		contract.Case{Name: "command-read-defaults", Path: "/api/automation/commands/{second_command}"},
		contract.Case{
			Name: "command-create-device-number", Method: "POST", Path: "/api/automation/commands",
			Body:    map[string]any{"device_id": 7, "command_type": "", "payload": "hostname"},
			Capture: map[string]string{"third_command": "command.id"},
		},
		contract.Case{Name: "command-read-device-number", Path: "/api/automation/commands/{third_command}"},
		contract.Case{
			Name: "command-create-device-in-a-list", Method: "POST", Path: "/api/automation/commands",
			Body:    map[string]any{"device_id": []string{"contract-list"}, "command_type": "reboot", "payload": "now"},
			Capture: map[string]string{"fourth_command": "command.id"},
		},
		contract.Case{Name: "command-read-device-in-a-list", Path: "/api/automation/commands/{fourth_command}"},
		contract.Case{
			Name: "command-create-device-flag", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": true, "payload": "whoami"},
		},
		contract.Case{
			Name: "command-create-device-object", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": map[string]any{"id": "900100001"}, "payload": "whoami"},
		},
		contract.Case{
			Name: "command-create-without-device", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"payload": "whoami"},
		},
		contract.Case{
			Name: "command-create-device-zero", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": 0, "payload": 5, "command_type": "nonsense"},
		},
		contract.Case{
			Name: "command-create-without-payload", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": "900100001", "command_type": "nonsense"},
		},
		contract.Case{
			Name: "command-create-blank-payload", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": "900100001", "payload": " \n\t "},
		},
		contract.Case{
			Name: "command-create-payload-number", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": "900100001", "payload": 5, "command_type": "nonsense"},
		},
		contract.Case{
			Name: "command-create-unknown-type", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": "900100001", "payload": "whoami", "command_type": "Shell"},
		},
		contract.Case{
			Name: "command-create-array-body", Method: "POST", Path: "/api/automation/commands",
			RawBody: `[{"device_id":"900100001","payload":"whoami"}]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "command-create-as-form", Method: "POST", Path: "/api/automation/commands",
			RawBody: "device_id=900100001&payload=whoami", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "command-create-without-csrf-token", Method: "POST", Path: "/api/automation/commands",
			Body: map[string]any{"device_id": "900100001", "payload": "whoami"}, NoCSRF: true,
		},
		contract.Case{Name: "commands", Path: "/api/automation/commands", Volatile: []string{"~commands", "total"}},
		contract.Case{Name: "commands-head", Method: "HEAD", Path: "/api/automation/commands", Roles: few},
		contract.Case{
			Name: "commands-newest-of-device", Path: "/api/automation/commands",
			Query: url.Values{"device_id": {"900100001"}, "status": {"pending"}, "limit": {"1"}}, Volatile: newest,
		},
		contract.Case{
			Name: "commands-limit-with-text", Path: "/api/automation/commands",
			Query: url.Values{"device_id": {"900100001"}, "limit": {" 1 row"}}, Volatile: newest,
		},
		contract.Case{Name: "commands-of-unknown-device", Path: "/api/automation/commands", Query: url.Values{"device_id": {"no-such-device"}}},
		contract.Case{Name: "commands-completed", Path: "/api/automation/commands", Query: url.Values{"status": {"completed"}}},
		contract.Case{Name: "commands-device-as-stored", Path: "/api/automation/commands", Query: url.Values{"device_id": {"7.0"}, "limit": {"1"}}, Volatile: newest},
		contract.Case{Name: "commands-limit-zero", Path: "/api/automation/commands", Query: url.Values{"device_id": {"no-such-device"}, "limit": {"0"}}},
		contract.Case{Name: "commands-limit-negative", Path: "/api/automation/commands", Query: url.Values{"device_id": {"no-such-device"}, "limit": {"-1"}}},
		// Since 3.5.264 the limit is held at 500 and the whole list comes
		// back, in an order the creation second of the earlier cases decides
		contract.Case{Name: "commands-limit-too-large", Path: "/api/automation/commands", Query: url.Values{"limit": {"99999999999999999999"}}, Volatile: []string{"commands"}},
		contract.Case{Name: "commands-limit-object", Path: "/api/automation/commands", Query: url.Values{"device_id": {"no-such-device"}, "limit[rows]": {"1"}}},
		contract.Case{Name: "commands-status-twice", Path: "/api/automation/commands", Query: url.Values{"status": {"pending", "running"}}},
		contract.Case{Name: "commands-status-object", Path: "/api/automation/commands", Query: url.Values{"status[is]": {"pending"}}},
		contract.Case{Name: "commands-device-in-brackets", Path: "/api/automation/commands", Query: url.Values{"device_id[]": {"no-such-device"}}},

		// SMTP settings. These stay in the instance.
		contract.Case{Name: "smtp-read-first", Path: "/api/automation/smtp"},
		contract.Case{Name: "smtp-read-head", Method: "HEAD", Path: "/api/automation/smtp", Roles: few},
		contract.Case{Name: "smtp-test-first", Method: "POST", Path: "/api/automation/smtp/test", Body: map[string]any{}},
		contract.Case{Name: "smtp-save-without-host", Method: "PUT", Path: "/api/automation/smtp", Body: map[string]any{"port": 1, "user": "contract"}},
		contract.Case{Name: "smtp-save-empty-host", Method: "PUT", Path: "/api/automation/smtp", Body: map[string]any{"host": "", "port": 1}},
		contract.Case{Name: "smtp-save-host-zero", Method: "PUT", Path: "/api/automation/smtp", Body: map[string]any{"host": 0, "port": 1}},
		contract.Case{
			Name: "smtp-save-array-body", Method: "PUT", Path: "/api/automation/smtp",
			RawBody: `[{"host":"127.0.0.1","port":1}]`, ContentType: "application/json",
		},
		contract.Case{
			Name: "smtp-save-as-form", Method: "PUT", Path: "/api/automation/smtp",
			RawBody: "host=127.0.0.1&port=1", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{Name: "smtp-save-without-csrf-token", Method: "PUT", Path: "/api/automation/smtp", Body: smtp, NoCSRF: true},
		contract.Case{Name: "smtp-read-after-refused-saves", Path: "/api/automation/smtp"},
		// Only a host: the port, the sender and the rest get their defaults.
		// Port 587 on the loopback address is closed as well.
		contract.Case{Name: "smtp-save-host-only", Method: "PUT", Path: "/api/automation/smtp", Body: map[string]any{"host": "127.0.0.1", "pass": ""}},
		contract.Case{Name: "smtp-read-host-only", Path: "/api/automation/smtp"},
		contract.Case{Name: "smtp-save", Method: "PUT", Path: "/api/automation/smtp", Body: smtp},
		contract.Case{Name: "smtp-read", Path: "/api/automation/smtp"},
		// The mask and an empty password both keep the stored password
		contract.Case{
			Name: "smtp-save-masked-password", Method: "PUT", Path: "/api/automation/smtp",
			Body: map[string]any{
				"host": "127.0.0.1", "port": 1.9, "secure": 0, "user": "contract", "pass": "********",
				"from": "", "alert_email": 0, "tlsVerify": "yes",
			},
		},
		contract.Case{Name: "smtp-read-masked-password", Path: "/api/automation/smtp"},
		contract.Case{
			Name: "smtp-save-other-types", Method: "PUT", Path: "/api/automation/smtp",
			Body: map[string]any{"host": []string{"127.0.0.1"}, "port": "abc", "secure": "false", "user": 5, "from": 7, "alert_email": 8, "tlsVerify": nil},
		},
		contract.Case{Name: "smtp-read-other-types", Path: "/api/automation/smtp"},
		contract.Case{
			Name: "smtp-save-without-password", Method: "PUT", Path: "/api/automation/smtp",
			Body: map[string]any{
				"host": "127.0.0.1", "port": 1, "secure": false, "user": "contract",
				"from": "console@example.com", "alert_email": "ops@example.com", "tlsVerify": false,
			},
		},
		contract.Case{Name: "smtp-read-without-password", Path: "/api/automation/smtp"},
		contract.Case{Name: "smtp-test", Method: "POST", Path: "/api/automation/smtp/test", Body: map[string]any{}},
		contract.Case{Name: "smtp-test-without-body", Method: "POST", Path: "/api/automation/smtp/test"},
		contract.Case{Name: "smtp-test-without-csrf-token", Method: "POST", Path: "/api/automation/smtp/test", Body: map[string]any{}, NoCSRF: true},
	)
}

// automation-device: the routes an agent fetches its commands with and
// reports their results to, GET /commands at /api/bd (at /api/automation
// the people's list answers first) and POST /commands/{id}/result at
// /api/bd and /api/automation. They take a Bearer token bound to a device;
// the result route then compares the command's device with the token's.
//
// In the signed-out turn admin sends a command to 900100002, and that
// device (the token in /stack/secrets/device_token_other) fetches it,
// reports it running and completed, and fetches nothing after that. The
// instance's own device (900100001, device_token) is the other device
// here: the automation group leaves pending commands for it, so its own
// list is compared by type only. Then each role's session in front of a
// device's request, for a command that does not exist. Commands cannot be
// deleted; the one of this group is completed when the group ends.
//
// The setup step goes to a route the Go console answered already when
// these cases were written, so its golden was recorded from Go with
// CONTRACT_RECORD_NEW=1, after its answer had been checked against the
// Node recordings that routes/automation replays for the same request
// (testdata/node_sqlite.json: a command with a device and a payload as
// text gets {"success":true,"command":{"id":...,"device_id":...,"status":"pending"}}).
func init() {
	const bd = "/api/bd"
	const own = "/api/automation"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	unknown := map[string]string{"Authorization": "Bearer contract-no-such-token"}
	basic := map[string]string{"Authorization": "Basic {secret:device_token_other}"}
	named := map[string]string{"X-Device-Id": "900100002"}
	none := map[string]string{}
	// The ids of the commands depend on the commands the automation group
	// sent before on the same instance
	ids := []string{"commands[*].id"}
	// asDevice sends a case the way 900100002 does: no session, no
	// cookies, no CSRF token, its token unless the case names other headers
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		if c.Headers == nil {
			c.Headers = other
		}
		if c.Body != nil {
			c.Method = "POST"
			if c.Path == "" {
				c.Path = bd + "/commands/{command}/result"
			}
		}
		if c.Path == "" {
			c.Path = bd + "/commands"
		}
		return c
	}
	result := func(name string, body map[string]any) contract.Case {
		return asDevice(contract.Case{Name: name, Body: body})
	}
	completed := map[string]any{"status": "completed", "result": "demo-reception-02"}

	contract.Group("automation-device",
		contract.Case{
			Name: "setup-command", Method: "POST", Path: own + "/commands", As: "admin", Roles: contract.Roles("signed-out"),
			Body:     map[string]any{"device_id": "900100002", "command_type": "shell", "payload": "hostname"},
			Capture:  map[string]string{"command": "command.id"},
			Volatile: []string{"command.id"},
		},

		// Fetching
		asDevice(contract.Case{Name: "fetch", Volatile: ids}),
		asDevice(contract.Case{Name: "fetch-own-device", Headers: device, Volatile: []string{"commands"}}),
		asDevice(contract.Case{Name: "fetch-without-token", Headers: none}),
		asDevice(contract.Case{Name: "fetch-unknown-token", Headers: unknown}),
		asDevice(contract.Case{Name: "fetch-basic", Headers: basic}),
		asDevice(contract.Case{Name: "fetch-device-id-alone", Headers: named}),
		asDevice(contract.Case{Name: "fetch-names-other-device", Query: url.Values{"device_id": {"900100001"}}, Volatile: ids}),

		// Reporting a result: who may
		result("result-running", map[string]any{"status": "running"}),
		asDevice(contract.Case{Name: "result-without-token", Body: completed, Headers: none}),
		asDevice(contract.Case{Name: "result-unknown-token", Body: completed, Headers: unknown}),
		asDevice(contract.Case{Name: "result-device-id-alone", Body: completed, Headers: named}),
		asDevice(contract.Case{Name: "result-of-other-device", Body: completed, Headers: device}),
		asDevice(contract.Case{Name: "result-of-other-device-bad-status", Body: map[string]any{"status": "done"}, Headers: device}),
		asDevice(contract.Case{Name: "result-at-automation", Path: own + "/commands/{command}/result", Body: completed}),
		asDevice(contract.Case{Name: "result-other-spelling", Path: "/API/BD/Commands/{command}/Result", Body: completed}),

		// The body and the command
		result("result-without-status", map[string]any{"result": "x"}),
		result("result-unknown-status", map[string]any{"status": "done"}),
		result("result-status-pending", map[string]any{"status": "pending"}),
		result("result-status-in-a-list", map[string]any{"status": []any{"completed"}}),
		result("result-status-number", map[string]any{"status": 1}),
		asDevice(contract.Case{Name: "result-missing-command", Path: bd + "/commands/999999999/result", Body: completed}),
		asDevice(contract.Case{Name: "result-id-too-long", Path: bd + "/commands/99999999999999999999/result", Body: completed}),
		asDevice(contract.Case{Name: "result-id-with-zeros", Path: bd + "/commands/00{command}/result", Body: map[string]any{"status": "running", "result": 5}}),
		asDevice(contract.Case{Name: "fetch-after-running", Volatile: ids}),
		result("result-no-text", map[string]any{"status": "completed", "result": map[string]any{"toString": 1}}),
		result("result-object", map[string]any{"status": "failed", "result": map[string]any{"lines": 2}}),
		result("result-completed", completed),
		result("result-again", map[string]any{"status": "running"}),
		result("result-completed-again", completed),
		asDevice(contract.Case{Name: "fetch-after-completed", Volatile: ids}),

		// A device's request with each role's browser: the session, its
		// cookies and its CSRF token come along
		contract.Case{Name: "result-with-session", Method: "POST", Path: bd + "/commands/999999999/result", Body: completed, Headers: other},
		contract.Case{Name: "result-with-session-without-csrf-token", Method: "POST", Path: bd + "/commands/999999999/result", Body: completed, Headers: other, NoCSRF: true},
		contract.Case{Name: "result-at-automation-with-session", Method: "POST", Path: own + "/commands/999999999/result", Body: completed, Headers: other},
		contract.Case{Name: "result-with-session-without-token", Method: "POST", Path: bd + "/commands/999999999/result", Body: completed},
		contract.Case{Name: "fetch-with-session", Path: bd + "/commands", Headers: other, Volatile: ids},
		contract.Case{Name: "fetch-with-session-without-token", Path: bd + "/commands"},
	)
}
