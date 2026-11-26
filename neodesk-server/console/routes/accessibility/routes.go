// Package accessibility holds the routes of the signed-in account's own
// accessibility settings (package console/accessibility). Every account
// that may sign in reads and changes its own, and only its own: the
// routes take the account from the session and need no permission.
// While an admin views the console as another account (Sign in as), they
// are the admin's own settings (kit.ActorID), never the viewed account's:
// PUT is then the one write the impersonation may make besides its own
// routes (impersonation.allowed), and it can only reach the admin's row.
//
//	GET /api/accessibility  {"success": true, "data": {"reduceMotion": "system", ...}}
//	PUT /api/accessibility  body: some or all of the settings; answers all of them
package accessibility

import (
	"encoding/json"
	"errors"
	"net/http"

	settings "github.com/neodeskapp/neodesk/neodesk-server/console/accessibility"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type handlers struct {
	d   *kit.Deps
	log *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, log: d.Log.Child("ACCESSIBILITY")}
	r := d.Router
	r.Get("/api/accessibility", h.get, d.RequireAuth)
	r.Put("/api/accessibility", h.put, d.RequireAuth)
}

func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	s, _, err := settings.Load(r.Context(), h.d.Store, kit.ActorID(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, s)
}

func (h *handlers) put(w http.ResponseWriter, r *http.Request) {
	var change map[string]json.RawMessage
	if err := kit.Bind(r, &change); err != nil || change == nil {
		kit.Fail(w, r, http.StatusBadRequest, "Send the settings as a JSON object")
		return
	}
	ctx := r.Context()
	// The admin's own row while viewing as another account
	id := kit.ActorID(r)
	current, _, err := settings.Load(ctx, h.d.Store, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	next, err := settings.Apply(current, change)
	var invalid *settings.InvalidError
	if errors.As(err, &invalid) {
		kit.Fail(w, r, http.StatusBadRequest, "Unknown setting or value: "+invalid.Key)
		return
	}
	if err := settings.Save(ctx, h.d.Store, id, next); err != nil {
		h.fail(w, r, err)
		return
	}
	kit.OK(w, r, next)
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Accessibility settings error:", err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
