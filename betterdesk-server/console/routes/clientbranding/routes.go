// Package clientbranding holds the routes of Main, Client Branding: the
// branding that BetterDesk desktop clients and RustDesk clients get from
// the API server (GET /api/branding there), which the API server keeps in
// its server_config. It is not the console's own appearance, which is in
// console/branding (see its storage.md).
package clientbranding

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
	edit := d.RequirePermission("branding.edit")
	r.Get("/api/client-branding", h.get, d.RequireAuth, edit)
	r.Post("/api/client-branding", h.save, d.RequireAuth, edit)
}

// failed answers 502 with the error of a call the API server refused, or
// with fallback when it had none.
func failed(w http.ResponseWriter, r *http.Request, result *goapi.Object, fallback string) {
	message := result.Get("error")
	if !goapi.Truthy(message) {
		message = fallback
	}
	kit.JSON(w, r, http.StatusBadGateway, goapi.NewObject("success", false, "error", message))
}

// get answers GET /api/client-branding: the API server's branding.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	result := h.api.GetBranding(r.Context())
	if !goapi.Truthy(result.Get("success")) {
		failed(w, r, result, "Failed to load branding from BetterDesk server")
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", result.Get("data")))
}

// payloadKeys are the members of the body that go to the API server as
// they are, when the body has them.
var payloadKeys = []string{"company_name", "phone", "email", "website", "accent_color", "support_contact", "colors"}

// save answers POST /api/client-branding: the fields of the body go to
// the API server, with clear_logo: true when the body asks for it, else
// the logo when it has one. The answer carries the API server's answer as
// data and its revision.
func (h *handlers) save(w http.ResponseWriter, r *http.Request) {
	body, _ := jsreq.Body(r).(*goapi.Object)
	payload := goapi.NewObject()
	for _, key := range payloadKeys {
		if body.Has(key) {
			payload.Set(key, body.Get(key))
		}
	}
	if goapi.Truthy(body.Get("clear_logo")) {
		payload.Set("clear_logo", true)
	} else if logo := body.Get("logo"); goapi.Truthy(logo) {
		payload.Set("logo", logo)
	}
	result := h.api.SaveBranding(r.Context(), payload)
	if !goapi.Truthy(result.Get("success")) {
		failed(w, r, result, "Failed to save branding on BetterDesk server")
		return
	}
	// revision: result.data?.revision || result.revision, left out when
	// neither is there
	data := result.Get("data")
	answer := goapi.NewObject("success", true, "data", data)
	if o, ok := data.(*goapi.Object); ok && goapi.Truthy(o.Get("revision")) {
		answer.Set("revision", o.Get("revision"))
	} else if result.Has("revision") {
		answer.Set("revision", result.Get("revision"))
	}
	kit.JSON(w, r, http.StatusOK, answer)
}
