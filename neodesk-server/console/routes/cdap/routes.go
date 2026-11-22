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
// the devices the account may see by the same rule. A call the API server
// fails is answered with its status and body. The websockets of the
// gateway pages (terminal, media) are not here.
package cdap

import (
	"errors"
	"net/http"
	"strings"

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
// wrapper of the client, {success: false, error} for an answer that
// carries "error" or {success: true, data}, with status 200.
func answer(w http.ResponseWriter, r *http.Request, result *goapi.Object) {
	if goapi.Truthy(result.Get("success")) {
		if data := result.Get("data"); goapi.Truthy(data) {
			goapipath.WriteJSON(w, r, http.StatusOK, data)
			return
		}
	}
	goapipath.WriteJSON(w, r, http.StatusOK, result)
}

// failed answers a call the API server failed with its status and its
// body, or with 500 {"error": message} when it gave no answer or an
// empty one. A path the client refused before any call (the path guard
// of package goapi) is 200 {success: false, error}, followed by the
// key and value pairs of empty.
func failed(w http.ResponseWriter, r *http.Request, err error, empty ...any) {
	message := err.Error()
	e := goapi.AsError(err)
	if e != nil {
		message = e.Message
	}
	if e != nil && e.Response == nil && e.Code == "" {
		goapipath.WriteJSON(w, r, http.StatusOK, goapi.NewObject(append([]any{"success", false, "error", message}, empty...)...))
		return
	}
	status, data := goapipath.FailureOf(err, message)
	goapipath.WriteJSON(w, r, status, data)
}

// fetch is a call into the API server with the wrapper the methods of the
// client give when it worked: {success: true, data}, or {success: false,
// error} for an answer that carries "error". A failed call is the error.
func (h *handlers) fetch(r *http.Request, method, path string, opts ...goapi.CallOption) (*goapi.Object, error) {
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err != nil {
		return nil, err
	}
	data := resp.Data()
	if o, isObject := data.(*goapi.Object); isObject && o != nil && o.Has("error") {
		return goapi.NewObject("success", false, "error", o.Get("error")), nil
	}
	return goapi.NewObject("success", true, "data", data), nil
}

// relay makes a call into the API server and answers with what it gave.
func (h *handlers) relay(w http.ResponseWriter, r *http.Request, method, path string, opts ...goapi.CallOption) {
	result, err := h.fetch(r, method, path, opts...)
	if err != nil {
		failed(w, r, err)
		return
	}
	answer(w, r, result)
}

// failure is the answer {"error": text} of the checks of this group.
type failure struct {
	Error string `json:"error"`
}

// devicePath is the API server's address of the device in the path, with
// what follows it.
func devicePath(r *http.Request, rest string) string {
	return "/cdap/devices/" + goapi.EncodeURIComponent(r.PathValue("id")) + rest
}

// status answers GET /api/cdap/status: whether the gateway runs, its port
// and how many devices are connected.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	h.relay(w, r, http.MethodGet, "/cdap/status")
}

// devices answers GET /api/cdap/devices with the connected devices the
// account may see.
func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	result, err := h.fetch(r, http.MethodGet, "/cdap/devices")
	if err != nil {
		failed(w, r, err)
		return
	}
	h.scoped(w, r, result, "devices", "id")
}

// device answers GET /api/cdap/devices/{id}: manifest, state and
// connection of one device.
func (h *handlers) device(w http.ResponseWriter, r *http.Request) {
	h.relay(w, r, http.MethodGet, devicePath(r, ""))
}

// manifest answers GET /api/cdap/devices/{id}/manifest: what the device
// can do, its widgets and alerts.
func (h *handlers) manifest(w http.ResponseWriter, r *http.Request) {
	h.relay(w, r, http.MethodGet, devicePath(r, "/manifest"))
}

// state answers GET /api/cdap/devices/{id}/state with the current widget
// values.
func (h *handlers) state(w http.ResponseWriter, r *http.Request) {
	h.relay(w, r, http.MethodGet, devicePath(r, "/state"))
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
// connected device. A reason that is missing or empty goes out as "". A
// command to a terminal widget runs in a shell and needs cdap.terminal
// besides cdap.command (terminalWidget).
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
	if !kit.HasPermission(r, "cdap.terminal") {
		terminal, err := h.terminalWidget(r, widget)
		if err != nil {
			failed(w, r, err)
			return
		}
		if terminal {
			kit.Fail(w, r, http.StatusForbidden, "Permission denied: cdap.terminal")
			return
		}
	}
	h.relay(w, r, http.MethodPost, devicePath(r, "/command"), goapipath.Body(body))
}

