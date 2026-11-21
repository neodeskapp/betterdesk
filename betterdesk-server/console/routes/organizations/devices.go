package organizations

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// The API server sees the console as an admin, so it checks no device
// scope: the routes below check it before they pass a request on.

// assignDevice answers POST /api/panel/org/{id}/devices with the body
// {device_id, ...}: a device the account may see goes into the
// organization.
func (h *handlers) assignDevice(w http.ResponseWriter, r *http.Request) {
	target, err := orgPath("/devices")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	if id, isText := kit.Body(r).Get("device_id").Str(); isText && id != "" && !h.mayUse(w, r, id) {
		return
	}
	h.proxy(w, r, http.MethodPost, target, true)
}

// unassignDevice answers DELETE /api/panel/org/{id}/devices/{did}: a
// device the account may see leaves the organization.
func (h *handlers) unassignDevice(w http.ResponseWriter, r *http.Request) {
	target, err := orgItemPath("/devices/", "did", "deviceId")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	if !h.mayUse(w, r, r.PathValue("did")) {
		return
	}
	h.proxy(w, r, http.MethodDelete, target, false)
}

// listDevices answers GET /api/panel/org/{id}/devices with the API
// server's list, cut to the devices the account may see.
func (h *handlers) listDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	target, err := orgPath("/devices")(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	resp, err := h.api.Request(ctx, http.MethodGet, target)
	if err != nil {
		status, data := goapipath.FailureOf(err, unreachable)
		goapipath.WriteJSON(w, r, status, data)
		return
	}
	data := resp.Data()
	answer, isObject := data.(*goapi.Object)
	list, isList := []any(nil), false
	if isObject && answer != nil {
		list, isList = answer.Get("devices").([]any)
	}
	if isList {
		user := kit.User(r)
		scope, err := h.groups.DeviceScopeForUser(ctx, user, nil)
		if err == nil && scope != nil {
			scope, err = h.groups.DeviceScopeForUser(ctx, user, h.backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
		}
		if err != nil {
			h.scopeFailed(w, r, err)
			return
		}
		if scope != nil {
			kept := make([]any, 0, len(list))
			for _, item := range list {
				if device, ok := item.(*goapi.Object); ok && device != nil && scope.Has(device.Str("device_id")) {
					kept = append(kept, item)
				}
			}
			answer.Set("devices", kept)
		}
	}
	goapipath.WriteJSON(w, r, resp.Status, data)
}

// mayUse reports whether the account may see the device. An id no device
// has passes, as for the device routes (kit.RequireDeviceInScope). It
// answers a refusal itself.
func (h *handlers) mayUse(w http.ResponseWriter, r *http.Request, id string) bool {
	ctx := r.Context()
	user := kit.User(r)
	scope, err := h.groups.DeviceScopeForUser(ctx, user, nil)
	if err == nil && scope == nil {
		return true
	}
	allowed := true
	if err == nil {
		if device := h.backend.GetDeviceByID(ctx, id, true); device != nil {
			allowed, err = h.groups.UserCanAccessDevice(ctx, user, device)
		}
	}
	if err != nil {
		h.scopeFailed(w, r, err)
		return false
	}
	if !allowed {
		kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.forbidden", nil))
	}
	return allowed
}

// scopeFailed logs a failed scope check and answers 500.
func (h *handlers) scopeFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.log.Error("Organization device scope error:", err.Error())
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
