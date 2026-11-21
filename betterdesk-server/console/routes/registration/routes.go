// Package registration answers the registration routes
// (routes/registration.routes.js): the two routes devices call after LAN
// discovery to ask for admission and to poll for the answer, the list of
// those requests for people with their approval and rejection, the API
// server's enrollment queue, and the help requests devices raise.
//
// The old console mounted the file at the root and at /api/bd, and the
// client API port mounted it at /api/bd as well, where only the two device
// routes pass its path list. Both mounts of the console share one list of
// the names of the accounts that accepted or resolved a help request; a
// Routes value holds it, so it is built once and registered at each mount:
//
//	routes := registration.New()
//	routes.Register(d)
//	routes.Register(d.At("/api/bd"))
//
// ClientAPI registers the two device routes on the client API port.
package registration

import (
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/registrations"
)

// Routes is the group with what its mounts share.
type Routes struct {
	actors *helpActors
}

// New returns the group with an empty list of help request handlers.
func New() *Routes {
	return &Routes{actors: newHelpActors()}
}

type handlers struct {
	d      *kit.Deps
	regs   *registrations.Service
	actors *helpActors
	log    *kit.Logger
	now    func() time.Time
	// limit is the limit of device registrations a request that adds a
	// registration counts against; nil without limiters.
	limit kit.Middleware
}

func newHandlers(d *kit.Deps, actors *helpActors) *handlers {
	return &handlers{
		d:      d,
		regs:   registrations.New(d.Store, d.GoAPI),
		actors: actors,
		log:    d.Log.Child("REGISTRATION"),
		now:    time.Now,
		limit:  registerLimit(d),
	}
}

// Register adds the group's routes below the mount of d, in the order of
// the old route file. Every route but the two of devices needs a
// permission: enrollment.approve, enrollment.manage to delete a
// registration, chat.access for the help requests. A registration request
// that adds a registration counts against the limit of device
// registrations (registerRequest).
func (g *Routes) Register(d *kit.Deps) {
	h := newHandlers(d, g.actors)
	r := d.Router
	approve := d.RequirePermission("enrollment.approve")
	chat := d.RequirePermission("chat.access")

	r.Post("/register-request", h.registerRequest)
	r.Get("/register-status", h.registerStatus)

	r.Get("/api/registrations", h.list, approve)
	r.Get("/api/registrations/count", h.count, approve)
	r.Get("/api/registrations/enrollment-ui", h.enrollmentUI, approve)
	r.Get("/api/registrations/{id}", h.one, approve)
	r.Put("/api/registrations/{id}/approve", h.approve, approve)
	r.Put("/api/registrations/{id}/reject", h.reject, approve)
	r.Delete("/api/registrations/{id}", h.remove, d.RequirePermission("enrollment.manage"))

	r.Get("/api/enrollment/pending", h.enrollmentPending, approve)
	r.Get("/api/enrollment/history", h.enrollmentHistory, approve)
	r.Post("/api/enrollment/approve/{id}", h.enrollmentApprove, approve)
	r.Post("/api/enrollment/reject/{id}", h.enrollmentReject, approve)
	r.Post("/api/enrollment/clear-rejection/{id}", h.enrollmentClearRejection, approve)

	r.Get("/api/help/requests", h.helpRequests, chat)
	r.Post("/api/help/requests/{id}/accept", h.helpAccept, chat)
	r.Post("/api/help/requests/{id}/resolve", h.helpResolve, chat)
}

// ClientAPI adds the two device routes at /api/bd on the client API port
// (startRustDeskApiServer in server.js), whose Deps carry the port's own
// router as Early. The port's path list lets nothing else of the group
// through. There the routes have no session, no CSRF gate, no JSON
// content-type rule of the console and no request language; in the
// port's proxy mode no body parser runs before them, so the request route
// sees no body at all.
func ClientAPI(d *kit.Deps) {
	h := newHandlers(d, newHelpActors())
	r := d.At("/api/bd").Early
	r.Post("/register-request", h.registerRequest)
	r.Get("/register-status", h.registerStatus)
}

// registerLimit is the limit of device registrations; nil for a Deps
// without limiters.
func registerLimit(d *kit.Deps) kit.Middleware {
	if d.Limits == nil || d.Limits.Register == nil {
		return nil
	}
	return d.Limits.Register.Middleware
}
