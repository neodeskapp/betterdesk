// Package settings holds the routes of the settings area: the restart
// transaction, the server's facts and addresses, device scope,
// availability history, the audit log, enrollment, the directory and
// single sign-on settings, client sessions, the configuration file
// editor, the connection mode, the public endpoints, and the routes that
// only read the branding (the settings and the appearance model, the
// profiles, the uploaded backgrounds, the built-in themes and the fonts
// for anyone signed in, and the stylesheet and favicon for everyone). The
// branding writes, backup and restore, updates and server management and
// the mail settings are still answered by the Node console.
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
	g := newGeneral(d, h.log)
	r := d.Router
	server := d.RequirePermission("server.config")
	r.Get("/api/settings/restart-status", g.restartStatus)
	r.Get("/api/settings/restart/pending", g.restartPending, d.RequireAuth, server)
	r.Post("/api/settings/restart/cancel", g.restartCancel, d.RequireAuth, server)
	r.Post("/api/settings/restart/confirm", g.restartConfirm, d.RequireAuth, server)
	r.Post("/api/settings/restart/complete", g.restartComplete, d.RequireAuth, server)
	r.Get("/api/settings/info", g.info, d.RequireAuth)
	r.Get("/api/settings/server-info", g.serverInfo, d.RequireAuth)
	r.Get("/api/settings/device-scope", g.getDeviceScope, d.RequireAuth, server)
	r.Post("/api/settings/device-scope", g.setDeviceScope, d.RequireAuth, server)
	r.Get("/api/settings/availability-history", g.getAvailabilityHistory, d.RequireAuth, server)
	r.Post("/api/settings/availability-history", g.setAvailabilityHistory, d.RequireAuth, server)
	r.Get("/api/settings/audit", g.audit, d.RequireAuth, d.RequirePermission("audit.view"))
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
	r.Get("/api/settings/enrollment", g.getEnrollment, d.RequireAuth, server)
	r.Put("/api/settings/enrollment", g.setEnrollment, d.RequireAuth, server)
	r.Get("/api/settings/ldap", g.getLDAP, d.RequireAuth, server)
	r.Put("/api/settings/ldap", g.saveLDAP, d.RequireAuth, server)
	r.Post("/api/settings/ldap/test", g.testLDAP, d.RequireAuth, server)
	r.Get("/api/settings/client-sessions", g.getClientSessions, d.RequireAuth, server)
	r.Put("/api/settings/client-sessions", g.setClientSessions, d.RequireAuth, server)
	r.Get("/api/settings/oidc", g.getOIDC, d.RequireAuth, server)
	r.Put("/api/settings/oidc", g.saveOIDC, d.RequireAuth, server)
	r.Post("/api/settings/oidc/test", g.testOIDC, d.RequireAuth, server)
	r.Get("/api/settings/advanced/files", g.listFiles, d.RequireAuth, server)
	r.Get("/api/settings/advanced/files/{id}", g.readFile, d.RequireAuth, server)
	r.Put("/api/settings/advanced/files/{id}", g.writeFile, d.RequireAuth, server)
	r.Post("/api/settings/advanced/restart", g.advancedRestart, d.RequireAuth, server)
	r.Get("/api/settings/connection-mode", g.getConnectionMode, d.RequireAuth, server)
	r.Put("/api/settings/connection-mode", g.setConnectionMode, d.RequireAuth, server)
	r.Post("/api/settings/connection-mode/restart", g.connectionModeRestart, d.RequireAuth, server)
	r.Get("/api/settings/public-endpoints", g.getPublicEndpoints, d.RequireAuth, server)
	r.Put("/api/settings/public-endpoints", g.setPublicEndpoints, d.RequireAuth, server)
}
