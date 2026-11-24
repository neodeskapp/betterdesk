package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// The browser viewer's sign-in with the device password an organization
// stored (BUGS L454): the server answers the device's password question,
// so the page never holds the password. GET /api/peers/{id}/connect-password
// still hands the password out readable to the command line and API
// tokens (org_peer_vault.go).

func (s *Server) connectAnswerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/peers/{id}/connect-password/answer", s.inProcessOnly(s.requirePermission(auth.PermDeviceConnect, s.scopedDevice(s.handleConnectPasswordAnswer))))
}

// orgConnectPassword finds the device password an organization stored for
// peerID that the caller may use: the one of the device's organization for
// its members, else the one of an organization whose address book the
// caller gets (a member with the shared address book on). A role that sees
// every organization may use any organization's. It returns the password,
// the organization and whether one was found.
func (s *Server) orgConnectPassword(r *http.Request, peerID string) (string, string, bool, error) {
	if s.peerVault == nil {
		return "", "", false, nil
	}
	serverLevel := auth.IsServerLevel(getRoleFromCtx(r))
	member := map[string]bool{}
	var candidates []string
	for _, orgID := range s.resolveUserOrgIDsForAB(r) {
		member[orgID] = true
	}
	deviceOrg, err := s.db.GetDeviceOrgID(peerID)
	if err != nil {
		return "", "", false, err
	}
	if deviceOrg != "" && (serverLevel || member[deviceOrg]) {
		candidates = append(candidates, deviceOrg)
	}
	for _, orgID := range s.resolveUserOrgIDsForAB(r) {
		if orgID != deviceOrg && s.orgSharedAddressBookEnabled(orgID) {
			candidates = append(candidates, orgID)
		}
	}
	if serverLevel {
		orgs, err := s.db.ListOrganizations()
		if err != nil {
			return "", "", false, err
		}
		for _, org := range orgs {
			if org != nil && org.ID != deviceOrg && !member[org.ID] && s.orgSharedAddressBookEnabled(org.ID) {
				candidates = append(candidates, org.ID)
			}
		}
	}
	for _, orgID := range candidates {
		row, err := s.db.GetOrgPeerCredential(orgID, peerID)
		if err != nil {
			return "", "", false, err
		}
		if row == nil || row.Ciphertext == "" {
			continue
		}
		plain, err := s.peerVault.Open(row.Nonce, row.Ciphertext, row.KeyID)
		if err != nil {
			log.Printf("[org] peer vault open error: %v", err)
			continue
		}
		if plain != "" {
			return plain, orgID, true, nil
		}
	}
	return "", "", false, nil
}

// POST /api/peers/{id}/connect-password/answer with {salt, challenge}: the
// answer to the device's password question, made from the device password
// an organization stored (orgConnectPassword), so the browser signs in
// without ever holding the password (BUGS L454). The answer is
// sha256(sha256(password + salt) + challenge), the one RustDesk's
// LoginRequest carries; it is good for this challenge only. 404 with
// {password_set:false} when there is no password the caller may use.
func (s *Server) handleConnectPasswordAnswer(w http.ResponseWriter, r *http.Request) {
	peerID := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Salt      string `json:"salt"`
		Challenge string `json:"challenge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if body.Salt == "" || body.Challenge == "" || len(body.Salt) > 256 || len(body.Challenge) > 256 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "salt_and_challenge_required"})
		return
	}
	plain, orgID, found, err := s.orgConnectPassword(r, peerID)
	if err != nil {
		log.Printf("[org] connect password answer for %s: %v", peerID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"password_set": false})
		return
	}
	first := sha256.Sum256([]byte(plain + body.Salt))
	answer := sha256.Sum256(append(first[:], body.Challenge...))
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionOrgPeerCredentialFetch, s.eventActor(r), peerID, map[string]string{
			"org_id": orgID,
			"actor":  actorFromCtx(r),
			"action": "login_answer",
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"password_set": true, "answer": base64.StdEncoding.EncodeToString(answer[:])})
}
