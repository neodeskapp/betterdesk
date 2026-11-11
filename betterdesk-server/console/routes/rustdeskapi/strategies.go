package rustdeskapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
)

// Access control strategies: a name, a user group and a device group, a
// switch and a permission object, assigned to devices, accounts and
// device groups by their guids.

type strategyItem struct {
	GUID            any  `json:"guid"`
	Name            any  `json:"name"`
	UserGroupGUID   any  `json:"user_group_guid"`
	DeviceGroupGUID any  `json:"device_group_guid"`
	Enabled         bool `json:"enabled"`
	Permissions     any  `json:"permissions"`
}

// strategies answers GET /api/strategies with a bearer token. enabled is
// the stored value 1, which a PostgreSQL boolean never is (BUGS RD3).
func (h *handlers) strategies(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.allStrategies(r.Context())
	if err != nil {
		h.strategyLog.Error("Error:", err.Error())
		kit.JSON(w, r, http.StatusOK, listAnswer{Data: []any{}})
		return
	}
	list := make([]any, len(rows))
	for i, s := range rows {
		list[i] = strategyItem{
			GUID: col(s, "guid"), Name: col(s, "name"),
			UserGroupGUID: or(col(s, "user_group_guid"), ""), DeviceGroupGUID: or(col(s, "device_group_guid"), ""),
			Enabled: plain(col(s, "enabled")) == float64(1), Permissions: or(col(s, "permissions"), goapi.NewObject()),
		}
	}
	kit.JSON(w, r, http.StatusOK, listAnswer{Data: list, Total: len(rows)})
}

// strategyFields reads the body of a strategy write.
func strategyFields(body middleware.JSONObject) strategyData {
	permissions := member(body, "permissions")
	switch permissions.(type) {
	case *goapi.Object, []any, nil:
	default:
		permissions = goapi.NewObject()
	}
	return strategyData{
		name:            sanitize(member(body, "name"), maxHostnameLen),
		userGroupGUID:   sanitize(or(member(body, "user_group_guid"), ""), 64),
		deviceGroupGUID: sanitize(or(member(body, "device_group_guid"), ""), 64),
		enabled:         jsbind.Of(body.Get("enabled")),
		permissions:     permissions,
	}
}

