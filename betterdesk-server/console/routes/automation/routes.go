// Package automation holds the routes people manage alerts and remote
// commands with, under /api/automation: alert rules, the alert history,
// the commands sent to devices, and the SMTP settings. All of them need
// the permission server.config.
//
// The routes check little and hand what the request gives them to the
// database, so an answer can depend on the database: a number in a text
// field is stored as "5.0" on SQLite and as "5" on PostgreSQL, a flag
// there fails on SQLite and is the text "true" on PostgreSQL. Package
// jsbind keeps both behaviours. A new rule or command is answered with
// the values of the request on SQLite and with the stored row on
// PostgreSQL.
//
// The routes for device agents (GET commands and POST commands/{id}/result)
// take a device token and are not here. They are why the old console
// mounted the group at /api/bd as well, where the routes of this package
// step aside. The SMTP settings routes hand their work to package mail.
package automation

import (
	"net/http"
	"slices"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/devicemount"
	"github.com/unitronix/betterdesk-server/console/store"
)

var (
	conditionTypes = []string{"cpu_usage", "memory_usage", "disk_usage", "offline_duration", "idle_duration", "custom"}
	operators      = []string{"gt", "gte", "lt", "lte", "eq", "neq"}
	severities     = []string{"info", "warning", "critical"}
	commandTypes   = []string{"shell", "powershell", "script", "restart_service", "reboot"}
)

type handlers struct {
	d   *kit.Deps
	db  *store.DB
	log *kit.Logger
}

