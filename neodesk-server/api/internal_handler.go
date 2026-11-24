package api

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// Identity is who an in-process request acts as. It stands in for the
// credential a request on the listener carries.
type Identity struct {
	Username string
	Role     string
	// OrgID scopes the caller to one organization, like the org_id claim of
	// a token. Empty for a caller without an organization scope.
	OrgID string
	// Actor is the person the caller acts for: the console's signed-in
	// account. The API server records it where it records who created or
	// handled something (pairing codes, device tokens, help requests,
	// billing work reports, CDAP commands and sessions). It decides
	// nothing: role, permissions and delegations go by Username and Role.
	Actor string
}

type identityCtxKey struct{}

type internalCtxKey struct{}

// WithIdentity returns a context that carries id for InternalHandler.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// IdentityFromContext returns the identity WithIdentity put into ctx.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(Identity)
	return id, ok
}

// arrivedInternally reports whether r came through InternalHandler. A
// request from the listener never does: only InternalHandler sets the mark.
func arrivedInternally(r *http.Request) bool {
	return r.Context().Value(internalCtxKey{}) != nil
}

// actorOf is the Actor of an in-process caller that names one, "" for
// any other request.
func actorOf(r *http.Request) string {
	if !arrivedInternally(r) {
		return ""
	}
	id, _ := IdentityFromContext(r.Context())
	return id.Actor
}

// actorFromCtx is who the request acts for, for what the API server
// records as the creator, handler or operator of something: the Actor of
// an in-process caller that names one, else the caller's username.
func actorFromCtx(r *http.Request) string {
	if actor := actorOf(r); actor != "" {
		return actor
	}
	return getUsernameFromCtx(r)
}

// sharedMux builds the route table once, for whichever of Start and
// InternalHandler asks first.
type sharedMux struct {
	once sync.Once
	mux  *http.ServeMux
}

func (m *sharedMux) get(s *Server) *http.ServeMux {
	m.once.Do(func() { m.mux = s.routes() })
	return m.mux
}

// InternalHandler serves the routes of the listener to callers inside the
// process, without a socket. The caller is the Identity in the request
// context (WithIdentity); a request without one gets the 401 of a request
// without credentials.
//
// The handlers see what they see behind authMiddleware for that caller:
// role, username, organization and the user row in the context on every
// path that needs a credential, nothing on the paths that need none, and a
// body capped at 1 MB. Left out is what belongs to the listener: the
// request log, the HTTPS requirement, the credential lookup and the secure
// envelope. Limits that count per address still count, by the RemoteAddr
// the caller sets on the request.
//
// The route table is built on the first call to this method or to Start,
// so call it after the Set and Init methods.
func (s *Server) InternalHandler() http.Handler {
	mux := s.mux.get(s)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFromContext(r.Context())
		if !ok || id.Username == "" || id.Role == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid or missing credentials"})
			return
		}

		ctx := context.WithValue(r.Context(), internalCtxKey{}, true)
		body := r.Body
		if body == nil {
			body = http.NoBody
		}
		if needsNoCredential(r.Method, r.URL.Path) {
			r = r.WithContext(ctx)
			r.Body = http.MaxBytesReader(w, body, 1<<20)
			mux.ServeHTTP(w, r)
			return
		}

		if auth.IsDeviceRole(id.Role) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Device credentials cannot access this endpoint"})
			return
		}
		ctx = context.WithValue(ctx, ctxKeyRole, id.Role)
		ctx = context.WithValue(ctx, ctxKeyUsername, id.Username)
		if id.OrgID != "" {
			ctx = context.WithValue(ctx, ctxKeyOrgID, id.OrgID)
		}
		if user, err := s.db.GetUser(id.Username); err == nil && user != nil {
			ctx = context.WithValue(ctx, ctxKeyUser, user)
		}
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(w, body, 1<<20)
		mux.ServeHTTP(w, r)
	})
}

// noCredentialPaths are the paths authMiddleware lets through without a
// credential, whatever the method.
var noCredentialPaths = map[string]bool{
	"/agent.ashx":                        true,
	"/meshrelay.ashx":                    true,
	"/neocore.js":                        true,
	"/meshcore.js":                       true,
	"/control.ashx":                      true,
	"/api/health":                        true,
	"/api/auth/login":                    true,
	"/api/auth/login/2fa":                true,
	"/api/server/pubkey":                 true,
	"/api/login":                         true,
	"/api/login-options":                 true,
	"/api/logout":                        true,
	"/api/oidc/auth":                     true,
	"/api/oidc/auth-query":               true,
	"/api/oidc/callback":                 true,
	"/api/heartbeat":                     true,
	"/api/sysinfo":                       true,
	"/api/sysinfo_ver":                   true,
	"/api/telemetry/key":                 true,
	"/api/server-key":                    true,
	"/api/server-key/fingerprint":        true,
	"/api/software":                      true,
	"/api/software/client-download-link": true,
	"/api/org/login":                     true,
	"/api/auth/oidc/status":              true,
	"/api/auth/oidc/authorize":           true,
	"/api/auth/oidc/callback":            true,
	"/api/auth/oidc/session":             true,
	"/api/auth/oidc/exchange":            true,
	"/api/auth/sso/status":               true,
	"/api/devices/register":              true,
	"/api/devices/register/status":       true,
	"/api/devices/self/access-policy":    true,
	"/api/devices/self/help-request":     true,
	"/api/devices/self/totp":             true,
	"/api/guest/access-links/validate":   true,
	"/api/guest/access-links/peers":      true,
}

// needsNoCredential reports whether authMiddleware serves the request
// without a credential, and so without putting a caller into the context.
// TestNoCredentialPathsMatchAuthMiddleware holds the two together.
func needsNoCredential(method, path string) bool {
	if noCredentialPaths[path] || strings.HasPrefix(path, "/ws/nd-mgmt/") {
		return true
	}
	switch path {
	case "/api/branding":
		return method == http.MethodGet
	case "/api/audit/conn", "/api/audit/file", "/api/audit/alarm", "/api/pairing/redeem", "/api/pairing/config":
		return method == http.MethodPost
	}
	return false
}
