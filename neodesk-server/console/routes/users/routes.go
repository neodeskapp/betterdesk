// Package users holds the account routes: the account list, user groups,
// creating, changing and deleting accounts, password resets, the device
// scope of an account and the organizations it belongs to.
//
// The console and the signal server share one users table, so nothing is
// copied to the signal server: a write here is the write the server
// sees. A new account's username is text: a number, a flag, a list or an
// object is refused.
//
// Creating an account and resetting a password count against the API
// limit alone: the limit of password changes belongs to the signed-in
// account's change of its own password in the sign-in routes. A role
// change, a new name and a deleted account end every session of the
// account and revoke its access tokens; a new password does that too, but
// keeps the session of the caller who changed their own.
package users

import (
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
)

type handlers struct {
	d     *kit.Deps
	log   *kit.Logger
	links *passwordlinks.Store
	// notices mail a person about changes to how their account signs in
	notices *securitymail.Notices
	// life holds end dates, inactivity exceptions and address changes.
	life *lifecycle.Store
}

// now is the clock of the account routes.
func (h *handlers) now() time.Time { return h.life.Now() }

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := newHandlers(d)
	r := d.Router
	r.Get("/api/users", h.list, d.RequireAuth, d.RequirePermission("user.view"))
	r.Get("/api/panel/user-groups", h.userGroups, d.RequireAuth, d.RequireAnyPermission("user.view", "device.edit"))
	r.Post("/api/panel/user-groups", h.createUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Patch("/api/panel/user-groups/{guid}", h.updateUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Delete("/api/panel/user-groups/{guid}", h.deleteUserGroup, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Post("/api/users", h.create, d.RequireAuth, d.RequirePermission("user.create"))
	r.Post("/api/users/invite", h.invite, d.RequireAuth, d.RequirePermission("user.create"))
	r.Get("/api/users/invites", h.invites, d.RequireAuth, d.RequirePermission("user.view"))
	r.Post("/api/users/{id}/invite", h.resendInvite, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Delete("/api/users/{id}/invite", h.revokeInvite, d.RequireAuth, d.RequirePermission("user.create"))
	r.Post("/api/users/{id}/password-reset-link", h.sendResetLink, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Delete("/api/users/{id}/password-reset-link", h.cancelResetLink, d.RequireAuth, d.RequirePermission("user.edit"))
	// Sign-in security of other accounts
	security := d.RequirePermission("user.security")
	r.Post("/api/users/{id}/temporary-password", h.temporaryPassword, d.RequireAuth, security)
	r.Post("/api/users/require-password-change", h.requirePasswordChange, d.RequireAuth, security)
	r.Post("/api/users/{id}/unlock", h.unlock, d.RequireAuth, security)
	r.Put("/api/users/{id}/two-factor-required", h.setTwoFactorRequired, d.RequireAuth, security)
	r.Post("/api/users/{id}/two-factor/reset", h.resetTwoFactor, d.RequireAuth, security)
	r.Post("/api/users/{id}/recovery-codes/invalidate", h.invalidateRecoveryCodes, d.RequireAuth, security)
	// Before the routes of /api/users/{id}, which would take "me" for an id.
	// The signed-in account's own address (Your account); API tokens are
	// kept from writes to /api/users/me
	r.Get("/api/users/me/email", h.ownEmail, d.RequireAuth)
	r.Post("/api/users/me/email", h.changeOwnEmail, d.RequireAuth)
	r.Delete("/api/users/me/email-change", h.cancelOwnEmailChange, d.RequireAuth)
	r.Post("/api/users/me/email-change/resend", h.resendOwnEmailChange, d.RequireAuth)
	r.Delete("/api/users/{id}/email-change", h.cancelEmailChange, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Post("/api/users/{id}/email-change/resend", h.resendEmailChange, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Patch("/api/users/{id}", h.update, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Get("/api/users/{id}/effective-scope", h.effectiveScope, d.RequireAuth, d.RequirePermission("user.view"))
	r.Delete("/api/users/{id}", h.remove, d.RequireAuth, d.RequirePermission("user.delete"))
	r.Post("/api/users/{id}/reset-password", h.resetPassword, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Get("/api/users/{id}/sessions", h.userSessions, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Delete("/api/users/{id}/sessions/{sessionId}", h.endUserSession, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Post("/api/users/{id}/sessions/end-all", h.endUserSessions, d.RequireAuth, d.RequirePermission("user.edit"))
	r.Get("/api/users/{id}/organizations", h.organizations, d.RequireAuth, d.RequirePermission("user.view"))
	// The sign-in history: the account's own, then any account's (securityevents.go)
	r.Get("/api/users/me/security-events", h.ownSecurityEvents, d.RequireAuth)
	r.Get("/api/users/{id}/security-events", h.userSecurityEvents, d.RequireAuth, d.RequirePermission("user.view"))
	r.Post("/api/users/{id}/organizations", h.assignOrganization, d.RequireAuth, d.RequirePermission("org.manage_users"))
}

// newHandlers builds the group's handlers on d.
func newHandlers(d *kit.Deps) *handlers {
	return &handlers{d: d, log: d.Log, links: passwordlinks.New(d.Store, nil), life: lifecycle.New(d.Store, nil), notices: securitymail.New(d)}
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
