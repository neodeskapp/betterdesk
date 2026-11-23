package organizations

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
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
	h.scopedList(w, r, "/devices", "devices", "device_id")
}

// listCredentials answers GET /api/panel/org/{id}/peer-credentials with
// the API server's list of stored-password flags, cut to the devices the
// account may see.
func (h *handlers) listCredentials(w http.ResponseWriter, r *http.Request) {
	h.scopedList(w, r, "/peer-credentials", "credentials", "peer_id")
}

// setCredential answers PUT and DELETE
// /api/panel/org/{id}/peer-credentials/{peerId} for a device the account
// may see.
func (h *handlers) setCredential(w http.ResponseWriter, r *http.Request) {
	target, err := credentialPath(r)
	if err != nil {
		invalid(w, r, err)
		return
	}
	if !h.mayUse(w, r, r.PathValue("peerId")) {
		return
	}
	h.proxy(w, r, r.Method, target, r.Method == http.MethodPut)
}

// scopedList calls the API server at the organization's suffix and cuts
// the answer's list (the member key) to the entries whose idKey names a
// device the account may see.
func (h *handlers) scopedList(w http.ResponseWriter, r *http.Request, suffix, key, idKey string) {
	ctx := r.Context()
	target, err := orgPath(suffix)(r)
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
		list, isList = answer.Get(key).([]any)
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
				if entry, ok := item.(*goapi.Object); ok && entry != nil && scope.Has(entry.Str(idKey)) {
					kept = append(kept, item)
				}
			}
			answer.Set(key, kept)
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
