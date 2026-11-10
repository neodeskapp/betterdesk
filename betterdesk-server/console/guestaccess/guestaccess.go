// Package guestaccess is the check behind guest access links
// (middleware/guestAccess.js of the old console): a link lets someone
// without an account reach a few devices for a while. The API server
// keeps the links; the console reads the link's token from a request,
// asks the API server whether it is good, and keeps it in a cookie for
// the pages and sockets that follow.
//
// The token comes from the query (?guest= or ?t=, an open link) or from
// the cookie bd.guest, betterdesk.guest in HTTPS mode. The cookie holds
// the token as it is, HttpOnly and SameSite=Lax, for as long as the link
// lasts and for a day at most.
//
// Revoke and OnRevoke connect the route that takes a link back with the
// sockets of sessions that run on it: the route calls Revoke, and
// whoever holds such sessions has asked to hear of it with OnRevoke.
package guestaccess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
)

// maxCookieAge is the longest the guest cookie lasts.
const maxCookieAge = 24 * time.Hour

// ErrNotText is the failure of a token that cannot be turned into text:
// a query parameter or JSON cookie that is an object with a member named
// toString. The old console stopped at such a request (BUGS.md MC1); a
// route closes the connection without an answer.
var ErrNotText = errors.New("Cannot convert object to primitive value")

// Options are the settings of a Service.
type Options struct {
	// HTTPS is the console's HTTPS mode (config.httpsEnabled,
	// HTTPS_ENABLED): the cookie is then called betterdesk.guest and is
	// Secure.
	HTTPS bool
	// GoAPI calls the API server, which keeps the links.
	GoAPI *goapi.Client
	// Now is the clock for the cookie's lifetime. Nil means time.Now.
	Now func() time.Time
}

// Service reads and checks guest link tokens. It is safe for concurrent
// use.
type Service struct {
	opts Options

	mu        sync.Mutex
	listeners []func(grantID string) int
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts}
}

// CookieName is the name of the guest cookie (GUEST_COOKIE).
func (s *Service) CookieName() string {
	if s.opts.HTTPS {
		return "betterdesk.guest"
	}
	return "bd.guest"
}

// HashToken is the SHA-256 of a token as hex.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// TokenFromQuery is the token of an open link: ?guest=, else ?t=, without
// the white space around it, "" when the query has neither. The error is
// ErrNotText.
func (s *Service) TokenFromQuery(r *http.Request) (string, error) {
	value := goapipath.Query(r, "guest")
	if !value.Truthy() {
		value = goapipath.Query(r, "t")
	}
	if !value.Truthy() {
		return "", nil
	}
	text, err := value.Text()
	if err != nil {
		return "", ErrNotText
	}
	return strings.TrimFunc(text, isJSSpace), nil
}

// TokenFromCookie is the token the guest cookie holds, "" without one.
// The error is ErrNotText.
func (s *Service) TokenFromCookie(r *http.Request) (string, error) {
	raw, found := cookie.Get(r, s.CookieName())
	if !found {
		return "", nil
	}
	var value any = raw
	// A value written as j:<JSON> is the JSON value when that is truthy
	if strings.HasPrefix(raw, "j:") {
		if parsed, err := goapi.Parse([]byte(raw[2:])); err == nil && goapi.Truthy(parsed) {
			value = parsed
		}
	}
	if !goapi.Truthy(value) {
		return "", nil
	}
	text, ok := JSText(value)
	if !ok {
		return "", ErrNotText
	}
	return strings.TrimFunc(text, isJSSpace), nil
}

// Token is the request's guest token: the one of the query, else the one
// of the cookie (getGuestToken). The error is ErrNotText.
func (s *Service) Token(r *http.Request) (string, error) {
	token, err := s.TokenFromQuery(r)
	if err != nil || token != "" {
		return token, err
	}
	return s.TokenFromCookie(r)
}

// SetCookie stores the token in the guest cookie. expiresAt is the end of
// the link as the API server gave it ("expires_at": a time as text, or
// milliseconds since 1970 as a number); the cookie lasts until then, a day
// at most, and a day when expiresAt is no time or lies in the past.
func (s *Service) SetCookie(w http.ResponseWriter, token string, expiresAt any) {
	now := s.opts.Now()
	maxAge := maxCookieAge
	if goapi.Truthy(expiresAt) {
		if end, ok := jsDate(expiresAt); ok {
			// Whole milliseconds, as the difference of two JavaScript dates
			left := time.Duration(end.UnixMilli()-now.UnixMilli()) * time.Millisecond
			if left > 0 && left < maxAge {
				maxAge = left
			}
		}
	}
	cookie.Set(w, s.CookieName(), token, cookie.Attributes{
		MaxAge:   maxAge,
		Path:     "/",
		Expires:  now.Add(maxAge),
		HTTPOnly: true,
		Secure:   s.opts.HTTPS,
		SameSite: "lax",
	})
}

