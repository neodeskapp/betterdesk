package authn

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
)

// Single sign-on. The signal server speaks the protocol with the identity
// provider and keeps state, nonce and code verifier; the console only
// passes the browser along (the route calls goapi for that). What is left
// here is the rule for the Redirect URL an administrator may save, the
// local account of a user who signed in that way, and the test for a
// client on the local network.

// The two paths the identity provider may send the browser back to.
const (
	oidcCallbackPath      = "/api/auth/oidc/callback"
	oidcShortCallbackPath = "/api/oidc/callback"
)

// OIDCRedirectURL is a Redirect URL as the settings accept it, or the
// reason it is refused.
type OIDCRedirectURL struct {
	OK bool
	// Reason says why not: "empty", "invalid" (no URL), "scheme" (not http
	// or https) or "path" (not one of the two callback paths).
	Reason string
	// Pathname is the callback path, without a trailing slash.
	Pathname string
	// Origin is scheme, host and port of the URL.
	Origin string
}

// ParseOIDCRedirectURL checks a Redirect URL for the single sign-on
// settings: an absolute http or https URL whose path, trailing slashes
// aside, is /api/auth/oidc/callback or /api/oidc/callback. The host can be
// the console's or the API's.
func ParseOIDCRedirectURL(raw string) OIDCRedirectURL {
	trimmed := jsTrim(toWellFormed(raw))
	if trimmed == "" {
		return OIDCRedirectURL{Reason: "empty"}
	}
	u, ok := parseAbsoluteURL(trimmed)
	if !ok {
		return OIDCRedirectURL{Reason: "invalid"}
	}
	if u.scheme != "http" && u.scheme != "https" {
		return OIDCRedirectURL{Reason: "scheme"}
	}
	pathname := strings.TrimRight(u.path, "/")
	if pathname != oidcCallbackPath && pathname != oidcShortCallbackPath {
		return OIDCRedirectURL{Reason: "path"}
	}
	return OIDCRedirectURL{OK: true, Pathname: pathname, Origin: u.origin()}
}

// IsValidOIDCRedirectURL reports whether raw passes ParseOIDCRedirectURL.
func IsValidOIDCRedirectURL(raw string) bool { return ParseOIDCRedirectURL(raw).OK }

// SuggestOIDCRedirectURL returns the Redirect URL for a console reached
// at panelOrigin ("https://host:5443"): its origin and
// /api/auth/oidc/callback. It is empty when panelOrigin is no http or
// https URL.
func SuggestOIDCRedirectURL(panelOrigin string) string {
	base := strings.TrimRight(jsTrim(toWellFormed(panelOrigin)), "/")
	if base == "" {
		return ""
	}
	u, ok := parseAbsoluteURL(base)
	if !ok || u.scheme != "http" && u.scheme != "https" {
		return ""
	}
	return u.origin() + oidcCallbackPath
}

// OIDCUser returns the local account of a user the signal server signed in
// through single sign-on, with what the session should carry as the role.
// A user without a local row gets one with provider "oidc", the given role
// (else "viewer") and an unusable password. An existing row gets the role
// (else it keeps its own) and provider "oidc". A failure to create or to
// update is logged, and the row is returned as it then is; the account is
// nil when there is still none.
func (s *Service) OIDCUser(ctx context.Context, username, role string) (*Account, error) {
	user, err := getUserByUsername(ctx, s.db, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		hash, err := s.unusablePasswordHash(ctx)
		if err != nil {
			return nil, err
		}
		newRole := role
		if newRole == "" {
			newRole = "viewer"
		}
		if err := createUser(ctx, s.db, username, hash, newRole, providerOIDC); err != nil {
			s.log.Error("[OIDC] Failed to auto-provision local user", username, err.Error())
		}
		return getUserByUsername(ctx, s.db, username)
	}
	change := userSync{authProvider: new(string)}
	*change.authProvider = providerOIDC
	switch {
	case role != "":
		change.role = &role
	case user.Role.Valid:
		change.role = &user.Role.String
	}
	if err := syncUserFromGo(ctx, s.db, user.ID.V, change); err != nil {
		s.log.Warn("[OIDC] Failed to sync local user from Go", username, err.Error())
		return user, nil
	}
	updated, err := getUserByUsername(ctx, s.db, username)
	if err != nil {
		s.log.Warn("[OIDC] Failed to sync local user from Go", username, err.Error())
		return user, nil
	}
	return updated, nil
}

// TouchLastLogin notes a finished sign-in on the account's row. Sign-in
// with a password does it itself when no second factor is due; the routes
// call it after the second factor and after single sign-on.
func (s *Service) TouchLastLogin(ctx context.Context, userID int64) error {
	return touchLastLogin(ctx, s.db, userID)
}

// UserByID returns the account with this id, or nil.
func (s *Service) UserByID(ctx context.Context, userID int64) (*Account, error) {
	return getUserByID(ctx, s.db, userID)
}

// UserByUsername returns the account with exactly this name, or nil.
func (s *Service) UserByUsername(ctx context.Context, username string) (*Account, error) {
	return getUserByUsername(ctx, s.db, username)
}

// IsPrivateClientIP reports whether a client address is on a private
// network: 10/8, 172.16/12, 192.168/16, 127/8, ::1, or an IPv6 address
// that starts with "fc" or "fd". An IPv4 address written as IPv6
// ("::ffff:10.0.0.1") counts as IPv4.
func IsPrivateClientIP(raw string) bool {
	ip := asciiLower(jsTrim(toWellFormed(raw)))
	ip = strings.TrimPrefix(ip, "::ffff:")
	if octets, ok := dottedQuad(ip); ok {
		return octets[0] == 10 ||
			octets[0] == 172 && octets[1] >= 16 && octets[1] <= 31 ||
			octets[0] == 192 && octets[1] == 168 ||
			octets[0] == 127
	}
	if !strings.Contains(ip, ":") {
		return false
	}
	if _, err := netip.ParseAddr(ip); err != nil {
		return false
	}
	return ip == "::1" || strings.HasPrefix(ip, "fc") || strings.HasPrefix(ip, "fd")
}

// dottedQuad reads four decimal numbers of 0 to 255 without leading
// zeros.
func dottedQuad(ip string) (octets [4]int, ok bool) {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return octets, false
	}
	for i, part := range parts {
		if part == "" || len(part) > 3 || len(part) > 1 && part[0] == '0' || strings.Trim(part, "0123456789") != "" {
			return octets, false
		}
		n, _ := strconv.Atoi(part)
		if n > 255 {
			return octets, false
		}
		octets[i] = n
	}
	return octets, true
}
