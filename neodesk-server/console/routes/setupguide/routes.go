// Package setupguide holds the routes of the first-install guide on the
// overview (package console/setupguide): its status for the signed-in
// account, and hiding and bringing it back. The guide is about the
// server, so every route needs server.config; an account without it gets
// the guard's 403 and learns nothing of the server's state.
//
//	GET  /api/setup-guide       {"visible": bool, "steps": [{"id": "password", "done": false}, ...]}
//	POST /api/setup-guide/show  opens the guide for the account, answers the status
//	POST /api/setup-guide/hide  closes it for the account, answers the status
package setupguide

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	guide "github.com/neodeskapp/neodesk/neodesk-server/console/setupguide"
)

type handlers struct {
	guide *guide.Service
	log   *kit.Logger
}

// Register adds the group's routes on the guide service of d.
func Register(d *kit.Deps) {
	h := &handlers{guide: d.SetupGuide, log: d.Log.Child("SETUP-GUIDE")}
	admin := d.RequirePermission("server.config")
	r := d.Router
	r.Get("/api/setup-guide", h.status, d.RequireAuth, admin)
	r.Post("/api/setup-guide/show", h.open(true), d.RequireAuth, admin)
	r.Post("/api/setup-guide/hide", h.open(false), d.RequireAuth, admin)
}

func viewer(r *http.Request) guide.Viewer {
	return guide.Viewer{
		UserID: kit.UserID(r),
		Can:    func(permission string) bool { return kit.HasPermission(r, permission) },
		Secure: kit.Secure(r),
	}
}

func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.guide.Status(r.Context(), viewer(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, st)
}

func (h *handlers) open(open bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h.guide.SetOpen(r.Context(), kit.UserID(r), open); err != nil {
			h.fail(w, r, err)
			return
		}
		h.status(w, r)
	}
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Setup guide error:", err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
