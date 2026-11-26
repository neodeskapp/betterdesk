package devices

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// reachAnswer is why a session to the device can or cannot start.
type reachAnswer struct {
	Success bool `json:"success"`
	// Reason is the server's: online, device_offline, device_unknown,
	// device_revoked, device_blocked, device_banned, device_removed or
	// key_changed
	Reason     string `json:"reason"`
	LastOnline string `json:"last_online"`
	// CanUnblock: this account may unblock a revoked or blocked ID
	CanUnblock bool `json:"can_unblock"`
}

// reach answers GET /api/devices/{id}/reach: the server's reason, which the
// viewer turns into what happened and what to do.
func (h *handlers) reach(w http.ResponseWriter, r *http.Request) {
	result := h.api.GetPeerReach(r.Context(), r.PathValue("id"))
	if result.Get("success") == false {
		h.log.Warn("Device reach:", result.Get("error"))
		fail(w, r, http.StatusBadGateway, "errors.server_error")
		return
	}
	data := result.Object("data")
	if data == nil {
		data = result
	}
	answer(w, r, http.StatusOK, reachAnswer{
		Success: true, Reason: data.Str("reason"), LastOnline: data.Str("last_online"),
		CanUnblock: kit.Access(r).HasPermission("device.delete"),
	})
}

// unblock answers POST /api/devices/{id}/unblock: the device's ID leaves the
// server's blocklist (a revoke put it there), so the computer can register
// and join again. 404 when it was not blocked.
func (h *handlers) unblock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result := h.api.UnblockPeer(r.Context(), id)
	if result.Get("success") == false {
		if result.Get("error") == "not_blocked" {
			fail(w, r, http.StatusNotFound, "devices.unblock_not_blocked")
			return
		}
		h.log.Warn("Unblock device:", result.Get("error"))
		fail(w, r, http.StatusBadGateway, "errors.server_error")
		return
	}
	if err := h.d.LogAction(r, "device_unblocked", "Device "+id+" unblocked"); err != nil {
		h.serverError(w, r, "Unblock device error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}
