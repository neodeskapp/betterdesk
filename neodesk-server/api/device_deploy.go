package api

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/events"
)

// The answers of POST /api/devices/deploy that RustDesk clients read
// (ui_interface::deploy_device). Any other body is shown to the person who
// ran `rustdesk --deploy` as it is.
const (
	deployOK           = "OK"
	deployIDTaken      = "ID_TAKEN"
	deployInvalidInput = "INVALID_INPUT"
	deployNotEnabled   = "NOT_ENABLED"
)

// deployPeerIDRegexp is the id rule of the signal server's RegisterPk
// (signal.peerIDRegexp): an id it would refuse can't be deployed.
var deployPeerIDRegexp = regexp.MustCompile(`^[A-Za-z0-9_-]{6,16}$`)

// deployMaxUUID is the longest uuid RegisterPk takes, in bytes.
const deployMaxUUID = 128

// enrollmentDecisionMu runs deploys, approvals and rejections one at a
// time. A deploy holds it from its identity checks to the peer row, so two
// machines deploying one id at once can't both pass the checks and the
// second overwrite the first one's key, and an admin's Reject can't land
// between the checks and the write.
var enrollmentDecisionMu sync.Mutex

// deployTokenPeerPrefix marks the binding of a token a deploy used:
// peer_id "deploy:<id>". No device has such an id, so the token is never
// taken as that device's credential (device self routes, CDAP, token
// reissue), only for deploying the same id again.
const deployTokenPeerPrefix = "deploy:"

func deployTokenPeer(id string) string { return deployTokenPeerPrefix + id }

// deployRequest is the body `rustdesk --deploy --token <token>` posts: the
// id to deploy (the client's own, or the one given with --id), and the
// client's uuid and Ed25519 public key, both in base64.
type deployRequest struct {
	ID   string `json:"id"`
	UUID string `json:"uuid"`
	PK   string `json:"pk"`
}

// pendingDeployEntry is the part of a waiting request that tells which
// machine it is: the uuid of the signal server's first RegisterPk, or the
// uuid a NeoDesk client sent with its registration.
type pendingDeployEntry struct {
	pendingEnrollmentMeta
	RegisterUUID string `json:"register_uuid"`
}

func writeDeployResult(w http.ResponseWriter, status int, result string) {
	writeJSON(w, status, map[string]string{"result": result})
}

