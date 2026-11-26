// Package phase45 holds the routes of routes/phase4_5.routes.js: the
// profile of the signed-in operator and what a consent popup shows about
// them, agent templates and enrollment with a template, the releases of
// the generic Support Agent with their downloads, the pairing of such an
// agent, and the installer list of the public portal.
package phase45

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/agentreleases"
	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

type handlers struct {
	d        *kit.Deps
	log      *kit.Logger
	releases *agentreleases.Service
}

// Register adds the group's routes, in the order of the Node file.
func Register(d *kit.Deps) {
	getenv := d.Config.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	h := &handlers{
		d:   d,
		log: d.Log.Child("phase4_5"),
		releases: agentreleases.New(agentreleases.Options{
			DataDir:   d.Config.DataDir,
			Repo:      getenv("NEODESK_SUPPORT_RELEASE_REPO"),
			PublicKey: getenv("NEODESK_SUPPORT_RELEASE_PUBLIC_KEY"),
		}),
	}
	manage, approve := d.RequirePermission("enrollment.manage"), d.RequirePermission("enrollment.approve")
	r := d.Router
	r.Get("/api/users/me/profile", h.profile, d.RequireAuth)
	r.Put("/api/users/me/profile", h.saveProfile, d.RequireAuth)
	r.Get("/api/nd/operator-info", h.operatorInfo)
	r.Get("/api/agent-templates", h.templates, d.RequireAuth, manage)
	r.Post("/api/agent-templates", h.createTemplate, d.RequireAuth, manage)
	r.Delete("/api/agent-templates/{id}", h.deleteTemplate, d.RequireAuth, manage)
	r.Get("/api/agent-releases", h.listReleases, d.RequireAuth, manage)
	r.Get("/api/agent-releases/signing-status", h.signingStatus, d.RequireAuth, manage)
	r.Post("/api/agent-releases/sync", h.syncRelease, d.RequireAuth, manage)
	r.Post("/api/agent-pairing/activate", h.activate, d.RequireAuth, approve)
	r.Get("/downloads/generic-agent/{tag}/{name}", h.download, d.Limits.FileAccess.Middleware)
	r.Post("/api/nd/enroll", h.enroll)
	r.Get("/api/portal/installers", h.installers, d.Limits.FileAccess.Middleware)
}

// field is req.body?.[key] as JavaScript saw it: Undefined when the body
// has no such member.
func field(r *http.Request, key string) any {
	raw := kit.Body(r).Raw(key)
	if raw == nil {
		return branding.Undefined
	}
	v, err := branding.ParseJSON(raw)
	if err != nil {
		return branding.Undefined
	}
	return v
}

// textOr is String(v || fallback). It fails where JavaScript cannot make
// text of the value.
func textOr(v any, fallback string) (string, error) {
	if !branding.Truthy(v) {
		return fallback, nil
	}
	return branding.ToString(v)
}

// failure is {success: false, error}, with code when the refusal has one.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}
