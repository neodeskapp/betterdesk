package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/guestaccess"
)

func (s *Server) guestAccessStore() *guestaccess.Store {
	return &guestaccess.Store{DB: s.db}
}

func (s *Server) isGuestAdmin(role string) bool {
	return role == auth.RoleAdmin || role == auth.RoleSuperAdmin || role == auth.RoleGlobalAdmin
}

// POST /api/guest/access-links
func (s *Server) handleGuestAccessCreate(w http.ResponseWriter, r *http.Request) {
	username := actorFromCtx(r)
	var body struct {
		PeerIDs    []string `json:"peer_ids"`
		TTLMinutes int      `json:"ttl_minutes"`
		ViewOnly   bool     `json:"view_only"`
		Label      string   `json:"label"`
		MaxUses    int      `json:"max_uses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	for _, id := range body.PeerIDs {
		if !s.deviceInScope(w, r, strings.TrimSpace(id)) {
			return
		}
	}
	if body.ViewOnly {
		if id, err := s.firstRustDeskDevice(body.PeerIDs); err != nil {
			writeInternalError(w, err, "firstRustDeskDevice")
			return
		} else if id != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "view_only is not available for RustDesk devices: the server cannot keep a guest from controlling " + id,
			})
			return
		}
	}
	var creatorID int64
	if account, err := s.db.GetUser(username); err == nil && account != nil {
		creatorID = account.ID
	}
	token, grant, err := s.guestAccessStore().Create(body.PeerIDs, username, creatorID, body.TTLMinutes, body.ViewOnly, body.Label, body.MaxUses)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entryPeer := grant.PeerIDs[0]
	path := "/remote/guest?t=" + token
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionPeerUpdated, username, entryPeer, map[string]string{
			"event":       "guest_access_created",
			"grant_id":    grant.ID,
			"peer_count":  strconv.Itoa(len(grant.PeerIDs)),
			"ttl_minutes": strconv.Itoa(body.TTLMinutes),
			"view_only":   strconv.FormatBool(body.ViewOnly),
		})
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":           grant.ID,
		"token":        token,
		"path":         path,
		"peer_ids":     grant.PeerIDs,
		"view_only":    grant.ViewOnly,
		"expires_at":   grant.ExpiresAt.Format(time.RFC3339),
		"label":        grant.Label,
		"token_prefix": grant.TokenPrefix,
	})
}

// firstRustDeskDevice returns the first of ids that is a RustDesk device,
// "" when all are mesh agents or CDAP devices. A view_only link holds only
// for those: their gateway opens the session in view only, while a RustDesk
// guest's input goes inside the encrypted stream. A device counts as a mesh
// agent or a CDAP device while its gateway has it connected, or by what the
// gateway stored: the device type mesh_agent, a CDAP manifest. When an id
// does not have the form of a device id, it returns "" and Create refuses
// the list as before.
func (s *Server) firstRustDeskDevice(ids []string) (string, error) {
	var trimmed []string
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if !guestPeerIDForm(id) {
			return "", nil
		}
		trimmed = append(trimmed, id)
	}
	for _, id := range trimmed {
		if (s.meshGw != nil && s.meshGw.IsConnected(id)) || (s.cdapGw != nil && s.cdapGw.IsConnected(id)) {
			continue
		}
		p, err := s.db.GetPeer(id)
		if err != nil {
			return "", fmt.Errorf("load device %s: %w", id, err)
		}
		if p != nil && p.DeviceType == "mesh_agent" {
			continue
		}
		manifest, err := s.db.GetConfig("cdap_manifest_" + id)
		if err != nil {
			return "", fmt.Errorf("load CDAP manifest of %s: %w", id, err)
		}
		if manifest != "" {
			continue
		}
		return id, nil
	}
	return "", nil
}

// guestPeerIDForm is the form guestaccess.Store.Create takes a device id
// in: 3 to 64 letters, digits, _ and -.
func guestPeerIDForm(id string) bool {
	if len(id) < 3 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// GET /api/guest/access-links/validate?token=&peer_id=
func (s *Server) handleGuestAccessValidate(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	peerID := strings.TrimSpace(r.URL.Query().Get("peer_id"))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token required"})
		return
	}
	grant, err := s.guestAccessStore().Validate(token, peerID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"valid": false, "error": err.Error()})
		return
	}
	pub := guestaccess.ToPublic(grant)
	writeJSON(w, http.StatusOK, pub)
}

// POST /api/guest/access-links/consume
// Atomically consumes one use after the panel has accepted a guest session.
func (s *Server) handleGuestAccessConsume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token  string `json:"token"`
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	grant, err := s.guestAccessStore().Consume(body.Token, body.PeerID)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, guestaccess.ToPublic(grant))
}

// POST /api/guest/access-links/events
// Receives bridge lifecycle events from the authenticated panel proxy. Secrets
// are deliberately not accepted or persisted here.
func (s *Server) handleGuestAccessEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Event      string `json:"event"`
		GrantID    string `json:"grant_id"`
		PeerID     string `json:"peer_id"`
		SessionID  string `json:"session_id"`
		Transport  string `json:"transport"`
		SourceIP   string `json:"source_ip"`
		DurationMS int64  `json:"duration_ms"`
		Reason     string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if body.Event != "opened" && body.Event != "closed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid guest event"})
		return
	}
	if body.GrantID == "" || body.PeerID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "grant_id and peer_id required"})
		return
	}
	action := audit.ActionGuestSessionOpened
	if body.Event == "closed" {
		action = audit.ActionGuestSessionClosed
	}
	details := map[string]string{
		"grant_id":    body.GrantID,
		"peer_id":     body.PeerID,
		"session_id":  body.SessionID,
		"transport":   body.Transport,
		"source_ip":   body.SourceIP,
		"duration_ms": strconv.FormatInt(body.DurationMS, 10),
		"reason":      body.Reason,
	}
	if s.auditLog != nil {
		s.auditLog.Log(action, body.SourceIP, body.PeerID, details)
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"recorded": true})
}

// GET /api/guest/access-links
func (s *Server) handleGuestAccessList(w http.ResponseWriter, r *http.Request) {
	username := actorFromCtx(r)
	role := getRoleFromCtx(r)
	grants, err := s.guestAccessStore().ListActive(username, s.isGuestAdmin(role))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	items := make([]map[string]interface{}, 0, len(grants))
	for _, g := range grants {
		items = append(items, map[string]interface{}{
			"id":           g.ID,
			"peer_ids":     g.PeerIDs,
			"view_only":    g.ViewOnly,
			"expires_at":   g.ExpiresAt.Format(time.RFC3339),
			"created_at":   g.CreatedAt.Format(time.RFC3339),
			"created_by":   g.CreatedBy,
			"label":        g.Label,
			"token_prefix": g.TokenPrefix,
			"use_count":    g.UseCount,
			"max_uses":     g.MaxUses,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"grants": items})
}

// DELETE /api/guest/access-links/{id}
func (s *Server) handleGuestAccessRevoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	username := actorFromCtx(r)
	role := getRoleFromCtx(r)
	ok, err := s.guestAccessStore().RevokeByID(id, username, s.isGuestAdmin(role))
	if err != nil {
		if err.Error() == "forbidden" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionPeerUpdated, username, id, map[string]string{
			"event":    "guest_access_revoked",
			"grant_id": id,
		})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// GET /api/guest/access-links/peers?token=  — safe device list for guest UI (ids + basic peer info)
func (s *Server) handleGuestAccessPeers(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token required"})
		return
	}
	grant, err := s.guestAccessStore().Validate(token, "")
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	onlineTimeout := 90 * time.Second
	devices := make([]map[string]interface{}, 0, len(grant.PeerIDs))
	for _, id := range grant.PeerIDs {
		item := map[string]interface{}{
			"id":       id,
			"online":   false,
			"hostname": "",
			"os":       "",
		}
		if s.peers != nil {
			item["online"] = s.peers.IsOnline(id, onlineTimeout)
		}
		if peer, err := s.db.GetPeer(id); err == nil && peer != nil {
			item["hostname"] = peer.Hostname
			item["os"] = peer.OS
			item["display_name"] = peer.DisplayName
			item["device_type"] = peer.DeviceType
		}
		devices = append(devices, item)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":      true,
		"view_only":  grant.ViewOnly,
		"expires_at": grant.ExpiresAt.Format(time.RFC3339),
		"label":      grant.Label,
		"devices":    devices,
	})
}
