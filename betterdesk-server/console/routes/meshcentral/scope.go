package meshcentral

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// seesEveryDevice reports the roles whose accounts see every device, as
// kit.RequireDeviceInScope has them.
func seesEveryDevice(role string) bool {
	switch role {
	case "super_admin", "admin", "global_admin", "server_admin":
		return true
	}
	return false
}

// recordingDevice is the device id a recording's file name starts with:
// the name is <device>_<type>_<time>_<relay>.mcrec, and the device id may
// hold "_" itself. "" for a name of another form.
func recordingDevice(name string) string {
	base, ok := strings.CutSuffix(name, ".mcrec")
	if !ok {
		return ""
	}
	parts := strings.Split(base, "_")
	if len(parts) < 4 {
		return ""
	}
	return strings.Join(parts[:len(parts)-3], "_")
}

// maySeeRecording reports whether the account may see a recording: an
// admin role sees every one, any other account those of a known device in
// its scope.
func (h *handlers) maySeeRecording(r *http.Request, name string) (bool, error) {
	user := kit.User(r)
	if user == nil || seesEveryDevice(user.Role) {
		return true, nil
	}
	id := recordingDevice(name)
	if id == "" {
		return false, nil
	}
	device := h.deps.Backend.GetDeviceByID(r.Context(), id, true)
	if device == nil {
		return false, nil
	}
	allowed, err := h.deps.DeviceGroups.UserCanAccessDevice(r.Context(), user, device)
	if err != nil {
		return false, fmt.Errorf("check the scope of device %s: %w", id, err)
	}
	return allowed, nil
}

// keepRecordings takes out of a recording list the recordings the account
// may not see. An answer of another shape is left as it is.
func (h *handlers) keepRecordings(r *http.Request, data any) error {
	answer, isObject := data.(*goapi.Object)
	if !isObject {
		return nil
	}
	list, isList := answer.Get("recordings").([]any)
	if !isList {
		return nil
	}
	kept := make([]any, 0, len(list))
	for _, entry := range list {
		var id string
		if o, isObject := entry.(*goapi.Object); isObject {
			id, _ = o.Get("id").(string)
		}
		seen, err := h.maySeeRecording(r, id)
		if err != nil {
			return err
		}
		if seen {
			kept = append(kept, entry)
		}
	}
	if len(kept) != len(list) {
		answer.Set("recordings", kept)
	}
	return nil
}

// scopeFailed answers a failed scope check as kit.RequireDeviceInScope
// does.
func (h *handlers) scopeFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.deps.Log.Error("Device scope guard error:", err.Error())
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}
