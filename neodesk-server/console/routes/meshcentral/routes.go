// Package meshcentral holds the console's MeshCentral routes: the state
// of the mesh server, its groups, the agent settings file, and the
// tunnels, power actions, commands and recordings of a device. Every
// route hands the request to the API server and passes its answer on.
// The routes of one device check that the account may see it
// (kit.RequireDeviceInScope, after the permission), and an account
// outside the admin roles gets the recordings of the devices it sees.
package meshcentral

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	api  *goapi.Client
	deps *kit.Deps
	// desktopForSession is the desktop route for someone who brings no
	// link: the session and the permission are checked first.
	desktopForSession http.Handler
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, deps: d}
	scope := d.RequireDeviceInScope
	h.desktopForSession = d.RequireAuth(d.RequirePermission("device.connect")(scope(http.HandlerFunc(h.desktopTunnel))))
	config := d.RequirePermission("server.config")
	connect := d.RequirePermission("device.connect")
	view := d.RequirePermission("device.view")
	page := d.Limits.RDClientPage.Middleware

	r := d.Router
	r.Get("/api/mesh/status", h.status, d.RequireAuth)
	r.Get("/api/mesh/server-id", h.get("/mesh/server-id"), d.RequireAuth, config)
	r.Get("/api/mesh/groups", h.get("/mesh/groups"), d.RequireAuth, config)
	r.Post("/api/mesh/groups", h.saveGroups, d.RequireAuth, config)
	r.Get("/api/mesh/download.msh", h.download, d.RequireAuth, config)
	r.Post("/api/mesh/devices/{id}/desktop", h.desktop, page)
	r.Post("/api/mesh/devices/{id}/terminal", h.device("terminal", false), d.RequireAuth, d.RequirePermission("mesh.terminal"), scope, h.needsFeature("terminal"))
	r.Post("/api/mesh/devices/{id}/files", h.device("files", false), d.RequireAuth, d.RequirePermission("mesh.files"), scope, h.needsFeature("file_transfer"))
	r.Post("/api/mesh/devices/{id}/share", h.share, d.RequireAuth, connect, scope)
	r.Get("/api/mesh/share/validate", h.validateShare, page)
	r.Post("/api/mesh/devices/{id}/tcp", h.device("tcp", true), d.RequireAuth, connect, scope, h.needsFeature("tunnel"))
	r.Post("/api/mesh/devices/{id}/udp", h.device("udp", true), d.RequireAuth, connect, scope, h.needsFeature("tunnel"))
	r.Post("/api/mesh/devices/{id}/power", h.device("power", true), d.RequireAuth, d.RequirePermission("mesh.power"), scope, h.powerNeedsReboot)
	r.Post("/api/mesh/devices/{id}/group", h.device("group", true), d.RequireAuth, config, scope)
	r.Get("/api/mesh/recordings", h.recordings("/mesh/recordings"), d.RequireAuth, view)
	r.Get("/api/mesh/recordings/{id}", h.recording, d.RequireAuth, connect)
	r.Get("/api/session/recordings", h.recordings("/session/recordings"), d.RequireAuth, view)
	// exec runs a shell command: the terminal's permission
	r.Post("/api/mesh/devices/{id}/exec", h.device("exec", true), d.RequireAuth, d.RequirePermission("mesh.terminal"), scope, h.needsFeature("terminal"))
}
