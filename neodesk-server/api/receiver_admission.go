package api

// One-time admissions of quick support sessions. When the console offers
// the person who got quick support to keep the NeoDesk app installed, the
// redeem answer carries an admission: a random secret that lets one
// device enroll in managed or locked mode without a join request, as a
// single-use enrollment token without a folder would. The console asks
// for it over the in-process route below; the app sends it as
// "admission" with POST /api/devices/register. The secret is kept only
// as its SHA-256, under receiver_admission_<hash>, with the session and
// the end of its 24 hours; its first use deletes it.
// Wire format of the app's side: console/routes/onboard/PROTOCOL.md.

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
)

const (
	receiverAdmissionPrefix = "receiver_admission_"
	// ReceiverAdmissionLifetime is how long an admission can be used.
	ReceiverAdmissionLifetime = 24 * time.Hour
	// receiverAdmissionLen is the length of the secret in hex digits.
	receiverAdmissionLen = 48
)

// admissionMu keeps the read and the delete of an admission together, so
// it admits one device only.
var admissionMu sync.Mutex

// receiverAdmission is the stored record of an admission.
type receiverAdmission struct {
	Session   string `json:"session"`
	ExpiresAt string `json:"expires_at"`
}

// handleReceiverAdmissionCreate makes an admission for a quick support
// session: {session} -> {success, admission, expires_at}. Expired
// admissions are removed on the way.
// POST /api/onboard/admissions
func (s *Server) handleReceiverAdmissionCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !supportSessionID.MatchString(body.Session) {
		onboardFail(w, http.StatusBadRequest, "invalid_session")
		return
	}
	s.pruneReceiverAdmissions()
	secret, err := generateSecureToken(receiverAdmissionLen)
	if err != nil {
		onboardFail(w, http.StatusInternalServerError, "admission_failed")
		return
	}
	expires := s.clock().Add(ReceiverAdmissionLifetime).UTC().Format(time.RFC3339)
	data, _ := json.Marshal(receiverAdmission{Session: body.Session, ExpiresAt: expires})
	if err := s.db.SetConfig(receiverAdmissionPrefix+hashToken(secret), string(data)); err != nil {
		onboardFail(w, http.StatusInternalServerError, "store_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "admission": secret, "expires_at": expires})
}

// clock is the time admissions are made and checked by.
func (s *Server) clock() time.Time {
	if s.admissionNow != nil {
		return s.admissionNow()
	}
	return time.Now()
}

// pruneReceiverAdmissions deletes the admissions whose time is up.
func (s *Server) pruneReceiverAdmissions() {
	rows, err := s.db.ListConfigByPrefix(receiverAdmissionPrefix)
	if err != nil {
		return
	}
	now := s.clock()
	for _, row := range rows {
		var rec receiverAdmission
		if json.Unmarshal([]byte(row.Value), &rec) != nil || admissionExpired(rec, now) {
			_ = s.db.DeleteConfig(row.Key)
		}
	}
}

func admissionExpired(rec receiverAdmission, now time.Time) bool {
	end, err := time.Parse(time.RFC3339, rec.ExpiresAt)
	return err != nil || !now.Before(end)
}

// spendReceiverAdmission uses up an admission: it reports the session it
// was made for when the admission exists and its time is not up, and
// deletes it either way.
func (s *Server) spendReceiverAdmission(secret string) (string, bool) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", false
	}
	key := receiverAdmissionPrefix + hashToken(secret)
	admissionMu.Lock()
	defer admissionMu.Unlock()
	raw, err := s.db.GetConfig(key)
	if err != nil || raw == "" {
		return "", false
	}
	if err := s.db.DeleteConfig(key); err != nil {
		log.Printf("[API] Delete a used quick support admission: %v", err)
		return "", false
	}
	var rec receiverAdmission
	if json.Unmarshal([]byte(raw), &rec) != nil || admissionExpired(rec, s.clock()) {
		return "", false
	}
	return rec.Session, true
}

// answerAdmission answers a registration in managed or locked mode that
// carries an admission: approved with a device token when the admission
// works, else 403 admission_rejected. The device goes into no folder.
func (s *Server) answerAdmission(w http.ResponseWriter, req *EnrollmentRequest, admission, clientIP, mode string) {
	session, ok := s.spendReceiverAdmission(admission)
	if ok {
		if err := s.createPeerFromEnrollment(req, clientIP, true); err != nil {
			log.Printf("[API] Failed to enroll %s by quick support admission: %v", req.DeviceID, err)
			ok = false
		}
	}
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(EnrollmentResponse{
			Status:     "rejected",
			DeviceID:   req.DeviceID,
			Error:      "admission_rejected",
			Message:    "The admission of the quick support session is invalid, expired, or already used",
			ServerTime: timeNowUnixMilli(),
		})
		if s.auditLog != nil {
			s.auditLog.Log(audit.ActionPeerRegistrationRejected, clientIP, req.DeviceID, map[string]string{
				"reason": "admission_rejected", "stage": "register", "mode": mode,
			})
		}
		return
	}
	resp := s.buildEnrollmentResponse("approved", req.DeviceID, "standard", "")
	if token, err := s.issueEnrollmentDeviceToken(req.DeviceID); err == nil {
		resp.DeviceToken = token
	} else {
		log.Printf("[API] Failed to issue device token after admission for %s: %v", req.DeviceID, err)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
	if s.auditLog != nil {
		s.auditLog.Log("device_enrolled", clientIP, req.DeviceID, map[string]string{
			"mode": mode, "method": "quick_support_admission", "session": session, "hostname": req.Hostname,
		})
	}
}
