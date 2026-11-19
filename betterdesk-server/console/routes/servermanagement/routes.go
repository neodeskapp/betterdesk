// Package servermanagement answers the routes of the server management
// page (routes/server-management.routes.js, beta): the runtime and the
// machine, the file browser and BetterDesk's system services. Every route
// needs server.config. The work is done by package console/servermgmt,
// which also has the terminal socket.
package servermanagement

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/servermgmt"
)

// permission is what every route of the group needs.
const permission = "server.config"

type handlers struct {
	d   *kit.Deps
	svc *servermgmt.Service
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, svc: d.ServerMgmt, log: d.Log.Child("SERVER-MGMT")}
	auth := []kit.Middleware{d.RequireAuth, d.RequirePermission(permission)}
	r := d.Router
	r.Get("/api/server-management/info", h.info, auth...)
	r.Get("/api/server-management/resources", h.resources, auth...)
	r.Get("/api/server-management/files", h.list, auth...)
	r.Get("/api/server-management/files/read", h.read, auth...)
	r.Post("/api/server-management/files/write", h.write, auth...)
	r.Post("/api/server-management/files/mkdir", h.mkdir, auth...)
	r.Post("/api/server-management/files/rename", h.rename, auth...)
	r.Post("/api/server-management/files/delete", h.delete, auth...)
	r.Get("/api/server-management/services", h.services, auth...)
	r.Post("/api/server-management/services/{name}/{action}", h.control, auth...)
}

// audit writes an audit entry: the session's user and the client's
// address under TRUST_PROXY. A failure does not fail the request.
func (h *handlers) audit(r *http.Request, action, details string) {
	_ = h.d.Audit.Write(r.Context(), kit.UserID(r), action, details, kit.ClientIP(r))
}

// fail answers 400 {"success":false,"error":message}, or with fallback
// when the message is empty.
func fail(w http.ResponseWriter, r *http.Request, status int, err error, fallback string) {
	message := err.Error()
	if message == "" {
		message = fallback
	}
	kit.Fail(w, r, status, message)
}
