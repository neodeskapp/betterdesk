package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// Impersonation ("Sign in as"): an admin views the console as another
// account sees it, read-only.
//
// It is a session of its own: a new id and row whose account is the
// person viewed, with an "impersonation" field that names the admin and
// the admin's own session, and impersonated_by set in the row. The browser
// keeps the admin's session cookie as it is and gets a second cookie,
// nd.imp (neodesk.imp in an HTTPS setup), for the impersonation session.
// A request counts as the impersonation only when both cookies come along
// and the admin's session is the one the impersonation names: a copy of
// the second cookie alone is worth nothing, and stopping only ends the
// second session and forgets its cookie, so the browser is back on the
// admin's untouched session.
//
// What may be done during an impersonation is decided by the console's
// request pipeline (package impersonation); this package keeps the
// session.

// The second cookie's names, for an HTTPS and a plain HTTP setup.
const (
	impCookieHTTPS = "neodesk.imp"
	impCookieHTTP  = "nd.imp"
)

// impersonationKey is the session field of an impersonation session.
const impersonationKey = "impersonation"

// The reasons an impersonation session ended.
const (
	// ReasonImpersonationStopped: the admin stopped it.
	ReasonImpersonationStopped = "impersonation_stopped"
	// ReasonImpersonationEnded: it ran out, or the admin or the person
	// viewed may no longer have it (package impersonation's checks).
	ReasonImpersonationEnded = "impersonation_ended"
)

// ErrImpersonation is what an impersonation session answers to sign-in
// and regeneration: it can carry no other account than the one it views.
var ErrImpersonation = errors.New("session: an impersonation session cannot sign in or be regenerated")

// Impersonation is what an impersonation session records. It stays on the
// server: no answer sends the admin's session id.
type Impersonation struct {
	// AdminID and AdminUsername are the admin who views.
	AdminID       int64  `json:"adminId"`
	AdminUsername string `json:"adminUsername"`
	// AdminSID is the id of the admin's own session.
	AdminSID string `json:"adminSid"`
	// Reason is what the admin gave as the reason.
	Reason string `json:"reason"`
	// StartedAt and ExpiresAt are in milliseconds.
	StartedAt int64 `json:"startedAt"`
	ExpiresAt int64 `json:"expiresAt"`
	// Extended says the impersonation was made longer once.
	Extended bool `json:"extended"`
}

// Expired reports whether the impersonation is over at now.
func (i Impersonation) Expired(now time.Time) bool {
	return now.UnixMilli() >= i.ExpiresAt
}

// Impersonation is what the session records about the admin who views
// the console as its account, nil for any other session.
func (s *Session) Impersonation() *Impersonation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.impersonationLocked()
}

func (s *Session) impersonationLocked() *Impersonation {
	if !s.impersonating || s.destroyed {
		return nil
	}
	raw, ok := s.data.get(impersonationKey)
	if !ok {
		return nil
	}
	var imp Impersonation
	if json.Unmarshal(raw, &imp) != nil || imp.AdminID == 0 || imp.AdminSID == "" {
		return nil
	}
	return &imp
}

// SetImpersonation changes what an impersonation session records (its
// expiry, when it is made longer). It does nothing on any other session.
func (s *Session) SetImpersonation(imp Impersonation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.impersonating || s.destroyed {
		return
	}
	s.data.set(impersonationKey, mustJSON(imp))
}

type impersonatorKey struct{}

// Impersonator is the admin's own session behind a request's impersonation
// session, nil when the request is no impersonation.
func Impersonator(r *http.Request) *Session {
	s, _ := r.Context().Value(impersonatorKey{}).(*Session)
	return s
}

// WithSession returns r with s as its session (From), for a step that
// hands a request on as another of the browser's sessions: sign-out during
// an impersonation signs the admin out.
func WithSession(r *http.Request, s *Session) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
	r = r.WithContext(context.WithValue(r.Context(), impersonatorKey{}, (*Session)(nil)))
	s.mu.Lock()
	s.req = r
	s.mu.Unlock()
	return r
}

// impCookieName is the name of the impersonation cookie.
func (m *Manager) impCookieName() string {
	if m.cookieName == cookieNameHTTPS {
		return impCookieHTTPS
	}
	return impCookieHTTP
}

// impCookieID is the session id the request's impersonation cookie carries
// with a good signature, "" without one.
func (m *Manager) impCookieID(r *http.Request) string {
	value, found := cookie.Get(r, m.impCookieName())
	if !found || !strings.HasPrefix(value, cookiePrefix) {
		return ""
	}
	id, ok := unsign(value[len(cookiePrefix):], m.secret)
	if !ok {
		return ""
	}
	return id
}

// ImpersonationCookieID is the id the impersonation cookie of a request
// carries with a good signature, "" when it carries none. It reads no row:
// a socket upgrade asks it to refuse what an impersonation may not open.
func (m *Manager) ImpersonationCookieID(r *http.Request) string {
	return m.impCookieID(r)
}

// clearImpCookie makes the browser forget the impersonation cookie under
// both names it can have.
func clearImpCookie(w http.ResponseWriter) {
	clearCookie(w, impCookieHTTPS)
	clearCookie(w, impCookieHTTP)
}