// saveStrategy answers POST /api/strategies: with a guid an update, else
// a new strategy; the answer is the strategy's row.
func (h *handlers) saveStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := kit.Body(r)
	name, isText := member(body, "name").(string)
	if !isText || utf16Len(jsTrim(name)) == 0 {
		fail(w, r, http.StatusBadRequest, "Strategy name is required")
		return
	}
	data := strategyFields(body)
	var row *goapi.Object
	var err error
	if guid := body.Get("guid"); guid.Truthy() {
		var key string
		if key, err = h.boundGUID(ctx, guid); err == nil {
			row, err = h.store.updateStrategy(ctx, key, data, false)
		}
		if err == nil && row == nil {
			fail(w, r, http.StatusNotFound, "Strategy not found")
			return
		}
	} else {
		row, err = h.store.createStrategy(ctx, data)
	}
	if err != nil {
		h.strategyLog.Error("Create/update error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, row)
}

type deletedAnswer struct {
	Status string `json:"status"`
	GUID   string `json:"guid"`
}

type statusAnswer struct {
	Status string `json:"status"`
}

// pathGUID is sanitizeStr(req.params.guid || ”, 64).
func pathGUID(r *http.Request) string { return sanitize(r.PathValue("guid"), 64) }

// deleteStrategy answers DELETE /api/strategies/:guid.
func (h *handlers) deleteStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid := pathGUID(r)
	if guid == "" {
		fail(w, r, http.StatusBadRequest, "Strategy guid is required")
		return
	}
	existing, err := h.store.strategyByGUID(ctx, guid)
	if err == nil && existing == nil {
		fail(w, r, http.StatusNotFound, "Strategy not found")
		return
	}
	if err == nil {
		err = h.store.deleteStrategy(ctx, guid)
	}
	if err != nil {
		h.strategyLog.Error("Delete error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, deletedAnswer{Status: "deleted", GUID: guid})
}

// strategy answers GET /api/strategies/:guid with a bearer token: the
// strategy with what it is assigned to.
func (h *handlers) strategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid := pathGUID(r)
	if guid == "" {
		fail(w, r, http.StatusBadRequest, "Strategy guid is required")
		return
	}
	s, err := h.store.strategyByGUID(ctx, guid)
	if err == nil && s == nil {
		fail(w, r, http.StatusNotFound, "Strategy not found")
		return
	}
	var summary assignmentSummary
	if err == nil {
		summary, err = h.store.assignmentSummary(ctx, guid)
	}
	if err != nil {
		h.strategyLog.Error("Get error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	enabled := plain(col(s, "enabled"))
	kit.JSON(w, r, http.StatusOK, goapi.NewObject(
		"guid", col(s, "guid"), "name", col(s, "name"),
		"user_group_guid", or(col(s, "user_group_guid"), ""), "device_group_guid", or(col(s, "device_group_guid"), ""),
		"enabled", enabled == float64(1) || enabled == true,
		"permissions", or(col(s, "permissions"), goapi.NewObject()),
		"peer_count", float64(summary.PeerCount), "user_count", float64(summary.UserCount), "device_group_count", float64(summary.DeviceGroupCount),
		"peers", summary.Peers, "users", summary.Users, "groups", summary.Groups,
	))
}

// iterationError is the TypeError of for...of over a value that is no
// list or text.
func iterationError(v any) error {
	switch x := v.(type) {
	case float64:
		return fmt.Errorf("number %s is not iterable (cannot read property Symbol(Symbol.iterator))", jsbind.NumberString(x))
	case bool:
		return fmt.Errorf("boolean %t is not iterable (cannot read property Symbol(Symbol.iterator))", x)
	}
	return fmt.Errorf("object is not iterable (cannot read property Symbol(Symbol.iterator))")
}

// refs is for (const ref of body[name] || []): the items of a list, the
// characters of a text; anything else throws.
func refs(body middleware.JSONObject, name string) ([]any, error) {
	v := or(member(body, name), []any{})
	switch x := v.(type) {
	case []any:
		return x, nil
	case string:
		return iterate(x), nil
	}
	return nil, iterationError(v)
}

// resolveRefs is resolveRustDeskStrategyRefs: the guids of the devices,
// accounts and device groups the body names, in that order; the first
// that cannot be resolved fails all.
func (h *handlers) resolveRefs(ctx context.Context, body middleware.JSONObject) (strategyTargets, error) {
	var targets strategyTargets
	resolve := func(name string, lookup func(ctx context.Context, ref string) (string, error), into *[]string) error {
		items, err := refs(body, name)
		if err != nil {
			return err
		}
		*into = []string{}
		for _, item := range items {
			text, err := str(or(item, ""))
			if err != nil {
				return err
			}
			key, err := lookup(ctx, text)
			if err != nil {
				return err
			}
			*into = append(*into, key)
		}
		return nil
	}
	if err := resolve("peers", h.store.resolvePeerAssignmentKey, &targets.peers); err != nil {
		return targets, err
	}
	if err := resolve("users", h.d.Authn.ResolveUserAssignmentKey, &targets.users); err != nil {
		return targets, err
	}
	if err := resolve("groups", h.d.DeviceGroups.ResolveDeviceGroupAssignmentKey, &targets.groups); err != nil {
		return targets, err
	}
	return targets, nil
}

// lengthOf is value?.length as a truthy test: the length of a list or a
// text, a member named length of an object.
func lengthOf(v any) bool {
	switch x := v.(type) {
	case []any:
		return len(x) > 0
	case string:
		return x != ""
	case *goapi.Object:
		return truthy(x.Get("length"))
	}
	return false
}

// assignStrategy answers POST /api/strategies/assign. Every failure is a
// 400 with its text.
func (h *handlers) assignStrategy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := kit.Body(r)
	if !(lengthOf(member(body, "peers")) || lengthOf(member(body, "users")) || lengthOf(member(body, "groups"))) {
		fail(w, r, http.StatusBadRequest, "At least one target is required")
		return
	}
	strategy := sanitize(or(member(body, "strategy"), ""), 64)
	targets, err := h.resolveRefs(ctx, body)
	if err == nil {
		err = h.store.assignStrategy(ctx, strategy, targets)
	}
	if err != nil {
		message := jsbind.Message(err)
		h.strategyLog.Error("Assign error:", message)
		if message == "" {
			message = "Assign failed"
		}
		fail(w, r, http.StatusBadRequest, message)
		return
	}
	kit.JSON(w, r, http.StatusOK, statusAnswer{Status: "ok"})
}

// strategyStatus answers PUT /api/strategies/:guid/status. The body is
// meant to be the JSON true or false, which the body parser refuses; so
// enabled is always false (BUGS RD2).
func (h *handlers) strategyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	guid := pathGUID(r)
	if guid == "" {
		fail(w, r, http.StatusBadRequest, "Strategy guid is required")
		return
	}
	row, err := h.store.updateStrategy(ctx, guid, strategyData{enabled: jsbind.Bool(false)}, true)
	if err == nil && row == nil {
		err = fmt.Errorf("strategy not found")
	}
	if err != nil {
		message := jsbind.Message(err)
		h.strategyLog.Error("Status error:", message)
		status := http.StatusInternalServerError
		if strings.Contains(message, "not found") {
			status = http.StatusNotFound
		}
		if message == "" {
			message = msgServerError
		}
		fail(w, r, status, message)
		return
	}
	kit.JSON(w, r, http.StatusOK, statusAnswer{Status: "ok"})
}
