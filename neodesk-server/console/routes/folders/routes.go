// Package folders holds the folder routes: device folders with the
// accounts and user groups that may see their devices, and putting
// devices into folders.
//
// A folder's access lists live on a device group with the guid
// "folder_<id>", which the routes create or refresh on the way.
//
// The routes hand ids and fields to the database as the request gave
// them, so an answer can depend on the database: a folder id that is no
// number is "not found" on SQLite and a server error on PostgreSQL.
//
// PATCH /api/devices/{id}/folder is behind the device scope check the
// device group puts in front of every address below /api/devices/{id}
// (kit.RequireDeviceInScope); the device group comes first in the table.
package folders

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	d       *kit.Deps
	groups  *devicegroups.Service
	backend *goapi.Backend
	log     *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, groups: d.DeviceGroups, backend: d.Backend, log: d.Log}
	r := d.Router
	r.Get("/api/folders", h.list, d.RequireAuth, d.RequirePermission("device.view"))
	r.Post("/api/folders", h.create, d.RequireAuth, d.RequirePermission("device.edit"))
	r.Patch("/api/folders/{id}", h.update, d.RequireAuth, d.RequirePermission("device.edit"))
	r.Delete("/api/folders/{id}", h.remove, d.RequireAuth, d.RequirePermission("device.edit"))
	r.Post("/api/folders/{id}/devices", h.assignDevices, d.RequireAuth, d.RequirePermission("device.edit"))
	r.Patch("/api/devices/{id}/folder", h.assignDevice, d.RequireAuth, d.RequirePermission("device.edit"))
}

// serverError logs what failed and answers 500 with the request
// language's text for a server error.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// fail answers {"success":false,"error":<text of key>}.
func fail(w http.ResponseWriter, r *http.Request, status int, key string) {
	kit.Fail(w, r, status, kit.T(r, key, nil))
}

// answer sends a JSON answer written as JSON.stringify writes it.
func answer(w http.ResponseWriter, r *http.Request, status int, v any) {
	goapipath.WriteJSON(w, r, status, v)
}

// done is {"success":true}.
type done struct {
	Success bool `json:"success"`
}
