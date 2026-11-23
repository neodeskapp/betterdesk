package rustdeskapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// The answers of the token checks.
const (
	msgAuthRequired  = "Authorization required"
	msgInvalidToken  = "Invalid or expired token"
	msgAdminRequired = "Admin privileges required"
	msgServerError   = "Server error"
)

// adminRoles are the roles ADMIN_ROLES of the client API lets write groups
// and strategies, and that it reports as is_admin.
var adminRoles = map[string]bool{"admin": true, "super_admin": true, "server_admin": true, "global_admin": true}

type errorBody struct {
	Error string `json:"error"`
}

// fail answers {"error": message}.
func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, errorBody{Error: message})
}

// serverFailure answers a request whose token check or device lookup
// failed in the database: 500 {"error":"Server error"}.
func (h *handlers) serverFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what, err)
	fail(w, r, http.StatusInternalServerError, msgServerError)
}

// bearerToken is extractBearerToken: the Authorization header as Node read
// it (the first of several, its bytes as Latin-1) must start with "Bearer ";
// the rest, trimmed, is the token. "" when there is none.
func bearerToken(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		return ""
	}
	rest, ok := strings.CutPrefix(httpx.Latin1(values[0]), "Bearer ")
	if !ok {
		return ""
	}
	return jsTrim(rest)
}

// tokenUser is the account behind a token, as the routes read it
// (req.authUser).
type tokenUser struct {
	*authn.TokenUser
}

func (u tokenUser) id() int64 { return u.ID.V }

func (u tokenUser) role() string { return u.Role.String }

// sessionUser is the account in the form the device scope rules take.
func (u tokenUser) sessionUser() *session.User {
	return &session.User{ID: u.ID.V, Username: u.Username, Role: u.Role.String}
}

func (u tokenUser) isPro() bool { return u.Role.Valid && u.Role.String == "pro" }

// validate is authService.validateAccessToken: the account of a token, nil
// for a token that is not valid. When the lookup fails the request is
// answered with 500 and ok is false.
func (h *handlers) validate(w http.ResponseWriter, r *http.Request, token string) (user *tokenUser, ok bool) {
	found, err := h.d.Authn.ValidateAccessToken(r.Context(), token)
	if err != nil {
		h.serverFailure(w, r, "Access token lookup failed:", err)
		return nil, false
	}
	if found == nil {
		return nil, true
	}
	return &tokenUser{found}, true
}

type authUserKey struct{}

// authUser is req.authUser behind requireAuth.
func authUser(r *http.Request) *tokenUser {
	u, _ := r.Context().Value(authUserKey{}).(*tokenUser)
	return u
}

// requireAuth is the route file's requireAuth: a valid token, else 401
// {"error":"Authorization required"}.
func (h *handlers) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		var user *tokenUser
		if token != "" {
			var ok bool
			if user, ok = h.validate(w, r, token); !ok {
				return
			}
		}
		if user == nil {
			fail(w, r, http.StatusUnauthorized, msgAuthRequired)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authUserKey{}, user)))
	})
}

// requireAdmin is the route file's requireAdmin: one of the four admin
// roles, else 403 {"error":"Admin privileges required"}.
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := authUser(r); u == nil || !adminRoles[u.role()] {
			fail(w, r, http.StatusForbidden, msgAdminRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unlessBearer is fallthroughUnlessBearer: without a bearer token the
// route steps aside for the console route of the same address.
func unlessBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if bearerToken(r) == "" {
			kit.PassOn(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenOrFail checks the token the way most handlers did themselves: no
// token is 401 "Authorization required", one that is not valid 401
// "Invalid or expired token". It is nil when it answered the request.
func (h *handlers) tokenOrFail(w http.ResponseWriter, r *http.Request) *tokenUser {
	token := bearerToken(r)
	if token == "" {
		fail(w, r, http.StatusUnauthorized, msgAuthRequired)
		return nil
	}
	user, ok := h.validate(w, r, token)
	if ok && user == nil {
		fail(w, r, http.StatusUnauthorized, msgInvalidToken)
	}
	return user
}

// The three checks of the route file that ask the built-in role map and
// not the role's overrides (canSyncDeviceInventory, canBrowseDeviceInventory,
// canSyncDeviceTags; spec/data.md). A role override that takes device.view
// from a role still lets its tokens browse the inventory, and one that
// gives device.edit does not let them change tags.

// canBrowseDeviceInventory: not pro, and device.view by the built-in map.
func canBrowseDeviceInventory(u *tokenUser) bool {
	return u != nil && !u.isPro() && access.RoleHasPermission(u.role(), "device.view")
}

// canSyncDeviceTags: not pro, and device.edit by the built-in map.
func canSyncDeviceTags(u *tokenUser) bool {
	return u != nil && !u.isPro() && access.RoleHasPermission(u.role(), "device.edit")
}

// userPayload is buildUserPayload, the user of a sign-in answer.
type userPayload struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Note    string `json:"note"`
	Status  int    `json:"status"`
	Grp     string `json:"grp"`
	IsAdmin bool   `json:"is_admin"`
}

func buildUserPayload(username, role string) userPayload {
	return userPayload{Name: username, Status: 1, IsAdmin: adminRoles[role]}
}