// terminalWidget reports whether the widget of a command is a terminal of
// the device's manifest, whose commands run in a shell on the device and
// so need cdap.terminal. The manifest is asked of the API server. A device
// without one (404) has no widget the API server would send a command to;
// any other failed call is the error, so a command is not sent on a guess.
func (h *handlers) terminalWidget(r *http.Request, widget any) (bool, error) {
	id, isText := widget.(string)
	if !isText {
		return false, nil
	}
	resp, err := h.api.Request(r.Context(), http.MethodGet, devicePath(r, "/manifest"))
	if e := goapi.AsError(err); e != nil && e.Status() == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	manifest, _ := resp.Data().(*goapi.Object)
	if manifest == nil {
		return false, nil
	}
	widgets, _ := manifest.Get("widgets").([]any)
	for _, item := range widgets {
		w, _ := item.(*goapi.Object)
		if w == nil {
			continue
		}
		if wid, _ := w.Get("id").(string); strings.TrimSpace(wid) == id {
			kind, _ := w.Get("type").(string)
			return strings.ToLower(strings.TrimSpace(kind)) == "terminal", nil
		}
	}
	return false, nil
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
// the setting had so that a cancel puts it back. Both are one step of the
// restart service: a transaction the change cannot join (another
// operator's, 409) leaves the setting as it was, and a save the API
// server refuses is answered with its error and leaves the transaction as
// it was.
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
	// The value to return to: the setting's value when the API server
	// named it as text, else "N"
	before := "N"
	if data, isObject := previous.Get("data").(*goapi.Object); isObject {
		if value, isText := data.Get("value").(string); isText {
			before = value
		}
	}
	value := "N"
	if enabled {
		value = "Y"
	}
	transaction, err := h.restart.RegisterChangeAfter(kit.UserID(r), "cdap-enabled", func() (restart.Change, error) {
		if refusal := h.save(r, value); refusal != nil {
			return restart.Change{}, &refusedSave{answer: refusal}
		}
		return restart.Change{
			Label:    kit.T(r, "cdap.devices_title", nil),
			Rollback: goapi.NewObject("type", "go-config", "key", "cdap_enabled", "value", before),
		}, nil
	})
	var refused *refusedSave
	if errors.As(err, &refused) {
		h.log.Error("Toggle error: the API server refused the setting:", refused.answer)
		goapipath.WriteJSON(w, r, http.StatusInternalServerError, toggleFailure{Error: refused.answer})
		return
	}
	if err != nil {
		h.log.Error("Toggle error:", err.Error())
		status := http.StatusInternalServerError
		if refusal, isRefusal := err.(*restart.Error); isRefusal && refusal.StatusCode() == http.StatusConflict {
			status = http.StatusConflict
		}
		kit.Fail(w, r, status, "Failed to toggle CDAP")
		return
	}
	kit.JSON(w, r, http.StatusOK, toggled{Success: true, Enabled: enabled, RestartRequired: true, Transaction: transaction})
}

// refusedSave is a save of cdap_enabled the API server refused; answer
// is the error to answer with (see save).
type refusedSave struct{ answer any }

func (e *refusedSave) Error() string { return "the API server refused cdap_enabled" }

// save writes cdap_enabled on the API server. It returns nil when the
// API server took it, else the error to answer: the "error" of the API
// server's answer when it has one, else the message of the failed call.
func (h *handlers) save(r *http.Request, value string) any {
	resp, err := h.api.Request(r.Context(), http.MethodPut, "/config/cdap_enabled", goapi.Body(goapi.NewObject("value", value)))
	e := goapi.AsError(err)
	body := e.Data()
	if err == nil {
		body = resp.Data()
	}
	if o, isObject := body.(*goapi.Object); isObject && goapi.Truthy(o.Get("error")) {
		return o.Get("error")
	}
	switch {
	case err == nil:
		return nil
	case e != nil:
		return e.Message
	}
	return err.Error()
}

// alerts answers GET /api/cdap/alerts with the alerts that are firing,
// of one device with ?device_id=, of the devices the account may see.
func (h *handlers) alerts(w http.ResponseWriter, r *http.Request) {
	var opts []goapi.CallOption
	if device := reqquery.Of(r).Get("device_id").JSON(); goapi.Truthy(device) {
		opts = append(opts, goapi.Query("device_id", device))
	}
	result, err := h.fetch(r, http.MethodGet, "/cdap/alerts", opts...)
	if err != nil {
		failed(w, r, err, "alerts", []any{}, "total", float64(0))
		return
	}
	h.scoped(w, r, result, "alerts", "device_id")
}

// linked answers GET /api/cdap/devices/{id}/linked with the peers linked
// to the device.
func (h *handlers) linked(w http.ResponseWriter, r *http.Request) {
	result, err := h.fetch(r, http.MethodGet, "/peers/"+goapi.EncodeURIComponent(r.PathValue("id"))+"/linked")
	if err != nil {
		failed(w, r, err, "linked", []any{}, "total", float64(0))
		return
	}
	answer(w, r, result)
}

// link answers POST /api/cdap/devices/{id}/link with the body
// {linked_peer_id}: the peer to link, or "" to remove the link.
func (h *handlers) link(w http.ResponseWriter, r *http.Request) {
	peer, defined := bodyValue(r, "linked_peer_id")
	if !defined {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "linked_peer_id is required"})
		return
	}
	if !goapi.Truthy(peer) {
		peer = ""
	}
	h.relay(w, r, http.MethodPatch, "/peers/"+goapi.EncodeURIComponent(r.PathValue("id")), goapi.Body(goapi.NewObject("linked_peer_id", peer)))
}
