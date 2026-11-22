package kit

import "net/http"

// RequireDeviceInScope is requireDeviceInScope of the device routes: the
// device scope check for every address below /api/devices/{id}. The device
// group adds it with Router.Use where the old console mounted it
// (router.use('/api/devices/:id', ...)), so it runs in front of every route
// added after it: the device routes, the routes that reach into an agent,
// and PATCH /api/devices/{id}/folder of the folder group, which comes later
// in the table. It runs for every method, before each route's own
// RequireAuth and permission check, and also for a path no route answers.
//
//	no session, or one without its account   goes on (RequireAuth answers)
//	super_admin, admin, global_admin,
//	server_admin                              goes on: they see every device
//	no such device, a deleted one included    goes on: the route decides
//	a device the account may not see          403 {"success":false,"error":"errors.forbidden"}
//	the scope check failed                    500 {"success":false,"error":<errors.server_error>}
//
// The refusal's text is the key itself: the language files have no
// errors.forbidden (BUGS FO1).
func (d *Deps) RequireDeviceInScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		user := User(r)
		if id == "" || UserID(r) == 0 || user == nil {
			next.ServeHTTP(w, r)
			return
		}
		switch user.Role {
		case "super_admin", "admin", "global_admin", "server_admin":
			next.ServeHTTP(w, r)
			return
		}
		device := d.Backend.GetDeviceByID(r.Context(), id, true)
		if device == nil {
			next.ServeHTTP(w, r)
			return
		}
		allowed, err := d.DeviceGroups.UserCanAccessDevice(r.Context(), user, device)
		if err != nil {
			d.Log.Error("Device scope guard error:", err.Error())
			Fail(w, r, http.StatusInternalServerError, T(r, "errors.server_error", nil))
			return
		}
		if !allowed {
			Fail(w, r, http.StatusForbidden, T(r, "errors.forbidden", nil))
			return
		}
		next.ServeHTTP(w, r)
	})
}
