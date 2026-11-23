package session

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// Session is the session of one request. It is safe for concurrent use.
//
// A session has fields with JSON values. The ones sign-in writes have
// their own accessors (UserID, User); Get, Set and Delete reach the rest.
// Changes are stored when the answer goes out.
type Session struct {
	m   *Manager
	req *http.Request

	mu sync.Mutex
	// id is the session's id. cookieID is the id the request's cookie
	// carried with a good signature, and originalID the id the session had
	// when the request came in.
	id         string
	cookieID   string
	originalID string
	// data holds every field in stored order. The cookie has its place in
	// the order, and its value is written from cookie.
	data   *fields
	cookie cookieState
	// original is the data as it was when the request came in, to tell
	// whether a handler changed it.
	original  string
	destroyed bool
	// committed is set once the cookie was decided and the store written.
	// saved is the data that write stored, to notice later changes, and
	// sentID the id the answer's cookie carries.
	committed bool
	savedID   string
	saved     string
	sentID    string
	// ephemeral marks the session of a request an API token signed in
	// (Manager.Ephemeral): nothing of it reaches the store or a cookie.
	ephemeral bool
}

// User is the account a session belongs to, as copied at sign-in.
type User struct {
	ID       int64
	Username string
	Role     string
	// PreferredLanguage is "" when the account has none.
	PreferredLanguage string
}

// MarshalJSON writes the user as the session stores it.
func (u User) MarshalJSON() ([]byte, error) {
	lang := json.RawMessage("null")
	if u.PreferredLanguage != "" {
		lang = mustJSON(u.PreferredLanguage)
	}
	var b strings.Builder
	b.WriteString(`{"id":`)
	b.WriteString(strconv.FormatInt(u.ID, 10))
	b.WriteString(`,"username":`)
	b.Write(mustJSON(u.Username))
	b.WriteString(`,"role":`)
	b.Write(mustJSON(u.Role))
	b.WriteString(`,"preferred_language":`)
	b.Write(lang)
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// Meta is what is recorded about the client at sign-in. The JSON form is
// the one a session waiting for the second factor keeps.
type Meta struct {
	IP          string `json:"ip"`
	UserAgent   string `json:"userAgent"`
	Fingerprint string `json:"clientFingerprint"`
}

func (m *Manager) newSession(r *http.Request) (*Session, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	s := &Session{m: m, req: r, id: id, originalID: id}
	s.reset()
	s.original = s.data.encode(false)
	return s, nil
}

// reset empties the session and gives it a new cookie.
func (s *Session) reset() {
	s.data = newFields()
	s.data.set(cookieKey, nil)
	s.cookie = newCookieState(s.m.now(), s.m.policy.IdleTimeoutMs, s.m.SecureCookie(s.req))
}

// restore fills the session from stored JSON.
func (s *Session) restore(text string) error {
	data, err := parseFields([]byte(text))
	if err != nil {
		return err
	}
	raw, _ := data.get(cookieKey)
	cookie, err := parseCookieState(raw)
	if err != nil {
		return err
	}
	s.data, s.cookie = data, cookie
	s.original = data.encode(false)
	return nil
}

// encode writes the session as it is stored.
func (s *Session) encode() string {
	s.data.set(cookieKey, s.cookie.encode())
	return s.data.encode(true)
}

// ctx is the context for the session's writes to the store. It outlives
// the request, so a client that hangs up does not leave a sign-out or a
// change half done, and it ends after ten seconds.
func (s *Session) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(s.req.Context()), 10*time.Second)
}

// ID is the session's id.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// FromCookie reports whether the request brought this session along: its
// cookie named a session the store holds, signed in or not (one waiting
// for its second factor, say). A request without the cookie, or whose
// cookie names no stored session, got a new, empty one.
func (s *Session) FromCookie() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookieID != "" && s.cookieID == s.originalID
}

// UserID is the id of the signed-in account, and 0 when nobody is signed
// in.
func (s *Session) UserID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userID()
}

