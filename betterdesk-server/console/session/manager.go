package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// The session cookie's name: one for a console set up for HTTPS, one for
// plain HTTP, so a Secure cookie left from an HTTPS setup never shadows
// the HTTP one.
const (
	cookieNameHTTPS = "betterdesk.sid"
	cookieNameHTTP  = "bd.sid"
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
	// Secure marks the cookie Secure: set it when the console serves
	// HTTPS. In HTTPS mode without a usable certificate the console serves
	// plain HTTP, and the cookie keeps its HTTPS name without Secure.
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
	s, _ := r.Context().Value(contextKey{}).(*Session)
	return s
}

// Load returns the session a request's cookie names, without renewing it:
// the row is not touched and no cookie is sent. Websocket upgrades use it.
// A request without a valid, live session gets an empty one.
func (m *Manager) Load(r *http.Request) (*Session, error) {
	s, expired, err := m.load(r)
	if err != nil || expired == "" {
		return s, err
	}
	return m.newSession(r)
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
	text, found, ended, err := m.table.get(r.Context(), id)
	if err != nil {
		return nil, "", err
	}
	if !found && ended == "" {
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

// cookieHeader is the Set-Cookie text for a session.
func (m *Manager) cookieHeader(s *Session) string {
	return cookie.Format(m.cookieName, cookiePrefix+sign(s.id, m.secret), cookie.Attributes{
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
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, expired, err := m.load(r)
		if err != nil {
			m.onError(w, r, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, s))
		s.req = r
		if expired != "" {
			m.answerExpired(w, r, s, expired)
			return
		}
		if s.signedInLocked() {
			if ok, reason := s.touchActivity(r); !ok {
				m.answerExpired(w, r, s, reason)
				return
			}
			s.noteIPChange(r)
		}
		rw := &responseWriter{ResponseWriter: w, session: s}
		defer s.finish(w)
		next.ServeHTTP(rw, r)
	})
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

type expiredAnswer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// responseWriter commits the session before the first byte of the answer.
type responseWriter struct {
	http.ResponseWriter
	session *Session
}

func (w *responseWriter) WriteHeader(status int) {
	// An interim answer (100 to 199, but not a protocol switch) is not the
	// answer yet
	if status < 100 || status > 199 || status == http.StatusSwitchingProtocols {
		w.session.commit(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	w.session.commit(w.ResponseWriter)
	return w.ResponseWriter.Write(p)
}

// ReadFrom keeps the fast path for files.
func (w *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.session.commit(w.ResponseWriter)
	return io.Copy(w.ResponseWriter, src)
}

// Flush sends what was written so far.
func (w *responseWriter) Flush() {
	w.session.commit(w.ResponseWriter)
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
	}
	return list, nil
}

// EndAccount ends every active session of an account with the reason
// "revoked" and empties it, but the session with the id keep ("" keeps
// none). A change to who an account is or what it may do calls it, so no
// session goes on with what the account was at sign-in. It returns how
// many sessions it ended.
func (m *Manager) EndAccount(ctx context.Context, userID int64, keep string) (int, error) {
	return m.table.endAccount(ctx, userID, keep)
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
