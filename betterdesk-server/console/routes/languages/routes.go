// Package languages mounts the language overview of the settings: every
// language file with its coverage, and the keys a language lacks.
package languages

import "github.com/unitronix/betterdesk-server/console/kit"

// Register adds the group's routes. The handlers are those of
// console/i18n.
func Register(d *kit.Deps) {
	h := d.I18n
	r := d.Router
	r.Get("/api/panel/languages", h.PanelLanguages, d.Limits.FileAccess.Middleware, d.RequireAuth, d.RequirePermission("server.config"))
	r.Get("/api/panel/languages/{code}/missing", h.PanelMissing, d.Limits.FileAccess.Middleware, d.RequireAuth, d.RequirePermission("server.config"))
	r.Post("/api/panel/languages/{code}/fix", h.PanelFix, d.Limits.Upload.Middleware, d.RequireAuth, d.RequirePermission("server.config"))
}