func (s *Session) userID() int64 {
	if !s.signedIn() {
		return 0
	}
	n := s.data.num("userId")
	if n != math.Trunc(n) {
		return 0
	}
	return toInt64(n)
}

// signedIn reports whether the session carries an account.
func (s *Session) signedIn() bool {
	return !s.destroyed && s.data.truthy("userId")
}

// User is the signed-in account, and nil when nobody is signed in.
func (s *Session) User() *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.signedIn() {
		return nil
	}
	u := &User{ID: s.userID()}
	if raw, ok := s.data.get("user"); ok {
		if obj, err := parseFields(raw); err == nil {
			if n := obj.num("id"); n != 0 && n == math.Trunc(n) {
				u.ID = toInt64(n)
			}
			u.Username = obj.str("username")
			u.Role = obj.str("role")
			u.PreferredLanguage = obj.str("preferred_language")
		}
	}
	// A single sign-on session also carries the name and role at the top
	if u.Username == "" {
		u.Username = s.data.str("username")
	}
	if u.Role == "" {
		u.Role = s.data.str("role")
	}
	return u
}

// Get reads the field key into dst, which must be a pointer as for
// json.Unmarshal. It answers false when the session has no such field or
// the value does not fit dst.
func (s *Session) Get(key string, dst any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed || key == cookieKey {
		return false
	}
	raw, ok := s.data.get(key)
	if !ok {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

// Set stores value in the field key. The value must be one encoding/json
// can write; Set panics on any other, which is a mistake in the calling
// code. A destroyed session takes no fields.
func (s *Session) Set(key string, value any) {
	raw := mustJSON(value)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed || key == cookieKey {
		return
	}
	s.data.set(key, raw)
}

// Delete removes the field key.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed || key == cookieKey {
		return
	}
	s.data.delete(key)
}

// Destroy ends the session: its row ends with the reason "destroyed"
// unless it already has one, its stored fields are emptied, and the answer
// carries no session cookie. The session object stays usable and is empty.
func (s *Session) Destroy() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.destroyed {
		return nil
	}
	s.destroyed = true
	s.data = newFields()
	if s.ephemeral {
		return nil
	}
	ctx, cancel := s.ctx()
	defer cancel()
	return s.m.table.destroy(ctx, s.id)
}

// Regenerate replaces the session with a new, empty one under a new id,
// and destroys the old one. Sign-in does this before it writes the
// account, so an id handed out before sign-in is worth nothing after.
// The new session is stored and its cookie sent with the answer, also when
// nothing is written to it.
func (s *Session) Regenerate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ephemeral {
		return ErrEphemeral
	}
	id, err := newID()
	if err != nil {
		return err
	}
	ctx, cancel := s.ctx()
	defer cancel()
	err = s.m.table.destroy(ctx, s.id)
	s.id = id
	s.destroyed = false
	s.reset()
	return err
}

