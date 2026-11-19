// Package devices holds the device routes: the device list the web app
// polls, one device with its system information, metrics, telemetry and
// connection mode, notes, names and tags, device groups and their
// members, bans, deletion and restore, a new id, the unattended access
// policy and the stored connect password, RDP and VNC remote targets,
// bulk deletion, and the access rules RustDesk clients get (strategies).
//
// In front of every address below /api/devices/{id}, the scope check
// (kit.RequireDeviceInScope, added with Use where the old console mounted
// it) refuses a device the account may not see with 403, before the
// route's own sign-in and permission checks; a device that does not exist
// goes on, and so does the bulk delete, whose path names no device. The
// check covers the routes added after it, in this group and in the
// folder group (PATCH /api/devices/{id}/folder). Every route that
// names a device then checks again that it exists and that the account
// may see it (the device scope of console/access), and answers 404 and
// 403 in that order.
//
// The device group writes go to the API server unless
// BACKEND_PANEL_GROUPING is node (panelgrouping.go).
//
// The routes that reach into a running agent (its services, processes,
// event log, activity, files, clipboard, screen, keyboard, mouse and
// terminal) ask it over its signal socket (console/bdrelay); agent.go.
//
// GET /api/devices shares its address with the RustDesk client API, which
// answers a request with a bearer token. That API has not moved: the list
// passes such a request on, and the front door hands it to the Node
// console, where the client API answers it.
package devices

