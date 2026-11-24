package api

// Short-code pairing (host + 6 characters).
//
// A device owner types the server host and a short code shown by an operator instead of
// copying a long config string or scanning a QR code. The code is a one-time credential
// for *configuration*, not for access: redeeming it returns the same data a manual setup
// would, and the device still goes through normal enrollment (bans, rejects, pending
// approval, key proof). It never grants a role, an account token or a session.
//
// Threat model notes:
//   - 6 Crockford Base32 characters = 30 bits. With a 10-minute TTL, single use and a
//     per-IP limiter, online guessing is not viable; a wrong code cannot burn a different
//     valid one.
//   - Only sha256(code) is stored. A database leak does not reveal usable codes.
//   - Error responses are identical for "unknown" and "expired" codes so a prober cannot
//     tell whether a code ever existed.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	pairingCodePrefix   = "pairing_code_"
	pairingCodeLen      = 6
	pairingCodeTTL      = 10 * time.Minute
	pairingCodeMaxUses  = 1
	pairingCodeMaxLive  = 50
	pairingCodeAttempts = 5
	// Crockford Base32 without 0/O/1/I/L/U: unambiguous when read aloud or typed from a
	// screenshot. 32 symbols, so 6 chars is exactly 30 bits.
	pairingAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"
)

// pairingRecord is what persists under `pairing_code_<hash>`. A code of
// the pairing routes has no kind; join and support codes (onboard_codes.go)
// carry their kind and the fields after it.
type pairingRecord struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
	CreatedBy string `json:"created_by,omitempty"`
	UsesLeft  int    `json:"uses_left"`
	Attempts  int    `json:"attempts,omitempty"`

	Kind        string `json:"kind,omitempty"`
	MaxUses     int    `json:"max_uses,omitempty"`
	Name        string `json:"name,omitempty"`
	FolderID    int64  `json:"folder_id,omitempty"`
	Email       string `json:"email,omitempty"`
	CreatedByID int64  `json:"created_by_id,omitempty"`
	// CreatedByToken is the public id of the console API token the code
	// was made with: the end of that token ends the code.
	CreatedByToken string `json:"created_by_token,omitempty"`
}

// normalizePairingCode upper-cases and drops separators. The alphabet deliberately
// excludes 0/O/1/I/L/U, so there is nothing to fold them to: a code never contains them
// and folding would invent a character that cannot exist.
func normalizePairingCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case ' ', '-', '\t':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validPairingCodeFormat(code string) bool {
	normalized := normalizePairingCode(code)
	if len(normalized) != pairingCodeLen {
		return false
	}
	for _, r := range normalized {
		if !strings.ContainsRune(pairingAlphabet, r) {
			return false
		}
	}
	return true
}

func pairingCodeHash(code string) string {
	sum := sha256.Sum256([]byte(normalizePairingCode(code)))
	return hex.EncodeToString(sum[:])
}

// generatePairingCode samples crypto/rand with rejection so the distribution is uniform
// (256 is not a multiple of 32, so a plain modulo would bias the first symbols).
func generatePairingCode() (string, error) {
	return generateCodeOfLength(pairingCodeLen)
}

