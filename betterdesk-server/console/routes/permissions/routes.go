// Package permissions answers the permissions page: the roles with their
// built-in permissions, a role's permissions in effect, and the stored
// overrides, which only super admins may change. The data is the API
// server's; the answers are those of its client (console/goapi), {success,
// data} or {success, error}. A call the API server refused is answered
// with its status and its reason, one that got no answer with 502.
package permissions

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

type handlers struct {
	api    *goapi.Client
	access *access.Service
}

// Register adds the group's routes. Changing an override takes the super
// admin role, not server.config: with that permission alone a server admin
// could grant its own role anything.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, access: d.Access}
	r := d.Router
	superAdminOnly := d.RequireRole("super_admin")
	r.Get("/api/panel/roles", h.roles, d.RequireAuth, d.RequirePermission("user.view"))
	r.Get("/api/panel/roles/{role}/permissions", h.rolePermissions, d.RequireAuth, d.RequirePermission("user.view"))
	r.Get("/api/panel/role-permissions", h.overrides, d.RequireAuth, d.RequirePermission("server.config"))
	r.Post("/api/panel/role-permissions", h.setOverride, d.RequireAuth, superAdminOnly)
	r.Delete("/api/panel/role-permissions/{role}/{permission}", h.deleteOverride, d.RequireAuth, superAdminOnly)
}

// failed is the answer to a call that did not work. The error is what the
// client reported; the key is left out when it reported none.
type failed struct {
	Success bool `json:"success"`
	Error   any  `json:"error,omitempty"`
}

// errorOf is result.error for the answer: nil when the result has no such
// key, so that the key is left out, as an undefined value is.
func errorOf(result *goapi.Object) any {
	if !result.Has("error") {
		return nil
	}
	if e := result.Get("error"); e != nil {
		return e
	}
	return jsonNull{}
}

// jsonNull is an error the result holds as null: it is sent as null.
type jsonNull struct{}

func (jsonNull) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// answerRead sends the result of a read: as it is when it worked, with
// the API server's status and reason when it refused, and with status 500
// and the error when the API server named one in a good answer.
func answerRead(w http.ResponseWriter, r *http.Request, result *goapi.Object) {
	if status, reason, refused := jsreq.Refusal(result); refused {
		kit.JSON(w, r, status, failed{Error: reason})
		return
	}
	if !result.Bool("success") {
		kit.JSON(w, r, http.StatusInternalServerError, failed{Error: errorOf(result)})
		return
	}
	kit.JSON(w, r, http.StatusOK, result)
}

// roles answers GET /api/panel/roles.
func (h *handlers) roles(w http.ResponseWriter, r *http.Request) {
	answerRead(w, r, h.api.ListRoles(r.Context()))
}

// rolePermissions answers GET /api/panel/roles/{role}/permissions: the
// built-in permissions of the role with its overrides applied.
func (h *handlers) rolePermissions(w http.ResponseWriter, r *http.Request) {
	role, err := access.SafeAPIID(r.PathValue("role"), "role")
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	answerRead(w, r, h.api.GetRolePermissions(r.Context(), role))
}

// overrides answers GET /api/panel/role-permissions, for one role with
// ?role=.
func (h *handlers) overrides(w http.ResponseWriter, r *http.Request) {
	role := jsreq.StringOr(jsreq.Query(r, "role"), "")
	answerRead(w, r, h.api.ListRolePermissionOverrides(r.Context(), role))
}

// setOverride answers POST /api/panel/role-permissions with the body
// {role, permission, granted}: it grants a permission to a role or takes
// it away, and drops the role's permissions the console has in memory.
func (h *handlers) setOverride(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	role, permission := jsreq.Prop(body, "role"), jsreq.Prop(body, "permission")
	granted, isBool := jsreq.Prop(body, "granted").(bool)
	if !goapi.Truthy(role) || !goapi.Truthy(permission) || !isBool {
		kit.Fail(w, r, http.StatusBadRequest, "Missing required fields: role, permission, granted")
		return
	}
	h.change(w, r, jsreq.String(role), jsreq.String(permission), "Failed to set permission",
		func(safeRole, safePermission string) *goapi.Object {
			return h.api.SetRolePermission(r.Context(), safeRole, safePermission, granted)
		})
}

// deleteOverride answers DELETE /api/panel/role-permissions/{role}/{permission}:
// the built-in permissions decide again.
func (h *handlers) deleteOverride(w http.ResponseWriter, r *http.Request) {
	h.change(w, r, r.PathValue("role"), r.PathValue("permission"), "Failed to delete override",
		func(safeRole, safePermission string) *goapi.Object {
			return h.api.DeleteRolePermission(r.Context(), safeRole, safePermission)
		})
}

// change checks the two names, makes the call and answers. A name that
// is no plain word answers 400, a call the API server refused its status
// and reason, and an error the API server named in a good answer 400;
// fallback stands in for a reason that is empty.
func (h *handlers) change(w http.ResponseWriter, r *http.Request, role, permission, fallback string, call func(safeRole, safePermission string) *goapi.Object) {
	safeRole, err := access.SafeAPIID(role, "role")
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	safePermission, err := access.SafeAPIID(permission, "permission")
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	result := call(safeRole, safePermission)
	if status, reason, refused := jsreq.Refusal(result); refused {
		if !goapi.Truthy(reason) {
			reason = fallback
		}
		kit.JSON(w, r, status, failed{Error: reason})
		return
	}
	if !result.Bool("success") {
		reason := result.Get("error")
		if !goapi.Truthy(reason) {
			reason = fallback
		}
		kit.JSON(w, r, http.StatusBadRequest, failed{Error: reason})
		return
	}
	h.access.InvalidateRole(safeRole)
	kit.JSON(w, r, http.StatusOK, result)
}
