package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The password of the test accounts and its bcrypt hash with the lowest
// cost, so a sign-in in a test takes no time. otherPassword is a second
// one for the password change.
const (
	password      = "Auth-Test-1!pass"
	passwordHash  = "$2b$04$dc.l7nhzKvP8b8PwtYT8l.FDZS7Yuc7QUJEihx.CgXH6mUu3MnMBy"
	otherPassword = "Other-Test-2!pass"
	testSecret    = "test-secret-0123456789abcdef0123456789abcdef"
	testAgent     = "auth-test/1"
)

// apiCall is one request the fake signal server got.
type apiCall struct {
	Method, Path, Query, Body string
}

// fakeAPI stands in for the signal server's API. A test sets the answer
// of a path; a path without one answers 404.
type fakeAPI struct {
	mu      sync.Mutex
	answers map[string]func(w http.ResponseWriter, r *http.Request, body string)
	calls   []apiCall
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, apiCall{r.Method, r.URL.Path, r.URL.RawQuery, string(raw)})
	answer := f.answers[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if answer == nil {
		http.NotFound(w, r)
		return
	}
	answer(w, r, string(raw))
}

// on sets the answer of a method and path ("POST /api/auth/login").
func (f *fakeAPI) on(route string, answer func(w http.ResponseWriter, r *http.Request, body string)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[route] = answer
}

// json makes a path answer a status with a JSON body.
func (f *fakeAPI) json(route string, status int, body string) {
	f.on(route, func(w http.ResponseWriter, _ *http.Request, _ string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// seen lists the calls of a method and path.
func (f *fakeAPI) seen(route string) []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []apiCall
	for _, call := range f.calls {
		if call.Method+" "+call.Path == route {
			out = append(out, call)
		}
	}
	return out
}

// setup says what kind of console a test wants.
type setup struct {
	env        map[string]string
	trustProxy string
	https      bool
	// build gives the console a build of the web app: one page.
	build bool
}

// testPage is the page of the web app build a test console gets.
const testPage = "<!doctype html>\n<html><head><title>BetterDesk</title></head><body></body></html>\n"

// world is a console on a database with a fake signal server behind it.
type world struct {
	t     *testing.T
	db    *store.DB
	s     *console.Server
	api   *fakeAPI
	front *httptest.Server
}

func newWorld(t *testing.T, db *store.DB, o setup) *world {
	t.Helper()
	if db == nil {
		db = storetest.Migrated(t)
	}
	data := t.TempDir()
	env := map[string]string{"SESSION_SECRET": testSecret, "LOGIN_RATE_LIMIT_MAX": "1000"}
	for k, v := range o.env {
		env[k] = v
	}
	trust, err := httpx.ParseTrustProxy(o.trustProxy)
	if err != nil {
		t.Fatal(err)
	}
	webUI := filepath.Join(data, "no-build")
	if o.build {
		webUI = filepath.Join(data, "build")
		if err := os.MkdirAll(webUI, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(webUI, "index.html"), []byte(testPage), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{
		Host:            "127.0.0.1",
		TrustProxy:      trust,
		NodeEnv:         "production",
		Production:      true,
		DataDir:         data,
		KeysPath:        data,
		UploadsDir:      filepath.Join(data, "uploads"),
		LangDir:         filepath.Join("..", "..", "static", "files", "lang"),
		WebUIDir:        webUI,
		HTTPS:           o.https,
		DefaultLanguage: "en",
		Getenv:          func(key string) string { return env[key] },
	}
	w := &world{t: t, db: db, api: &fakeAPI{answers: map[string]func(http.ResponseWriter, *http.Request, string){}}}
	s, err := console.New(context.Background(), console.Options{Config: cfg, Store: db, API: w.api})
	if err != nil {
		t.Fatalf("console.New: %v", err)
	}
	w.s = s
	t.Cleanup(s.Stop)

	s.Router().Get("/__test/token", func(rw http.ResponseWriter, r *http.Request) {
		kit.JSON(rw, r, http.StatusOK, map[string]string{"token": kit.CSRFToken(r)})
	})
	// Sets fields of the request's session from a JSON object in ?set=
	s.Router().Get("/__test/session", func(rw http.ResponseWriter, r *http.Request) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.URL.Query().Get("set")), &fields); err != nil {
			t.Errorf("session fields: %v", err)
		}
		for key, value := range fields {
			kit.Session(r).Set(key, value)
		}
		kit.OK(rw, r, nil)
	})
	w.front = httptest.NewServer(s)
	t.Cleanup(w.front.Close)
	return w
}

// eachWorld runs a test on SQLite and, when a server is configured, on
// PostgreSQL.
func eachWorld(t *testing.T, o setup, fn func(t *testing.T, w *world)) {
	t.Helper()
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) { fn(t, newWorld(t, db, o)) })
}

