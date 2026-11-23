package scaling

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// matchTypes are what a rule can match a device by.
var matchTypes = map[string]bool{"subnet": true, "group": true, "tag": true, "country": true, "device_id": true}

// validateRule returns what is wrong with the body of a rule, "" when it
// can be stored.
func validateRule(body middleware.JSONObject) string {
	if n := utf16Len(text(body.Get("name"), "")); n < 1 || n > 80 {
		return "name must be 1-80 characters"
	}
	if !matchTypes[text(body.Get("match_type"), "")] {
		return "invalid match_type"
	}
	if n := utf16Len(text(body.Get("match_value"), "")); n < 1 || n > 200 {
		return "match_value required"
	}
	if n, ok := parseInt(body.Get("priority")); ok && (n < 0 || n > 1000) {
		return "priority out of range"
	}
	if utf16Len(text(body.Get("target_relay"), "")) > 80 {
		return "target_relay must be at most 80 characters"
	}
	if utf16Len(text(body.Get("fallback"), "")) > 80 {
		return "fallback must be at most 80 characters"
	}
	return ""
}

// ruleFrom builds the stored rule from a body, new or from the existing
// one. A rule is on unless the body says "enabled": false; a body without
// "enabled" leaves an existing rule as it is.
func (h *handlers) ruleFrom(body middleware.JSONObject, existing *object) (*object, error) {
	out, err := h.fresh(existing)
	if err != nil {
		return nil, err
	}
	out.set("name", text(body.Get("name"), ""))
	out.set("match_type", text(body.Get("match_type"), ""))
	out.set("match_value", text(body.Get("match_value"), ""))
	out.set("target_relay", text(body.Get("target_relay"), ""))
	out.set("fallback", text(body.Get("fallback"), "master"))
	out.set("priority", numberOr(body.Get("priority"), 10))
	enabled := true
	if given := body.Get("enabled"); given.Exists() {
		value, isBool := given.Bool()
		enabled = !isBool || value
	} else if existing != nil {
		enabled = existing.m["enabled"] != false
	}
	out.set("enabled", enabled)
	out.set("updated_at", h.isoNow())
	return out, nil
}

// listRules answers GET /api/panel/scaling/rules: {"data":[...]}.
func (h *handlers) listRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.load(r.Context(), keyRules)
	if err != nil {
		h.log.Error("[Scaling] list rules:", err)
		fail(w, r, http.StatusInternalServerError, "failed to load rules")
		return
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{list(rules)})
}

// createRule answers POST /api/panel/scaling/rules with the body
// {"name","match_type","match_value","target_relay","fallback",
// "priority","enabled"}: 201 and the stored rule. The list holds 200
// rules.
func (h *handlers) createRule(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if problem := validateRule(body); problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	listWrites.Lock()
	defer listWrites.Unlock()
	rules, err := h.load(r.Context(), keyRules)
	if err == nil && len(rules) >= maxRules {
		fail(w, r, http.StatusBadRequest, "too many rules (max 200)")
		return
	}
	var rule *object
	if err == nil {
		rule, err = h.ruleFrom(body, nil)
	}
	if err == nil {
		err = h.save(r.Context(), keyRules, append(rules, rule))
	}
	if err != nil {
		h.log.Error("[Scaling] create rule:", err)
		fail(w, r, http.StatusInternalServerError, "failed to save rule")
		return
	}
	kit.JSON(w, r, http.StatusCreated, dataAnswer{rule})
}

// updateRule answers PUT /api/panel/scaling/rules/{ruleId} with the body
// of createRule.
func (h *handlers) updateRule(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	if problem := validateRule(body); problem != "" {
		fail(w, r, http.StatusBadRequest, problem)
		return
	}
	listWrites.Lock()
	defer listWrites.Unlock()
	rules, err := h.load(r.Context(), keyRules)
	i := -1
	if err == nil {
		i = find(rules, r.PathValue("ruleId"))
	}
	if err == nil && i < 0 {
		fail(w, r, http.StatusNotFound, "not found")
		return
	}
	var rule *object
	if err == nil {
		existing, _ := rules[i].(*object)
		rule, err = h.ruleFrom(body, existing)
	}
	if err == nil {
		rules[i] = rule
		err = h.save(r.Context(), keyRules, rules)
	}
	if err != nil {
		h.log.Error("[Scaling] update rule:", err)
		fail(w, r, http.StatusInternalServerError, "failed to update rule")
		return
	}
	kit.JSON(w, r, http.StatusOK, dataAnswer{rule})
}

// deleteRule answers DELETE /api/panel/scaling/rules/{ruleId}.
func (h *handlers) deleteRule(w http.ResponseWriter, r *http.Request) {
	h.remove(w, r, keyRules, r.PathValue("ruleId"), "failed to delete rule")
}
