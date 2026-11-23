package cdap

import (
	"context"
	"fmt"
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// keepToScope takes out of a list answer of the API server (the list
// under listKey of {success: true, data}) the entries of devices the
// account may not see, by the device id under idKey of each entry, and
// sets total to what is left. The rule is kit.RequireDeviceInScope's for
// the routes of one device: the four admin roles see every device, and an
// entry whose device the device layer does not know stays. An answer of
// another shape is left as it is.
func (h *handlers) keepToScope(r *http.Request, result *goapi.Object, listKey, idKey string) error {
	user := kit.User(r)
	if user == nil || !goapi.Truthy(result.Get("success")) {
		return nil
	}
	switch user.Role {
	case "super_admin", "admin", "global_admin", "server_admin":
		return nil
	}
	data, isObject := result.Get("data").(*goapi.Object)
	if !isObject {
		return nil
	}
	list, isList := data.Get(listKey).([]any)
	if !isList {
		return nil
	}
	visible := map[string]bool{}
	kept := make([]any, 0, len(list))
	for _, entry := range list {
		var id string
		if o, isObject := entry.(*goapi.Object); isObject {
			id, _ = o.Get(idKey).(string)
		}
		seen, known := visible[id]
		if !known {
			var err error
			if seen, err = h.maySee(r.Context(), user, id); err != nil {
				return err
			}
			visible[id] = seen
		}
		if seen {
			kept = append(kept, entry)
		}
	}
	if len(kept) == len(list) {
		return nil
	}
	data.Set(listKey, kept)
	if data.Has("total") {
		data.Set("total", float64(len(kept)))
	}
	return nil
}

// maySee reports whether an account outside the admin roles may see the
// device with the id: true for an id the device layer does not know.
func (h *handlers) maySee(ctx context.Context, user *session.User, id string) (bool, error) {
	if id == "" {
		return true, nil
	}
	device := h.deps.Backend.GetDeviceByID(ctx, id, true)
	if device == nil {
		return true, nil
	}
	allowed, err := h.deps.DeviceGroups.UserCanAccessDevice(ctx, user, device)
	if err != nil {
		return false, fmt.Errorf("check the scope of device %s: %w", id, err)
	}
	return allowed, nil
}

// scoped answers a list of the API server with the entries the account may
// see, or 500 when the scope check failed.
func (h *handlers) scoped(w http.ResponseWriter, r *http.Request, result *goapi.Object, listKey, idKey string) {
	if err := h.keepToScope(r, result, listKey, idKey); err != nil {
		h.log.Error("Device scope guard error:", err.Error())
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
		return
	}
	answer(w, r, result)
}
