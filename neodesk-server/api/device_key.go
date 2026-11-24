package api

// A computer that comes back with a new key keeps its device ID: the ID
// comes from the machine, and a reinstall, a reset or an install after
// "Run without installing" makes a new key. The key binds the ID, and
// neither the ID nor the machine's UUID is a secret, so the server does
// not let a new key in on its own. An invite cannot vouch for it either:
// it names no computer and can be good for many, so anyone who has one
// could take over any enrolled ID with it.
//
// A redeem refused for its key leaves the key it brought as a key
// request: an administrator who expects it approves the new key on the
// device's page (or deletes the device), and the computer then rejoins
// with the same invite, which the refusal did not spend. Approving
// replaces the key the server knows for the ID, for the signal server and
// for the device's signed requests, and ends what the old key had: the
// device's tokens, its management socket and the state of its signed
// telemetry.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const deviceKeyRequestPrefix = "device_key_request_"

// keyChangedRefusal is the refusal of a computer whose ID is enrolled
// with another key. The client shows it after "did not accept this
// computer:".
const keyChangedRefusal = "This computer's ID is already enrolled with another key. Ask your administrator to approve the new key or to delete the device in the console."

// deviceKeyRequest is the new key a refused computer brought, and what it
// said about itself.
type deviceKeyRequest struct {
	PublicKey   string `json:"public_key"`
	Hostname    string `json:"hostname"`
	Platform    string `json:"platform"`
	Version     string `json:"version"`
	IP          string `json:"ip"`
	RequestedAt string `json:"requested_at"`
}

// storeDeviceKeyRequest keeps the key of a computer an invite redeem
// refused for its key, with the address it came from. A later request
// replaces an earlier one.
func (s *Server) storeDeviceKeyRequest(req *EnrollmentRequest, ip string) {
	key, err := canonicalizeDevicePublicKey(req.PublicKey)
	if err != nil {
		return
	}
	data, err := json.Marshal(deviceKeyRequest{
		PublicKey: key, Hostname: req.Hostname, Platform: req.Platform, Version: req.Version,
		IP: ip, RequestedAt: s.now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = s.db.SetConfig(deviceKeyRequestPrefix+req.DeviceID, string(data))
}

// loadDeviceKeyRequest is the key request of the device, nil when there is
// none.
func (s *Server) loadDeviceKeyRequest(deviceID string) *deviceKeyRequest {
	raw, err := s.db.GetConfig(deviceKeyRequestPrefix + deviceID)
	if err != nil || raw == "" {
		return nil
	}
	var req deviceKeyRequest
	if json.Unmarshal([]byte(raw), &req) != nil || req.PublicKey == "" {
		return nil
	}
	return &req
}

// keyRequestOf is the view of the device's key request, nil when it has
// none.
func (s *Server) keyRequestOf(deviceID string) map[string]any {
	if req := s.loadDeviceKeyRequest(deviceID); req != nil {
		return keyRequestView(req)
	}
	return nil
}

// keyRequestView is a key request as the console shows it: the key's
// fingerprint instead of the key.
func keyRequestView(req *deviceKeyRequest) map[string]any {
	fingerprint := ""
	if raw, err := base64.StdEncoding.DecodeString(req.PublicKey); err == nil {
		fingerprint = keyFingerprint(raw)
	}
	return map[string]any{
		"hostname": req.Hostname, "platform": req.Platform, "version": req.Version,
		"ip": req.IP, "requested_at": req.RequestedAt, "fingerprint": fingerprint,
	}
}

// keyFingerprint is the first 8 bytes of a key in hex groups, to tell two
// keys apart on screen.
func keyFingerprint(key []byte) string {
	if len(key) > 8 {
		key = key[:8]
	}
	parts := make([]string, len(key))
	for i, b := range key {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// handleDeviceKeyRequest answers the key request of a device, null when it
// has none.
// GET /api/onboard/devices/{id}/key-request
func (s *Server) handleDeviceKeyRequest(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var view any
	if req := s.loadDeviceKeyRequest(id); req != nil {
		view = keyRequestView(req)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "request": view})
}

// handleApproveDeviceKey makes the requested key the device's key.
// POST /api/onboard/devices/{id}/key-request/approve {actor}
func (s *Server) handleApproveDeviceKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Actor string `json:"actor"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.onboardMu.Lock()
	defer s.onboardMu.Unlock()
	req := s.loadDeviceKeyRequest(id)
	if req == nil {
		onboardFail(w, http.StatusNotFound, "no_key_request")
		return
	}
	if err := s.replaceDeviceKey(id, req.PublicKey); err != nil {
		onboardFail(w, http.StatusInternalServerError, "replace_failed")
		return
	}
	_ = s.db.DeleteConfig(deviceKeyRequestPrefix + id)
	if s.auditLog != nil {
		actor := strings.TrimSpace(body.Actor)
		if actor == "" {
			actor = actorFromCtx(r)
		}
		raw, _ := base64.StdEncoding.DecodeString(req.PublicKey)
		s.auditLog.Log("device_key_replaced", s.remoteIP(r), actor, map[string]string{
			"device_id": id, "hostname": req.Hostname, "from_ip": req.IP, "fingerprint": keyFingerprint(raw),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// replaceDeviceKey binds key (base64 Ed25519) to the device: in the peer
// row and the live peer map for the signal server, and as the key of its
// signed requests. The device's tokens end, its management socket closes
// and the state of its signed telemetry starts over.
func (s *Server) replaceDeviceKey(deviceID, key string) error {
	canonical, err := canonicalizeDevicePublicKey(key)
	if err != nil {
		return err
	}
	pk, _ := base64.StdEncoding.DecodeString(canonical)
	p, err := s.db.GetPeer(deviceID)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("no device %s", deviceID)
	}
	p.PK = pk
	if err := s.db.UpsertPeer(p); err != nil {
		return err
	}
	if err := s.storeNdMgmtPublicKey(deviceID, canonical); err != nil {
		return err
	}
	if s.peers != nil {
		s.peers.SetKeys(deviceID, nil, pk)
	}
	// The signal server's note of the key it refused (signal: refuseKey)
	_ = s.db.DeleteConfig(keyRefusedNote + deviceID)
	if err := s.db.EndDeviceTokensExcept(deviceID, 0); err != nil {
		return err
	}
	if err := s.resetSecureDeviceState(deviceID); err != nil {
		return err
	}
	if old := mgmtHub.Get(deviceID); old != nil {
		mgmtHub.Remove(old)
		old.Cancel()
		old.Conn.Close(websocket.StatusPolicyViolation, "device key replaced")
	}
	return nil
}

// keyRefusedNote is the prefix of the signal server's note of a key it
// refused, by device id (signal: keyRefusedConfigPrefix). The note goes when
// the device registers with its stored key or an administrator approves
// the new one.
const keyRefusedNote = "device_key_refused_"

// keyRefused reports whether the signal server refuses the device's key:
// its live entry says so, or, while the device has no live entry, the
// signal server's note. The refused heartbeats of such a device do not
// renew its entry, so the sweeper takes it out of the map until the next
// heartbeat puts it back, and a restart empties the map; in between only
// the note says the key is refused (L452).
func (s *Server) keyRefused(id string) bool {
	if s.peers != nil {
		if s.peers.KeyRefused(id) {
			return true
		}
		if s.peers.Get(id) != nil {
			return false
		}
	}
	if s.db == nil {
		return false
	}
	note, err := s.db.GetConfig(keyRefusedNote + id)
	return err == nil && note != ""
}