// Establish signs the account in on this session: it writes the account
// and the client's details, gives the cookie the idle lifetime, stores the
// session, and applies the per-account limit. It answers how many of the
// account's other sessions the limit ended (reason "replaced").
//
// Call Regenerate first. Fields set after Establish are stored with the
// answer.
func (s *Session) Establish(user User, meta Meta) (replaced int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ephemeral {
		return 0, ErrEphemeral
	}
	if s.destroyed {
		return 0, errors.New("session: Establish on a destroyed session")
	}
	now := s.m.now()
	at := now.UnixMilli()
	s.data.set("userId", mustJSON(user.ID))
	s.data.set("user", mustJSON(user))
	s.data.set("createdAt", mustJSON(at))
	s.data.set("lastActivityAt", mustJSON(at))
	s.data.set("loginIp", mustJSON(meta.IP))
	s.data.set("loginUserAgent", mustJSON(meta.UserAgent))
	s.data.set("clientFingerprint", mustJSON(meta.Fingerprint))
	s.cookie.setMaxAge(now, min(s.m.policy.IdleTimeoutMs, s.m.policy.MaxAgeMs))

	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.m.table.set(ctx, s.id, s.record(now)); err != nil {
		return 0, err
	}
	ended, err := s.m.table.activate(ctx, s.id, user.ID, user.Username, meta, at)
	if err != nil {
		return 0, err
	}
	if len(ended) > 0 {
		logging.Logf("session", "WARN: replaced %d prior session(s) for user %s (id=%d) from new login ip=%s",
			len(ended), user.Username, user.ID, orUnknown(meta.IP))
		for _, prev := range ended {
			logging.Logf("session", "WARN: prior session ended (replaced): user=%s prevIp=%s prevUa=%s",
				user.Username, orUnknown(prev.ip), cut(prev.userAgent, 120))
		}
	}
	return len(ended), nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// End ends the session's row with a reason (ReasonLogout at sign-out) and
// empties its stored fields. The session object is unchanged: call Destroy
// after it. A failure is logged and not returned, so sign-out goes on.
func (s *Session) End(reason string) {
	s.mu.Lock()
	id, ephemeral := s.id, s.ephemeral
	s.mu.Unlock()
	if ephemeral {
		return
	}
	ctx, cancel := s.ctx()
	defer cancel()
	if err := s.m.table.end(ctx, id, reason); err != nil {
		logging.Logf("session", "WARN: ending the session failed: %v", err)
	}
}

// ClearCookies makes the browser forget the session cookie under both
// names it can have, as a sign-out answers.
func (s *Session) ClearCookies(w http.ResponseWriter) {
	clearCookie(w, cookieNameHTTPS)
	clearCookie(w, cookieNameHTTP)
}

// record is the session as the table stores it.
func (s *Session) record(now time.Time) record {
	nowMs := now.UnixMilli()
	rec := record{
		json:           s.encode(),
		createdAt:      s.data.millis("createdAt"),
		lastActivityAt: s.data.millis("lastActivityAt"),
		ip:             cleanIP(s.data.str("loginIp")),
		userAgent:      cleanUserAgent(s.data.str("loginUserAgent")),
		fingerprint:    cleanFingerprint(s.data.str("clientFingerprint")),
	}
	if rec.createdAt == 0 {
		rec.createdAt = nowMs
	}
	if rec.lastActivityAt == 0 {
		rec.lastActivityAt = nowMs
	}
	rec.expiresAt = s.expiresAt(nowMs)
	if raw, ok := s.data.get("userId"); ok && isJSONNumber(raw) {
		if n := jsNumber(raw); n == math.Trunc(n) {
			rec.userID = toInt64(n)
		} else {
			rec.userID = n
		}
	}
	if raw, ok := s.data.get("user"); ok {
		if obj, err := parseFields(raw); err == nil {
			rec.username = obj.str("username")
		}
	}
	if rec.username == "" {
		rec.username = s.data.str("username")
	}
	return rec
}

// expiresAt is the row's expiry: the cookie's, or for a cookie without one
// the earlier of the absolute end and the idle end.
func (s *Session) expiresAt(nowMs int64) int64 {
	if !s.cookie.expires.IsZero() {
		return s.cookie.expires.UnixMilli()
	}
	created := s.data.millis("createdAt")
	if created == 0 {
		created = nowMs
	}
	return min(created+s.m.policy.MaxAgeMs, nowMs+s.m.policy.IdleTimeoutMs)
}

// touchActivity checks a signed-in session against the absolute and the
// idle limit and, for a request that counts as activity, moves the last
// activity and the cookie's lifetime. A request with the header
// X-ND-Background: 1 is checked and does not count.
func (s *Session) touchActivity(r *http.Request) (ok bool, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.m.now()
	nowMs := now.UnixMilli()
	createdAt := s.data.millis("createdAt")
	if createdAt == 0 {
		createdAt = nowMs
	}
	if !s.data.truthy("createdAt") {
		s.data.set("createdAt", mustJSON(createdAt))
	}
	policy := s.m.policy
	if nowMs-createdAt > policy.MaxAgeMs {
		return false, ReasonMaxAge
	}
	last := s.data.millis("lastActivityAt")
	if last == 0 {
		last = createdAt
	}
	if nowMs-last > policy.IdleTimeoutMs {
		return false, ReasonIdle
	}
	if strings.Join(r.Header.Values("X-ND-Background"), ", ") == "1" {
		return true, ""
	}
	s.data.set("lastActivityAt", mustJSON(nowMs))
	nextIdle := min(policy.IdleTimeoutMs, createdAt+policy.MaxAgeMs-nowMs)
	if nextIdle <= 0 {
		return false, ReasonMaxAge
	}
	s.cookie.setMaxAge(now, nextIdle)
	return true, ""
}

// noteIPChange reports a request from another address than the one that
// signed in, once per new address.
func (s *Session) noteIPChange(r *http.Request) {
	s.mu.Lock()
	loginIP := s.data.str("loginIp")
	current := cleanIP(s.m.trust.ClientIP(r))
	if !s.signedIn() || loginIP == "" || current == "" || current == loginIP || s.data.str("_lastSeenIp") == current {
		s.mu.Unlock()
		return
	}
	s.data.set("_lastSeenIp", mustJSON(current))
	userID := s.userID()
	s.mu.Unlock()

	details := "IP change during session: loginIp=" + loginIP + " currentIp=" + current +
		" ua=" + cut(cleanUserAgent(httpx.Latin1(r.UserAgent())), 160)
	logging.Logf("session", "WARN: %s userId=%d", details, userID)
	if s.m.audit != nil {
		s.m.audit(r.Context(), userID, "session_ip_change", details, current)
	}
}

// commit decides the session cookie and writes the session to the store.
// It runs once, before the first byte of the answer goes out.
//
// The cookie is sent when the browser already holds this session (every
// answer renews it), or when the session is new and a handler wrote to it.
// A cookie marked Secure is not sent over a plain connection. The store
// gets the whole session when a handler changed it, and only the new
// expiry when the browser's session is unchanged.
func (s *Session) commit(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.committed {
		return
	}
	s.committed = true
	if s.destroyed {
		return
	}
	now := s.m.now()
	s.cookie.touch(now)

	fromCookie := s.cookieID == s.id
	changed := s.changed()
	if (fromCookie || changed) && (!s.cookie.secure() || s.m.secureRequest(s.req)) {
		w.Header().Add("Set-Cookie", s.m.cookieHeader(s))
		s.sentID = s.id
	}
	s.store(now, changed, fromCookie)
}

// changed reports whether the session differs from what the request came
// in with: another id, or other fields.
func (s *Session) changed() bool {
	return s.id != s.originalID || s.data.encode(false) != s.original
}

func (s *Session) store(now time.Time, changed, fromCookie bool) {
	ctx, cancel := s.ctx()
	defer cancel()
	var err error
	switch {
	case changed:
		err = s.m.table.set(ctx, s.id, s.record(now))
	case fromCookie:
		last := s.data.millis("lastActivityAt")
		if last == 0 {
			last = now.UnixMilli()
		}
		err = s.m.table.touch(ctx, s.id, s.expiresAt(now.UnixMilli()), last)
	}
	if err != nil {
		logging.Logf("session", "ERROR: storing the session failed: %v", err)
	}
	s.savedID, s.saved = s.id, s.data.encode(false)
}

// finish runs when the handler has returned. It commits if no byte went
// out, and stores the session once more if the handler changed it after
// the answer had started.
func (s *Session) finish(w http.ResponseWriter) {
	s.commit(w)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ephemeral || s.destroyed || (s.id == s.savedID && s.data.encode(false) == s.saved) {
		return
	}
	if s.cookieID != s.id && s.sentID != s.id {
		logging.Logf("session", "WARN: %s %s wrote to a new session after the answer had started; it is stored, but the browser has no cookie for it",
			s.req.Method, s.req.URL.Path)
	}
	s.store(s.m.now(), true, false)
}
