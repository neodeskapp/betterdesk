package api

// Rollout tokens (A278): an enrollment token for many computers, made for
// a rollout of the receiver MSI to a whole fleet. It admits computers
// until it expires, max_uses computers joined or an admin revoked it.
// Each computer that joins gets its own device token, as with a token of
// one computer; the rollout token itself is never bound to a device and
// never counts as a device's credential (hasEnrollmentCredential, the
// console's device sockets). Revoking it stops new joins and leaves the
// computers that joined alone.

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// The error codes of a registration a rollout token no longer admits.
const (
	rolloutTokenExpired = "rollout_token_expired"
	rolloutTokenRevoked = "rollout_token_revoked"
	rolloutTokenUsedUp  = "rollout_token_used_up"
)

// rolloutRefusalMessages are the messages of those refusals.
var rolloutRefusalMessages = map[string]string{
	rolloutTokenExpired: "The rollout token has expired",
	rolloutTokenRevoked: "The rollout token was revoked",
	rolloutTokenUsedUp:  "All the computers the rollout token allows have joined",
}

// rolloutRefusal is why the rollout token with this hash admits no more
// computers: rolloutTokenRevoked, rolloutTokenExpired or
// rolloutTokenUsedUp. It is "" for an unknown token, a token of one
// computer, or a rollout token that still admits computers.
func (s *Server) rolloutRefusal(tokenHash string) string {
	tok, err := s.db.GetDeviceTokenByHash(tokenHash)
	if err != nil || !tok.IsRollout() {
		return ""
	}
	switch {
	case tok.Status == db.TokenStatusRevoked:
		return rolloutTokenRevoked
	case tok.Status == db.TokenStatusExpired || tok.ExpiresAt != nil && !time.Now().Before(*tok.ExpiresAt):
		return rolloutTokenExpired
	case tok.UseCount >= tok.MaxUses:
		return rolloutTokenUsedUp
	}
	return ""
}

// admitByRolloutToken admits a new computer with a rollout token: it takes
// one of the token's places atomically (db.ClaimRolloutJoin, so two
// registrations at once never pass the limit), creates the peer, puts it
// into the token's folder and organization and issues the computer's own
// device token. A join that fails after the claim gives the place back.
// The result is that of admitByEnrollmentToken.
func (s *Server) admitByRolloutToken(req *EnrollmentRequest, tok *db.DeviceToken, clientIP, mode string) (bool, string, string) {
	claimed, err := s.db.ClaimRolloutJoin(tok.ID)
	if err != nil {
		log.Printf("[API] Rollout token %d for %s: %v", tok.ID, req.DeviceID, err)
		return false, "", ""
	}
	if !claimed {
		refusal := s.rolloutRefusal(tok.TokenHash)
		if refusal == "" {
			refusal = rolloutTokenUsedUp
		}
		return false, "", refusal
	}
	if err := s.createPeerFromEnrollment(req, clientIP, true); err != nil {
		log.Printf("[API] Failed to enroll %s by rollout token %d: %v", req.DeviceID, tok.ID, err)
		if err := s.db.ReleaseRolloutJoin(tok.ID); err != nil {
			log.Printf("[API] Give back the place of rollout token %d: %v", tok.ID, err)
		}
		return false, "", ""
	}
	s.placeEnrolledDevice(tok, req.DeviceID, req.Group)
	deviceToken, err := s.issueEnrollmentDeviceToken(req.DeviceID)
	if err != nil {
		// The computer is enrolled; it can get its token later with its key
		// proof. The rollout token is never handed out as its credential.
		log.Printf("[API] Failed to issue device token after rollout enroll for %s: %v", req.DeviceID, err)
		deviceToken = ""
	}
	if s.auditLog != nil {
		s.auditLog.Log("device_enrolled", clientIP, req.DeviceID, map[string]string{
			"mode": mode, "method": "rollout_token", "token_id": strconv.FormatInt(tok.ID, 10), "hostname": req.Hostname,
		})
	}
	return true, deviceToken, ""
}

// refuseRolloutToken answers a registration whose rollout token admits no
// more computers: 403 in the shape of a refused enrollment token, with the
// refusal's own error code.
func (s *Server) refuseRolloutToken(w http.ResponseWriter, req *EnrollmentRequest, refusal, clientIP, mode string) {
	resp := EnrollmentResponse{
		Status:     "rejected",
		DeviceID:   req.DeviceID,
		Error:      refusal,
		Message:    rolloutRefusalMessages[refusal],
		ServerTime: timeNowUnixMilli(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(resp)
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionPeerRegistrationRejected, clientIP, req.DeviceID, map[string]string{
			"reason": refusal,
			"stage":  "register",
			"mode":   mode,
		})
	}
}