// user is a row of users for a test.
type user struct {
	name, hash, role, provider string
	totpSecret, recoveryCodes  string
	totpEnabled                bool
	language                   any
}

// addUser inserts an account and returns its id. The password is the test
// password unless hash says otherwise.
func (w *world) addUser(u user) int64 {
	w.t.Helper()
	if u.hash == "" {
		u.hash = passwordHash
	}
	if u.role == "" {
		u.role = "operator"
	}
	if u.provider == "" {
		u.provider = "local"
	}
	var enabled any = u.totpEnabled
	if !w.db.IsPostgres() {
		enabled = 0
		if u.totpEnabled {
			enabled = 1
		}
	}
	columns, values := "username, password_hash, role, auth_provider, preferred_language, totp_enabled", []any{u.name, u.hash, u.role, u.provider, u.language, enabled}
	// The secret has no NULL where the signal server made the table
	if u.totpSecret != "" {
		columns, values = columns+", totp_secret", append(values, u.totpSecret)
	}
	if u.recoveryCodes != "" {
		columns, values = columns+", totp_recovery_codes", append(values, u.recoveryCodes)
	}
	w.exec(`INSERT INTO users (`+columns+`) VALUES (?`+strings.Repeat(", ?", len(values)-1)+`)`, values...)
	return w.int(`SELECT id FROM users WHERE username = ?`, u.name)
}

func (w *world) exec(query string, args ...any) {
	w.t.Helper()
	if _, err := w.db.Exec(context.Background(), query, args...); err != nil {
		w.t.Fatalf("%s: %v", query, err)
	}
}

