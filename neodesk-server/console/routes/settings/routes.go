// Package settings holds the routes of the settings area: the restart
// transaction, the server's facts and addresses, device scope,
// availability history, the audit log, enrollment, the directory and
// single sign-on settings, client sessions, the configuration file
// editor, the connection mode, the public endpoints, the SMTP settings,
// and the console's branding: the settings and the appearance model, the
// profiles, the uploaded backgrounds, the built-in themes and the fonts
// for anyone signed in, the stylesheet and favicon for everyone, and
// saving, importing, uploading and the font downloads for branding.edit.
// Backup and restore are in routes/settingsbackup, updates and the
// management settings in routes/settingsupdates.
//
// The branding handlers use d.Branding at every request.
package settings

import (
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes, in the order of the Node file.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("SETTINGS")}
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
	edit := d.RequirePermission("branding.edit")
	r.Get("/api/settings/branding", h.getBranding, d.RequireAuth)
	r.Get("/api/settings/appearance", h.getAppearance, d.RequireAuth)
	r.Post("/api/settings/branding", h.saveBranding, d.RequireAuth, edit)
	r.Post("/api/settings/branding/upload-logo", h.uploadLogo, d.RequireAuth, edit)
	r.Get("/api/settings/branding/backgrounds", h.listBackgrounds, d.RequireAuth)
	r.Post("/api/settings/branding/upload-background", h.uploadBackground, d.RequireAuth, edit)
	r.Post("/api/settings/branding/reset", h.resetBranding, d.RequireAuth, edit)
	r.Get("/api/settings/branding/export", h.exportBranding, d.RequireAuth, edit)
	r.Post("/api/settings/branding/import", h.importBranding, d.RequireAuth, edit)
	r.Get("/api/settings/branding/profiles", h.listProfiles, d.RequireAuth)
	r.Post("/api/settings/branding/profiles", h.createProfile, d.RequireAuth, edit)
	r.Put("/api/settings/branding/profiles/{id}", h.updateProfile, d.RequireAuth, edit)
	r.Post("/api/settings/branding/profiles/{id}/apply", h.applyProfile, d.RequireAuth, edit)
	r.Post("/api/settings/branding/profiles/{id}/duplicate", h.duplicateProfile, d.RequireAuth, edit)
	r.Delete("/api/settings/branding/profiles/{id}", h.deleteProfile, d.RequireAuth, edit)
	r.Get("/api/settings/themes", h.listThemes, d.RequireAuth)
	r.Post("/api/settings/themes/{id}/apply", h.applyTheme, d.RequireAuth, edit)
	r.Get("/api/settings/fonts", h.searchFonts, d.RequireAuth)
	r.Get("/api/settings/fonts/local", h.localFonts, d.RequireAuth)
	r.Post("/api/settings/fonts/download", h.downloadFont, d.RequireAuth, edit)
	r.Delete("/api/settings/fonts/{family}", h.deleteFont, d.RequireAuth, edit)
	r.Post("/api/settings/fonts/upload", h.uploadFont, d.RequireAuth, edit)
	r.Get("/css/branding.css", h.brandingCSS)
	r.Get("/css/theme.css", h.themeCSS)
	r.Get("/branding/favicon.svg", h.favicon)
	r.Get("/api/settings/enrollment", g.getEnrollment, d.RequireAuth, server)
	// The mode also asks the permission of the Device tokens page (A170)
	r.Put("/api/settings/enrollment", g.setEnrollment, d.RequireAuth, server, d.RequirePermission("enrollment.manage"))
	r.Get("/api/settings/ldap", g.getLDAP, d.RequireAuth, server)
	r.Put("/api/settings/ldap", g.saveLDAP, d.RequireAuth, server)
	r.Post("/api/settings/ldap/test", g.testLDAP, d.RequireAuth, server)
	r.Get("/api/settings/client-sessions", g.getClientSessions, d.RequireAuth, server)
	r.Put("/api/settings/client-sessions", g.setClientSessions, d.RequireAuth, server)
	r.Get("/api/settings/two-factor", g.getTwoFactor, d.RequireAuth, server)
	r.Put("/api/settings/two-factor", g.setTwoFactor, d.RequireAuth, server)
	r.Get("/api/settings/security-notices", g.getSecurityNotices, d.RequireAuth, server)
	r.Put("/api/settings/security-notices", g.setSecurityNotices, d.RequireAuth, server)
	r.Get("/api/settings/console-sessions", g.getConsoleSessions, d.RequireAuth, server)
	r.Put("/api/settings/console-sessions", g.setConsoleSessions, d.RequireAuth, server)
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
	r.Get("/api/settings/email/smtp", h.getSMTP, d.RequireAuth, server)
	r.Put("/api/settings/email/smtp", h.saveSMTP, d.RequireAuth, server)
	r.Post("/api/settings/email/smtp/test", h.testSMTP, d.RequireAuth, server)
}