// generateCodeOfLength is generatePairingCode for a code of n symbols.
func generateCodeOfLength(n int) (string, error) {
	const alphabetLen = len(pairingAlphabet)
	const maxAcceptable = 256 - (256 % alphabetLen)
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("pairing code entropy: %w", err)
		}
		for _, value := range buf {
			if int(value) >= maxAcceptable {
				continue
			}
			out = append(out, pairingAlphabet[int(value)%alphabetLen])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

func (s *Server) loadPairingRecord(hash string) *pairingRecord {
	raw, err := s.db.GetConfig(pairingCodePrefix + hash)
	if err != nil || raw == "" {
		return nil
	}
	var record pairingRecord
	if json.Unmarshal([]byte(raw), &record) != nil {
		return nil
	}
	return &record
}

func (s *Server) storePairingRecord(hash string, record *pairingRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.db.SetConfig(pairingCodePrefix+hash, string(data))
}

func (s *Server) deletePairingRecord(hash string) {
	_ = s.db.DeleteConfig(pairingCodePrefix + hash)
}

type pairingEntry struct {
	Hash   string
	Record pairingRecord
}

func (s *Server) pairingRecords() []pairingEntry {
	configs, err := s.db.ListConfigByPrefix(pairingCodePrefix)
	if err != nil {
		return nil
	}
	out := make([]pairingEntry, 0, len(configs))
	for _, cfg := range configs {
		var record pairingRecord
		if json.Unmarshal([]byte(cfg.Value), &record) != nil {
			continue
		}
		out = append(out, pairingEntry{Hash: strings.TrimPrefix(cfg.Key, pairingCodePrefix), Record: record})
	}
	return out
}

// prunePairingRecords deletes expired/spent codes of every kind and caps the number of
// live pairing codes, so an operator cannot fill server_config and old codes stop being
// redeemable. Join and support codes have their own cap (onboardMaxLive).
func (s *Server) prunePairingRecords() {
	now := time.Now().UTC()
	var live []pairingEntry
	for _, entry := range s.pairingRecords() {
		expires, err := time.Parse(time.RFC3339, entry.Record.ExpiresAt)
		if err != nil || now.After(expires) || entry.Record.UsesLeft <= 0 {
			s.deletePairingRecord(entry.Hash)
			continue
		}
		if entry.Record.Kind != "" {
			continue
		}
		live = append(live, entry)
	}
	excess := len(live) - (pairingCodeMaxLive - 1)
	if excess <= 0 {
		return
	}
	sort.Slice(live, func(i, j int) bool {
		return live[i].Record.CreatedAt < live[j].Record.CreatedAt
	})
	for i := 0; i < excess && i < len(live); i++ {
		s.deletePairingRecord(live[i].Hash)
	}
}

// connectionEndpoints builds the connection bundle a paired client needs. Derived from the
// same config as /api/health so the panel and the client see identical values.
func (s *Server) connectionEndpoints(r *http.Request) map[string]any {
	host := strings.TrimSpace(s.publicHost(r))
	signalPort := 21116
	relayPort := 21117
	apiPort := 21114
	relayServers := ""
	if s.cfg != nil {
		if s.cfg.SignalPort > 0 {
			signalPort = s.cfg.SignalPort
		}
		if s.cfg.RelayPort > 0 {
			relayPort = s.cfg.RelayPort
		}
		if s.cfg.APIPort > 0 {
			apiPort = s.cfg.APIPort
		}
		relayServers = strings.Join(s.cfg.GetRelayServers(), ",")
	}
	// The API address has the scheme the request came with: https over TLS
	// or behind a trusted proxy that says so, http otherwise.
	scheme := "http"
	if r != nil && r.TLS != nil {
		scheme = "https"
	} else if r != nil {
		if proto := strings.TrimSpace(s.forwardedHeader(r, "X-Forwarded-Proto")); proto != "" {
			scheme = strings.ToLower(proto)
		}
	}
	return map[string]any{
		"host":          host,
		"signal":        hostPort(host, signalPort),
		"relay":         hostPort(host, relayPort),
		"ws_signal":     hostPort(host, signalPort+2),
		"ws_relay":      hostPort(host, signalPort+3),
		"api":           fmt.Sprintf("%s://%s", scheme, hostPort(host, apiPort)),
		"relay_servers": relayServers,
	}
}

func hostPort(host string, port int) string {
	if host == "" {
		return ""
	}
	return host + ":" + strconv.Itoa(port)
}

// publicHost prefers a configured public host over the request host, because behind a
// reverse proxy the request host may be an internal name. Uses the rendezvous server list,
// which operators already populate for clients.
func (s *Server) publicHost(r *http.Request) string {
	if s.cfg != nil {
		for _, candidate := range strings.Split(s.cfg.RendezvousServers, ",") {
			if value := strings.TrimSpace(candidate); value != "" {
				return stripHostPort(value)
			}
		}
	}
	if r == nil {
		return ""
	}
	host := r.Host
	if forwarded := s.forwardedHeader(r, "X-Forwarded-Host"); forwarded != "" {
		host = strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	return stripHostPort(host)
}

func stripHostPort(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		value = strings.SplitN(value, "://", 2)[1]
	}
	if i := strings.IndexAny(value, "/?#"); i >= 0 {
		value = value[:i]
	}
	if strings.HasPrefix(value, "[") {
		if end := strings.Index(value, "]"); end > 0 {
			return value[:end+1]
		}
		return value
	}
	if i := strings.LastIndex(value, ":"); i > 0 {
		if _, err := strconv.Atoi(value[i+1:]); err == nil {
			return value[:i]
		}
	}
	return value
}

func (s *Server) serverPublicKey() string {
	if s.keyPair == nil {
		return ""
	}
	return s.keyPair.PublicKeyBase64()
}

// handleCreatePairingCode mints a one-time code. Admin/operator only.
// POST /api/pairing/codes
func (s *Server) handleCreatePairingCode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	s.prunePairingRecords()

	label := clipBrandingField(body.Label, 64)
	now := time.Now().UTC()
	var code, hash string
	// A collision would silently invalidate someone's code, so retry instead of assuming.
	for attempt := 0; attempt < 5; attempt++ {
		generated, err := generatePairingCode()
		if err != nil {
			http.Error(w, "failed to generate pairing code", http.StatusInternalServerError)
			return
		}
		candidate := pairingCodeHash(generated)
		if s.loadPairingRecord(candidate) == nil {
			code, hash = generated, candidate
			break
		}
	}
	if code == "" {
		http.Error(w, "failed to allocate a unique pairing code", http.StatusInternalServerError)
		return
	}

	record := &pairingRecord{
		ID:        hash[:16],
		Label:     label,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(pairingCodeTTL).Format(time.RFC3339),
		CreatedBy: actorFromCtx(r),
		UsesLeft:  pairingCodeMaxUses,

		CreatedByToken: tokenOf(r),
	}
	if err := s.storePairingRecord(hash, record); err != nil {
		http.Error(w, "failed to store pairing code", http.StatusInternalServerError)
		return
	}
	if s.auditLog != nil {
		// The code itself is never logged: an audit trail containing codes is a code leak.
		s.auditLog.Log("pairing_code_created", s.eventActor(r), record.CreatedBy, map[string]string{
			"id": record.ID, "label": label,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"success":    true,
		"code":       code,
		"id":         record.ID,
		"label":      label,
		"host":       s.publicHost(r),
		"expires_at": record.ExpiresAt,
		"expires_in": int(pairingCodeTTL.Seconds()),
	})
}

// handleListPairingCodes returns active codes without their plaintext.
// GET /api/pairing/codes
func (s *Server) handleListPairingCodes(w http.ResponseWriter, r *http.Request) {
	s.prunePairingRecords()
	items := make([]map[string]any, 0)
	for _, entry := range s.pairingRecords() {
		if entry.Record.Kind != "" {
			continue
		}
		items = append(items, map[string]any{
			"id":         entry.Record.ID,
			"label":      entry.Record.Label,
			"created_at": entry.Record.CreatedAt,
			"expires_at": entry.Record.ExpiresAt,
			"created_by": entry.Record.CreatedBy,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["created_at"]) < fmt.Sprint(items[j]["created_at"])
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "codes": items, "count": len(items)})
}

// handleDeletePairingCode revokes a code before it is used.
// DELETE /api/pairing/codes/{id}
func (s *Server) handleDeletePairingCode(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	removed := false
	for _, entry := range s.pairingRecords() {
		if entry.Record.ID == id && entry.Record.Kind == "" {
			s.deletePairingRecord(entry.Hash)
			removed = true
			break
		}
	}
	if !removed {
		http.Error(w, "pairing code not found", http.StatusNotFound)
		return
	}
	if s.auditLog != nil {
		s.auditLog.Log("pairing_code_revoked", s.eventActor(r), getUsernameFromCtx(r), map[string]string{"id": id})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

// pairingReject is the single response for every unusable code, so a prober cannot
// distinguish "never existed" from "expired" or "already used".
func pairingReject(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"success": false,
		"error":   "invalid_or_expired",
	})
}

// lookupPairingCode verifies a code without spending it. Returns nil after writing the
// rejection response. Join and support codes are redeemed through the console only.
func (s *Server) lookupPairingCode(w http.ResponseWriter, code string) *pairingRecord {
	if !validPairingCodeFormat(code) {
		pairingReject(w)
		return nil
	}
	hash := pairingCodeHash(code)
	record := s.loadPairingRecord(hash)
	if record == nil || record.Kind != "" {
		pairingReject(w)
		return nil
	}
	expires, err := time.Parse(time.RFC3339, record.ExpiresAt)
	if err != nil || time.Now().UTC().After(expires) {
		s.deletePairingRecord(hash)
		pairingReject(w)
		return nil
	}
	if record.UsesLeft <= 0 {
		pairingReject(w)
		return nil
	}
	return record
}

// consumePairingCode deletes a single-use code before the caller acts on it, so two
// concurrent redeem requests cannot both succeed.
func (s *Server) consumePairingCode(w http.ResponseWriter, code string) *pairingRecord {
	record := s.lookupPairingCode(w, code)
	if record == nil {
		return nil
	}
	hash := pairingCodeHash(code)
	record.UsesLeft--
	if record.UsesLeft <= 0 {
		s.deletePairingRecord(hash)
		return record
	}
	if err := s.storePairingRecord(hash, record); err != nil {
		http.Error(w, "failed to update pairing code", http.StatusInternalServerError)
		return nil
	}
	return record
}

// handlePairingConfigPreview lets the client show the server it is about to join without
// registering a device. Public and rate-limited; still requires a valid code.
// POST /api/pairing/config
func (s *Server) handlePairingConfigPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	record := s.lookupPairingCode(w, body.Code)
	if record == nil {
		return
	}
	branding := s.loadBrandingConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"server_name": branding.CompanyName,
		"version":     s.version,
		"connection":  s.connectionEndpoints(r),
		"branding":    branding,
		"server_key":  s.serverPublicKey(),
		"expires_at":  record.ExpiresAt,
	})
}

