// Package users holds the account routes: the account list, user groups,
// creating, changing and deleting accounts, password resets, the device
// scope of an account and the organizations it belongs to.
//
// The console and the signal server share one users table, so nothing is
// copied to the signal server: a write here is the write the server
// sees. Values of a request go to the database as the request gave them,
// as they always did: a username that is a number or a list passes the
// username rule by its text and is stored the way each database's driver
// stores such a value.
//
// Creating an account and resetting a password count against the limit
// of password changes (three in five minutes per address), which the
// password change of the sign-in routes shares. A role change, a new
// password, a new name and a deleted account end no session.
package users

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log}
	r := d.Router
	r.Get("/api/users", h.list, d.RequireAuth, d.RequirePermission("user.view"))
	r.Get("/api/panel/user-groups", h.userGroups, d.RequireAuth, d.RequireAnyPermission("user.view", "device.edit"))
	r.Post("/api/panel/user-groups", h.createUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Patch("/api/panel/user-groups/{guid}", h.updateUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Delete("/api/panel/user-groups/{guid}", h.deleteUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Post("/api/users", h.create, d.RequireAuth, d.RequirePermission("user.create"), d.Limits.PasswordChange.Middleware)
	r.Patch("/api/users/{id}", h.update, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Get("/api/users/{id}/effective-scope", h.effectiveScope, d.RequireAuth, d.RequirePermission("user.view"))
	r.Delete("/api/users/{id}", h.remove, d.RequireAuth, d.RequirePermission("user.delete"))
	r.Post("/api/users/{id}/reset-password", h.resetPassword, d.RequireAuth, d.RequirePermission("user.edit"), d.Limits.PasswordChange.Middleware)
	r.Get("/api/users/{id}/organizations", h.organizations, d.RequireAuth, d.RequirePermission("user.view"))
	r.Post("/api/users/{id}/organizations", h.assignOrganization, d.RequireAuth, d.RequirePermission("org.manage_users"))
}

// answer sends a JSON answer written as JSON.stringify writes it.
func answer(w http.ResponseWriter, r *http.Request, status int, v any) {
	goapipath.WriteJSON(w, r, status, v)
}

// failure is {"success":false,"error":...}.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// fail answers {"success":false,"error":message}.
func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	answer(w, r, status, failure{Error: message})
}

// failKey answers {"success":false,"error":<text of key>}.
func failKey(w http.ResponseWriter, r *http.Request, status int, key string) {
	fail(w, r, status, kit.T(r, key, nil))
}

// serverError logs what failed and answers 500 with the request
// language's text for a server error.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	failKey(w, r, http.StatusInternalServerError, "errors.server_error")
}

// done is {"success":true}.
type done struct {
	Success bool `json:"success"`
}

// invalidID answers the refusal of an id that is no positive number.
func invalidID(w http.ResponseWriter, r *http.Request) {
	fail(w, r, http.StatusBadRequest, "Invalid user ID")
}
