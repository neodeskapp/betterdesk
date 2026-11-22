// Package pages answers the old page addresses that are not pages of the
// web app any more (routes/pages.routes.js): /tenants sends people to
// /organizations, the page tenants were merged into, which checks access
// itself.
package pages

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// Register adds the group's route.
func Register(d *kit.Deps) {
	d.Router.Get("/tenants", tenants)
}

// tenants answers GET /tenants with a redirect to /organizations.
func tenants(w http.ResponseWriter, r *http.Request) {
	kit.Redirect(w, r, "/organizations")
}