// Register adds the group's routes below the mount.
func Register(d *kit.Deps) {
	h := &handlers{d: d, db: d.Store, log: d.Log.Child("Automation")}
	r := d.Router
	// id is the pattern :id(\d+) of the old routes. It goes first, because
	// a pattern decides before any check of a route runs.
	notOnDeviceMount, id := kit.Middleware(devicemount.Not), kit.Middleware(writtenInDigits)
	canConfigure := d.RequirePermission("server.config")

	r.Get("/rules", h.rules, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Post("/rules", h.createRule, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Get("/rules/{id}", h.rule, id, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Patch("/rules/{id}", h.updateRule, id, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Delete("/rules/{id}", h.deleteRule, id, notOnDeviceMount, d.RequireAuth, canConfigure)

	r.Get("/alerts", h.alerts, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Post("/alerts/{id}/ack", h.acknowledge, id, notOnDeviceMount, d.RequireAuth, canConfigure)

	r.Post("/commands", h.sendCommand, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Get("/commands", h.commands, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Get("/commands/{id}", h.command, id, notOnDeviceMount, d.RequireAuth, canConfigure)

	r.Get("/smtp", h.smtp, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Put("/smtp", h.saveSMTP, notOnDeviceMount, d.RequireAuth, canConfigure)
	r.Post("/smtp/test", h.testSMTP, notOnDeviceMount, d.RequireAuth, canConfigure)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// serverError answers what every route's catch block answered.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what+":", err)
	fail(w, r, http.StatusInternalServerError, "Internal server error")
}

type done struct {
	Success bool `json:"success"`
}

// writtenInDigits lets a route with an id answer only when the id is made
// of digits in the address as the client wrote it, and passes the request
// on otherwise. The old console matched its pattern before it decoded the
// address, so an escaped digit (%31 for 1) is no digit. The id is the
// second part of the path below the mount in every route that has one.
func writtenInDigits(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(kit.RoutePath(r), "/"), "/")
		ok := len(parts) > 1 && parts[1] != ""
		for i := 0; ok && i < len(parts[1]); i++ {
			ok = parts[1][i] >= '0' && parts[1][i] <= '9'
		}
		if !ok {
			kit.PassOn(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// number is +req.params[name] for a parameter made of digits. More digits
// than a JavaScript number holds give the nearest number it does hold.
func number(r *http.Request, name string) jsbind.Value {
	return jsbind.Number(jsbind.ParseNumber(r.PathValue(name)))
}

// oneOf is list.includes(v): v is a text and one of the list.
func oneOf(list []string, v jsbind.Value) bool {
	return v.IsString() && slices.Contains(list, v.Str())
}

type ruleList struct {
	Rules []record `json:"rules"`
	Total int      `json:"total"`
}

// rules answers GET rules: every alert rule, the newest first.
func (h *handlers) rules(w http.ResponseWriter, r *http.Request) {
	rules, err := listRules(r.Context(), h.db)
	if err != nil {
		h.serverError(w, r, "Rules list error", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, ruleList{Rules: rules, Total: len(rules)})
}

type ruleCreated struct {
	Success bool `json:"success"`
	Rule    any  `json:"rule"`
}

// createRule answers POST rules with the body {name, condition_type,
// description?, condition_op?, condition_value?, severity?,
// scope_device_id?, cooldown_secs?, notify_emails?, enabled?}.
//
// The name has to be a text, the type one of six and the comparison, when
// given, one of six. A severity that is none of the three is "warning".
// Everything else is stored as it came, or fails in the database. A rule
// is on unless enabled is false, 0, "0" or "false".
func (h *handlers) createRule(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	name := member(body, "name")
	if !name.value.Truthy() {
		fail(w, r, http.StatusBadRequest, "Rule name is required")
		return
	}
	if !name.value.IsString() {
		h.serverError(w, r, "Rule create error", errNotAFunction("name.trim"))
		return
	}
	if jsbind.Trim(name.value.Str()) == "" {
		fail(w, r, http.StatusBadRequest, "Rule name is required")
		return
	}
	conditionType := member(body, "condition_type")
	if !oneOf(conditionTypes, conditionType.value) {
		fail(w, r, http.StatusBadRequest, "Invalid condition_type. Valid: cpu_usage, memory_usage, disk_usage, offline_duration, idle_duration, custom")
		return
	}
	op := member(body, "condition_op")
	if op.value.Truthy() && !oneOf(operators, op.value) {
		fail(w, r, http.StatusBadRequest, "Invalid condition_op. Valid: gt, gte, lt, lte, eq, neq")
		return
	}

	severity := member(body, "severity")
	if !oneOf(severities, severity.value) {
		severity = text("warning")
	}
	// condition_value ?? 0
	value := member(body, "condition_value")
	if value.value.IsUndefined() || value.value.IsNull() {
		value = constant(jsbind.Number(0))
	}
	rule, err := createRule(r.Context(), h.db, newRule{
		Name:           name.trimmed(),
		Description:    member(body, "description").or(text("")),
		ConditionType:  conditionType,
		ConditionOp:    op.or(text("gt")),
		ConditionValue: value,
		Severity:       severity,
		ScopeDeviceID:  member(body, "scope_device_id").or(constant(jsbind.Null)),
		CooldownSecs:   member(body, "cooldown_secs").or(constant(jsbind.Number(300))),
		NotifyEmails:   member(body, "notify_emails").or(text("")),
		Enabled:        !turnedOff(jsbind.Of(body.Get("enabled"))),
		CreatedBy:      kit.User(r).Username,
	})
	if err != nil {
		h.serverError(w, r, "Rule create error", err)
		return
	}
	_ = h.d.LogAction(r, "alert_rule_created", "Rule: "+name.value.Str())
	h.log.Info("Alert rule created: " + name.value.Str())
	kit.JSON(w, r, http.StatusCreated, ruleCreated{Success: true, Rule: rule})
}

// rule answers GET rules/{id}.
func (h *handlers) rule(w http.ResponseWriter, r *http.Request) {
	rule, err := ruleByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.serverError(w, r, "Rule get error", err)
		return
	}
	if rule == nil {
		fail(w, r, http.StatusNotFound, "Rule not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, rule)
}

// updateRule answers PATCH rules/{id}: every member of the body that
// names a field of a rule is written, null included. The name has to be a
// text, and the type and the comparison one of theirs. enabled is stored
// by its truth, so "false" turns a rule on.
func (h *handlers) updateRule(w http.ResponseWriter, r *http.Request) {
	rule, err := ruleByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.serverError(w, r, "Rule update error", err)
		return
	}
	if rule == nil {
		fail(w, r, http.StatusNotFound, "Rule not found")
		return
	}
	body := kit.Body(r)
	var changes []change
	if body.Has("name") {
		name := jsbind.Of(body.Get("name"))
		if !name.IsString() {
			h.serverError(w, r, "Rule update error", errNotAFunction("name.trim"))
			return
		}
		changes = append(changes, change{"name", jsbind.String(jsbind.Trim(name.Str()))})
	}
	if body.Has("description") {
		changes = append(changes, change{"description", jsbind.Of(body.Get("description"))})
	}
	if body.Has("condition_type") {
		conditionType := jsbind.Of(body.Get("condition_type"))
		if !oneOf(conditionTypes, conditionType) {
			fail(w, r, http.StatusBadRequest, "Invalid condition_type")
			return
		}
		changes = append(changes, change{"condition_type", conditionType})
	}
	if body.Has("condition_op") {
		op := jsbind.Of(body.Get("condition_op"))
		if !oneOf(operators, op) {
			fail(w, r, http.StatusBadRequest, "Invalid condition_op")
			return
		}
		changes = append(changes, change{"condition_op", op})
	}
	for _, column := range []string{"condition_value", "severity", "scope_device_id", "cooldown_secs", "notify_emails"} {
		if body.Has(column) {
			changes = append(changes, change{column, jsbind.Of(body.Get(column))})
		}
	}
	if body.Has("enabled") {
		changes = append(changes, change{"enabled", flag(h.db, body.Truthy("enabled"))})
	}
	if err := updateRule(r.Context(), h.db, rule.number("id"), changes); err != nil {
		h.serverError(w, r, "Rule update error", err)
		return
	}
	h.log.Info("Rule #" + rule.text("id") + " updated")
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// deleteRule answers DELETE rules/{id}. The alerts of the rule go with it.
func (h *handlers) deleteRule(w http.ResponseWriter, r *http.Request) {
	rule, err := ruleByID(r.Context(), h.db, number(r, "id"))
	if err == nil && rule != nil {
		err = deleteRule(r.Context(), h.db, rule.number("id"))
	}
	if err != nil {
		h.serverError(w, r, "Rule delete error", err)
		return
	}
	if rule == nil {
		fail(w, r, http.StatusNotFound, "Rule not found")
		return
	}
	_ = h.d.LogAction(r, "alert_rule_deleted", "Rule #"+rule.text("id")+": "+rule.text("name"))
	h.log.Info("Rule #" + rule.text("id") + " deleted")
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}