// handleDeviceDeploy admits a RustDesk client that runs
// `rustdesk --deploy --token <device token>` in Managed and Locked mode.
// POST /api/devices/deploy, Authorization: Bearer <device token>
//
// The token is a device token from the console's Device tokens page, with
// the same checks as at registration: not revoked, not expired, uses left,
// and unbound or bound to this id. The first id deployed with a token binds
// it. A successful deploy is an approval: the peer row is written with the
// client's uuid and key, so the signal server takes its next RegisterPk and
// refuses any other key for the id.
//
// The token never changes a device the server knows. An id whose stored
// uuid or key differs from the request's answers ID_TAKEN, as does an id
// waiting for approval from another machine. A known id with the same
// identity answers OK and nothing changes. Removed, banned and rejected
// ids stay refused. Open mode needs no deploy and answers NOT_ENABLED.
func (s *Server) handleDeviceDeploy(w http.ResponseWriter, r *http.Request) {
	clientIP := s.remoteIP(r)

	var req deployRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDeployResult(w, http.StatusBadRequest, deployInvalidInput)
		return
	}

	candidates := enrollmentTokenCandidates(r, "")
	var tok *db.DeviceToken
	for _, candidate := range candidates {
		if len(candidate) > 512 {
			continue
		}
		found, err := s.db.ValidateToken(hashToken(candidate))
		if err != nil {
			log.Printf("[API] deploy: validate token: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}
		if found != nil {
			tok = found
			break
		}
	}
	if tok == nil {
		s.auditDeployRefusal(clientIP, req.ID, "deploy_token_rejected", nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "The device token is invalid, expired, revoked or used up"})
		return
	}

	mode := s.cfg.EnrollmentMode
	if mode == "" {
		mode = config.EnrollmentModeOpen
	}
	if mode != config.EnrollmentModeManaged && mode != config.EnrollmentModeLocked {
		writeDeployResult(w, http.StatusOK, deployNotEnabled)
		return
	}

	id := strings.TrimSpace(req.ID)
	uuidRaw, uuidErr := base64.StdEncoding.DecodeString(strings.TrimSpace(req.UUID))
	pk, pkErr := base64.StdEncoding.DecodeString(strings.TrimSpace(req.PK))
	if !deployPeerIDRegexp.MatchString(id) || uuidErr != nil || len(uuidRaw) == 0 || len(uuidRaw) > deployMaxUUID ||
		pkErr != nil || len(pk) != 32 {
		s.auditDeployRefusal(clientIP, id, "invalid_input", tok)
		writeDeployResult(w, http.StatusBadRequest, deployInvalidInput)
		return
	}

	// A token an admin bound to this id in the console is that device's
	// already; any other binding is to another device.
	if tok.PeerID != "" && tok.PeerID != id && tok.PeerID != deployTokenPeer(id) {
		s.auditDeployRefusal(clientIP, id, "deploy_token_bound_elsewhere", tok)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This device token belongs to another device"})
		return
	}

	enrollmentDecisionMu.Lock()
	defer enrollmentDecisionMu.Unlock()

	// An admin's decision about the id stands over a token, as at
	// registration. A lookup that fails stops the deploy.
	internalError := func(what string, err error) {
		log.Printf("[API] deploy: %s %s: %v", what, id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
	}
	banned, err := s.db.IsPeerBanned(id)
	if err != nil {
		internalError("ban check of", err)
		return
	}
	if banned {
		s.auditDeployRefusal(clientIP, id, "banned", tok)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device is banned"})
		return
	}
	removed, err := s.db.IsPeerSoftDeleted(id)
	if err != nil {
		internalError("removal check of", err)
		return
	}
	if removed {
		s.auditDeployRefusal(clientIP, id, "soft_deleted", tok)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device has been removed"})
		return
	}
	rejected, err := s.db.GetConfig(rejectedDevicePrefix + id)
	if err != nil {
		internalError("rejection check of", err)
		return
	}
	if rejected != "" {
		s.auditDeployRefusal(clientIP, id, "rejected", tok)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device enrollment was rejected"})
		return
	}
	// An id renamed in the console belongs to the device under its new id
	// while that one exists.
	renamed, err := s.db.IsRenamedPeerID(id)
	if err != nil {
		internalError("rename check of", err)
		return
	}
	if renamed {
		newID, err := s.db.GetLatestRenameTarget(id)
		if err != nil {
			internalError("rename target of", err)
			return
		}
		var successor *db.Peer
		if newID != "" {
			if successor, err = s.db.GetPeer(newID); err != nil {
				internalError("rename successor of", err)
				return
			}
		}
		if newID == "" || successor != nil {
			s.auditDeployRefusal(clientIP, id, "renamed", tok)
			writeDeployResult(w, http.StatusConflict, deployIDTaken)
			return
		}
	}

	existing, err := s.db.GetPeer(id)
	if err != nil {
		log.Printf("[API] deploy: load peer %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	if existing != nil {
		if (len(existing.PK) > 0 && !bytes.Equal(existing.PK, pk)) ||
			(existing.UUID != "" && !deployUUIDMatches(existing.UUID, uuidRaw)) {
			s.auditDeployRefusal(clientIP, id, "id_taken", tok)
			writeDeployResult(w, http.StatusConflict, deployIDTaken)
			return
		}
		if existing.Disabled {
			s.auditDeployRefusal(clientIP, id, "disabled", tok)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device is disabled"})
			return
		}
		// Already on the server with this identity: nothing to do, and the
		// token is not used.
		writeDeployResult(w, http.StatusOK, deployOK)
		return
	}

	var pending pendingDeployEntry
	raw, err := s.db.GetConfig(pendingDevicePrefix + id)
	if err != nil {
		internalError("waiting request of", err)
		return
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &pending)
		for _, stored := range []string{pending.RegisterUUID, pending.UUID} {
			if stored != "" && !deployUUIDMatches(stored, uuidRaw) {
				s.auditDeployRefusal(clientIP, id, "id_taken", tok)
				writeDeployResult(w, http.StatusConflict, deployIDTaken)
				return
			}
		}
	}

	if tok.PeerID == "" {
		if err := s.db.BindTokenToPeer(tok.TokenHash, deployTokenPeer(id)); err != nil {
			log.Printf("[API] deploy: bind token %d to %s: %v", tok.ID, id, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
			return
		}
		// Only a new token binds. Of two deploys with one new token at
		// once, only the one whose id the token now names goes on.
		bound, err := s.db.GetDeviceTokenByHash(tok.TokenHash)
		if err != nil || bound == nil || bound.PeerID != deployTokenPeer(id) {
			s.auditDeployRefusal(clientIP, id, "deploy_token_bound_elsewhere", tok)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "This device token belongs to another device"})
			return
		}
	}
	if err := s.db.IncrementTokenUse(tok.TokenHash); err != nil {
		log.Printf("[API] deploy: count the use of token %d: %v", tok.ID, err)
	}

	deviceType := strings.TrimSpace(pending.DeviceType)
	if deviceType == "" {
		deviceType = "rustdesk"
	}
	enrollment := &EnrollmentRequest{
		DeviceID:     id,
		UUID:         rendezvousUUIDHex(uuidRaw),
		Hostname:     pending.Hostname,
		Platform:     pending.Platform,
		Version:      pending.Version,
		DeviceType:   deviceType,
		Tags:         pending.Tags,
		RendezvousPK: pk,
	}
	if err := s.createPeerFromEnrollment(enrollment, clientIP, true); err != nil {
		log.Printf("[API] deploy: create peer %s: %v", id, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal server error"})
		return
	}
	s.placeEnrolledDevice(tok, id, "")
	s.db.SetConfig("device_sync_mode_"+id, "standard")
	s.db.DeleteConfig(pendingDevicePrefix + id)

	s.storeEnrollmentDecision(enrollmentDecision{
		DeviceID:   id,
		UUID:       enrollment.UUID,
		Hostname:   pending.Hostname,
		Platform:   pending.Platform,
		Version:    pending.Version,
		DeviceType: deviceType,
		Tags:       normalizeEnrollmentTags(pending.Tags),
		IP:         clientIP,
		Status:     "approved",
		DecidedAt:  timeNowISO(),
		CreatedAt:  pending.CreatedAt,
		Actor:      "token:" + tok.Name,
	})
	if s.auditLog != nil {
		s.auditLog.Log("device_enrolled", clientIP, id, map[string]string{
			"mode": mode, "method": "deploy", "token_id": strconv.FormatInt(tok.ID, 10), "token_name": tok.Name,
		})
	}
	if s.eventBus != nil {
		s.eventBus.Publish(events.Event{
			Type: "device_approved",
			Data: map[string]string{"device_id": id, "sync_mode": "standard"},
		})
	}
	log.Printf("[API] Deployed %s with device token %d (%s mode)", id, tok.ID, mode)
	writeDeployResult(w, http.StatusOK, deployOK)
}

// auditDeployRefusal logs a deploy the server turned down.
func (s *Server) auditDeployRefusal(clientIP, id, reason string, tok *db.DeviceToken) {
	if s.auditLog == nil {
		return
	}
	details := map[string]string{"reason": reason, "stage": "deploy"}
	if tok != nil {
		details["token_id"] = strconv.FormatInt(tok.ID, 10)
		details["token_name"] = tok.Name
	}
	s.auditLog.Log(audit.ActionPeerRegistrationRejected, clientIP, truncate(id, 64), details)
}

// deployUUIDMatches reports whether stored, a uuid the server keeps for an
// id, is the machine uuid uuid: in the signal server's hex form, or in a
// form a NeoDesk client registered it in.
func deployUUIDMatches(stored string, uuid []byte) bool {
	stored = strings.TrimSpace(stored)
	return stored != "" && (strings.EqualFold(stored, rendezvousUUIDHex(uuid)) ||
		sameRegisteredUUID(stored, string(uuid)) ||
		sameRegisteredUUID(stored, base64.StdEncoding.EncodeToString(uuid)))
}

// rendezvousUUIDHex is the uuid as the signal server stores it from a
// RegisterPk (signal.normalizePeerUUIDBytes, then hex): 16 bytes as they
// are, an even-length hex text decoded, anything else as it is.
func rendezvousUUIDHex(raw []byte) string {
	if len(raw) != 16 && len(raw)%2 == 0 {
		if decoded, err := hex.DecodeString(string(raw)); err == nil && len(decoded) > 0 {
			raw = decoded
		}
	}
	return hex.EncodeToString(raw)
}
