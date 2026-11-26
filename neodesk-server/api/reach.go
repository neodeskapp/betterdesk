package api

import (
	"net/http"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// The reasons GET /api/peers/{id}/reach answers: why a session to the
// device can or cannot start, as the server knows it. The console's viewer
// asks when the signal server answered "offline" or "not found", so it
// names the real cause (console/reasons in the web-ui lists the texts).
const (
	reachOnline     = "online"
	reachOffline    = "device_offline"
	reachUnknown    = "device_unknown"
	reachRevoked    = "device_revoked"
	reachBlocked    = "device_blocked"
	reachBanned     = "device_banned"
	reachRemoved    = "device_removed"
	reachKeyChanged = "key_changed"
)

// peerReach is the reason a device can or cannot be reached, and when it
// was last seen ("" when never).
func (s *Server) peerReach(id string) (reason, lastOnline string) {
	if blocked := s.blockedReason(id); blocked != "" {
		return blocked, ""
	}
	if removed, _ := s.db.IsPeerSoftDeleted(id); removed {
		return reachRemoved, ""
	}
	p, err := s.db.GetPeer(id)
	if err != nil || p == nil {
		return reachUnknown, ""
	}
	if !p.LastOnline.IsZero() {
		lastOnline = p.LastOnline.UTC().Format(time.RFC3339)
	}
	switch {
	case p.Banned:
		return reachBanned, lastOnline
	case s.peers.KeyRefused(id) || s.keyRefused(id):
		return reachKeyChanged, lastOnline
	case s.peers.IsOnline(id, config.RegTimeout):
		return reachOnline, lastOnline
	}
	return reachOffline, lastOnline
}

// handlePeerReach answers why a session to the device can or cannot start:
// {reason, last_online}. A device that is not there is device_unknown, not
// 404, so a revoked and deleted ID still says it is revoked.
// GET /api/peers/{id}/reach
func (s *Server) handlePeerReach(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.deviceInScope(w, r, id) {
		return
	}
	reason, lastOnline := s.peerReach(id)
	writeJSON(w, http.StatusOK, map[string]any{"reason": reason, "last_online": lastOnline})
}

// handleUnblockPeer takes a device ID off the blocklist, which a revoke put
// there, so the device can register again. 404 not_blocked when it is not
// blocked.
// POST /api/peers/{id}/unblock
func (s *Server) handleUnblockPeer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.deviceInScope(w, r, id) {
		return
	}
	reason := s.blockedReason(id)
	if reason == "" || !s.blocklist.UnblockID(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_blocked"})
		return
	}
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionBlocklistRemove, s.eventActor(r), id, map[string]string{"type": "id", "was": reason})
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unblocked", "id": id})
}
