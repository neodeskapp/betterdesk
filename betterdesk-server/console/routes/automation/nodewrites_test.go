package automation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
)

// What the old routes wrote for a request the Go console now refuses, so
// that the steps of a recording after it see the rows Node had. Each
// helper writes as the old route did: the values of the request bound as
// the old console's driver bound them (package jsbind), with its
// defaults. They run as a Fix.After and do nothing where Node's request
// failed.

// nodeBody is req.body of a step: the members of a JSON object.
func nodeBody(t *testing.T, step replay.Step) middleware.JSONObject {
	t.Helper()
	text := ""
	if step.Body != nil {
		text = *step.Body
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(text))
	r.Header.Set("Content-Type", "application/json")
	var body middleware.JSONObject
	parse := middleware.Bodies(middleware.BodyOptions{OnError: func(http.ResponseWriter, *http.Request, int, error) {}})
	parse(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { body = middleware.Body(r) })).ServeHTTP(httptest.NewRecorder(), r)
	return body
}

// nodeExec runs a statement with values bound as the old console bound
// them.
func nodeExec(t *testing.T, w *replay.World, query string, values ...jsbind.Value) {
	t.Helper()
	args, err := jsbind.Args(w.DB.Dialect(), strings.Count(query, "?"), values...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if _, err := w.DB.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// nodeFlag is a flag as the old statements bound it.
func nodeFlag(db *store.DB, on bool) jsbind.Value {
	switch {
	case db.IsPostgres():
		return jsbind.Bool(on)
	case on:
		return jsbind.Number(1)
	}
	return jsbind.Number(0)
}

// nodeTurnedOff is enabled === false || enabled === 0 || enabled === '0'
// || enabled === 'false'.
func nodeTurnedOff(v jsbind.Value) bool {
	switch {
	case v.IsString():
		return v.Str() == "0" || v.Str() == "false"
	case v.IsNumber():
		return v.ToNumber() == 0
	case v.IsUndefined(), v.IsNull(), v.IsArray(), v.IsObject():
		return false
	}
	return !v.Truthy()
}

// nodeRule is what POST rules wrote for a rule Node created.
func nodeRule(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusCreated {
		return
	}
	body := nodeBody(t, step)
	get := func(key string) jsbind.Value { return jsbind.Of(body.Get(key)) }
	value := get("condition_value")
	if value.IsUndefined() || value.IsNull() {
		value = jsbind.Number(0)
	}
	severity := get("severity")
	if !severity.IsString() || !slices.Contains([]string{"info", "warning", "critical"}, severity.Str()) {
		severity = jsbind.String("warning")
	}
	nodeExec(t, w, `INSERT INTO alert_rules (name, description, enabled, condition_type, condition_op, condition_value,
                    severity, scope_device_id, cooldown_secs, notify_emails, created_by)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jsbind.String(jsbind.Trim(get("name").Str())), get("description").Or(jsbind.String("")), nodeFlag(w.DB, !nodeTurnedOff(get("enabled"))),
		get("condition_type"), get("condition_op").Or(jsbind.String("gt")), value.Or(jsbind.Number(0)),
		severity, get("scope_device_id").Or(jsbind.Null), get("cooldown_secs").Or(jsbind.Number(300)),
		get("notify_emails").Or(jsbind.String("")), jsbind.String(replay.Users[step.Role].Username))
	nodeAudit(t, w, step, "alert_rule_created", "Rule: "+get("name").Str())
}

// nodeAudit is the audit log entry an old route wrote for the step, with
// the details cleaned as the audit log cleans them.
func nodeAudit(t *testing.T, w *replay.World, step replay.Step, action, details string) {
	t.Helper()
	nodeExec(t, w, "INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (?, ?, ?, ?)",
		jsbind.Number(float64(replay.Users[step.Role].ID)), jsbind.String(action), jsbind.String(auditlog.RedactDetails(details)), jsbind.String("203.0.113.7"))
}

// nodeRuleUpdate is what PATCH rules/{id} wrote for a change Node took:
// every member that names a column, as it came, enabled by its truth.
func nodeRuleUpdate(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusOK {
		return
	}
	body := nodeBody(t, step)
	var sets []string
	var values []jsbind.Value
	for _, column := range []string{"name", "description", "condition_type", "condition_op", "condition_value", "severity", "scope_device_id", "cooldown_secs", "notify_emails", "enabled"} {
		if !body.Has(column) {
			continue
		}
		v := jsbind.Of(body.Get(column))
		switch column {
		case "name":
			v = jsbind.String(jsbind.Trim(v.Str()))
		case "enabled":
			v = nodeFlag(w.DB, v.Truthy())
		}
		sets = append(sets, column+" = ?, ")
		values = append(values, v)
	}
	if len(sets) == 0 {
		return
	}
	id := step.Path[strings.LastIndexByte(step.Path, '/')+1:]
	nodeExec(t, w, "UPDATE alert_rules SET "+strings.Join(sets, "")+"updated_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = ?",
		append(values, jsbind.Number(jsbind.ParseNumber(id)))...)
}

// nodeAck is what POST alerts/{id}/ack wrote for an alert that exists:
// the alert acknowledged by the account, now, also over an earlier
// acknowledgement.
func nodeAck(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusOK {
		return
	}
	parts := strings.Split(step.Path, "/")
	nodeExec(t, w, "UPDATE alert_history SET acknowledged = "+w.DB.Pick("1", "TRUE")+", acknowledged_by = ?, acknowledged_at = "+store.NowSQL(w.DB.Dialect())+" WHERE id = ?",
		jsbind.String(replay.Users[step.Role].Username), jsbind.Number(jsbind.ParseNumber(parts[len(parts)-2])))
}

// nodeCommand is what POST commands wrote for a device the old route took:
// the device as it came, with the audit log entry of a command that was
// answered 201. The route inserted before it wrote its log line, so a
// device it could not make text of (a member named toString) failed with
// 500 after the row was stored; where the driver refused the device,
// nothing was.
func nodeCommand(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusCreated && step.Status != http.StatusInternalServerError {
		return
	}
	body := nodeBody(t, step)
	device := jsbind.Of(body.Get("device_id"))
	commandType := jsbind.Of(body.Get("command_type")).Or(jsbind.String("shell"))
	const insert = `INSERT INTO remote_commands (device_id, command_type, payload, status, created_by)
                VALUES (?, ?, ?, 'pending', ?)`
	args, err := jsbind.Args(w.DB.Dialect(), 4, device, commandType, jsbind.String(jsbind.Trim(jsbind.Of(body.Get("payload")).Str())),
		jsbind.String(replay.Users[step.Role].Username))
	if err != nil {
		return
	}
	if _, err := w.DB.Exec(context.Background(), insert, args...); err != nil {
		if step.Status == http.StatusCreated {
			t.Fatalf("%s: %v", step.Name, err)
		}
		return
	}
	if step.Status == http.StatusCreated {
		nodeAudit(t, w, step, "remote_command_sent", commandType.Str()+" to "+device.ToString())
	}
}

// nodeRuleDelete is what DELETE rules/{id} wrote for a rule Node found:
// the rule is gone, with the audit log entry of its id and name.
func nodeRuleDelete(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusOK {
		return
	}
	id := jsbind.Number(jsbind.ParseNumber(step.Path[strings.LastIndexByte(step.Path, '/')+1:]))
	args, err := jsbind.Args(w.DB.Dialect(), 1, id)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := w.DB.QueryRow(context.Background(), "SELECT name FROM alert_rules WHERE id = ?", args...).Scan(&name); err != nil {
		t.Fatalf("%s: %v", step.Name, err)
	}
	nodeExec(t, w, "DELETE FROM alert_rules WHERE id = ?", id)
	nodeAudit(t, w, step, "alert_rule_deleted", "Rule #"+id.ToString()+": "+name)
}

// nodeResult is what POST commands/{id}/result wrote for a report Node
// took, in any state of the command: the status, the result as String()
// made it (NULL when there is none), the time it began running with
// "running" and the time it ended with "completed" and "failed".
func nodeResult(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	if step.Status != http.StatusOK {
		return
	}
	body := nodeBody(t, step)
	result := jsbind.Null
	if raw := body.Get("result"); raw.Exists() {
		result = jsbind.String(jsbind.Of(raw).ToString())
	}
	status := jsbind.Of(body.Get("status")).Str()
	set := ""
	switch status {
	case "running":
		set = ", executed_at = " + store.NowSQL(w.DB.Dialect())
	case "completed", "failed":
		set = ", completed_at = " + store.NowSQL(w.DB.Dialect())
	}
	parts := strings.Split(strings.TrimSuffix(step.Path, "/"), "/")
	nodeExec(t, w, "UPDATE remote_commands SET status = ?, result = ?"+set+" WHERE id = ?",
		jsbind.String(status), result, jsbind.Number(jsbind.ParseNumber(parts[len(parts)-2])))
}
