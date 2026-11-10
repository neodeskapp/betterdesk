package folders

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// notFoundDevice is the answer for a device of a list that does not
// exist: {"success":false,"error":...,"id":...}.
type notFoundDevice struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	ID      string `json:"id"`
}

// moved is {"success":true,"data":{"count":n}}.
type moved struct {
	Success bool       `json:"success"`
	Data    movedCount `json:"data"`
}

type movedCount struct {
	Count int `json:"count"`
}

// assignDevices answers POST /api/folders/{id}/devices with {deviceIds}:
// the devices go into the folder, or out of their folder for the id 0.
// Every device has to exist and be one the account may see.
func (h *handlers) assignDevices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := folderID(r)
	list, isList := kit.Body(r).Get("deviceIds").Array()
	if !isList || len(list) == 0 {
		fail(w, r, http.StatusBadRequest, "folders.no_devices_selected")
		return
	}
	unassign := id.ToNumber() == 0
	if !unassign {
		folder, err := h.groups.FolderByID(ctx, id)
		if err != nil {
			h.serverError(w, r, "Assign devices error:", err)
			return
		}
		if folder == nil {
			fail(w, r, http.StatusNotFound, "folders.not_found")
			return
		}
	}
	ids := make([]jsbind.Value, len(list))
	devices := make([]*goapi.Object, 0, len(list))
	for i, item := range list {
		ids[i] = jsbind.Of(item)
		text := ids[i].ToString()
		device := h.backend.GetDeviceByID(ctx, text, false)
		if device == nil {
			answer(w, r, http.StatusNotFound, notFoundDevice{Error: kit.T(r, "devices.not_found", nil), ID: text})
			return
		}
		devices = append(devices, device)
	}
	scope, err := h.groups.DeviceScopeForUser(ctx, kit.User(r), devices)
	if err != nil {
		h.serverError(w, r, "Assign devices error:", err)
		return
	}
	if scope != nil {
		for _, device := range devices {
			if !scope.Has(jsreq.String(plainOf(device.Get("id")))) {
				fail(w, r, http.StatusForbidden, "errors.forbidden")
				return
			}
		}
	}
	target := id
	if unassign {
		target = jsbind.Null
	}
	if err := h.groups.AssignDevicesToFolder(ctx, ids, target); err != nil {
		h.serverError(w, r, "Assign devices error:", err)
		return
	}
	details := "Moved " + jsbind.NumberString(float64(len(list))) + " device(s) to folder ID: " + id.ToString()
	if err := h.d.LogAction(r, "devices_moved", details); err != nil {
		h.serverError(w, r, "Assign devices error:", err)
		return
	}
	answer(w, r, http.StatusOK, moved{Success: true, Data: movedCount{Count: len(list)}})
}

// assignDevice answers PATCH /api/devices/{id}/folder with {folderId}: the
// device goes into the folder, or out of its folder for null or no
// folderId.
func (h *handlers) assignDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deviceID := r.PathValue("id")
	folder := jsbind.Of(kit.Body(r).Get("folderId"))
	device := h.backend.GetDeviceByID(ctx, deviceID, false)
	if device == nil {
		fail(w, r, http.StatusNotFound, "devices.not_found")
		return
	}
	allowed, err := h.groups.UserCanAccessDevice(ctx, kit.User(r), device)
	if err != nil {
		h.serverError(w, r, "Assign device folder error:", err)
		return
	}
	if !allowed {
		fail(w, r, http.StatusForbidden, "errors.forbidden")
		return
	}
	if !folder.IsNull() && !folder.IsUndefined() {
		row, err := h.groups.FolderByID(ctx, folder)
		if err != nil {
			h.serverError(w, r, "Assign device folder error:", err)
			return
		}
		if row == nil {
			fail(w, r, http.StatusNotFound, "folders.not_found")
			return
		}
	}
	if err := h.groups.AssignDeviceToFolder(ctx, jsbind.String(deviceID), folder); err != nil {
		h.serverError(w, r, "Assign device folder error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}