// ClearCookie removes the guest cookie.
func (s *Service) ClearCookie(w http.ResponseWriter) {
	cookie.Clear(w, s.CookieName(), cookie.Attributes{Path: "/", HTTPOnly: true, Secure: s.opts.HTTPS, SameSite: "lax"})
}

// Grant is a link the API server found good for a request.
type Grant struct {
	// Token is the token the request brought.
	Token string
	// Data is the API server's answer: {valid, id, peer_ids, view_only,
	// expires_at, ...}.
	Data any
}

// ViewOnly reports a link that only allows watching.
func (g *Grant) ViewOnly() bool {
	return g != nil && goapi.Truthy(goapi.Dig(g.Data, "view_only"))
}

// AttachGrant asks the API server whether the request's token is a good
// link, for the device when peerID is not empty, and returns the link
// (attachGuestGrant). It returns nil without a token, for a link the API
// server does not find good and when the API server cannot be asked; the
// caller decides what that means. The error is ErrNotText.
func (s *Service) AttachGrant(ctx context.Context, r *http.Request, peerID string) (*Grant, error) {
	token, err := s.Token(r)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, nil
	}
	opts := []goapi.CallOption{goapi.Query("token", token)}
	if peerID != "" {
		opts = append(opts, goapi.Query("peer_id", peerID))
	}
	resp, err := s.opts.GoAPI.Get(ctx, "/guest/access-links/validate", opts...)
	if err != nil {
		return nil, nil
	}
	data := resp.Data()
	if !goapi.Truthy(goapi.Dig(data, "valid")) {
		return nil, nil
	}
	return &Grant{Token: token, Data: data}, nil
}

// ErrBadGrant is the failure of a link whose list of devices is neither a
// list nor a text. The API server sends a list; the old console stopped
// at anything else, and a route closes the connection without an answer.
var ErrBadGrant = errors.New("ids.includes is not a function")

// PeerAllowed reports whether a link reaches the device: its "peer_ids",
// else its "allowed_peer_ids", hold peerID (peerAllowedByGrant). grant is
// the API server's answer (Grant.Data). The error is ErrBadGrant.
func PeerAllowed(grant any, peerID string) (bool, error) {
	if !goapi.Truthy(grant) || peerID == "" {
		return false, nil
	}
	ids := goapi.Dig(grant, "peer_ids")
	if !goapi.Truthy(ids) {
		ids = goapi.Dig(grant, "allowed_peer_ids")
	}
	if !goapi.Truthy(ids) {
		return false, nil
	}
	switch list := ids.(type) {
	case []any:
		for _, id := range list {
			if text, isText := id.(string); isText && text == peerID {
				return true, nil
			}
		}
		return false, nil
	case string:
		return strings.Contains(list, peerID), nil
	}
	return false, ErrBadGrant
}

// OnRevoke adds a function that is called when a link is taken back, with
// the link's id. It returns how many sessions it ended.
func (s *Service) OnRevoke(fn func(grantID string) int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listeners = append(s.listeners, fn)
}

// Revoke tells everyone who asked with OnRevoke that the link is gone
// (invalidateGuestGrant) and returns how many sessions that ended.
func (s *Service) Revoke(grantID string) int {
	s.mu.Lock()
	listeners := append([]func(string) int(nil), s.listeners...)
	s.mu.Unlock()
	ended := 0
	for _, fn := range listeners {
		ended += fn(grantID)
	}
	return ended
}

// JSText is String(value) for a parsed JSON value (goapi.Parse): a text
// as it is, a number as JavaScript writes it, the elements of a list
// joined by commas with nothing for null, "[object Object]" for an
// object. ok is false where JavaScript throws: an object with a member
// named toString.
func JSText(value any) (text string, ok bool) {
	switch t := value.(type) {
	case nil:
		return "null", true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case float64:
		return goapipath.NumberText(t), true
	case string:
		return t, true
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item == nil {
				continue
			}
			part, ok := JSText(item)
			if !ok {
				return "", false
			}
			parts[i] = part
		}
		return strings.Join(parts, ","), true
	case *goapi.Object:
		if t.Has("toString") {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

// jsDate is new Date(value) for what an "expires_at" holds: milliseconds
// since 1970 as a number, or a time as text in the forms the API server
// writes (RFC 3339, with or without a fraction) and a plain date. ok is
// false for anything else, where the old console's date was not a time
// either or a form the API server never sends.
func jsDate(value any) (time.Time, bool) {
	switch t := value.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) || math.Abs(t) > 8.64e15 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(t)), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// isJSSpace reports the white space JavaScript's trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}
