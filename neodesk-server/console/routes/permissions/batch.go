package permissions

import (
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// batchChange is one change of a batch: grant (true), take away (false)
// or back to the role's default (nil, the override is deleted).
type batchChange struct {
	permission string
	granted    *bool
}

// batchError is a batch the route refuses before it writes anything.
type batchError string

func (e batchError) Error() string { return string(e) }

// parseBatch reads and checks {role, changes: [{permission, granted}]}:
// every change is checked before the first write, so a batch is written
// whole or not at all.
func parseBatch(r *http.Request) (string, []batchChange, error) {
	body, err := goapi.Parse(middleware.BodyValue(r).Raw())
	if err != nil {
		body = goapi.NewObject()
	}
	role, list := jsreq.Prop(body, "role"), jsreq.Prop(body, "changes")
	items, isList := list.([]any)
	if !goapi.Truthy(role) || !isList || len(items) == 0 {
		return "", nil, batchError("Missing required fields: role, changes")
	}
	name, isText := role.(string)
	if !isText {
		return "", nil, batchError("Invalid role")
	}
	safeRole, err := access.SafeAPIID(name, "role")
	if err != nil || !auth.ValidRole(safeRole) {
		return "", nil, batchError("Invalid role")
	}
	if auth.IsSuperAdminRole(safeRole) {
		return "", nil, batchError("Cannot override super admin permissions")
	}
	fixed := map[string]bool{}
	for _, f := range access.FixedPermissions(safeRole) {
		fixed[f.Permission] = true
	}
	seen := map[string]bool{}
	changes := make([]batchChange, 0, len(items))
	for _, item := range items {
		change, isObject := item.(*goapi.Object)
		permission, isText := change.Get("permission").(string)
		if !isObject || !isText {
			return "", nil, batchError("Each change needs a permission and granted")
		}
		permission = strings.TrimSpace(permission)
		if !auth.ValidPermission(permission) {
			return "", nil, batchError("Unknown permission: " + permission)
		}
		c := batchChange{permission: permission}
		switch granted := change.Get("granted").(type) {
		case bool:
			c.granted = &granted
		case nil:
			if !change.Has("granted") {
				return "", nil, batchError("granted must be true, false or null: " + permission)
			}
		default:
			return "", nil, batchError("granted must be true, false or null: " + permission)
		}
		if fixed[permission] {
			return "", nil, batchError(permission + " is fixed for the role " + safeRole + " and cannot be changed")
		}
		if seen[permission] {
			return "", nil, batchError(permission + " is changed twice")
		}
		seen[permission] = true
		changes = append(changes, c)
	}
	return safeRole, changes, nil
}

// batch answers POST /api/panel/role-permissions/batch with the body
// {role, changes: [{permission, granted: true|false|null}]}. It reads the
// role's stored overrides, writes the changes one by one and answers
// {previous, permissions}: previous holds, for each change, the override
// stored before (null for none), so that sending it back as changes
// undoes the batch; permissions are the role's permissions in effect
// after. When a write fails, the writes before it are put back to what
// was stored and the failure is answered. One audit entry names the role
// and every change.
func (h *handlers) batch(w http.ResponseWriter, r *http.Request) {
	role, changes, err := parseBatch(r)
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	stored := h.api.ListRolePermissionOverrides(r.Context(), role)
	if failedCall(w, r, stored, "Failed to read role permissions") {
		return
	}
	before := map[string]*bool{}
	if list, isList := goapi.Dig(stored, "data", "overrides").([]any); isList {
		for _, entry := range list {
			o, isObject := entry.(*goapi.Object)
			permission, isText := o.Get("permission").(string)
			granted, isBool := o.Get("granted").(bool)
			if isObject && isText && isBool && o.Get("role") == role {
				before[permission] = &granted
			}
		}
	}
	previous := make([]batchChange, len(changes))
	for i, c := range changes {
		previous[i] = batchChange{permission: c.permission, granted: before[c.permission]}
	}
	for i, c := range changes {
		result := h.write(r, role, c)
		if !callFailed(result) {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if undo := h.write(r, role, previous[j]); callFailed(undo) {
				h.log.Warn("Undoing a permission change of role", role, "failed:", previous[j].permission)
			}
		}
		h.access.InvalidateRole(role)
		failedCall(w, r, result, "Failed to set permission")
		return
	}
	h.access.InvalidateRole(role)

	if err := h.deps.LogAction(r, "role_permissions_changed", "Role "+role+": "+describe(changes)); err != nil {
		h.log.Warn("Audit entry of a permission change failed:", err)
	}
	data := goapi.NewObject("previous", changeList(previous), "permissions", nil)
	if after := h.api.GetRolePermissions(r.Context(), role); !callFailed(after) {
		data.Set("permissions", goapi.Dig(after, "data", "permissions"))
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", data))
}

// write sends one change to the API server: a set, or a delete for nil.
func (h *handlers) write(r *http.Request, role string, c batchChange) *goapi.Object {
	if c.granted == nil {
		return h.api.DeleteRolePermission(r.Context(), role, c.permission)
	}
	return h.api.SetRolePermission(r.Context(), role, c.permission, *c.granted)
}

// callFailed reports whether the API server refused a call or named an
// error in its answer.
func callFailed(result *goapi.Object) bool {
	_, _, refused := jsreq.Refusal(result)
	return refused || !result.Bool("success")
}

// failedCall answers a failed call as change does, with fallback for an
// empty reason, and reports whether it answered.
func failedCall(w http.ResponseWriter, r *http.Request, result *goapi.Object, fallback string) bool {
	if status, reason, refused := jsreq.Refusal(result); refused {
		if !goapi.Truthy(reason) {
			reason = fallback
		}
		kit.JSON(w, r, status, failed{Error: reason})
		return true
	}
	if !result.Bool("success") {
		reason := result.Get("error")
		if !goapi.Truthy(reason) {
			reason = fallback
		}
		kit.JSON(w, r, http.StatusBadRequest, failed{Error: reason})
		return true
	}
	return false
}

// changeList is changes as the answer sends them.
func changeList(changes []batchChange) []any {
	out := make([]any, 0, len(changes))
	for _, c := range changes {
		var granted any
		if c.granted != nil {
			granted = *c.granted
		}
		out = append(out, goapi.NewObject("permission", c.permission, "granted", granted))
	}
	return out
}

// describe is the audit text of the changes: "device.edit granted,
// device.view back to the default".
func describe(changes []batchChange) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		switch {
		case c.granted == nil:
			parts = append(parts, c.permission+" back to the default")
		case *c.granted:
			parts = append(parts, c.permission+" granted")
		default:
			parts = append(parts, c.permission+" taken away")
		}
	}
	return strings.Join(parts, ", ")
}
