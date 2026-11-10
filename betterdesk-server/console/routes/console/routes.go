// Package console holds the endpoints the web app itself needs. So far
// that is the strings of a language; the boot script, the bootstrap and
// the supporters list are still answered by the Node console.
package console

import "github.com/unitronix/betterdesk-server/console/kit"

// Register adds the group's routes.
func Register(d *kit.Deps) {
	d.Router.Get("/api/console/strings/{code}", d.I18n.Strings)
}
