// Package cdap holds the console's routes for the device gateway (CDAP,
// the Custom Device Automation Protocol): the state of the gateway, its
// connected devices with their manifests, widget values and alerts,
// commands to a device, the link between a gateway device and a peer, and
// the switch that turns the gateway on or off at the next restart.
//
// Every route asks the API server. The routes of one device first check
// that the account may see it (kit.RequireDeviceInScope, after the sign-in
// and the permission), as the gateway's sockets do: a device outside the
// account's scope is 403, a device the device layer does not know goes on
// to the API server. The lists of devices and alerts keep the entries of
// the devices the account may see by the same rule. The websockets of the
// gateway pages (terminal, media) are not here.
package cdap

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
)

type handlers struct {
	api     *goapi.Client
	restart *restart.Service
	log     *kit.Logger
	// deps gives the device layer and the device scope of the lists
	deps *kit.Deps
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, restart: d.Restart, log: d.Log.Child("CDAP"), deps: d}
	command := d.RequirePermission("cdap.command")
	scope := d.RequireDeviceInScope

	r := d.Router
	r.Get("/api/cdap/status", h.status, d.RequireAuth)
	r.Get("/api/cdap/devices", h.devices, d.RequireAuth)
	r.Get("/api/cdap/devices/{id}", h.device, d.RequireAuth, scope)
	r.Get("/api/cdap/devices/{id}/manifest", h.manifest, d.RequireAuth, scope)
	r.Get("/api/cdap/devices/{id}/state", h.state, d.RequireAuth, scope)
	r.Post("/api/cdap/devices/{id}/command", h.command, d.RequireAuth, command, scope)
	r.Post("/api/cdap/toggle", h.toggle, d.RequireAuth, d.RequirePermission("server.config"))
	r.Get("/api/cdap/alerts", h.alerts, d.RequireAuth)
	r.Get("/api/cdap/devices/{id}/linked", h.linked, d.RequireAuth, scope)
	r.Post("/api/cdap/devices/{id}/link", h.link, d.RequireAuth, command, scope)
}

// answer sends what a call into the API server gave: the API server's
// own answer when the call worked and the answer is not empty, else the
// wrapper of the client, {success: false, error} or {success: true,
// data}. The status is 200 either way.
func answer(w http.ResponseWriter, r *http.Request, result *goapi.Object) {
	if goapi.Truthy(result.Get("success")) {
		if data := result.Get("data"); goapi.Truthy(data) {
			goapipath.WriteJSON(w, r, http.StatusOK, data)
			return
		}
	}
	goapipath.WriteJSON(w, r, http.StatusOK, result)
}

// failure is the answer {"error": text} of the checks of this group.
type failure struct {
	Error string `json:"error"`
}

// status answers GET /api/cdap/status: whether the gateway runs, its port
// and how many devices are connected.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	answer(w, r, h.api.GetCDAPStatus(r.Context()))
}

// devices answers GET /api/cdap/devices with the connected devices the
// account may see.
func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	h.scoped(w, r, h.api.GetCDAPDevices(r.Context()), "devices", "id")
}

// device answers GET /api/cdap/devices/{id}: manifest, state and
// connection of one device.
func (h *handlers) device(w http.ResponseWriter, r *http.Request) {
	answer(w, r, h.api.GetCDAPDeviceInfo(r.Context(), r.PathValue("id")))
}

// manifest answers GET /api/cdap/devices/{id}/manifest: what the device
// can do, its widgets and alerts.
func (h *handlers) manifest(w http.ResponseWriter, r *http.Request) {
	answer(w, r, h.api.GetCDAPDeviceManifest(r.Context(), r.PathValue("id")))
}

// state answers GET /api/cdap/devices/{id}/state with the current widget
// values.
func (h *handlers) state(w http.ResponseWriter, r *http.Request) {
	answer(w, r, h.api.GetCDAPDeviceState(r.Context(), r.PathValue("id")))
}

// bodyValue is req.body[key] as a parsed JSON value. defined is false
// when the body does not have the key.
func bodyValue(r *http.Request, key string) (value any, defined bool) {
	raw := kit.Body(r).Get(key).Raw()
	if raw == nil {
		return nil, false
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		return nil, false
	}
	return value, true
}

