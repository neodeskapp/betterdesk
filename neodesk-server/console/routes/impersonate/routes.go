// Package impersonate holds the routes of "Sign in as": an admin with
// user.impersonate views the console as an account with fewer rights, to
// reproduce a problem or check what a role can do. It is read-only; the
// rules are package impersonation's, the session package session's.
//
//	GET  /api/users/{id}/impersonation  whether the caller may, and what the start asks for
//	POST /api/users/{id}/impersonate    start: the caller's password (and code), a reason
//	GET  /api/impersonation             the impersonation of this browser, if any
//	POST /api/impersonation/extend      15 minutes more, once
//	POST /api/impersonation/stop        back to the admin's own session
//	GET  /api/settings/impersonation    the notice mail switch
//	PUT  /api/settings/impersonation
//
// An API token can't start one, and the command line has no command for
// these routes but the settings.
package impersonate

import (
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/impersonation"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type handlers struct {
	d   *kit.Deps
	svc *impersonation.Service
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, svc: impersonation.New(d), log: d.Log.Child("IMPERSONATION")}
	r := d.Router
	may := d.RequirePermission(auth.PermUserImpersonate)
	r.Get("/api/users/{id}/impersonation", h.check, d.RequireAuth, may)
	// Starting reads the users section, it changes no account
	r.SectionLevel("users", access.LevelRead).Post("/api/users/{id}/impersonate", h.start, d.RequireAuth, may)
	r.Get("/api/impersonation", h.status, d.RequireAuth)
	r.Post("/api/impersonation/extend", h.extend, d.RequireAuth)
	r.Post("/api/impersonation/stop", h.stop, d.RequireAuth)
	server := d.RequirePermission(auth.PermServerConfig)
	r.Get("/api/settings/impersonation", h.settings, d.RequireAuth, server)
	r.Put("/api/settings/impersonation", h.saveSettings, d.RequireAuth, server)
}

// failure is {"success":false,"error":...,"code":...}.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	kit.JSON(w, r, status, failure{Error: message, Code: code})
}

// failKey answers with the text of a language key, english when the
// language files have none.
func (h *handlers) failKey(w http.ResponseWriter, r *http.Request, status int, code, key, english string, vars ...any) {
	fail(w, r, status, code, h.text(r, key, english, vars...))
}

// text is the request language's text for key, english (with the vars
// filled in) when the files have none.
func (h *handlers) text(r *http.Request, key, english string, vars ...any) string {
	refusal := &impersonation.Refusal{Key: key, English: english, Vars: kit.Vars(vars...)}
	return refusal.Text(kit.Lang(r), h.d.I18n.Bundle)
}

func (h *handlers) refuse(w http.ResponseWriter, r *http.Request, status int, why *impersonation.Refusal) {
	fail(w, r, status, why.Code, why.Text(kit.Lang(r), h.d.I18n.Bundle))
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// pathID is the {id} of the path as a positive number.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
