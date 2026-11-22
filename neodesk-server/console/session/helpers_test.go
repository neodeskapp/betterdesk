package session

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/store"
)

// testSecret is the secret of the spec's test vectors.
const testSecret = "test-secret-0123456789abcdef0123456789abcdef"

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type auditEntry struct {
	userID              int64
	action, details, ip string
}

type fixture struct {
	t      *testing.T
	db     *store.DB
	m      *Manager
	clock  *fakeClock
	mu     sync.Mutex
	audits []auditEntry
	errs   []error
}

// newFixture returns a Manager on db with a clock the test moves. The clock
// starts at the real time, so the database's own clock agrees with it.
func newFixture(t *testing.T, db *store.DB, change ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{t: t, db: db, clock: &fakeClock{t: time.Now().Truncate(time.Millisecond)}}
	opts := Options{
		DB:     db,
		Secret: testSecret,
		Policy: PolicyFromEnv(func(string) string { return "" }),
		Now:    f.clock.Now,
		Audit: func(_ context.Context, userID int64, action, details, ip string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.audits = append(f.audits, auditEntry{userID, action, details, ip})
		},
		OnError: func(w http.ResponseWriter, _ *http.Request, err error) {
			f.mu.Lock()
			f.errs = append(f.errs, err)
			f.mu.Unlock()
			http.Error(w, "session error", http.StatusInternalServerError)
		},
		LoginRedirect: func(r *http.Request) string { return "/login?expired=1&return=" + r.URL.Path },
	}
	for _, fn := range change {
		fn(&opts)
	}
	m, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.m = m
	return f
}

// serve runs one request through the middleware and the handler.
func (f *fixture) serve(r *http.Request, h func(w http.ResponseWriter, r *http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	if h == nil {
		h = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	}
	rec := httptest.NewRecorder()
	f.m.Middleware(http.HandlerFunc(h)).ServeHTTP(rec, r)
	return rec
}

// get builds a request with the given Cookie header values.
func get(path string, cookies ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "203.0.113.5:40000"
	r.Header.Set("User-Agent", "GoTest/1.0")
	for _, c := range cookies {
		r.Header.Add("Cookie", c)
	}
	return r
}

// setCookies returns the Set-Cookie headers of an answer.
func setCookies(rec *httptest.ResponseRecorder) []string {
	return rec.Result().Header.Values("Set-Cookie")
}

// sessionCookie returns the cookie pair ("bd.sid=...") an answer set for
// the session, and "" when it set none.
func (f *fixture) sessionCookie(rec *httptest.ResponseRecorder) string {
	for _, c := range setCookies(rec) {
		if pair, _, _ := strings.Cut(c, ";"); strings.HasPrefix(pair, f.m.cookieName+"=") && !strings.HasSuffix(pair, "=") {
			return pair
		}
	}
	return ""
}

// cookieFor is the cookie pair a browser sends for a session id.
func (f *fixture) cookieFor(sid string) string {
	return f.m.cookieName + "=" + httpx.EncodeURIComponent(cookiePrefix+sign(sid, f.m.secret))
}

// sidOf reads the session id out of a cookie pair.
func (f *fixture) sidOf(pair string) string {
	f.t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", pair)
	sid, ok := f.m.cookieID(r)
	if !ok {
		f.t.Fatalf("no valid session id in %q", pair)
	}
	return sid
}

var alice = User{ID: 7, Username: "alice", Role: "operator"}

// signIn signs a user in through the middleware and returns the cookie pair
// of the new session.
func (f *fixture) signIn(user User, change ...func(r *http.Request)) string {
	f.t.Helper()
	r := get("/api/auth/login")
	r.Method = http.MethodPost
	for _, fn := range change {
		fn(r)
	}
	rec := f.serve(r, func(w http.ResponseWriter, r *http.Request) {
		s := From(r)
		if err := s.Regenerate(); err != nil {
			f.t.Errorf("Regenerate: %v", err)
		}
		if _, err := s.Establish(user, f.m.ClientMeta(r, "fp_abcdef123456")); err != nil {
			f.t.Errorf("Establish: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})
	pair := f.sessionCookie(rec)
	if pair == "" {
		f.t.Fatalf("sign-in set no session cookie: %q", setCookies(rec))
	}
	return pair
}

// rowDump is a row of panel_sessions.
type rowDump struct {
	json                                string
	expiresAt                           int64
	userID                              sql.NullInt64
	username, ip, userAgent, fp, reason sql.NullString
	createdAt, lastActivityAt, endedAt  sql.NullInt64
	active                              bool
}

// row reads a session's row, nil when there is none.
func (f *fixture) row(sid string) *rowDump {
	f.t.Helper()
	return readRow(f.t, f.db, sid)
}

func readRow(t *testing.T, db *store.DB, sid string) *rowDump {
	t.Helper()
	var d rowDump
	var expires any
	var active int64
	err := db.QueryRow(context.Background(),
		`SELECT session_json, expires_at, user_id, username, ip_address, user_agent, client_fingerprint, end_reason,
		        created_at, last_activity_at, ended_at, `+db.Pick("CASE WHEN is_active = 1 THEN 1 ELSE 0 END", "CASE WHEN is_active THEN 1 ELSE 0 END")+`
		 FROM panel_sessions WHERE sid = ?`, sid,
	).Scan(&d.json, &expires, &d.userID, &d.username, &d.ip, &d.userAgent, &d.fp, &d.reason,
		&d.createdAt, &d.lastActivityAt, &d.endedAt, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatalf("read row %s: %v", sid, err)
	}
	d.expiresAt = columnMillis(expires)
	d.active = active == 1
	return &d
}

// rowCount counts the rows of panel_sessions.
func (f *fixture) rowCount() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM panel_sessions`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// exec runs a statement on the test database.
func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(context.Background(), query, args...); err != nil {
		f.t.Fatalf("%s: %v", query, err)
	}
}

// setExpires moves a row's expires_at.
func (f *fixture) setExpires(sid string, ms int64) {
	f.t.Helper()
	f.exec(`UPDATE panel_sessions SET expires_at = `+f.m.table.sqlTime()+` WHERE sid = ?`, ms, sid)
}
