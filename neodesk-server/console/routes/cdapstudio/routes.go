// Package cdapstudio stores the flows of the CDAP studio, the editor in
// which people wire up bridge flows from nodes: list, read, create,
// change, delete, mark as deployed, a dry run, export and import as a
// .bdflow file, and the templates the editor offers.
//
// A flow belongs to the account that made it. Super admins see and change
// every flow; everyone else only their own.
package cdapstudio

import (
	"crypto/rand"
	_ "embed"
	"io"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// templates is the answer to GET /api/cdap-studio/templates: the five
// flows the editor offers to start from.
//
//go:embed templates.json
var templates []byte

type handlers struct {
	db  *store.DB
	log *kit.Logger
	// now is the clock; it stamps new and changed flows on SQLite and
	// exported files.
	now func() time.Time
	// random gives the bytes of a new flow's id.
	random io.Reader
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	register(d, time.Now, rand.Reader)
}

func register(d *kit.Deps, now func() time.Time, random io.Reader) {
	h := &handlers{db: d.Store, log: d.Log, now: now, random: random}
	operator := d.RequireRole("operator")
	r := d.Router
	r.Get("/api/cdap-studio/flows", h.list, d.RequireAuth)
	r.Get("/api/cdap-studio/flows/{id}", h.get, d.RequireAuth)
	r.Post("/api/cdap-studio/flows", h.create, d.RequireAuth, operator)
	r.Put("/api/cdap-studio/flows/{id}", h.update, d.RequireAuth, operator)
	r.Delete("/api/cdap-studio/flows/{id}", h.remove, d.RequireAuth, operator)
	r.Post("/api/cdap-studio/flows/{id}/deploy", h.deploy, d.RequireAuth, operator)
	r.Post("/api/cdap-studio/flows/{id}/test", h.test, d.RequireAuth, operator)
	r.Get("/api/cdap-studio/flows/{id}/export", h.export, d.RequireAuth)
	r.Post("/api/cdap-studio/flows/import", h.importFlow, d.RequireAuth, operator)
	r.Get("/api/cdap-studio/templates", h.templates, d.RequireAuth)
}
