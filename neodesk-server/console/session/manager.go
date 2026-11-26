package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session/cookie"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The session cookie's name: one for a console set up for HTTPS, one for
// plain HTTP, so a Secure cookie left from an HTTPS setup never shadows
// the HTTP one.
const (
	cookieNameHTTPS = "neodesk.sid"
	cookieNameHTTP  = "nd.sid"
)

// Options configure a Manager.
type Options struct {
	// DB is the console's database. The sessions live in its
	// panel_sessions table.
	DB *store.DB
	// Secret signs the cookie (see LoadSecret).
	Secret string
	// HTTPS says the console is set up for HTTPS (HTTPS_ENABLED=true). It
	// picks the cookie's name.
	HTTPS bool
	// Secure marks every cookie Secure: set it when the console serves
	// HTTPS. Without it a cookie is Secure only for a request that came
	// over HTTPS (through a proxy TrustProxy trusts), in HTTPS mode without
	// a usable certificate and in HTTP mode behind a TLS reverse proxy.
	Secure bool
	// Policy holds the lifetimes and the per-account limit (see
	// PolicyFromEnv). The zero value is the default policy.
	Policy Policy
	// TrustProxy tells the client's address and whether the request came
	// over HTTPS.
	TrustProxy httpx.TrustProxy
	// LoginRedirect is where a page request goes when its session has
	// ended. nil means httpx.LoginRedirectURL.
	LoginRedirect func(r *http.Request) string
	// Audit writes an entry to the audit log. nil writes none.
	Audit func(ctx context.Context, userID int64, action, details, ip string)
	// OnError answers a request whose session could not be loaded: the
	// database failed, or the stored session is broken. nil answers 500
	// with a plain text.
	OnError func(w http.ResponseWriter, r *http.Request, err error)
	// Now is the clock. nil means time.Now.
	Now func() time.Time
}

// Manager loads and stores sessions.
type Manager struct {
	table         *table
	secret        string
	secure        bool
	cookieName    string
	policy        Policy
	trust         httpx.TrustProxy
	loginRedirect func(r *http.Request) string
	audit         func(ctx context.Context, userID int64, action, details, ip string)
	onError       func(w http.ResponseWriter, r *http.Request, err error)
	now           func() time.Time
	endHooks
}

