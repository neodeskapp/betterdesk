package apitokens

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// An account's API tokens on the user's page. Anyone with user.view sees
// them (name, hint, permissions, use; never the token); revoking one takes
// user.security and an account the caller may change (its own, or one
// whose role the caller could give), as the other sign-in security of an
// account does. Only the four admin roles make tokens, so other accounts
// have none unless their role changed after.

// accountOf reads the account of the path: 400 for an id that is no
// positive number, 404 for none. It answers the request itself when not.
func (h *handlers) accountOf(w http.ResponseWriter, r *http.Request) (*access.PolicyUser, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid user ID")
		return nil, false
	}
	user, err := h.d.Access.PolicyUserByID(r.Context(), id)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list the API tokens")
		return nil, false
	}
	if user == nil {
		kit.Fail(w, r, http.StatusNotFound, kit.T(r, "users.not_found", nil))
		return nil, false
	}
	return user, true
}

// accountTokens answers GET /api/users/{id}/api-tokens: the account's
// tokens, newest first, as the caller's own list has them.
func (h *handlers) accountTokens(w http.ResponseWriter, r *http.Request) {
	user, ok := h.accountOf(w, r)
	if !ok {
		return
	}
	tokens, err := h.tokens.List(r.Context(), user.ID)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list the API tokens")
		return
	}
	out := listData{Tokens: make([]tokenJSON, 0, len(tokens))}
	for _, t := range tokens {
		out.Tokens = append(out.Tokens, h.toJSON(t))
	}
	kit.OK(w, r, out)
}

// revokeAccountToken answers DELETE /api/users/{id}/api-tokens/{tokenId}:
// the token of that account ends, with what it made that is still open,
// as Revoke on the account's own list does. A token of another account
// answers 404. The audit entry is about the account.
func (h *handlers) revokeAccountToken(w http.ResponseWriter, r *http.Request) {
	user, ok := h.accountOf(w, r)
	if !ok {
		return
	}
	caller := kit.User(r)
	may, err := h.d.Access.MayChangeAccount(r.Context(), kit.UserID(r), caller.Role, *user)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to revoke the API token")
		return
	}
	if !may {
		kit.Fail(w, r, http.StatusForbidden, "Cannot assign a role higher than your own")
		return
	}
	id := strings.TrimSpace(r.PathValue("tokenId"))
	if parsed, ok := admintoken.Parse(id); ok {
		id = parsed
	}
	id = strings.TrimSuffix(strings.TrimPrefix(id, admintoken.Prefix), "_…")
	tok, err := h.tokens.Get(r.Context(), id)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to revoke the API token")
		return
	}
	if tok == nil || tok.UserID != user.ID {
		kit.Fail(w, r, http.StatusNotFound, "API token not found")
		return
	}
	if !tok.RevokedAt.IsZero() {
		kit.OK(w, r, revoked{tokenJSON: h.toJSON(tok)})
		return
	}
	ending, err := h.tokens.Revoke(r.Context(), tok.ID, caller.Username)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to revoke the API token")
		return
	}
	if ending == nil || ending.Token == nil {
		// Revoked by another request meanwhile
		if tok, err = h.tokens.Get(r.Context(), tok.ID); err != nil || tok == nil {
			kit.OK(w, r, nil)
			return
		}
		kit.OK(w, r, revoked{tokenJSON: h.toJSON(tok)})
		return
	}
	_ = h.d.LogAccountAction(r, user.ID, ending.AuditAction(), ending.AuditText())
	kit.OK(w, r, revoked{tokenJSON: h.toJSON(ending.Token), Ended: ending.Ended})
}