// clearImpCookieOf is clearImpCookie for a request that came with an
// impersonation cookie, and nothing for any other: the answers of the
// browsers that never impersonate stay as they were.
func clearImpCookieOf(w http.ResponseWriter, r *http.Request) {
	if r == nil {
		return
	}
	_, https := cookie.Get(r, impCookieHTTPS)
	_, plain := cookie.Get(r, impCookieHTTP)
	if https || plain {
		clearImpCookie(w)
	}
}

// ClearImpersonationCookie makes the browser forget its impersonation
// cookie.
func (m *Manager) ClearImpersonationCookie(w http.ResponseWriter) { clearImpCookie(w) }

// Impersonate opens an impersonation session for the request's signed-in
// account (the admin): a new session and row whose account is target, with
// imp recorded (its AdminID, AdminUsername and AdminSID are filled in from
// the admin's session), and the cookie for it on the answer. The admin's
// session stays as it is. Nothing of the target's own sessions changes:
// the new one counts against no limit and is no device of the account, and
// the account's last sign-in is not touched.
func (m *Manager) Impersonate(w http.ResponseWriter, r *http.Request, target User, imp Impersonation) error {
	admin := From(r)
	if admin == nil || admin.Ephemeral() || !admin.signedInLocked() {
		return errors.New("session: impersonation needs a signed-in browser session")
	}
	if admin.Impersonation() != nil {
		return ErrImpersonation
	}
	adminUser := admin.User()
	imp.AdminID, imp.AdminUsername, imp.AdminSID = adminUser.ID, adminUser.Username, admin.ID()
	s, err := m.newSession(r)
	if err != nil {
		return err
	}
	s.impersonating = true
	now := m.now()
	at := now.UnixMilli()
	meta := m.ClientMeta(r, "")
	s.data.set("userId", mustJSON(target.ID))
	s.data.set("user", mustJSON(target))
	s.data.set("createdAt", mustJSON(at))
	s.data.set("lastActivityAt", mustJSON(at))
	s.data.set("loginIp", mustJSON(meta.IP))
	s.data.set("loginUserAgent", mustJSON(meta.UserAgent))
	s.data.set("clientFingerprint", mustJSON(meta.Fingerprint))
	s.data.set(impersonationKey, mustJSON(imp))
	s.cookie.setMaxAge(now, min(m.policy.IdleTimeoutMs, m.policy.MaxAgeMs))
	ctx, cancel := s.ctx()
	defer cancel()
	if err := m.table.set(ctx, s.id, s.record(now)); err != nil {
		return err
	}
	w.Header().Add("Set-Cookie", m.cookieHeader(s))
	return nil
}

// EndImpersonation ends the request's impersonation session with a reason
// (ReasonImpersonationStopped, ReasonImpersonationEnded), its sockets
// included, and makes the browser forget its cookie. The admin's session
// goes on. It reports whether the request was an impersonation.
func (m *Manager) EndImpersonation(w http.ResponseWriter, r *http.Request, reason string) bool {
	s := From(r)
	if s == nil || s.Impersonation() == nil {
		return false
	}
	s.End(reason)
	if err := s.Destroy(); err != nil {
		logging.Logf("session", "WARN: destroying the impersonation session failed: %v", err)
	}
	clearImpCookie(w)
	return true
}

// loadImpersonation is the impersonation session the request's second
// cookie names, for the admin's session admin: nil when there is none, it
// has ended, or it is not one this admin's session opened. A row the
// admin's session does not fit, and one past its limits, is ended.
func (m *Manager) loadImpersonation(r *http.Request, admin *Session, id string) (*Session, error) {
	text, found, ended, _, err := m.table.get(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if !found {
		if ended != "" {
			m.endedIDs([]string{id})
		}
		return nil, nil
	}
	s, err := m.newSession(r)
	if err != nil {
		return nil, err
	}
	s.impersonating = true
	if err := s.restore(text); err != nil {
		return nil, m.endRow(r.Context(), id, ReasonUnreadable)
	}
	s.id, s.originalID, s.cookieID = id, id, id
	imp := s.Impersonation()
	adminUser := admin.User()
	if imp == nil || adminUser == nil || !s.signedInLocked() || imp.AdminSID != admin.ID() || imp.AdminID != adminUser.ID {
		return nil, m.endRow(r.Context(), id, ReasonImpersonationEnded)
	}
	return s, nil
}

// endRow ends a row with a reason and closes its sockets.
func (m *Manager) endRow(ctx context.Context, id, reason string) error {
	if err := m.table.end(ctx, id, reason); err != nil {
		return err
	}
	m.endedIDs([]string{id})
	return nil
}

// impersonationEnded answers a request of a browser whose impersonation
// is over, when the answer is one that tells (an API call): 409 with the
// code impersonation_ended, so the web app loads again as the admin and
// nothing the request asked for runs as the admin. It reports whether it
// answered; a page goes on as the admin.
func impersonationEnded(w http.ResponseWriter, r *http.Request) bool {
	clearImpCookie(w)
	if !strings.HasPrefix(r.URL.EscapedPath(), "/api/") && !wantsJSON(r) {
		return false
	}
	httpx.WriteJSON(w, r, http.StatusConflict, expiredAnswer{Error: ImpersonationEndedText, Code: ImpersonationEndedCode})
	return true
}

// The answer to a request of an impersonation that is over.
const (
	ImpersonationEndedCode = "impersonation_ended"
	ImpersonationEndedText = "You are no longer viewing NeoDesk as another account. The page loads again as you."
)
