// Package dashboards holds the routes of the overview's layouts (package
// console/dashboards). Every account that may sign in reads and changes
// its own boards, and only its own: those routes take the account from
// the session and need no permission. While an admin views the console as
// another account (Sign in as), they are the admin's own boards
// (kit.ActorID); the writes stay refused then, like every write of an
// impersonation.
//
// The shared boards are published by an account with server.config. An
// account sees those for every account and those that name its role; an
// account that may publish sees all of them, to edit them.
//
//	GET    /api/dashboards         {"own": {...} or null, "shared": [...], "canPublish": false}
//	PUT    /api/dashboards         body: the account's layout; answers it as stored
//	DELETE /api/dashboards         back to the default; answers {"own": null}
//	PUT    /api/dashboards/shared  body: {"boards": [...]}; answers them as stored
package dashboards

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/dashboards"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// publish is the permission that publishes shared boards.
const publish = "server.config"

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("DASHBOARDS")}
	r := d.Router
	r.Get("/api/dashboards", h.get, d.RequireAuth)
	r.Put("/api/dashboards", h.put, d.RequireAuth)
	r.Delete("/api/dashboards", h.reset, d.RequireAuth)
	r.Put("/api/dashboards/shared", h.putShared, d.RequireAuth, d.RequirePermission(publish))
}

type answer struct {
	Own        *dashboards.Own     `json:"own"`
	Shared     []dashboards.Shared `json:"shared"`
	CanPublish bool                `json:"canPublish"`
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	own, err := dashboards.Load(ctx, h.d.Store, kit.ActorID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	shared, err := dashboards.LoadShared(ctx, h.d.Store)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	canPublish := kit.HasPermission(r, publish)
	kit.OK(w, r, answer{
		Own:        own,
		Shared:     dashboards.VisibleTo(shared, kit.Access(r).Role, canPublish),
		CanPublish: canPublish,
	})
}

func (h *handlers) put(w http.ResponseWriter, r *http.Request) {
	own, err := dashboards.Parse(middleware.BodyValue(r).Raw())
	if h.refused(w, r, err) {
		return
	}
	if err := dashboards.Save(r.Context(), h.d.Store, kit.ActorID(r), own); err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, own)
}

func (h *handlers) reset(w http.ResponseWriter, r *http.Request) {
	if err := dashboards.Reset(r.Context(), h.d.Store, kit.ActorID(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, map[string]any{"own": nil})
}

func (h *handlers) putShared(w http.ResponseWriter, r *http.Request) {
	boards, err := dashboards.ParseShared(middleware.BodyValue(r).Raw())
	if h.refused(w, r, err) {
		return
	}
	if err := dashboards.SaveShared(r.Context(), h.d.Store, boards); err != nil {
		h.fail(w, r, err)
		return
	}
	_ = h.d.LogAction(r, "dashboards_shared_changed", "Shared overview boards published: "+strconv.Itoa(len(boards)))
	kit.OK(w, r, map[string]any{"boards": boards})
}

// refused answers 400 for a layout that is not valid and reports whether
// it did.
func (h *handlers) refused(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	var bad *dashboards.InvalidError
	if errors.As(err, &bad) {
		kit.Fail(w, r, http.StatusBadRequest, bad.Message)
	} else {
		h.fail(w, r, err)
	}
	return true
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Dashboards error:", err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
