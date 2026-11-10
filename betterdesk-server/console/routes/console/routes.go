// Package console holds the endpoints the web app itself needs before it
// renders: the boot script that runs in the page's head, the bootstrap of
// a signed-in account, the strings of a language and the supporters list
// of the Help panel.
package console

import (
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

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
	h := &handlers{d: d, appName: configuredAppName, appVersion: productVersion(d)}
	r := d.Router
	r.Get("/console-boot.js", h.bootScript)
	r.Get("/api/console/strings/{code}", d.I18n.Strings)
	r.Get("/api/console/bootstrap", h.bootstrap, d.RequireAuth)
	r.Get("/api/console/supporters", h.supporters, d.RequireAuth)
}

// productVersion is the product's version as the old console read it at
// start: the VERSION file above the console's folder, the one in it, its
// package.json, else 0.0.0.
func productVersion(d *kit.Deps) string {
	dir := d.Config.ConsoleDir
	return serverinfo.ReadProductVersion(serverinfo.OSFS{}, filepath.Join(dir, ".."), dir, serverinfo.DefaultVersionFallback)
}
