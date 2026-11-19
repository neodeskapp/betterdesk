// Package automation holds the automation routes. The console mounts the
// group twice, at /api/automation and at /api/bd:
//
//   - The routes people manage alerts and remote commands with: alert
//     rules, the alert history, the commands sent to devices, and the SMTP
//     settings. All of them need the permission server.config, and all of
//     them step aside at /api/bd for the routes of other groups there.
//   - The routes of the device agent, behind the device token guard
//     (device.go): it fetches its pending commands (GET commands, reached
//     at /api/bd only, because at /api/automation the people's list of
//     commands answers first) and reports a command's result (POST
//     commands/{id}/result, at both mounts).
//
// The fields of a rule that hold text or a number take only their type
// (rulecheck.go). Other values go to the database as the request gives
// them, bound by package jsbind as the old console's drivers bound them,
// so a command's device that is a number is stored as "5.0" on SQLite and
// as "5" on PostgreSQL. A new rule or command is answered with the row as
// it is stored. The SMTP settings routes hand their work to package mail.
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

	r.Get("/commands", h.pendingCommands, d.RequireDeviceToken)
	r.Post("/commands/{id}/result", h.commandResult, id, d.RequireDeviceToken)

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
	Success bool    `json:"success"`
	Rule    *record `json:"rule"`
}

// createRule answers POST rules with the body {name, condition_type,
// description?, condition_op?, condition_value?, severity?,
// scope_device_id?, cooldown_secs?, notify_emails?, enabled?} and the
// rule as it is stored.
//
// The name has to be a text that is not blank, the type one of six and
// the comparison, when given, one of six, and the fields of wrongType
// have their type. A severity that is none of the three is "warning". A
// rule is on unless enabled is false, 0, "0" or "false".
func (h *handlers) createRule(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	get := func(key string) jsbind.Value { return jsbind.Of(body.Get(key)) }
	name := get("name")
	if !name.IsString() || jsbind.Trim(name.Str()) == "" {
		fail(w, r, http.StatusBadRequest, "Rule name is required")
		return
	}
	conditionType := get("condition_type")
	if !oneOf(conditionTypes, conditionType) {
		fail(w, r, http.StatusBadRequest, "Invalid condition_type. Valid: cpu_usage, memory_usage, disk_usage, offline_duration, idle_duration, custom")
		return
	}
	op := get("condition_op")
	if op.Truthy() && !oneOf(operators, op) {
		fail(w, r, http.StatusBadRequest, "Invalid condition_op. Valid: gt, gte, lt, lte, eq, neq")
		return
	}
	if refused := wrongType(body); refused != "" {
		fail(w, r, http.StatusBadRequest, refused)
		return
	}

	severity := get("severity")
	if !oneOf(severities, severity) {
		severity = jsbind.String("warning")
	}
	// condition_value ?? 0
	value := get("condition_value")
	if value.IsUndefined() || value.IsNull() {
		value = jsbind.Number(0)
	}
	rule, err := createRule(r.Context(), h.db, newRule{
		Name:           jsbind.String(jsbind.Trim(name.Str())),
		Description:    get("description").Or(jsbind.String("")),
		ConditionType:  conditionType,
		ConditionOp:    op.Or(jsbind.String("gt")),
		ConditionValue: value,
		Severity:       severity,
		ScopeDeviceID:  get("scope_device_id").Or(jsbind.Null),
		CooldownSecs:   get("cooldown_secs").Or(jsbind.Number(300)),
		NotifyEmails:   get("notify_emails").Or(jsbind.String("")),
		Enabled:        !turnedOff(get("enabled")),
		CreatedBy:      kit.User(r).Username,
	})
	if err != nil {
		h.serverError(w, r, "Rule create error", err)
		return
	}
	_ = h.d.LogAction(r, "alert_rule_created", "Rule: "+name.Str())
	h.log.Info("Alert rule created: " + name.Str())
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
// names a field of a rule is written, null included, with the checks of
// the create. The name has to be a text that is not blank, the type, the
// comparison and the severity one of theirs, the fields of wrongType have
// their type, and condition_value is not null. enabled is read as on a
// new rule: off for false, 0, "0" and "false".
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
		if !name.IsString() || jsbind.Trim(name.Str()) == "" {
			fail(w, r, http.StatusBadRequest, "Rule name is required")
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
	if refused := wrongType(body); refused != "" {
		fail(w, r, http.StatusBadRequest, refused)
		return
	}
	if body.Has("severity") && !oneOf(severities, jsbind.Of(body.Get("severity"))) {
		fail(w, r, http.StatusBadRequest, "Invalid severity")
		return
	}
	// The column takes no NULL
	if body.Has("condition_value") && jsbind.Of(body.Get("condition_value")).IsNull() {
		fail(w, r, http.StatusBadRequest, "Invalid condition_value")
		return
	}
	for _, column := range []string{"condition_value", "severity", "scope_device_id", "cooldown_secs", "notify_emails"} {
		if body.Has(column) {
			changes = append(changes, change{column, jsbind.Of(body.Get(column))})
		}
	}
	if body.Has("enabled") {
		changes = append(changes, change{"enabled", flag(h.db, !turnedOff(jsbind.Of(body.Get("enabled"))))})
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