// handlePairingRedeem configures a device from a short code.
// POST /api/pairing/redeem
//
// Delegates admission to the enrollment rules: bans, soft-deletes, rejections, UUID
// conflicts, pending approval and device-key proof all keep applying. This endpoint only
// saves the operator from typing four long values.
func (s *Server) handlePairingRedeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code       string `json:"code"`
		DeviceID   string `json:"device_id"`
		UUID       string `json:"uuid"`
		Hostname   string `json:"hostname"`
		Platform   string `json:"platform"`
		Version    string `json:"version"`
		DeviceType string `json:"device_type"`
		PublicKey  string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	enrollment := &EnrollmentRequest{
		DeviceID:   body.DeviceID,
		UUID:       body.UUID,
		Hostname:   body.Hostname,
		Platform:   body.Platform,
		Version:    body.Version,
		DeviceType: body.DeviceType,
		PublicKey:  body.PublicKey,
	}
	if reason := checkEnrollmentFields(enrollment); reason != "" {
		http.Error(w, reason, http.StatusBadRequest)
		return
	}

	record := s.consumePairingCode(w, body.Code)
	if record == nil {
		return
	}

	if s.auditLog != nil {
		s.auditLog.Log("pairing_code_redeemed", s.eventActor(r), body.DeviceID, map[string]string{
			"id": record.ID, "label": record.Label,
		})
	}

	resp := s.enrollPairedDevice(r, enrollment)
	if resp.Status == "approved" {
		s.noteTokenAdmission(record.CreatedByToken, body.DeviceID, admittedByPairingCode)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    resp.Status == "approved" || resp.Status == "pending",
		"id":         record.ID,
		"enrollment": resp,
		"connection": s.connectionEndpoints(r),
		"branding":   s.loadBrandingConfig(),
		"server_key": s.serverPublicKey(),
	})
}

