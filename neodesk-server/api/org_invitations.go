package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// Redeeming an organization invitation. An invitation's code is 32 random
// bytes in hex; the table keeps only its SHA-256 (invitationHash), and the
// code goes out once, in the answer that creates the invitation. A person
// redeems it on the console's /invite page: the console asks lookup what
// the code is for and redeem to make a sign-in for the BetterDesk client
// in the invitation's organization (an organization account, the default
// role, the invitation's email). The two routes answer only the console
// (requireConsoleCaller), which limits them per address; a code that is
// unknown, used, expired or revoked, or whose organization is gone, gets
// the same 404 from both.

// invitationCode is the form of an invitation's code.
var invitationCode = regexp.MustCompile(`^[0-9a-f]{64}$`)

// orgUsernamePattern is the form of a username a person picks for the
// organization sign-in an invitation makes: 3 to 64 letters, digits and
// . _ @ -, as the console's own usernames.
var orgUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,64}$`)

// invitationHash is what the table stores for an invitation's code.
func invitationHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// openInvitation is the invitation of the code and its organization when
// the code can be used now; nil, nil when it cannot.
func (s *Server) openInvitation(code string) (*db.OrgInvitation, *db.Organization, error) {
	code = strings.ToLower(strings.TrimSpace(code))
	if !invitationCode.MatchString(code) {
		return nil, nil, nil
	}
	inv, err := s.db.GetOrgInvitationByToken(invitationHash(code))
	if err != nil || inv == nil {
		return nil, nil, err
	}
	if inv.UsedAt != nil || !time.Now().Before(inv.ExpiresAt) {
		return nil, nil, nil
	}
	org, err := s.db.GetOrganization(inv.OrgID)
	if err != nil || org == nil {
		return nil, nil, err
	}
	return inv, org, nil
}

func invitationNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
}

// POST /api/org-invitations/lookup {token}: the organization and the end of
// an invitation that can be used.
func (s *Server) handleLookupOrgInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	inv, org, err := s.openInvitation(body.Token)
	if err != nil {
		log.Printf("[org] invitation lookup: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if inv == nil {
		invitationNotFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"org_id":     org.ID,
		"org_name":   org.Name,
		"org_slug":   org.Slug,
		"expires_at": inv.ExpiresAt,
	})
}

// POST /api/org-invitations/redeem {token, username, password}: makes the
// organization sign-in and uses the invitation, in one transaction. 404 for
// a code that cannot be used, 400 for a username or password against the
// rules, 409 for a username the organization has; none of them uses the
// invitation.
func (s *Server) handleRedeemOrgInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	inv, org, err := s.openInvitation(body.Token)
	if err != nil {
		log.Printf("[org] invitation redeem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if inv == nil {
		invitationNotFound(w)
		return
	}
	username := strings.TrimSpace(body.Username)
	if !orgUsernamePattern.MatchString(username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "A username has 3 to 64 characters: letters, numbers and . _ @ - only."})
		return
	}
	if err := auth.CheckNewPassword(body.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "password hashing failed"})
		return
	}
	user := &db.OrgUser{
		ID:           uuid.New().String(),
		OrgID:        inv.OrgID,
		Username:     username,
		Email:        inv.Email,
		PasswordHash: hash,
		Role:         db.OrgRoleUser,
		CreatedAt:    time.Now().UTC(),
	}
	used, err := s.db.RedeemOrgInvitation(invitationHash(strings.ToLower(strings.TrimSpace(body.Token))), user)
	switch {
	case errors.Is(err, db.ErrInvitationNotOpen):
		invitationNotFound(w)
		return
	case errors.Is(err, db.ErrOrgUsernameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username already exists in this organization"})
		return
	case err != nil:
		log.Printf("[org] invitation redeem: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	if s.auditLog != nil {
		s.auditLog.Log("org_invitation_redeemed", s.remoteIP(r), getUsernameFromCtx(r), map[string]string{
			"org_id":        org.ID,
			"invitation_id": used.ID,
			"username":      username,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user":          user,
		"invitation_id": used.ID,
		"org":           map[string]string{"id": org.ID, "name": org.Name, "slug": org.Slug},
	})
}

// DELETE /api/org/{id}/invitations/{invId}: an invitation nobody used yet
// stops working (it is deleted). A used one stays as the record of who
// joined: 409.
func (s *Server) handleRevokeOrgInvitation(w http.ResponseWriter, r *http.Request) {
	orgID, invID := r.PathValue("id"), r.PathValue("invId")
	list, err := s.db.ListOrgInvitations(orgID)
	if err != nil {
		log.Printf("[org] ListOrgInvitations error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}
	var inv *db.OrgInvitation
	for _, candidate := range list {
		if candidate.ID == invID {
			inv = candidate
			break
		}
	}
	if inv == nil {
		invitationNotFound(w)
		return
	}
	if inv.UsedAt != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "invitation already used"})
		return
	}
	if err := s.db.DeleteOrgInvitation(inv.ID); err != nil {
		log.Printf("[org] DeleteOrgInvitation error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke invitation"})
		return
	}
	if s.auditLog != nil {
		s.auditLog.Log("org_invitation_revoked", s.remoteIP(r), getUsernameFromCtx(r), map[string]string{
			"org_id":        orgID,
			"invitation_id": inv.ID,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