import (
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	d       *kit.Deps
	api     *goapi.Client
	backend *goapi.Backend
	groups  *devicegroups.Service
	log     *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, api: d.GoAPI, backend: d.Backend, groups: d.DeviceGroups, log: d.Log}
	r := d.Router
	view, edit := d.RequirePermission("device.view"), d.RequirePermission("device.edit")

	r.Get("/api/devices", h.list, d.RequireAuth, view)

	r.Get("/api/remote-targets", h.remoteTargets, d.RequireAuth, d.RequirePermission("remote_target.view"))
	r.Post("/api/remote-targets", h.createRemoteTarget, d.RequireAuth, d.RequirePermission("remote_target.edit"))
	r.Post("/api/remote-targets/test", h.testRemoteTarget, d.RequireAuth, d.RequirePermission("remote_target.test"))
	r.Get("/api/remote-targets/{id}", h.remoteTarget, d.RequireAuth, d.RequirePermission("remote_target.view"))
	r.Patch("/api/remote-targets/{id}", h.updateRemoteTarget, d.RequireAuth, d.RequirePermission("remote_target.edit"))
	r.Get("/api/remote-targets/{id}/credentials", h.remoteTargetCredentials, d.RequireAuth, d.RequirePermission("remote_target.view"))
	r.Put("/api/remote-targets/{id}/credentials", h.setRemoteTargetCredentials, d.RequireAuth, d.RequirePermission("remote_target.edit"))
	r.Delete("/api/remote-targets/{id}/credentials", h.clearRemoteTargetCredentials, d.RequireAuth, d.RequirePermission("remote_target.edit"))
	r.Post("/api/remote-targets/{id}/certificate/accept", h.acceptRemoteTargetCertificate, d.RequireAuth, d.RequirePermission("remote_target.connect"))
	r.Delete("/api/remote-targets/{id}", h.deleteRemoteTarget, d.RequireAuth, d.RequirePermission("remote_target.delete"))

	r.Get("/api/tags", h.tags, d.RequireAuth, view)
	r.Post("/api/tags/rename", h.renameTag, d.RequireAuth, edit)
	r.Post("/api/tags/delete", h.deleteTag, d.RequireAuth, edit)

	r.Get("/api/device-groups", h.deviceGroups, d.RequireAuth, view)
	r.Post("/api/device-groups", h.saveDeviceGroup, d.RequireAuth, edit)
	r.Delete("/api/device-groups/{guid}", h.deleteDeviceGroup, d.RequireAuth, edit)

	r.Use("/api/devices/{id}", h.deviceInScope)
	r.Get("/api/devices/{id}/groups", h.memberships, d.RequireAuth, view)
	r.Put("/api/devices/{id}/groups", h.setMemberships, d.RequireAuth, edit)

	r.Get("/api/devices/{id}", h.device, d.RequireAuth, view)
	r.Get("/api/devices/{id}/availability", h.availability, d.RequireAuth, view)
	r.Get("/api/devices/{id}/connection-sessions", h.connectionSessions, d.RequireAuth, view)
	r.Post("/api/devices/{id}/telemetry/refresh", h.refreshTelemetry, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/telemetry/command", h.telemetryCommand, d.RequireAuth, edit)
	r.Patch("/api/devices/{id}", h.update, d.RequireAuth, edit)
	r.Delete("/api/devices/{id}", h.remove, d.RequireAuth, d.RequirePermission("device.delete"))
	r.Post("/api/devices/{id}/restore", h.restore, d.RequireAuth, d.RequirePermission("device.delete"))
	r.Post("/api/devices/{id}/connection-mode", h.connectionMode, d.RequireAuth, d.RequirePermission("device.connection_mode"))
	r.Post("/api/devices/{id}/ban", h.ban, d.RequireAuth, d.RequirePermission("device.ban"))
	r.Post("/api/devices/{id}/unban", h.unban, d.RequireAuth, d.RequirePermission("device.ban"))
	r.Post("/api/devices/{id}/change-id", h.changeID, d.RequireAuth, d.RequirePermission("device.change_id"))
	r.Put("/api/devices/{id}/tags", h.setTags, d.RequireAuth, edit)
	r.Post("/api/devices/bulk-delete", h.bulkDelete, d.RequireAuth, d.RequirePermission("device.delete"))

	r.Get("/api/devices/{id}/access-policy", h.accessPolicy, d.RequireAuth, view)
	r.Put("/api/devices/{id}/access-policy", h.saveAccessPolicy, d.RequireAuth, edit)
	r.Delete("/api/devices/{id}/access-policy", h.deleteAccessPolicy, d.RequireAuth, edit)
	r.Get("/api/devices/{id}/connect-password", h.connectPassword, d.RequireAuth, d.RequirePermission("device.connect"))

	r.Get("/api/devices/{id}/services", h.services, d.RequireAuth, view)
	r.Get("/api/devices/{id}/processes", h.processes, d.RequireAuth, view)
	r.Get("/api/devices/{id}/events", h.events, d.RequireAuth, view)
	r.Get("/api/devices/{id}/activity", h.activity, d.RequireAuth, view)
	r.Post("/api/devices/{id}/files/browse", h.filesBrowse, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/files/read", h.filesRead, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/files/write", h.filesWrite, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/files/delete", h.filesDelete, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/files/rename", h.filesRename, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/files/mkdir", h.filesMkdir, d.RequireAuth, edit)
	r.Get("/api/devices/{id}/clipboard", h.clipboard, d.RequireAuth, view)
	r.Post("/api/devices/{id}/clipboard", h.setClipboard, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/screenshot", h.screenshot, d.RequireAuth, view)
	r.Post("/api/devices/{id}/input/mouse", h.mouse, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/input/key", h.key, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/input/text", h.typeText, d.RequireAuth, edit)
	r.Post("/api/devices/{id}/terminal/execute", h.terminal, d.RequireAuth, edit)

	r.Post("/api/devices/{id}/rename", h.rename, d.RequireAuth, edit)

	r.Get("/api/panel/strategies", h.strategies, d.RequireAuth, view)
	r.Post("/api/panel/strategies", h.createStrategy, d.RequireAuth, edit)
	r.Patch("/api/panel/strategies/{guid}", h.updateStrategy, d.RequireAuth, edit)
	r.Get("/api/panel/strategies/{guid}", h.strategy, d.RequireAuth, view)
	r.Post("/api/panel/strategies/{guid}/assign", h.assignStrategy, d.RequireAuth, edit)
	r.Post("/api/panel/strategies/unassign", h.unassignStrategy, d.RequireAuth, edit)
	r.Delete("/api/panel/strategies/{guid}", h.deleteStrategy, d.RequireAuth, edit)
}

// deviceInScope is the scope check of kit.RequireDeviceInScope in front of
// the addresses below /api/devices/{id}, except for POST
// /api/devices/bulk-delete (in any letter case, with or without a slash at
// its end): there bulk-delete names no device, and the route checks each
// device of its list itself.
func (h *handlers) deviceInScope(next http.Handler) http.Handler {
	checked := h.d.RequireDeviceInScope(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.EqualFold(strings.TrimSuffix(kit.RoutePath(r), "/"), "/api/devices/bulk-delete") {
			next.ServeHTTP(w, r)
			return
		}
		checked.ServeHTTP(w, r)
	})
}

// serverError logs what failed and answers 500 with the request
// language's text for a server error.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	fail(w, r, http.StatusInternalServerError, "errors.server_error")
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

// failure is {"success":false,"error":...} with an error that came from
// elsewhere and may be any JSON value.
type failure struct {
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

// withData is {"success":true,"data":...}.
type withData struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}
