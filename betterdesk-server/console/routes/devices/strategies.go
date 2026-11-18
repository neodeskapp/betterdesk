package devices

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Strategies are the access rules RustDesk clients get: which features a
// session may use, for the devices, accounts and device groups the rule
// is assigned to. The console keeps them in strategies and
// strategy_assignments; the server keeps a copy it applies, which the
// routes update on assignment and deletion.

// strategyPermissionKeys are the permissions a rule can set.
var strategyPermissionKeys = []string{
	"enable-file-transfer",
	"disable-clipboard",
	"enable-clipboard",
	"enable-audio",
	"enable-tunnel",
	"enable-camera",
	"enable-remote-restart",
	"enable-block-input",
}

// badRequest is a validation failure of a rule's payload: 400 with its
// text.
type badRequest string

func (e badRequest) Error() string { return string(e) }

// strategyPayload is a submitted rule after normalizeStrategyPayload.
type strategyPayload struct {
	Name, UserGroupGUID, DeviceGroupGUID string
	Enabled                              bool
	Permissions                          *goapi.Object
}

// normalizeStrategyPermissions keeps the members of an object that are
// one of strategyPermissionKeys and read as a setting: true, "Y" and "y"
// are "Y", false, "N" and "n" are "N". Every other member is left out.
func normalizeStrategyPermissions(raw any) *goapi.Object {
	out := goapi.NewObject()
	o, ok := raw.(*goapi.Object)
	if !ok || o == nil {
		return out
	}
	for _, key := range o.Keys() {
		if !slices.Contains(strategyPermissionKeys, key) {
			continue
		}
		switch v := o.Get(key); v {
		case true, "Y", "y":
			out.Set(key, "Y")
		case false, "N", "n":
			out.Set(key, "N")
		}
	}
	return out
}

// normalizeStrategyPayload reads a submitted rule: a name (cut to 80
// characters) is required, the group guids may be empty or valid.
func normalizeStrategyPayload(body *goapi.Object) (strategyPayload, error) {
	name, err := textOr(body.Get("name"), "")
	if err != nil {
		return strategyPayload{}, err
	}
	name = jsTrim(name)
	if name == "" {
		return strategyPayload{}, badRequest("Strategy name is required")
	}
	userGroup, err := textOr(body.Get("user_group_guid"), "")
	if err != nil {
		return strategyPayload{}, err
	}
	deviceGroup, err := textOr(body.Get("device_group_guid"), "")
	if err != nil {
		return strategyPayload{}, err
	}
	userGroup, deviceGroup = jsTrim(userGroup), jsTrim(deviceGroup)
	if userGroup != "" && !validGroupGUID(userGroup) {
		return strategyPayload{}, badRequest("Invalid user group")
	}
	if deviceGroup != "" && !validGroupGUID(deviceGroup) {
		return strategyPayload{}, badRequest("Invalid device group")
	}
	return strategyPayload{
		Name:            slice(name, 80),
		UserGroupGUID:   userGroup,
		DeviceGroupGUID: deviceGroup,
		Enabled:         body.Get("enabled") != false,
		Permissions:     normalizeStrategyPermissions(body.Get("permissions")),
	}, nil
}

// serializeStrategy is a rule as the routes answer it.
func serializeStrategy(row *goapi.Object) *goapi.Object {
	permissions := row.Get("permissions")
	switch p := permissions.(type) {
	case *goapi.Object:
		if p == nil {
			permissions = goapi.NewObject()
		}
	case []any:
	default:
		permissions = goapi.NewObject()
	}
	enabled := row.Get("enabled")
	return goapi.NewObject(
		"guid", row.Get("guid"),
		"name", or(row.Get("name"), ""),
		"user_group_guid", or(row.Get("user_group_guid"), ""),
		"device_group_guid", or(row.Get("device_group_guid"), ""),
		"enabled", enabled == true || enabled == float64(1),
		"permissions", permissions,
		"permission_keys", strategyPermissionKeys,
		"created_at", or(row.Get("created_at"), nil),
		"updated_at", or(row.Get("updated_at"), nil),
	)
}