// command answers POST /api/cdap/devices/{id}/command with the body
// {widget_id, action, value, reason}: it sends a command to a widget of a
// connected device. A reason that is missing or empty goes out as "".
func (h *handlers) command(w http.ResponseWriter, r *http.Request) {
	widget, _ := bodyValue(r, "widget_id")
	action, _ := bodyValue(r, "action")
	if !goapi.Truthy(widget) || !goapi.Truthy(action) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "widget_id and action are required"})
		return
	}
	body := goapi.NewObject("widget_id", widget, "action", action)
	if value, defined := bodyValue(r, "value"); defined {
		body.Set("value", value)
	}
	body.Set("reason", "")
	if reason, _ := bodyValue(r, "reason"); goapi.Truthy(reason) {
		body.Set("reason", reason)
	}
	path := "/cdap/devices/" + goapi.EncodeURIComponent(r.PathValue("id")) + "/command"
	answer(w, r, h.call(r, http.MethodPost, path, goapipath.Body(body)))
}

// call is a call into the API server with the wrapper the methods of the
// client give: {success: true, data}, or {success: false, error} for a
// failed call and for an answer that carries "error".
func (h *handlers) call(r *http.Request, method, path string, opts ...goapi.CallOption) *goapi.Object {
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err != nil {
		message := err.Error()
		if failed := goapi.AsError(err); failed != nil {
			message = failed.Message
		}
		return goapi.NewObject("success", false, "error", message)
	}
	data := resp.Data()
	if o, isObject := data.(*goapi.Object); isObject && o != nil && o.Has("error") {
		return goapi.NewObject("success", false, "error", o.Get("error"))
	}
	return goapi.NewObject("success", true, "data", data)
}

// toggled is the answer of the gateway switch.
type toggled struct {
	Success         bool            `json:"success"`
	Enabled         bool            `json:"enabled"`
	RestartRequired bool            `json:"restart_required"`
	Transaction     *restart.Public `json:"restartRequired"`
}

// toggleFailure is the answer {success: false, error} of the switch; the
// error is whatever the API server put there.
type toggleFailure struct {
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

// toggle answers POST /api/cdap/toggle with the body {enabled}. It saves
// the setting on the API server, which reads it at its next start, and
// adds the change to the operator's restart transaction, with the value
// the setting had so that a cancel puts it back.
func (h *handlers) toggle(w http.ResponseWriter, r *http.Request) {
	enabled, isBool := kit.Body(r).Get("enabled").Bool()
	if !isBool {
		kit.Fail(w, r, http.StatusBadRequest, "enabled must be a boolean")
		return
	}
	ctx := r.Context()
	previous := h.api.GetConfig(ctx, "cdap_enabled")
	if !goapi.Truthy(previous.Get("success")) {
		message := previous.Get("error")
		if !goapi.Truthy(message) {
			message = "Failed to read CDAP setting"
		}
		goapipath.WriteJSON(w, r, http.StatusInternalServerError, toggleFailure{Error: message})
		return
	}
	value := "N"
	if enabled {
		value = "Y"
	}
	h.api.SetConfig(ctx, "cdap_enabled", value)

	// The value to return to: the setting's value, the whole answer when
	// it names none, "N" when there was no answer
	var before any = "N"
	if data := previous.Get("data"); data != nil {
		before = data
		if o, isObject := data.(*goapi.Object); isObject && o.Get("value") != nil {
			before = o.Get("value")
		}
	}
	transaction, err := h.restart.RegisterChange(kit.UserID(r), restart.Change{
		Key:      "cdap-enabled",
		Label:    kit.T(r, "cdap.devices_title", nil),
		Rollback: goapi.NewObject("type", "go-config", "key", "cdap_enabled", "value", before),
	})
	if err != nil {
		h.log.Error("Toggle error:", err.Error())
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to toggle CDAP")
		return
	}
	kit.JSON(w, r, http.StatusOK, toggled{Success: true, Enabled: enabled, RestartRequired: true, Transaction: transaction})
}

// alerts answers GET /api/cdap/alerts with the alerts that are firing,
// of one device with ?device_id=, of the devices the account may see.
func (h *handlers) alerts(w http.ResponseWriter, r *http.Request) {
	h.scoped(w, r, h.api.GetCDAPAlerts(r.Context(), reqquery.Of(r).Get("device_id").JSON()), "alerts", "device_id")
}

// linked answers GET /api/cdap/devices/{id}/linked with the peers linked
// to the device.
func (h *handlers) linked(w http.ResponseWriter, r *http.Request) {
	answer(w, r, h.api.GetLinkedPeers(r.Context(), r.PathValue("id")))
}

// link answers POST /api/cdap/devices/{id}/link with the body
// {linked_peer_id}: the peer to link, or "" to remove the link.
func (h *handlers) link(w http.ResponseWriter, r *http.Request) {
	peer, defined := bodyValue(r, "linked_peer_id")
	if !defined {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "linked_peer_id is required"})
		return
	}
	answer(w, r, h.api.LinkDevice(r.Context(), r.PathValue("id"), peer))
}
