package meshcentral

import (
	"log"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/auth"
)

// pendingSecondFactorRole is the role of the partial token a sign-in gets
// while it still waits for its second factor.
const pendingSecondFactorRole = "__2fa_pending__"

// PermissionChecker answers whether a role holds a permission, with the role
// overrides stored in the database, the way the API's requirePermission
// answers it for a REST route.
type PermissionChecker interface {
	RoleHasPermission(role, permission string) (bool, error)
}

// SetPermissionChecker attaches the permission check of /control.ashx.
// Without one every action that needs a permission is refused.
func (g *Gateway) SetPermissionChecker(pc PermissionChecker) { g.perms = pc }

// controlPrincipal is the account a /control.ashx socket acts for.
type controlPrincipal struct {
	user string
	role string
}

// controlPrincipalOf resolves the token of a /control.ashx request the way
// the API's middleware resolves a server sign-in token: a valid signature,
// not the partial token of a pending second factor, not a device token,
// not an organization token (its subject is no server account), an account
// that exists with a known role, and a token issued no earlier than the
// account's last password or second factor change. The socket acts with
// the stored role, not the one inside the token. The token comes from
// x-meshauth (its first part) or else from Authorization: Bearer.
func (g *Gateway) controlPrincipalOf(r *http.Request) (controlPrincipal, bool) {
	if g.jwt == nil || g.db == nil {
		return controlPrincipal{}, false
	}
	var tokens []string
	if tok := r.Header.Get("x-meshauth"); tok != "" {
		tokens = append(tokens, strings.Split(tok, ",")[0])
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tokens = append(tokens, strings.TrimPrefix(h, "Bearer "))
	}
	for _, tok := range tokens {
		claims, err := g.jwt.Validate(tok)
		if err != nil || claims.Sub == "" {
			continue
		}
		if claims.Role == pendingSecondFactorRole || auth.IsDeviceRole(claims.Role) || claims.OrgID != "" {
			continue
		}
		user, err := g.db.GetUser(claims.Sub)
		if err != nil || user == nil || user.Disabled || !auth.ValidRole(user.Role) {
			continue
		}
		if user.CredentialsChangedAt > 0 && claims.Iat < user.CredentialsChangedAt {
			continue
		}
		return controlPrincipal{user: user.Username, role: user.Role}, true
	}
	return controlPrincipal{}, false
}

// may reports whether p holds permission, asked through the permission
// checker; no checker or a failed lookup refuses.
func (g *Gateway) may(p controlPrincipal, permission string) bool {
	if g.perms == nil {
		return false
	}
	ok, err := g.perms.RoleHasPermission(p.role, permission)
	if err != nil {
		log.Printf("[mesh] control: permission lookup for role %q failed: %v", p.role, err)
		return false
	}
	return ok
}