// strategyFailure answers the catch of the create and update routes: 400
// with the text of a validation failure, else 500.
func (h *handlers) strategyFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	var bad badRequest
	if errors.As(err, &bad) {
		h.log.Error(what, err)
		answer(w, r, http.StatusBadRequest, failure{Error: string(bad)})
		return
	}
	h.serverError(w, r, what, err)
}

// strategyGUID is the guid of the path, trimmed; ok is false when it is
// no valid guid, which is answered.
func strategyGUID(w http.ResponseWriter, r *http.Request) (string, bool) {
	guid := jsTrim(r.PathValue("guid"))
	if !validGroupGUID(guid) {
		answer(w, r, http.StatusBadRequest, failure{Error: "Invalid strategy identifier"})
		return "", false
	}
	return guid, true
}

// strategies answers GET /api/panel/strategies.
func (h *handlers) strategies(w http.ResponseWriter, r *http.Request) {
	rows, err := allStrategies(r.Context(), h.d.Store)
	if err != nil {
		h.serverError(w, r, "Get strategies error:", err)
		return
	}
	list := make([]*goapi.Object, len(rows))
	for i, row := range rows {
		list[i] = serializeStrategy(row)
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: strategyList{Strategies: list, Total: len(rows)}})
}

type strategyList struct {
	Strategies []*goapi.Object `json:"strategies"`
	Total      int             `json:"total"`
}

type strategyAnswer struct {
	Strategy *goapi.Object `json:"strategy"`
}

// createStrategy answers POST /api/panel/strategies.
func (h *handlers) createStrategy(w http.ResponseWriter, r *http.Request) {
	payload, err := normalizeStrategyPayload(plainBody(r))
	var created *goapi.Object
	if err == nil {
		created, err = createStrategy(r.Context(), h.d.Store, payload)
	}
	var name string
	if err == nil {
		name, err = nameOf(created)
	}
	if err == nil {
		err = h.d.LogAction(r, "strategy_created", "Created strategy: "+name)
	}
	if err != nil {
		h.strategyFailure(w, r, "Create strategy error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: strategyAnswer{Strategy: serializeStrategy(created)}})
}

// nameOf is String(row.name), failing for a row that is not there.
func nameOf(row *goapi.Object) (string, error) {
	if row == nil {
		return "", errNoGroup
	}
	return text(row.Get("name"))
}

// updateStrategy answers PATCH /api/panel/strategies/{guid}: the body's
// fields over the stored rule. A field the body leaves out keeps its
// value, but a rule that is off is switched on unless the body says
// enabled:false, because the stored 0 (SQLite) is not false (BUGS DE2).
func (h *handlers) updateStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid, ok := strategyGUID(w, r)
	if !ok {
		return
	}
	existing, err := strategyByGUID(ctx, h.d.Store, guid)
	if err != nil {
		h.strategyFailure(w, r, "Update strategy error:", err)
		return
	}
	if existing == nil {
		fail(w, r, http.StatusNotFound, "devices.strategy_not_found")
		return
	}
	merged := existing.Clone()
	body := plainBody(r)
	for _, key := range body.Keys() {
		merged.Set(key, body.Get(key))
	}
	payload, err := normalizeStrategyPayload(merged)
	var updated *goapi.Object
	if err == nil {
		updated, err = updateStrategy(ctx, h.d.Store, guid, payload)
	}
	var name string
	if err == nil {
		name, err = nameOf(updated)
	}
	if err == nil {
		err = h.d.LogAction(r, "strategy_updated", "Updated strategy: "+name)
	}
	if err != nil {
		h.strategyFailure(w, r, "Update strategy error:", err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: strategyAnswer{Strategy: serializeStrategy(updated)}})
}

// strategy answers GET /api/panel/strategies/{guid}: the rule, from the
// server when the console has none, with its assignments.
func (h *handlers) strategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid, ok := strategyGUID(w, r)
	if !ok {
		return
	}
	strategy, err := strategyByGUID(ctx, h.d.Store, guid)
	if err != nil {
		h.serverError(w, r, "Get strategy detail error:", err)
		return
	}
	if strategy == nil {
		remote := h.api.GetStrategy(ctx, guid)
		if data, ok := remote.Get("data").(*goapi.Object); remote.Bool("success") && ok && data != nil && data.Bool("guid") {
			strategy = data
		}
	}
	if strategy == nil {
		fail(w, r, http.StatusNotFound, "devices.strategy_not_found")
		return
	}
	refs, err := assignmentDisplayRefs(ctx, h.d.Store, guid)
	if err != nil {
		h.serverError(w, r, "Get strategy detail error:", err)
		return
	}
	out := serializeStrategy(strategy)
	for _, key := range refs.Keys() {
		out.Set(key, refs.Get(key))
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: strategyAnswer{Strategy: out}})
}

