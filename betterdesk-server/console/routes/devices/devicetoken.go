package devices

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/registrations"
)

// issueDeviceToken answers POST /api/devices/{id}/device-token ("Issue a
// new token"): every device token of the device ends now, and the device
// gets a new one the next time it checks in. A device approved through a
// registration request finds it in its next registration status, once
// (registrations.ReissueToken); the BetterDesk client gets one at its next
// enrollment request signed with its key, and an agent at its next signed
// registration, since the server issues a token to a device that proves
// its key and holds none that works. No token is shown or stored in plain
// text.
func (h *handlers) issueDeviceToken(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.found(w, r, "Issue device token error:"); !ok {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if err := h.d.Authn.RevokeDeviceTokens(ctx, id); err != nil {
		h.serverError(w, r, "Issue device token error:", err)
		return
	}
	if _, err := registrations.New(h.d.Store, h.api).ReissueToken(ctx, id); err != nil {
		h.serverError(w, r, "Issue device token error:", err)
		return
	}
	if err := h.d.LogAction(r, "device_token_issued", "New device token issued for device "+id); err != nil {
		h.serverError(w, r, "Issue device token error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}
