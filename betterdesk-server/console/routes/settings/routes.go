// Package settings holds the routes of the settings area. So far these are
// the ones that only read the branding: the settings and the appearance
// model, the profiles, the uploaded backgrounds, the built-in themes and
// the fonts for anyone signed in, and the stylesheet and favicon for
// everyone. Every other route of routes/settings.routes.js is still
// answered by the Node console, the branding writes among them.
package settings

import (
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	d        *kit.Deps
	log      *kit.Logger
	branding *branding.Service
}

// Register adds the group's routes, in the order of the Node file.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("SETTINGS"), branding: d.Branding}
	r := d.Router
	r.Get("/api/settings/branding", h.getBranding, d.RequireAuth)
	r.Get("/api/settings/appearance", h.getAppearance, d.RequireAuth)
	r.Get("/api/settings/branding/backgrounds", h.listBackgrounds, d.RequireAuth)
	r.Get("/api/settings/branding/profiles", h.listProfiles, d.RequireAuth)
	r.Get("/api/settings/themes", h.listThemes, d.RequireAuth)
	r.Get("/api/settings/fonts", h.searchFonts, d.RequireAuth)
	r.Get("/api/settings/fonts/local", h.localFonts, d.RequireAuth)
	r.Get("/css/branding.css", h.brandingCSS)
	r.Get("/css/theme.css", h.themeCSS)
	r.Get("/branding/favicon.svg", h.favicon)
}