// targets are the peers, users and groups of an assignment body.
type targets struct {
	peers, users, groups any
}

func targetsOf(body *goapi.Object) targets {
	return targets{body.Get("peers"), body.Get("users"), body.Get("groups")}
}

// any reports body.peers?.length || body.users?.length ||
// body.groups?.length.
func (t targets) any() bool {
	for _, v := range []any{t.peers, t.users, t.groups} {
		if goapi.Truthy(dig(lengthHolder(v), "length")) {
			return true
		}
	}
	return false
}

// lengthHolder is v as an object whose length the check reads: text and
// lists have one, an object may have its own.
func lengthHolder(v any) any {
	switch t := v.(type) {
	case string:
		return goapi.NewObject("length", float64(length(t)))
	case []any:
		return goapi.NewObject("length", float64(len(t)))
	}
	return v
}

// errNotIterable is the TypeError of for...of over a value that is no
// list or text.
var errNotIterable = errors.New("is not iterable")

// refs is for (const ref of v || []): the elements of a list, the
// characters of text, nothing for a falsy value; anything else fails.
func refs(v any) ([]any, error) {
	if !goapi.Truthy(v) {
		return nil, nil
	}
	switch t := v.(type) {
	case []any:
		return t, nil
	case string:
		var out []any
		for _, r := range t {
			out = append(out, string(r))
		}
		return out, nil
	}
	return nil, errNotIterable
}

// resolved are the stored keys of an assignment's targets.
type resolved struct {
	peers, users, groups []string
}

// resolveAssignKeys turns the references of an assignment body into the
// keys the assignments are stored under, one after the other; the first
// that cannot be resolved ends it (guids written before stay).
func resolveAssignKeys(ctx context.Context, db *store.DB, t targets) (resolved, error) {
	var out resolved
	steps := []struct {
		list    any
		resolve func(context.Context, *store.DB, string) (string, error)
		into    *[]string
	}{
		{t.peers, resolvePeerAssignmentKey, &out.peers},
		{t.users, resolveUserAssignmentKey, &out.users},
		{t.groups, resolveDeviceGroupAssignmentKey, &out.groups},
	}
	for _, step := range steps {
		items, err := refs(step.list)
		if err != nil {
			return out, err
		}
		for _, item := range items {
			key, err := textOr(item, "")
			if err != nil {
				return out, err
			}
			resolvedKey, err := step.resolve(ctx, db, jsTrim(key))
			if err != nil {
				return out, err
			}
			*step.into = append(*step.into, resolvedKey)
		}
	}
	return out, nil
}

// syncAssignToServer gives the server's copy the same assignment, with
// the references of the body; a failure is only logged.
func (h *handlers) syncAssignToServer(ctx context.Context, guid string, t targets) {
	payload := goapi.NewObject()
	if guid != "" {
		payload.Set("strategy", guid)
	}
	payload.Set("peers", or(t.peers, []any{}))
	payload.Set("users", or(t.users, []any{}))
	payload.Set("groups", or(t.groups, []any{}))
	h.api.AssignStrategy(ctx, payload)
}

