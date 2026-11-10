// Package i18n mounts the language routes under /api/i18n: the list of
// languages, a language's texts, the visitor's choice, and upload,
// validation and removal of language files.
package i18n

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// Register adds the group's routes. The handlers are those of
// console/i18n; this group gives them what belongs to a request: the
// signed-in account's stored choice and the audit log.
func Register(d *kit.Deps) {
	h := d.I18n
	h.SetPreferredLanguage = func(r *http.Request, code string) error {
		return setPreferredLanguage(d, r, code)
	}
	h.Audit = d.LogAction

	r := d.Router.Mount("/api/i18n")
	r.Get("/languages", h.Languages)
	r.Get("/translations/{code}", h.Translations)
	r.Post("/set/{code}", h.Set)
	r.Get("/validate/{code}", h.Validate, d.RequireAuth)
	r.Post("/upload", h.Upload, d.RequireAuth, d.RequirePermission("server.config"))
	r.Delete("/{code}", h.Delete, d.RequireAuth, d.RequirePermission("server.config"))
}

// setPreferredLanguage remembers a language choice for the signed-in
// account: in its users row and in the session. Signed out it does
// nothing. The session gets the choice also when the row cannot be
// written; the error is for the log.
func setPreferredLanguage(d *kit.Deps, r *http.Request, code string) error {
	id := kit.UserID(r)
	if id == 0 {
		return nil
	}
	err := updateUserLanguage(r.Context(), d.Store, id, code)
	s := kit.Session(r)
	if user := s.User(); user != nil {
		user.PreferredLanguage = code
		s.Set("user", *user)
	} else {
		s.Set("user", map[string]string{"preferred_language": code})
	}
	return err
}