// New returns a Manager and makes sure the panel_sessions table has every
// column and index, on an empty database and on one an older console
// created.
func New(ctx context.Context, opts Options) (*Manager, error) {
	if opts.DB == nil {
		return nil, errors.New("session: no database")
	}
	if opts.Secret == "" {
		return nil, errors.New("session: the secret is empty")
	}
	m := &Manager{
		secret:        opts.Secret,
		secure:        opts.Secure,
		cookieName:    cookieNameHTTP,
		policy:        opts.Policy.normalized(),
		trust:         opts.TrustProxy,
		loginRedirect: opts.LoginRedirect,
		audit:         opts.Audit,
		onError:       opts.OnError,
		now:           opts.Now,
	}
	if opts.HTTPS {
		m.cookieName = cookieNameHTTPS
	}
	if m.loginRedirect == nil {
		m.loginRedirect = func(r *http.Request) string { return httpx.LoginRedirectURL(r, nil) }
	}
	if m.onError == nil {
		m.onError = func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}
	if m.now == nil {
		m.now = time.Now
	}
	m.table = &table{db: opts.DB, policy: m.policy, now: m.now}
	if err := m.table.ensureSchema(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// Policy is the policy in force.
func (m *Manager) Policy() Policy { return m.policy }

// ClientMeta collects what sign-in records about the client: its address,
// its user agent and its fingerprint. fingerprint is the one from the
// request body; when it is empty the X-Client-Fingerprint header is used.
func (m *Manager) ClientMeta(r *http.Request, fingerprint string) Meta {
	if fingerprint == "" {
		fingerprint = strings.Join(r.Header.Values("X-Client-Fingerprint"), ", ")
	}
	return Meta{
		IP:          cleanIP(m.trust.ClientIP(r)),
		UserAgent:   cleanUserAgent(httpx.Latin1(r.UserAgent())),
		Fingerprint: cleanFingerprint(fingerprint),
	}
}

type contextKey struct{}

// From returns the request's session. Behind Middleware it is never nil; a
// visitor without a session has an empty one.
func From(r *http.Request) *Session {
	return FromContext(r.Context())
}

// FromContext is From for the context of a request behind Middleware, or
// one made from it; nil for any other context.
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(contextKey{}).(*Session)
	return s
}

// Load returns the session a request's cookie names, without renewing it:
// the row is not touched and no cookie is sent. Websocket upgrades use it.
// A request without a valid, live session gets an empty one. A browser
// that views the console as another account (Impersonate) gets the
// impersonation session, as Middleware gives its requests.
func (m *Manager) Load(r *http.Request) (*Session, error) {
	s, expired, err := m.load(r)
	if err != nil {
		return s, err
	}
	if expired != "" {
		return m.newSession(r)
	}
	if id := m.impCookieID(r); id != "" && s.signedInLocked() {
		imp, err := m.loadImpersonation(r, s, id)
		if err != nil {
			return nil, err
		}
		if imp != nil {
			if i := imp.Impersonation(); i != nil && !i.Expired(m.now()) {
				return imp, nil
			}
		}
	}
	return s, nil
}

// load is Load that also reports a signed-in session the request's cookie
// names whose row it found past a limit and ended: expired is the reason,
// and the session holds what the row held, so the answer knows the
// account. A session without an account that ran out is no session.
func (m *Manager) load(r *http.Request) (s *Session, expired string, err error) {
	s, err = m.newSession(r)
	if err != nil {
		return nil, "", err
	}
	id, ok := m.cookieID(r)
	if !ok {
		return s, "", nil
	}
	s.cookieID = id
	text, found, ended, gone, err := m.table.get(r.Context(), id)
	if err != nil {
		return nil, "", err
	}
	if !found && ended == "" {
		// A session another browser or an admin ended: the browser is told
		// why at its next request (Middleware)
		if _, _, ok := Notice(gone); ok {
			s.notice = gone
		}
		return s, "", nil
	}
	if err := s.restore(text); err != nil {
		// A stored session that cannot be read is no session: its row
		// ends, and the request goes on with a new one
		if err := m.table.end(r.Context(), id, ReasonUnreadable); err != nil {
			return nil, "", err
		}
		empty, err := m.newSession(r)
		return empty, "", err
	}
	s.id, s.originalID = id, id
	if ended == "" {
		return s, "", nil
	}
	if s.signedInLocked() {
		return s, ended, nil
	}
	empty, err := m.newSession(r)
	if err != nil {
		return nil, "", err
	}
	empty.cookieID = id
	return empty, "", nil
}

// cookieID reads the session id from the request's cookie. ok is false
// when there is no cookie, the value is not a signed one, or the signature
// is not ours.
func (m *Manager) cookieID(r *http.Request) (id string, ok bool) {
	value, found := cookie.Get(r, m.cookieName)
	if !found || !strings.HasPrefix(value, cookiePrefix) {
		return "", false
	}
	id, ok = unsign(value[len(cookiePrefix):], m.secret)
	return id, ok && id != ""
}

// CookieSessionID is the session id the request's cookie carries with a
// good signature, "" without one. It reads no row: an upgrade notes it,
// so its socket closes when that session ends (OnEnd).
func (m *Manager) CookieSessionID(r *http.Request) string {
	id, ok := m.cookieID(r)
	if !ok {
		return ""
	}
	return id
}

// cookieHeader is the Set-Cookie text for a session.
func (m *Manager) cookieHeader(s *Session) string {
	name := m.cookieName
	if s.impersonating {
		name = m.impCookieName()
	}
	return cookie.Format(name, cookiePrefix+sign(s.id, m.secret), cookie.Attributes{
		Path:     s.cookie.path(),
		Expires:  s.cookie.expires,
		HTTPOnly: s.cookie.httpOnly(),
		Secure:   s.cookie.secure(),
		SameSite: s.cookie.sameSite(),
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	cookie.Clear(w, name, cookie.Attributes{})
}

// SecureCookie reports whether a cookie the console sets in answer to r
// is Secure: when the console serves HTTPS, and otherwise when the request
// came over HTTPS, also in HTTP mode behind a TLS reverse proxy the
// console trusts. A new session's cookie follows it, and so do the
// console's other cookies.
func (m *Manager) SecureCookie(r *http.Request) bool {
	return m.secure || r != nil && m.secureRequest(r)
}

// secureRequest reports whether the request came over HTTPS: on a TLS
// connection, or through a trusted proxy that says so.
func (m *Manager) secureRequest(r *http.Request) bool {
	return m.trust.Scheme(r) == "https"
}

// Middleware gives every request its session and checks the lifetimes of
// a signed-in one.
//
// It reads the cookie, checks the signature and loads the row. A request
// without a valid, live session goes on with a new, empty one. For a
// signed-in session it then applies the absolute and the idle limit: a
// session past one is ended and the request is answered here, with 401 and
// a code for the API and a redirect to the sign-in page otherwise. It also
// reports a request from a new address to the audit log.
//
// When the answer starts, the session cookie is added and the session is
// written to the store, before the first byte goes out.
//
// A browser that views the console as another account sends a second
// cookie (Impersonate). When the impersonation session it names is live
// and belongs to the admin's session the first cookie names, the request
// goes on with the impersonation session (From) and the admin's behind it
// (Impersonator); both count the request as activity. When it is over, the
// browser forgets the second cookie, and an API call is answered with 409
// and the code impersonation_ended rather than run as the admin.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, expired, err := m.load(r)
		if err != nil {
			m.onError(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
		s.req = r
		impID := m.impCookieID(r)
		if expired != "" {
			m.endImpersonationRow(r, impID)
			m.answerExpired(w, r, s, expired)
			return
		}
		if s.notice != "" && tellsEnd(r) {
			m.endImpersonationRow(r, impID)
			m.answerEnded(w, r, s.notice)
			return
		}
		if s.signedInLocked() {
			if ok, reason := s.touchActivity(r); !ok {
				m.endImpersonationRow(r, impID)
				m.answerExpired(w, r, s, reason)
				return
			}
			s.noteIPChange(r)
		}
		admin := (*Session)(nil)
		if impID != "" {
			imp, err := m.impersonationFor(r, s, impID)
			if err != nil {
				m.onError(w, r, err)
				return
			}
			if imp == nil {
				if impersonationEnded(w, r) {
					return
				}
			} else {
				admin, s = s, imp
				r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
				r = r.WithContext(context.WithValue(r.Context(), impersonatorKey{}, admin))
				s.req, admin.req = r, r
			}
		}
		rw := &responseWriter{ResponseWriter: w, session: s, also: admin}
		if admin != nil {
			defer admin.finish(w)
		}
		defer s.finish(w)
		next.ServeHTTP(rw, r)
	})
}

// impersonationFor is the live impersonation session the request's second
// cookie names for the signed-in session admin, its activity counted; nil
// when there is none. A session that is past its limits is ended.
func (m *Manager) impersonationFor(r *http.Request, admin *Session, id string) (*Session, error) {
	if !admin.signedInLocked() {
		m.endImpersonationRow(r, id)
		return nil, nil
	}
	imp, err := m.loadImpersonation(r, admin, id)
	if err != nil || imp == nil {
		return nil, err
	}
	if ok, reason := imp.touchActivity(r); !ok {
		return nil, m.endRow(r.Context(), id, reason)
	}
	return imp, nil
}

// endImpersonationRow ends the impersonation session a request's second
// cookie names, when the admin's session it came with is gone.
func (m *Manager) endImpersonationRow(r *http.Request, id string) {
	if id == "" {
		return
	}
	if err := m.endRow(context.WithoutCancel(r.Context()), id, ReasonImpersonationEnded); err != nil {
		logging.Logf("session", "WARN: ending the impersonation session failed: %v", err)
	}
}

func (s *Session) signedInLocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signedIn()
}

// answerExpired ends a session that is past a limit and answers the
// request: the row ends with the reason, the cookie is cleared under every
// name it can have, and an audit entry is written.
func (m *Manager) answerExpired(w http.ResponseWriter, r *http.Request, s *Session, reason string) {
	userID := s.UserID()
	s.End(reason)
	if err := s.Destroy(); err != nil {
		logging.Logf("session", "WARN: destroying the expired session failed: %v", err)
	}
	clearCookie(w, m.cookieName)
	s.ClearCookies(w)
	if strings.HasPrefix(r.URL.EscapedPath(), "/api/") || strings.Contains(strings.Join(r.Header.Values("Accept"), ", "), "application/json") {
		code := "session_expired"
		if reason == ReasonIdle {
			code = "session_idle"
		}
		httpx.WriteJSON(w, r, http.StatusUnauthorized, expiredAnswer{Error: "Session expired. Please log in again.", Code: code})
	} else {
		httpx.Redirect(w, r, http.StatusFound, m.loginRedirect(r))
	}
	if m.audit != nil {
		m.audit(r.Context(), userID, "session_expired", "reason="+reason, m.trust.ClientIP(r))
	}
}

// tellsEnd reports whether the answer to r tells a browser that its
// session was ended elsewhere: an API call or a page, but not a file the
// page loads, and not the sign-in itself, which then goes on signed out.
func tellsEnd(r *http.Request) bool {
	path := r.URL.EscapedPath()
	for _, quiet := range []string{"/login", "/logout", "/api/auth/login", "/api/auth/login-page", "/api/auth/logout",
		"/api/auth/totp/verify", "/api/oidc/callback"} {
		if path == quiet {
			return false
		}
	}
	for _, quiet := range []string{"/api/auth/oidc/", "/api/auth/totp/required/"} {
		if strings.HasPrefix(path, quiet) {
			return false
		}
	}
	if strings.HasPrefix(path, "/api/") || wantsJSON(r) {
		return true
	}
	last := path[strings.LastIndex(path, "/")+1:]
	return !strings.Contains(last, ".")
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(strings.Join(r.Header.Values("Accept"), ", "), "application/json")
}

// answerEnded answers a request whose cookie names a session that was
// ended from another browser, by an admin, by the limit or by a change to
// the account: 401 with the reason's code for the API, the sign-in page
// with the reason otherwise. The cookie is cleared, so the browser is told
// once.
func (m *Manager) answerEnded(w http.ResponseWriter, r *http.Request, reason string) {
	code, text, _ := Notice(reason)
	clearCookie(w, cookieNameHTTPS)
	clearCookie(w, cookieNameHTTP)
	clearImpCookieOf(w, r)
	// The reason stays a few minutes, so the answers after this one say it
	// too, whichever request of the browser was first (EndedReason)
	w.Header().Add("Set-Cookie", cookie.Format(httpx.EndedCookie, code, cookie.Attributes{
		Path: "/", MaxAge: endedNoticeFor, HTTPOnly: true, Secure: m.SecureCookie(r), SameSite: "lax",
	}))
	if strings.HasPrefix(r.URL.EscapedPath(), "/api/") || wantsJSON(r) {
		httpx.WriteJSON(w, r, http.StatusUnauthorized, expiredAnswer{Error: text, Code: code})
		return
	}
	target := m.loginRedirect(r)
	if rest, ok := strings.CutPrefix(target, "/login?expired=1"); ok {
		target = "/login?expired=1&reason=" + code + rest
	} else {
		target = "/login?expired=1&reason=" + code
	}
	httpx.Redirect(w, r, http.StatusFound, target)
}

// endedNoticeFor is how long a browser keeps the reason its session was
// ended elsewhere after it was first told.
const endedNoticeFor = 10 * time.Minute

// NoticeText is the English text of a notice code (Notice), ok false for
// a code that is none.
func NoticeText(code string) (text string, ok bool) {
	for _, reason := range []string{ReasonSignedOut, ReasonEndedByAdmin, ReasonReplaced, ReasonLimit, ReasonRevoked} {
		if c, t, _ := Notice(reason); c == code {
			return t, true
		}
	}
	return "", false
}

// ClearEndedNotice makes the browser forget the reason its last session
// ended, when it holds one: a sign-in does it.
func (m *Manager) ClearEndedNotice(w http.ResponseWriter, r *http.Request) {
	if httpx.EndedReason(r) == "" {
		return
	}
	cookie.Clear(w, httpx.EndedCookie, cookie.Attributes{Path: "/", HTTPOnly: true, Secure: m.SecureCookie(r), SameSite: "lax"})
}

// Notice is what a browser is told about a session that ended for a
// reason: the code the web app knows and the English text. ok is false
// for a reason the browser is not told about (its own sign-out, idle and
// absolute ends, which have their own answer).
func Notice(reason string) (code, text string, ok bool) {
	switch reason {
	case ReasonSignedOut:
		return "session_signed_out", "You were signed out from another device. Please sign in again.", true
	case ReasonEndedByAdmin:
		return "session_ended_by_admin", "An administrator signed you out. Please sign in again.", true
	case ReasonReplaced:
		return "session_replaced", "You were signed out because this account signed in on another browser. This server allows one session per account.", true
	case ReasonLimit:
		return "session_limit", "You were signed out because this account signed in on more browsers than this server allows.", true
	case ReasonRevoked:
		return "session_revoked", "You were signed out because the account's password, two-factor sign-in or access changed. Please sign in again.", true
	}
	return "", "", false
}

type expiredAnswer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// responseWriter commits the session before the first byte of the answer,
// and the admin's session behind an impersonation (also) too.
type responseWriter struct {
	http.ResponseWriter
	session *Session
	also    *Session
}

func (w *responseWriter) commit() {
	if w.also != nil {
		w.also.commit(w.ResponseWriter)
	}
	w.session.commit(w.ResponseWriter)
}

func (w *responseWriter) WriteHeader(status int) {
	// An interim answer (100 to 199, but not a protocol switch) is not the
	// answer yet
	if status < 100 || status > 199 || status == http.StatusSwitchingProtocols {
		w.commit()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	w.commit()
	return w.ResponseWriter.Write(p)
}

// ReadFrom keeps the fast path for files.
func (w *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.commit()
	return io.Copy(w.ResponseWriter, src)
}

// Flush sends what was written so far.
func (w *responseWriter) Flush() {
	w.commit()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Info is one entry of an account's session list.
type Info struct {
	// ID is the session id.
	ID string
	// Current marks the session that asked for the list.
	Current           bool
	Username          string
	IPAddress         string
	UserAgent         string
	ClientFingerprint string
	// CreatedAt, LastActivityAt, EndedAt and ExpiresAt are in milliseconds,
	// 0 when not set.
	CreatedAt      int64
	LastActivityAt int64
	EndedAt        int64
	EndReason      string
	Active         bool
	ExpiresAt      int64

	// On PostgreSQL the three times that are BIGINT columns have always
	// gone out as text, also a 0.
	wideAsText                    bool
	hasCreated, hasLast, hasEnded bool
}

// MarshalJSON writes the entry as the console's API always has: null for
// what is not set.
func (i Info) MarshalJSON() ([]byte, error) {
	text := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	wide := func(n int64, has bool) any {
		switch {
		case i.wideAsText && has:
			return strconv.FormatInt(n, 10)
		case n == 0:
			return nil
		}
		return n
	}
	var expires any
	if i.ExpiresAt != 0 {
		expires = i.ExpiresAt
	}
	return httpx.Marshal(struct {
		ID                string `json:"id"`
		Current           bool   `json:"current"`
		Username          any    `json:"username"`
		IPAddress         any    `json:"ipAddress"`
		UserAgent         any    `json:"userAgent"`
		ClientFingerprint any    `json:"clientFingerprint"`
		CreatedAt         any    `json:"createdAt"`
		LastActivityAt    any    `json:"lastActivityAt"`
		EndedAt           any    `json:"endedAt"`
		EndReason         any    `json:"endReason"`
		Active            bool   `json:"active"`
		ExpiresAt         any    `json:"expiresAt"`
	}{
		i.ID, i.Current, text(i.Username), text(i.IPAddress), text(i.UserAgent), text(i.ClientFingerprint),
		wide(i.CreatedAt, i.hasCreated), wide(i.LastActivityAt, i.hasLast), wide(i.EndedAt, i.hasEnded),
		text(i.EndReason), i.Active, expires,
	})
}

// List returns the account's sessions for the "sessions" page, ended ones
// included, the most recently active first, at most 50. current is the id
// of the session that asks.
func (m *Manager) List(ctx context.Context, userID int64, current string) ([]Info, error) {
	list, err := m.table.list(ctx, userID, true, 50)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Current = list[i].ID == current
		// The list names a session by its handle, never by the id the cookie carries
		list[i].ID = Handle(list[i].ID)
	}
	return list, nil
}

// EndAccount ends every active session of an account with the reason
// "revoked" and empties it, but the session with the id keep ("" keeps
// none). A change to who an account is or what it may do calls it, so no
// session goes on with what the account was at sign-in. It returns how
// many sessions it ended.
func (m *Manager) EndAccount(ctx context.Context, userID int64, keep string) (int, error) {
	ended, err := m.EndDevices(ctx, userID, keep, ReasonRevoked)
	return len(ended), err
}

// Cleanup ends the sessions that are past a limit, deletes old rows that
// never belonged to an account and the rows of accounts 90 days after
// they ended.
func (m *Manager) Cleanup(ctx context.Context) error {
	return m.table.cleanup(ctx)
}

// RunCleanup calls Cleanup every Policy().CleanupInterval() until ctx
// ends. It blocks: start it with go.
func (m *Manager) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(m.policy.CleanupInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Cleanup(ctx); err != nil && ctx.Err() == nil {
				logging.Logf("session", "WARN: cleanup failed: %v", err)
			}
		}
	}
}