// int reads one whole number.
func (w *world) int(query string, args ...any) int64 {
	w.t.Helper()
	var n int64
	if err := w.db.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// text reads one text, "" for NULL and for no row.
func (w *world) text(query string, args ...any) string {
	w.t.Helper()
	var s *string
	err := w.db.QueryRow(context.Background(), query, args...).Scan(&s)
	if err != nil && !strings.Contains(err.Error(), "no rows") {
		w.t.Fatalf("%s: %v", query, err)
	}
	if s == nil {
		return ""
	}
	return *s
}

// rows reads rows as lines of their columns joined by "|".
func (w *world) rows(query string, args ...any) []string {
	w.t.Helper()
	rows, err := w.db.Query(context.Background(), query, args...)
	if err != nil {
		w.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			w.t.Fatal(err)
		}
		parts := make([]string, len(values))
		for i, v := range values {
			switch x := v.(type) {
			case nil:
				parts[i] = ""
			case []byte:
				parts[i] = string(x)
			default:
				parts[i] = fmt.Sprint(x)
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

// audit lists the audit log as "action|details|address|user id".
func (w *world) audit() []string {
	w.t.Helper()
	return w.rows(`SELECT action, COALESCE(details, ''), COALESCE(ip_address, ''), COALESCE(user_id, 0) FROM audit_log ORDER BY id`)
}

// attempts lists the sign-in attempts as "name|address|success", success
// as 0 or 1.
func (w *world) attempts() []string {
	w.t.Helper()
	return w.rows(`SELECT username, ip_address, CASE WHEN ` + w.db.Pick("success = 1", "success") + ` THEN 1 ELSE 0 END FROM login_attempts ORDER BY id`)
}

// sessions lists an account's session rows as "end reason|active", oldest
// first.
func (w *world) sessions(userID int64) []string {
	w.t.Helper()
	return w.rows(`SELECT COALESCE(end_reason, ''), CASE WHEN `+w.db.Pick("is_active = 1", "is_active")+` THEN 1 ELSE 0 END
		FROM panel_sessions WHERE user_id = ? ORDER BY created_at, updated_at`, userID)
}

// stored is the stored JSON of the session a cookie names.
func (w *world) stored(a *agent) string {
	w.t.Helper()
	return w.text(`SELECT session_json FROM panel_sessions WHERE sid = ?`, a.sessionID())
}

// answer is what the console answered.
type answer struct {
	status int
	header http.Header
	body   string
}

// cookies are the Set-Cookie lines of the answer.
func (a answer) cookies() []string { return a.header.Values("Set-Cookie") }

// cookie is the Set-Cookie line of a cookie name, "" when the answer sets
// none.
func (a answer) cookie(name string) string {
	for _, line := range a.cookies() {
		if strings.HasPrefix(line, name+"=") {
			return line
		}
	}
	return ""
}

// agent is a browser: it keeps cookies between requests.
type agent struct {
	w       *world
	cookies map[string]string
	token   string
	// header is a list of name, value pairs sent with every request.
	header []string
}

func (w *world) agent(header ...string) *agent {
	return &agent{w: w, cookies: map[string]string{}, header: append([]string{"User-Agent", testAgent}, header...)}
}

var plainClient = &http.Client{
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       60 * time.Second,
}

// do sends a request. header is a list of name, value pairs.
func (a *agent) do(method, target, body string, header ...string) answer {
	a.w.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, a.w.front.URL+target, reader)
	if err != nil {
		a.w.t.Fatal(err)
	}
	header = append(append([]string{}, a.header...), header...)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	var pairs []string
	for name, value := range a.cookies {
		pairs = append(pairs, name+"="+value)
	}
	if len(pairs) > 0 {
		req.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	res, err := plainClient.Do(req)
	if err != nil {
		a.w.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
			delete(a.cookies, c.Name)
			continue
		}
		a.cookies[c.Name] = c.Value
	}
	return answer{status: res.StatusCode, header: res.Header, body: string(raw)}
}

func (a *agent) get(target string, header ...string) answer {
	a.w.t.Helper()
	return a.do("GET", target, "", header...)
}

// post sends a JSON body with the agent's CSRF token.
func (a *agent) post(target, body string, header ...string) answer {
	a.w.t.Helper()
	header = append([]string{"Content-Type", "application/json", "X-CSRF-Token", a.csrf()}, header...)
	return a.do("POST", target, body, header...)
}

// csrf fetches the agent's CSRF token once.
func (a *agent) csrf() string {
	a.w.t.Helper()
	if a.token == "" {
		var out map[string]string
		_ = json.Unmarshal([]byte(a.get("/__test/token").body), &out)
		if a.token = out["token"]; a.token == "" {
			a.w.t.Fatal("no CSRF token")
		}
	}
	return a.token
}

// signIn signs the agent in with the test password and fails the test when
// the console refuses.
func (a *agent) signIn(name string) answer {
	a.w.t.Helper()
	res := a.post("/api/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, name, password))
	if res.status != http.StatusOK || !strings.Contains(res.body, `"user"`) {
		a.w.t.Fatalf("sign in as %s: %d %s", name, res.status, res.body)
	}
	return res
}

// sessionID is the id in the agent's session cookie, "" without one.
func (a *agent) sessionID() string {
	for _, name := range []string{"bd.sid", "betterdesk.sid"} {
		if value, err := url.QueryUnescape(a.cookies[name]); err == nil && strings.HasPrefix(value, "s:") {
			id, _, _ := strings.Cut(value[2:], ".")
			return id
		}
	}
	return ""
}

// setSession writes fields into the agent's session.
func (a *agent) setSession(fields string) {
	a.w.t.Helper()
	if res := a.get("/__test/session?set=" + url.QueryEscape(fields)); res.status != http.StatusOK {
		a.w.t.Fatalf("set session fields: %d %s", res.status, res.body)
	}
}

// want fails the test unless the answer has the status and the body.
func want(t *testing.T, what string, got answer, status int, body string) {
	t.Helper()
	if got.status != status || got.body != body {
		t.Errorf("%s:\n got %d %s\nwant %d %s", what, got.status, got.body, status, body)
	}
}

func equal(t *testing.T, what string, got, wanted []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(wanted, "\n") {
		t.Errorf("%s:\n got %s\nwant %s", what, strings.Join(got, "\n     "), strings.Join(wanted, "\n     "))
	}
}

// The texts of lang/en.json these routes answer with.
const (
	invalidCredentials = `{"success":false,"error":"Invalid username or password."}`
	serverErrorBody    = `{"success":false,"error":"Server error"}`
	invalidCode        = `{"success":false,"error":"Invalid verification code. Please try again."}`
	notSignedIn        = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	clearedGuest       = "bd.guest=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax"
)

// totpSecret is a two-factor secret for the tests: the base32 of
// "12345678901234567890".
const totpSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// totpAt is the time-based code of RFC 6238 for a base32 secret: six
// digits, steps of 30 seconds, HMAC-SHA1.
func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("the secret is not base32: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// currentCode is the code of the step that is running. The console accepts
// no other step, so near the end of one this waits for the next.
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	if left := 30 - time.Now().Unix()%30; left <= 4 {
		time.Sleep(time.Duration(left)*time.Second + 200*time.Millisecond)
	}
	return totpAt(t, secret, time.Now())
}

// wrongCode is six digits that are not the code of this step or the next.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	for _, candidate := range []string{"000000", "111111", "222222"} {
		if candidate != totpAt(t, secret, now) && candidate != totpAt(t, secret, now.Add(30*time.Second)) {
			return candidate
		}
	}
	return "333333"
}

// agentFrom is a browser at another address, for a console that trusts
// the proxy on loopback.
func (w *world) agentFrom(ip string) *agent {
	return w.agent("X-Forwarded-For", ip)
}
