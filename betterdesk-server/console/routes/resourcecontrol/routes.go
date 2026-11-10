// Package resourcecontrol holds the console's routes for what a device
// may use: USB, optical drives, monitors, disks and quotas, per device
// and as the default of the organization. The routes pass their calls on
// to the API server.
//
// The route devices call (GET /api/bd/resource-policy) is not here: it
// waits for the device token check.
package resourcecontrol

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

type handlers struct {
	api *goapi.Client
}

// kinds are the parts of a device's resource policy, each with its own
// write route.
var kinds = []string{"usb", "optical", "monitors", "disks", "quotas"}

const (
	orgPolicyPath = "/org/default/resource-policy"
	unreachable   = "Failed to reach Go server"
	// deviceListLimit is how many devices the device list asks the API
	// server for.
	deviceListLimit = 200
)

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI}
	r := d.Router
	config := []kit.Middleware{d.RequireAuth, d.RequirePermission("server.config")}

	r.Get("/api/panel/resource-control/devices", h.devices, config...)
	r.Get("/api/panel/resource-control/policies/{deviceId}", h.devicePolicy(http.MethodGet, ""), config...)
	for _, kind := range kinds {
		r.Post("/api/panel/resource-control/policies/{deviceId}/"+kind, h.devicePolicy(http.MethodPost, "/"+kind), config...)
	}
	r.Get("/api/panel/resource-control/compliance", h.compliance, config...)
	r.Get("/api/panel/resource-control/org-policy", h.orgPolicy, config...)
	r.Put("/api/panel/resource-control/org-policy", h.orgPolicy, config...)
}

// proxy calls the API server and answers with its status and body; a PUT
// or POST sends the request's body along. A failed call is answered with
// the API server's status and body, 500 when there was no answer, and
// {"error":"Failed to reach Go server"} when the body is empty, false, 0
// or null.
func (h *handlers) proxy(w http.ResponseWriter, r *http.Request, method, path string) {
	var opts []goapi.CallOption
	if method != http.MethodGet {
		opts = append(opts, goapipath.Body(goapipath.RequestBody(r)))
	}
	resp, err := h.api.Request(r.Context(), method, path, opts...)
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	goapipath.WriteJSON(w, r, resp.Status, resp.Data())
}

// devicePolicy answers GET /api/panel/resource-control/policies/{deviceId}
// (suffix "") and the POST routes below it, one per kind. A device id
// that cannot go into a path is answered with 400 before any call.
func (h *handlers) devicePolicy(method, suffix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := goapipath.AssertSafeAPIID(r.PathValue("deviceId"), "deviceId")
		if err != nil {
			kit.JSON(w, r, http.StatusBadRequest, goapipath.ErrorBody{Error: err.Error()})
			return
		}
		h.proxy(w, r, method, "/devices/"+goapi.EncodeURIComponent(id)+"/resources"+suffix)
	}
}

// orgPolicy answers GET and PUT /api/panel/resource-control/org-policy:
// the default policy of the organization.
func (h *handlers) orgPolicy(w http.ResponseWriter, r *http.Request) {
	method := http.MethodGet
	if r.Method == http.MethodPut {
		method = http.MethodPut
	}
	h.proxy(w, r, method, orgPolicyPath)
}

type deviceList struct {
	Devices []*goapi.Object `json:"devices"`
	Total   int             `json:"total"`
}

// devices answers GET /api/panel/resource-control/devices: the first 200
// devices of the API server with the fields the page shows. When the call
// fails, or the list holds something that is no device, the answer is the
// empty list.
func (h *handlers) devices(w http.ResponseWriter, r *http.Request) {
	list := deviceList{Devices: []*goapi.Object{}}
	if resp, err := h.api.Get(r.Context(), "/peers", goapi.Query("limit", deviceListLimit)); err == nil {
		if devices, ok := devicesOf(resp.Data()); ok {
			list.Devices = devices
		}
	}
	list.Total = len(list.Devices)
	goapipath.WriteJSON(w, r, http.StatusOK, list)
}

// devicesOf reads the API server's device list: the list under "data" or
// "peers", or the answer itself when it is the list. Anything else is no
// devices. ok is false when the list has a null in it.
func devicesOf(data any) (devices []*goapi.Object, ok bool) {
	devices = []*goapi.Object{}
	peers := data
	for _, key := range []string{"data", "peers"} {
		if inner := goapi.Dig(data, key); goapi.Truthy(inner) {
			peers = inner
			break
		}
	}
	list, _ := peers.([]any)
	for _, peer := range list {
		if peer == nil {
			return nil, false
		}
		device := goapi.NewObject()
		// An entry that is no object has none of the fields
		p, _ := peer.(*goapi.Object)
		if p.Has("id") {
			device.Set("id", goapipath.Plain(p.Get("id")))
		}
		device.Set("hostname", orText(p.Get("hostname"), ""))
		device.Set("platform", orText(p.Get("platform"), ""))
		status := p.Get("live_status")
		if !goapi.Truthy(status) {
			status = p.Get("status")
		}
		device.Set("status", orText(status, "offline"))
		device.Set("device_type", orText(p.Get("device_type"), ""))
		device.Set("tags", orText(p.Get("tags"), ""))
		devices = append(devices, device)
	}
	return devices, true
}

// orText is value || fallback.
func orText(value any, fallback string) any {
	if goapi.Truthy(value) {
		return goapipath.Plain(value)
	}
	return fallback
}

type enforced struct {
	Enforced int `json:"enforced"`
	Total    int `json:"total"`
}

type noCompliance struct {
	TotalDevices   int `json:"total_devices"`
	Compliant      int `json:"compliant"`
	NonCompliant   int `json:"non_compliant"`
	NoPolicy       int `json:"no_policy"`
	ComplianceRate int `json:"compliance_rate"`
	Categories     struct {
		USB      enforced `json:"usb"`
		Optical  enforced `json:"optical"`
		Monitors enforced `json:"monitors"`
		Disks    enforced `json:"disks"`
		Quotas   enforced `json:"quotas"`
	} `json:"categories"`
}

// compliance answers GET /api/panel/resource-control/compliance: the API
// server's summary, and a summary of zeros when the call fails.
func (h *handlers) compliance(w http.ResponseWriter, r *http.Request) {
	resp, err := h.api.Get(r.Context(), orgPolicyPath+"/compliance")
	if err != nil {
		kit.JSON(w, r, http.StatusOK, noCompliance{})
		return
	}
	goapipath.WriteJSON(w, r, http.StatusOK, resp.Data())
}