// assignStrategy answers POST /api/panel/strategies/{guid}/assign with
// {peers, users, groups}.
func (h *handlers) assignStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid, ok := strategyGUID(w, r)
	if !ok {
		return
	}
	existing, err := strategyByGUID(ctx, h.d.Store, guid)
	if err != nil {
		h.assignFailure(w, r, err)
		return
	}
	if existing == nil {
		fail(w, r, http.StatusNotFound, "devices.strategy_not_found")
		return
	}
	t := targetsOf(plainBody(r))
	if !t.any() {
		answer(w, r, http.StatusBadRequest, failure{Error: "At least one target is required"})
		return
	}
	keys, err := resolveAssignKeys(ctx, h.d.Store, t)
	if err == nil {
		err = assign(ctx, h.d.Store, guid, keys)
	}
	var summary *goapi.Object
	if err == nil {
		h.syncAssignToServer(ctx, guid, t)
		summary, err = assignmentSummary(ctx, h.d.Store, guid)
	}
	var name string
	if err == nil {
		name, err = nameOf(existing)
	}
	if err == nil {
		err = h.d.LogAction(r, "strategy_assigned", "Assigned strategy: "+name)
	}
	if err != nil {
		h.assignFailure(w, r, err)
		return
	}
	answer(w, r, http.StatusOK, withData{Success: true, Data: summaryAnswer{Summary: summary}})
}

type summaryAnswer struct {
	Summary *goapi.Object `json:"summary"`
}

// assignFailure is the catch of the assignment: 400 with the text of a
// reference that names nothing, else 500.
func (h *handlers) assignFailure(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Assign strategy error:", err)
	if strings.Contains(err.Error(), "not found") {
		answer(w, r, http.StatusBadRequest, failure{Error: err.Error()})
		return
	}
	fail(w, r, http.StatusInternalServerError, "errors.server_error")
}

// unassignStrategy answers POST /api/panel/strategies/unassign with
// {peers, users, groups}: their direct assignments are removed. Every
// failure is 400 with its text.
func (h *handlers) unassignStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	t := targetsOf(plainBody(r))
	if !t.any() {
		answer(w, r, http.StatusBadRequest, failure{Error: "At least one target is required"})
		return
	}
	keys, err := resolveAssignKeys(ctx, h.d.Store, t)
	if err == nil {
		err = assign(ctx, h.d.Store, "", keys)
	}
	if err != nil {
		h.log.Error("Unassign strategy error:", err)
		message := err.Error()
		if message == "" {
			message = kit.T(r, "errors.server_error", nil)
		}
		answer(w, r, http.StatusBadRequest, failure{Error: message})
		return
	}
	h.syncAssignToServer(ctx, "", t)
	answer(w, r, http.StatusOK, done{Success: true})
}

// deleteStrategy answers DELETE /api/panel/strategies/{guid}: the rule and
// its assignments go, and so does the server's copy.
func (h *handlers) deleteStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid, ok := strategyGUID(w, r)
	if !ok {
		return
	}
	existing, err := strategyByGUID(ctx, h.d.Store, guid)
	if err != nil {
		h.serverError(w, r, "Delete strategy error:", err)
		return
	}
	if existing == nil {
		fail(w, r, http.StatusNotFound, "devices.strategy_not_found")
		return
	}
	if err := deleteStrategy(ctx, h.d.Store, guid); err != nil {
		h.serverError(w, r, "Delete strategy error:", err)
		return
	}
	remote := h.api.DeleteStrategy(ctx, guid)
	if !remote.Bool("success") && !strings.Contains(strings.ToLower(jsreqText(remote.Get("error"))), "not found") {
		h.log.Warn("[strategies] Go delete failed:", jsreqText(remote.Get("error")))
	}
	name, err := nameOf(existing)
	if err == nil {
		err = h.d.LogAction(r, "strategy_deleted", "Deleted strategy: "+name)
	}
	if err != nil {
		h.serverError(w, r, "Delete strategy error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// jsreqText is String(v || ”) for a value of the server.
func jsreqText(v any) string {
	s, _ := textOr(v, "")
	return s
}

// randomUUID is a version 4 UUID as crypto.randomUUID writes it.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make a guid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
