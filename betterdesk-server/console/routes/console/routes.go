// Package console holds the endpoints the web app itself needs before it
// renders: the boot script that runs in the page's head, the bootstrap of
// a signed-in account, the strings of a language and the supporters list
// of the Help panel.
package console

import "github.com/unitronix/betterdesk-server/console/kit"

type handlers struct {
	d *kit.Deps
	// appName is the console's configured name, the bootstrap's appName
	// when the branding has none.
	appName string
	// appVersion is the product version, read once at start.
	appVersion string
}

// configuredAppName is the name the old console's configuration gave
// itself.
const configuredAppName = "BetterDesk Console"

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, appName: configuredAppName, appVersion: d.Config.Version}
	r := d.Router
	r.Get("/console-boot.js", h.bootScript)
	r.Get("/api/console/strings/{code}", d.I18n.Strings)
	r.Get("/api/console/bootstrap", h.bootstrap, d.RequireAuth)
	r.Get("/api/console/supporters", h.supporters, d.RequireAuth)
}