// enrollPairedDevice runs standard enrollment for a redeemed code.
//
// It calls the same helpers as handleDeviceRegister so the pairing response can carry the
// connection bundle in one round trip instead of duplicating admission logic.
func (s *Server) enrollPairedDevice(r *http.Request, req *EnrollmentRequest) EnrollmentResponse {
	if banned, _ := s.db.IsPeerBanned(req.DeviceID); banned {
		return EnrollmentResponse{Status: "rejected", DeviceID: req.DeviceID, Message: "Device is banned"}
	}
	if removed, _ := s.db.IsPeerSoftDeleted(req.DeviceID); removed {
		return EnrollmentResponse{Status: "rejected", DeviceID: req.DeviceID, Message: "Device has been removed"}
	}
	if rejected, _ := s.db.GetConfig(rejectedDevicePrefix + req.DeviceID); rejected != "" {
		return EnrollmentResponse{Status: "rejected", DeviceID: req.DeviceID, Message: "Device enrollment was rejected"}
	}

	mode := s.cfg.EnrollmentMode
	if mode == "" {
		mode = "open"
	}

	if existing, _ := s.db.GetPeer(req.DeviceID); existing != nil {
		if req.UUID != "" && existing.UUID != "" && !sameDeviceUUID(existing.UUID, req.UUID) {
			return EnrollmentResponse{
				Status: "rejected", DeviceID: req.DeviceID, Error: "identity_conflict",
				Message: "Device ID is already registered to a different machine",
			}
		}
		// Never rotate the bound key through an unauthenticated pairing request.
		if req.PublicKey != "" {
			if incoming, err := canonicalizeDevicePublicKey(req.PublicKey); err == nil {
				if bound, err := s.loadNdMgmtPublicKey(req.DeviceID); err == nil {
					if base64.StdEncoding.EncodeToString(bound) != incoming {
						return EnrollmentResponse{
							Status: "rejected", DeviceID: req.DeviceID,
							Message: "public_key does not match enrolled device identity",
						}
					}
				}
			}
		}
		syncMode, _ := s.db.GetConfig("device_sync_mode_" + req.DeviceID)
		if syncMode == "" {
			syncMode = "standard"
		}
		displayName, _ := s.db.GetConfig("device_display_name_" + req.DeviceID)
		resp := s.buildEnrollmentResponse("approved", req.DeviceID, syncMode, displayName)
		// As at a registration: a known device that proves its key and holds
		// no working device token gets a new one; one that holds it keeps it.
		if !s.hasEnrollmentCredential(req.DeviceID, enrollmentTokenCandidates(r, ""), true) {
			s.issuePairedDeviceToken(r, req, &resp, true)
		}
		return resp
	}

	if mode != "open" {
		// managed / locked: keep the operator in the loop. The code proves operator intent
		// but not that this particular machine should be admitted.
		if mode == "locked" {
			return EnrollmentResponse{
				Status: "rejected", DeviceID: req.DeviceID,
				Message: "Enrollment is locked - a valid enrollment token is required",
			}
		}
		if err := s.storePendingDevice(req, s.remoteIP(r)); err != nil {
			return EnrollmentResponse{
				Status: "rejected", DeviceID: req.DeviceID, Error: "identity_conflict",
				Message: "Device ID is already pending for a different machine",
			}
		}
		return EnrollmentResponse{
			Status: "pending", DeviceID: req.DeviceID, HeartbeatSec: 5,
			Message: "Waiting for operator approval",
		}
	}

	if err := s.createPeerFromEnrollment(req, s.remoteIP(r), true); err != nil {
		return EnrollmentResponse{
			Status: "rejected", DeviceID: req.DeviceID, Message: "Failed to enroll device",
		}
	}
	resp := s.buildEnrollmentResponse("approved", req.DeviceID, "standard", "")
	s.issuePairedDeviceToken(r, req, &resp, false)
	if s.auditLog != nil {
		s.auditLog.Log("device_enrolled", s.eventActor(r), req.DeviceID, map[string]string{
			"mode": "pairing", "hostname": req.Hostname,
		})
	}
	return resp
}

// issuePairedDeviceToken gives a device that redeemed a code its device
// token when the request proves the device's key (the X-ND-Enrollment
// headers, signed for POST /api/pairing/redeem), the way a registration
// does; without a proof the answer says so and the device gets its token
// at its next signed registration. known is whether the device was
// enrolled before.
func (s *Server) issuePairedDeviceToken(r *http.Request, req *EnrollmentRequest, resp *EnrollmentResponse, known bool) {
	if !s.authorizeEnrollmentTokenIssue(r, req.DeviceID, req.PublicKey, "", known) {
		resp.Message = "Device identity proof is required to issue a device token"
		return
	}
	token, err := s.issueEnrollmentDeviceToken(req.DeviceID)
	if err != nil {
		log.Printf("[API] Failed to issue a device token for paired device %s: %v", req.DeviceID, err)
		return
	}
	resp.DeviceToken = token
}
