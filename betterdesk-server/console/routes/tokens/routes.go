// Package tokens passes the enrollment token routes and the enrollment
// mode on to the API server. Every answer is 200 with what the client
// (console/goapi) returned: {success:true, data} or {success:false, error},
// where the error of a refused call is the client's text ("Request failed
// with status code 404"), not the API server's.
package tokens

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

type handlers struct {
	api *goapi.Client
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI}
	r := d.Router
	manage := d.RequirePermission("enrollment.manage")
	r.Get("/api/panel/tokens", h.list, d.RequireAuth, manage)
	r.Post("/api/panel/tokens", h.create, d.RequireAuth, manage)
	r.Post("/api/panel/tokens/generate-bulk", h.generateBulk, d.RequireAuth, manage)
	r.Put("/api/panel/tokens/{id}", h.update, d.RequireAuth, manage)
	r.Delete("/api/panel/tokens/{id}", h.revoke, d.RequireAuth, manage)
	r.Post("/api/panel/tokens/{id}/bind", h.bind, d.RequireAuth, manage)
	r.Get("/api/panel/enrollment/mode", h.mode, d.RequireAuth, manage)
	r.Put("/api/panel/enrollment/mode", h.setMode, d.RequireAuth, manage)
}

// list answers GET /api/panel/tokens. Revoked tokens are in the list only
// for ?include_revoked=true, written once.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	includeRevoked := jsreq.Query(r, "include_revoked") == "true"
	kit.JSON(w, r, http.StatusOK, h.api.ListDeviceTokens(r.Context(), includeRevoked))
}

// create answers POST /api/panel/tokens. The body goes to the API server
// as it is: name, max_uses, expires_in, note.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.CreateDeviceToken(r.Context(), jsreq.Body(r)))
}

// generateBulk answers POST /api/panel/tokens/generate-bulk with the body
// count, name_prefix, max_uses, expires_in.
func (h *handlers) generateBulk(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.BulkGenerateTokens(r.Context(), jsreq.Body(r)))
}

// update answers PUT /api/panel/tokens/{id} with the body name, max_uses,
// note.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.UpdateDeviceToken(r.Context(), r.PathValue("id"), jsreq.Body(r)))
}

// revoke answers DELETE /api/panel/tokens/{id}.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.RevokeDeviceToken(r.Context(), r.PathValue("id")))
}

// bind answers POST /api/panel/tokens/{id}/bind with the body {peer_id}.
// The peer id is read as text, whatever JSON type it came as.
func (h *handlers) bind(w http.ResponseWriter, r *http.Request) {
	peerID := jsreq.StringOr(jsreq.Prop(jsreq.Body(r), "peer_id"), "")
	kit.JSON(w, r, http.StatusOK, h.api.BindTokenToPeer(r.Context(), r.PathValue("id"), peerID))
}

// mode answers GET /api/panel/enrollment/mode.
func (h *handlers) mode(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.GetEnrollmentMode(r.Context()))
}

// setMode answers PUT /api/panel/enrollment/mode with the body {mode}.
func (h *handlers) setMode(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, h.api.SetEnrollmentMode(r.Context(), jsreq.Prop(jsreq.Body(r), "mode")))
}
