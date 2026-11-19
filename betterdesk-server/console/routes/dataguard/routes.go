// Package dataguard holds the data loss prevention routes: the policies
// that say what devices watch (USB sticks, file operations) and the
// events devices reported. The group is mounted twice, at
// /api/dataguard and at /api/bd, and every route answers at both.
//
// The two routes for devices, GET dlp-policies and POST dlp-events, take
// a device token instead of a session (device.go).
package dataguard

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
)

type handlers struct {
	db  *store.DB
	log *kit.Logger
}

// Register adds the group's routes below the mount.
func Register(d *kit.Deps) {
	h := &handlers{db: d.Store, log: d.Log.Child("DataGuard")}
	r := d.Router
	r.Get("/policies", h.policies, d.RequireAuth, d.RequirePermission("device.view"))
	r.Get("/policies/{id}", h.policy, d.RequireAuth, d.RequirePermission("device.view"))
	r.Post("/policies", h.create, d.RequireAuth, d.RequirePermission("server.config"))
	r.Patch("/policies/{id}", h.update, d.RequireAuth, d.RequirePermission("server.config"))
	r.Delete("/policies/{id}", h.remove, d.RequireAuth, d.RequirePermission("server.config"))
	r.Get("/events", h.events, d.RequireAuth, d.RequirePermission("audit.view"))
	r.Get("/stats", h.stats, d.RequireAuth, d.RequirePermission("audit.view"))
	r.Get("/dlp-policies", h.activePolicies, d.RequireDeviceToken)
	r.Post("/dlp-events", h.report, d.RequireDeviceToken)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// number is Number(req.params[name]).
func number(r *http.Request, name string) jsbind.Value {
	return jsbind.Number(jsbind.ParseNumber(r.PathValue(name)))
}

// policies answers GET policies: every policy with its rules as a list.
func (h *handlers) policies(w http.ResponseWriter, r *http.Request) {
	policies, err := listPolicies(r.Context(), h.db)
	if err != nil {
		h.log.Error("GET /policies error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch policies")
		return
	}
	kit.JSON(w, r, http.StatusOK, policies)
}

// policy answers GET policies/{id}.
func (h *handlers) policy(w http.ResponseWriter, r *http.Request) {
	policy, err := policyByID(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.log.Error("GET /policies/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch policy")
		return
	}
	if policy == nil {
		fail(w, r, http.StatusNotFound, "Policy not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, policy)
}

// rulesText is the rules of a request as the text that is stored:
// JSON.stringify of the list.
func rulesText(rules jsbind.Value) jsbind.Value {
	if rules.IsUndefined() {
		return jsbind.String("[]")
	}
	return jsbind.String(rules.JSON())
}

// enabledFlag is a flag as the statement takes it: 1 or 0 on SQLite, and
// true or false on PostgreSQL.
func (h *handlers) enabledFlag(on bool) jsbind.Value {
	if h.db.IsPostgres() {
		return jsbind.Bool(on)
	}
	if on {
		return jsbind.Number(1)
	}
	return jsbind.Number(0)
}

// flag reads enabled from a body: true or false, also written 1 or 0. A
// text has quotes in its JSON, so "true" as text is no flag.
func flag(v jsbind.Value) (on, ok bool) {
	switch v.JSON() {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}
	return false, false
}

// checkMembers answers 400 for the first member of the body that is not
// what a policy takes: texts, which may also be null where nullable says
// so, and enabled as a flag.
func checkMembers(w http.ResponseWriter, r *http.Request, texts []string, nullable map[string]bool) bool {
	body := kit.Body(r)
	for _, member := range texts {
		v := jsbind.Of(body.Get(member))
		if v.IsUndefined() || v.IsString() || (v.IsNull() && nullable[member]) {
			continue
		}
		fail(w, r, http.StatusBadRequest, "Invalid "+member)
		return false
	}
	if enabled := jsbind.Of(body.Get("enabled")); !enabled.IsUndefined() {
		if _, ok := flag(enabled); !ok {
			fail(w, r, http.StatusBadRequest, "Invalid enabled")
			return false
		}
	}
	return true
}

// create answers POST policies with the body
// {name, description?, policy_type?, action?, scope?, enabled?, rules?}:
// a name that is text, the other texts text or null (null takes the
// default), enabled a flag (without it a policy is on) and rules a list.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	name, isText := body.String("name")
	name = jsbind.Trim(name)
	if !isText || name == "" {
		fail(w, r, http.StatusBadRequest, "Policy name is required")
		return
	}
	rules := jsbind.Of(body.Get("rules"))
	if !rules.IsUndefined() && !rules.IsArray() {
		fail(w, r, http.StatusBadRequest, "rules must be an array")
		return
	}
	texts := []string{"description", "policy_type", "action", "scope"}
	if !checkMembers(w, r, texts, map[string]bool{"description": true, "policy_type": true, "action": true, "scope": true}) {
		return
	}
	on := true
	if enabled := jsbind.Of(body.Get("enabled")); !enabled.IsUndefined() {
		on, _ = flag(enabled)
	}
	enabled := h.enabledFlag(on)
	empty := jsbind.String("")
	policy, err := createPolicy(r.Context(), h.db, newPolicy{
		Name:        jsbind.String(name),
		Description: jsbind.Of(body.Get("description")).Or(empty),
		PolicyType:  jsbind.Of(body.Get("policy_type")).Or(empty),
		Action:      jsbind.Of(body.Get("action")).Or(jsbind.String("log")),
		Scope:       jsbind.Of(body.Get("scope")).Or(empty),
		Enabled:     enabled,
		Rules:       rulesText(rules),
	})
	if err != nil {
		h.log.Error("POST /policies error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to create policy")
		return
	}
	kit.JSON(w, r, http.StatusCreated, policy)
}

// update answers PATCH policies/{id}: every member of the body that names
// a column is written: a name that is text, the other texts text (scope
// also null), enabled a flag and rules a list. A body that names none
// answers the policy as it is.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	id := number(r, "id")
	exists, err := policyExists(r.Context(), h.db, id)
	if err != nil {
		h.log.Error("PATCH /policies/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to update policy")
		return
	}
	if !exists {
		fail(w, r, http.StatusNotFound, "Policy not found")
		return
	}
	body := kit.Body(r)
	rules := jsbind.Of(body.Get("rules"))
	if !rules.IsUndefined() && !rules.IsArray() {
		fail(w, r, http.StatusBadRequest, "rules must be an array")
		return
	}
	if name, isText := body.String("name"); body.Has("name") && (!isText || jsbind.Trim(name) == "") {
		fail(w, r, http.StatusBadRequest, "Policy name is required")
		return
	}
	if !checkMembers(w, r, []string{"description", "policy_type", "action", "scope"}, map[string]bool{"scope": true}) {
		return
	}
	var changes []change
	for _, column := range []string{"name", "description", "policy_type", "action", "scope"} {
		if body.Has(column) {
			value := jsbind.Of(body.Get(column))
			if column == "name" {
				value = jsbind.String(jsbind.Trim(value.Str()))
			}
			changes = append(changes, change{column, value})
		}
	}
	if body.Has("enabled") {
		on, _ := flag(jsbind.Of(body.Get("enabled")))
		changes = append(changes, change{"enabled", h.enabledFlag(on)})
	}
	if !rules.IsUndefined() {
		changes = append(changes, change{"rules", rulesText(rules)})
	}
	updated, err := updatePolicy(r.Context(), h.db, id, changes)
	if err != nil {
		h.log.Error("PATCH /policies/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to update policy")
		return
	}
	kit.JSON(w, r, http.StatusOK, updated)
}

type done struct {
	OK bool `json:"ok"`
}

// remove answers DELETE policies/{id}.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	ok, err := deletePolicy(r.Context(), h.db, number(r, "id"))
	if err != nil {
		h.log.Error("DELETE /policies/:id error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to delete policy")
		return
	}
	if !ok {
		fail(w, r, http.StatusNotFound, "Policy not found")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{OK: true})
}

// events answers GET events?device_id=&event_source=&event_type=&limit=
// &from=&to=: the reported events, newest first.
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	query := func(name string) jsbind.Value { return jsbind.Query(r.URL.RawQuery, name) }
	filter := eventFilter{
		DeviceID:    query("device_id"),
		EventSource: query("event_source"),
		EventType:   query("event_type"),
		From:        query("from"),
		To:          query("to"),
	}
	// limit ? Number(limit) : undefined
	if limit := query("limit"); limit.Truthy() {
		filter.Limit = jsbind.Number(limit.ToNumber())
	}
	events, err := listEvents(r.Context(), h.db, filter)
	if err != nil {
		h.log.Error("GET /events error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch events")
		return
	}
	kit.JSON(w, r, http.StatusOK, events)
}

// stats answers GET stats.
func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	stats, err := countEvents(r.Context(), h.db)
	if err != nil {
		h.log.Error("GET /stats error:", err)
		fail(w, r, http.StatusInternalServerError, "Failed to fetch stats")
		return
	}
	kit.JSON(w, r, http.StatusOK, stats)
}
