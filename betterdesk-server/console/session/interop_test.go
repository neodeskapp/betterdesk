//go:build nodeinterop

package session_test

// Interop tests against the Node console's real session and CSRF code
// (testdata/interop/app.js). Go and Node run side by side on one database
// with one secret, and a browser moves between them.
//
//	go test -tags nodeinterop ./console/session/ -run Interop
//
// They need node on the PATH and the Node console at /repo/web-nodejs, as
// in the dev image.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/csrf"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

const interopSecret = "interop-secret-5f2c9a7e41d04b6f8a3c1e9d7b5f2a60"

// setup is what both servers are started with.
type setup struct {
	secret     string
	https      bool
	trustProxy string
	env        map[string]string // SESSION_* policy
}

func (s setup) getenv(key string) string { return s.env[key] }

func defaultSetup() setup { return setup{secret: interopSecret} }

// shortLives is the shortest policy the settings allow: one minute idle,
// five minutes in all. The tests move the rows' times to get past them.
func shortLives() setup {
	return setup{secret: interopSecret, env: map[string]string{"SESSION_IDLE_TIMEOUT": "60000", "SESSION_MAX_AGE": "300000"}}
}

// server is one of the two consoles.
type server struct {
	name string
	url  string
}

// startNode runs the Express app on the database.
func startNode(t *testing.T, db *store.DB, s setup) *server {
	t.Helper()
	cmd := exec.Command("node", "testdata/interop/app.js")
	dir := t.TempDir()
	cmd.Env = append(os.Environ(),
		"NODE_ENV=production", "DATA_DIR="+dir, "KEYS_PATH="+dir,
		"SESSION_SECRET="+s.secret, "HTTPS_ENABLED="+fmt.Sprint(s.https), "TRUST_PROXY="+s.trustProxy)
	if db.IsPostgres() {
		cmd.Env = append(cmd.Env, "DB_TYPE=postgres", "DATABASE_URL="+postgresURL(t, db))
	} else {
		cmd.Env = append(cmd.Env, "DB_TYPE=sqlite", "DB_PATH="+sqlitePath(t, db))
	}
	for key, value := range s.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start node: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("node stderr:\n%s", stderr.String())
		}
	})
	port := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if rest, ok := strings.CutPrefix(scanner.Text(), "LISTENING "); ok {
				port <- rest
			}
		}
		close(port)
	}()
	select {
	case p, ok := <-port:
		if !ok {
			t.Fatalf("node stopped before it listened:\n%s", stderr.String())
		}
		return &server{name: "node", url: "http://127.0.0.1:" + p}
	case <-time.After(30 * time.Second):
		t.Fatalf("node did not listen within 30 s:\n%s", stderr.String())
	}
	return nil
}

// sqlitePath is the file of a SQLite test database.
func sqlitePath(t *testing.T, db *store.DB) string {
	t.Helper()
	var seq int
	var name, file string
	if err := db.QueryRow(context.Background(), `PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// postgresURL is the address of a PostgreSQL test database for Node: the
// test server, with the test's own schema as the search path.
func postgresURL(t *testing.T, db *store.DB) string {
	t.Helper()
	var schema string
	if err := db.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv(storetest.PostgresEnv)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "options=" + url.QueryEscape("-c search_path="+schema)
}

// audit is an audit entry either server wrote.
type audit struct {
	UserID  int64  `json:"userId"`
	Action  string `json:"action"`
	Details string `json:"details"`
	IP      string `json:"ip"`
}

type goServer struct {
	*server
	m      *session.Manager
	mu     sync.Mutex
	audits []audit
}

// startGo serves the same routes as the Express app from the Go packages.
func startGo(t *testing.T, db *store.DB, s setup) *goServer {
	t.Helper()
	trust, err := httpx.ParseTrustProxy(s.trustProxy)
	if err != nil {
		t.Fatal(err)
	}
	g := &goServer{}
	m, err := session.New(context.Background(), session.Options{
		DB: db, Secret: s.secret, HTTPS: s.https, Policy: session.PolicyFromEnv(s.getenv), TrustProxy: trust,
		Audit: func(_ context.Context, userID int64, action, details, ip string) {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.audits = append(g.audits, audit{userID, action, details, ip})
		},
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	g.m = m
	protector, err := csrf.New(csrf.Options{
		Secret: s.secret, HTTPS: s.https,
		SignedIn: func(r *http.Request) bool { return session.From(r).UserID() != 0 },
		OnError: func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, r, http.StatusForbidden, map[string]any{"success": false, "error": "csrf"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Routes before the CSRF gate
	open := http.NewServeMux()
	whoami := func(w http.ResponseWriter, r *http.Request) {
		sess := session.From(r)
		fields := map[string]json.RawMessage{}
		for _, key := range strings.Split(r.URL.Query().Get("keys"), ",") {
			var raw json.RawMessage
			if key != "" && sess.Get(key, &raw) {
				fields[key] = raw
			}
		}
		var userID any
		if id := sess.UserID(); id != 0 {
			userID = id
		}
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"sid": sess.ID(), "userId": userID, "user": sess.User(), "session": fields})
	}
	open.HandleFunc("GET /t/whoami", whoami)
	open.HandleFunc("GET /api/t/whoami", whoami)
	open.HandleFunc("POST /t/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			User struct {
				ID       int64   `json:"id"`
				Username string  `json:"username"`
				Role     string  `json:"role"`
				Language *string `json:"preferred_language"`
			} `json:"user"`
			Fingerprint string `json:"clientFingerprint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		meta := m.ClientMeta(r, body.Fingerprint)
		sess := session.From(r)
		if err := sess.Regenerate(); err != nil {
			httpx.WriteJSON(w, r, http.StatusInternalServerError, map[string]any{"success": false, "error": "Server error"})
			return
		}
		user := session.User{ID: body.User.ID, Username: body.User.Username, Role: body.User.Role}
		if body.User.Language != nil {
			user.PreferredLanguage = *body.User.Language
		}
		replaced, err := sess.Establish(user, meta)
		if err != nil {
			httpx.WriteJSON(w, r, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"success": true, "sid": sess.ID(), "replaced": replaced})
	})
	open.HandleFunc("POST /t/set", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sess := session.From(r)
		sess.Set(body.Key, body.Value)
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"success": true, "sid": sess.ID()})
	})
	open.HandleFunc("POST /t/logout", func(w http.ResponseWriter, r *http.Request) {
		sess := session.From(r)
		sess.End(session.ReasonLogout)
		_ = sess.Destroy()
		sess.ClearCookies(w)
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"success": true})
	})
	open.HandleFunc("GET /t/logout", func(w http.ResponseWriter, r *http.Request) {
		sess := session.From(r)
		sess.End(session.ReasonLogout)
		_ = sess.Destroy()
		sess.ClearCookies(w)
		w.Header().Set("Location", "/login")
		w.WriteHeader(http.StatusFound)
	})
	open.HandleFunc("GET /t/sessions", func(w http.ResponseWriter, r *http.Request) {
		sess := session.From(r)
		list, err := m.List(r.Context(), sess.UserID(), sess.ID())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"sessions": list, "policy": m.Policy()})
	})
	open.HandleFunc("GET /t/csrf", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"token": csrf.Token(r)})
	})
	open.HandleFunc("GET /t/audits", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		httpx.WriteJSON(w, r, http.StatusOK, append([]audit{}, g.audits...))
	})

	// Routes behind it
	gated := http.NewServeMux()
	written := func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"success": true})
	}
	gated.HandleFunc("POST /t/write", written)
	gated.HandleFunc("POST /api/bd/t/write", written)
	gate := protector.Gate(gated)

	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, pattern := open.Handler(r); pattern != "" {
			h.ServeHTTP(w, r)
			return
		}
		gate.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(m.Middleware(protector.Provider(routes)))
	t.Cleanup(srv.Close)
	g.server = &server{name: "go", url: srv.URL}
	return g
}

// browser keeps cookies the way a browser does, and sends them to
// whichever server it is pointed at.
type browser struct {
	t       *testing.T
	cookies map[string]string
	headers map[string]string
}

func newBrowser(t *testing.T) *browser {
	return &browser{t: t, cookies: map[string]string{}, headers: map[string]string{"User-Agent": "Interop/1.0", "Accept": "*/*"}}
}

// copy is a second browser with the same cookies: what an attacker with a
// stolen cookie, or the same browser's other tab, would send.
func (b *browser) copy() *browser {
	c := newBrowser(b.t)
	for k, v := range b.cookies {
		c.cookies[k] = v
	}
	for k, v := range b.headers {
		c.headers[k] = v
	}
	return c
}

type reply struct {
	status     int
	header     http.Header
	body       []byte
	setCookies []string
}

func (r *reply) json(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("answer is not a JSON object: %v\n%s", err, r.body)
	}
	return v
}

var noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       30 * time.Second,
}

func (b *browser) do(s *server, method, path string, body any, headers ...string) *reply {
	b.t.Helper()
	var reader io.Reader
	contentType := ""
	switch x := body.(type) {
	case nil:
	case string:
		reader, contentType = strings.NewReader(x), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			b.t.Fatal(err)
		}
		reader, contentType = bytes.NewReader(raw), "application/json"
	}
	req, err := http.NewRequest(method, s.url+path, reader)
	if err != nil {
		b.t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range b.headers {
		req.Header.Set(k, v)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	names := make([]string, 0, len(b.cookies))
	for name := range b.cookies {
		names = append(names, name)
	}
	sort.Strings(names)
	var pairs []string
	for _, name := range names {
		pairs = append(pairs, name+"="+b.cookies[name])
	}
	if len(pairs) > 0 {
		req.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	res, err := noRedirects.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s on %s: %v", method, path, s.name, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	out := &reply{status: res.StatusCode, header: res.Header, body: raw, setCookies: res.Header.Values("Set-Cookie")}
	for _, line := range out.setCookies {
		pair, attrs, _ := strings.Cut(line, ";")
		name, value, _ := strings.Cut(pair, "=")
		if value == "" || strings.Contains(attrs, "Expires=Thu, 01 Jan 1970") {
			delete(b.cookies, name)
		} else {
			b.cookies[name] = value
		}
	}
	return out
}

// who is what a server says about the browser's session.
type who struct {
	sid     string
	userID  string // the JSON text of userId: a number, or "null"
	user    map[string]any
	session map[string]any
}

func (b *browser) whoami(s *server, keys ...string) who {
	b.t.Helper()
	r := b.do(s, "GET", "/t/whoami?keys="+strings.Join(keys, ","), nil)
	if r.status != http.StatusOK {
		b.t.Fatalf("whoami on %s: %d %s", s.name, r.status, r.body)
	}
	v := r.json(b.t)
	w := who{sid: v["sid"].(string), userID: fmt.Sprint(v["userId"])}
	if v["userId"] == nil {
		w.userID = "null"
	}
	w.user, _ = v["user"].(map[string]any)
	w.session, _ = v["session"].(map[string]any)
	return w
}

var aliceUser = map[string]any{"id": 7, "username": "alice", "role": "operator", "preferred_language": nil}

func (b *browser) login(s *server, user map[string]any) map[string]any {
	b.t.Helper()
	r := b.do(s, "POST", "/t/login", map[string]any{"user": user, "clientFingerprint": "fp_abcdef123456"})
	if r.status != http.StatusOK {
		b.t.Fatalf("login on %s: %d %s", s.name, r.status, r.body)
	}
	return r.json(b.t)
}

// wantUser checks that both servers see the browser signed in as alice, or
// both as nobody.
func (b *browser) wantUser(signedIn bool, servers ...*server) {
	b.t.Helper()
	for _, s := range servers {
		w := b.copy().whoami(s)
		if signedIn && (w.userID != "7" || fmt.Sprint(w.user["username"]) != "alice") {
			b.t.Fatalf("%s sees user %s %v, want alice (7)", s.name, w.userID, w.user)
		}
		if !signedIn && (w.userID != "null" || w.user != nil) {
			b.t.Fatalf("%s sees user %s %v, want nobody", s.name, w.userID, w.user)
		}
	}
}

// row is a row of panel_sessions.
type row struct {
	json      string
	userID    *int64
	username  *string
	ip        *string
	userAgent *string
	fp        *string
	reason    *string
	created   *int64
	last      *int64
	ended     *int64
	active    bool
}

func readRow(t *testing.T, db *store.DB, sid string) row {
	t.Helper()
	var r row
	var active int
	err := db.QueryRow(context.Background(),
		`SELECT session_json, user_id, username, ip_address, user_agent, client_fingerprint, end_reason,
		        created_at, last_activity_at, ended_at, `+db.Pick("CASE WHEN is_active = 1 THEN 1 ELSE 0 END", "CASE WHEN is_active THEN 1 ELSE 0 END")+`
		 FROM panel_sessions WHERE sid = ?`, sid,
	).Scan(&r.json, &r.userID, &r.username, &r.ip, &r.userAgent, &r.fp, &r.reason, &r.created, &r.last, &r.ended, &active)
	if err != nil {
		t.Fatalf("row %s: %v", sid, err)
	}
	r.active = active == 1
	return r
}

func str(p *string) string {
	if p == nil {
		return "<null>"
	}
	return *p
}

func exec1(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// keyOrder lists the keys of a JSON object in written order.
func keyOrder(t *testing.T, text string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(text))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("%v in %s", err, text)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

var expiresPattern = regexp.MustCompile(`; Expires=([^;]+)`)

// splitExpires takes the Expires attribute out of a Set-Cookie header.
func splitExpires(t *testing.T, header string) (rest string, expires time.Time) {
	t.Helper()
	m := expiresPattern.FindStringSubmatch(header)
	if m == nil {
		t.Fatalf("no Expires in %q", header)
	}
	expires, err := time.Parse(http.TimeFormat, m[1])
	if err != nil {
		t.Fatalf("Expires %q: %v", m[1], err)
	}
	return strings.Replace(header, m[0], "", 1), expires
}

func sessionSetCookie(t *testing.T, r *reply, name string) string {
	t.Helper()
	for _, line := range r.setCookies {
		if strings.HasPrefix(line, name+"=") {
			return line
		}
	}
	t.Fatalf("no %s cookie in %q", name, r.setCookies)
	return ""
}

// both starts the two servers on the database, in the order given: the
// first one creates the table, the second finds it.
func both(t *testing.T, db *store.DB, s setup, nodeFirst bool) (*goServer, *server) {
	t.Helper()
	if nodeFirst {
		node := startNode(t, db, s)
		return startGo(t, db, s), node
	}
	g := startGo(t, db, s)
	return g, startNode(t, db, s)
}

// Scenario 1: Node signs in, Go continues the session.
func TestInteropNodeSignsInGoContinues(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		b := newBrowser(t)
		user := map[string]any{"id": 7, "username": "alice", "role": "operator", "preferred_language": "de"}
		sid := b.login(node, user)["sid"].(string)
		nodeRow := readRow(t, db, sid)

		time.Sleep(20 * time.Millisecond)
		r := b.do(g.server, "GET", "/t/whoami?keys=createdAt,lastActivityAt,loginIp,loginUserAgent,clientFingerprint", nil)
		v := r.json(t)
		wantUser := map[string]any{"id": json.Number("7"), "username": "alice", "role": "operator", "preferred_language": "de"}
		if v["sid"] != sid || fmt.Sprint(v["userId"]) != "7" || !reflect.DeepEqual(v["user"], wantUser) {
			t.Fatalf("Go sees sid %v, user %v %v; Node signed in %s as alice", v["sid"], v["userId"], v["user"], sid)
		}
		fields := v["session"].(map[string]any)
		if fmt.Sprint(fields["createdAt"]) != fmt.Sprint(*nodeRow.created) || fields["loginUserAgent"] != "Interop/1.0" ||
			fields["clientFingerprint"] != "fp_abcdef123456" || fields["loginIp"] != "127.0.0.1" {
			t.Fatalf("Go reads fields %v", fields)
		}

		// Go sent the cookie again, as Node does on its next answer
		goCookie := sessionSetCookie(t, r, "bd.sid")
		nodeCookie := sessionSetCookie(t, b.do(node, "GET", "/t/whoami", nil), "bd.sid")
		goRest, goExpires := splitExpires(t, goCookie)
		nodeRest, nodeExpires := splitExpires(t, nodeCookie)
		if goRest != nodeRest {
			t.Fatalf("Set-Cookie differs:\n  Go %s\nNode %s", goCookie, nodeCookie)
		}
		wantExpires := time.Now().Add(30 * time.Minute)
		for name, got := range map[string]time.Time{"Go": goExpires, "Node": nodeExpires} {
			if d := got.Sub(wantExpires); d < -5*time.Second || d > 5*time.Second {
				t.Fatalf("%s: Expires %v, want about %v", name, got, wantExpires)
			}
		}

		// The idle rules: a request on Go counts as activity, in the row and
		// in the JSON, and Node reads what Go wrote
		time.Sleep(20 * time.Millisecond)
		b.do(g.server, "GET", "/t/whoami", nil)
		goRow := readRow(t, db, sid)
		if *goRow.last <= *nodeRow.last || !strings.Contains(goRow.json, fmt.Sprintf(`"lastActivityAt":%d`, *goRow.last)) {
			t.Fatalf("after a request on Go: last_activity_at %d (was %d), JSON %s", *goRow.last, *nodeRow.last, goRow.json)
		}
		if *goRow.created != *nodeRow.created || *goRow.userID != 7 || str(goRow.username) != "alice" ||
			str(goRow.userAgent) != "Interop/1.0" || str(goRow.fp) != "fp_abcdef123456" || !goRow.active {
			t.Fatalf("the row's columns changed under Go: %+v", goRow)
		}
		if got, want := keyOrder(t, goRow.json), keyOrder(t, nodeRow.json); !reflect.DeepEqual(got, want) {
			t.Fatalf("Go wrote the fields in the order %v, Node in %v", got, want)
		}
		// A background request on Go does not count
		b.do(g.server, "GET", "/t/whoami", nil, "X-BD-Background", "1")
		if after := readRow(t, db, sid); *after.last != *goRow.last || after.json != goRow.json {
			t.Fatalf("a background request on Go counted as activity")
		}
		w := b.whoami(node)
		cookie := w.session["cookie"].(map[string]any)
		if w.userID != "7" || fmt.Sprint(cookie["originalMaxAge"]) != "1800000" || w.user["preferred_language"] != "de" {
			t.Fatalf("Node after Go's writes: user %s, session %v", w.userID, w.session)
		}
		if _, err := time.Parse("2006-01-02T15:04:05.000Z", fmt.Sprint(cookie["expires"])); err != nil {
			t.Fatalf("cookie.expires %v is not the ISO form Node writes", cookie["expires"])
		}
	})
}

// Scenario 2: Go signs in, Node continues the session and writes a field.
func TestInteropGoSignsInNodeContinues(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), false)
		b := newBrowser(t)
		sid := b.login(g.server, aliceUser)["sid"].(string)
		goRow := readRow(t, db, sid)

		time.Sleep(20 * time.Millisecond)
		w := b.whoami(node)
		wantUser := map[string]any{"id": json.Number("7"), "username": "alice", "role": "operator", "preferred_language": nil}
		if w.sid != sid || w.userID != "7" || !reflect.DeepEqual(w.user, wantUser) {
			t.Fatalf("Node sees sid %s, userId %s, user %v", w.sid, w.userID, w.user)
		}
		if got := w.session["userId"]; got != json.Number("7") {
			t.Fatalf("req.session.userId is %#v on Node, want the number 7", got)
		}
		// touchActivity passed: Node answered signed in and moved the activity
		if r := b.do(node, "GET", "/api/t/whoami", nil); r.status != http.StatusOK {
			t.Fatalf("Node refused Go's session: %d %s", r.status, r.body)
		}
		nodeRow := readRow(t, db, sid)
		if *nodeRow.last <= *goRow.last || !nodeRow.active || *nodeRow.created != *goRow.created {
			t.Fatalf("after requests on Node: %+v (Go wrote last activity %d)", nodeRow, *goRow.last)
		}
		if got, want := keyOrder(t, nodeRow.json), keyOrder(t, goRow.json); !reflect.DeepEqual(got, want) {
			t.Fatalf("Node wrote the fields in the order %v, Go in %v", got, want)
		}

		// A Node route writes a field; Go reads it back
		if r := b.do(node, "POST", "/t/set", map[string]any{"key": "x", "value": 1}); r.status != http.StatusOK {
			t.Fatalf("set on Node: %d %s", r.status, r.body)
		}
		gw := b.whoami(g.server, "x", "createdAt")
		if gw.userID != "7" || gw.session["x"] != json.Number("1") || fmt.Sprint(gw.session["createdAt"]) != fmt.Sprint(*goRow.created) {
			t.Fatalf("Go after Node's write: user %s, fields %v", gw.userID, gw.session)
		}
		final := readRow(t, db, sid)
		if !final.active || !strings.Contains(final.json, `"x":1`) || *final.userID != 7 {
			t.Fatalf("row: %+v", final)
		}
		var rows int
		if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM panel_sessions`).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("%d rows (%v), want the one session", rows, err)
		}
	})
}

// A session before sign-in, as the one that waits for the second factor:
// one server starts it, the other continues it and renews its cookie.
func TestInteropSessionBeforeSignIn(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), false)
		for _, order := range [][2]*server{{g.server, node}, {node, g.server}} {
			starter, other := order[0], order[1]
			b := newBrowser(t)
			pending := map[string]any{"id": 7, "username": "alice", "totpRequired": true, "goPartialToken": nil}
			first := b.do(starter, "POST", "/t/set", map[string]any{"key": "pendingTotpUser", "value": pending})
			sid := first.json(t)["sid"].(string)
			startRest, _ := splitExpires(t, sessionSetCookie(t, first, "bd.sid"))

			r := b.do(other, "GET", "/t/whoami?keys=pendingTotpUser", nil)
			v := r.json(t)
			want := map[string]any{"id": json.Number("7"), "username": "alice", "totpRequired": true, "goPartialToken": nil}
			if v["sid"] != sid || v["userId"] != nil || !reflect.DeepEqual(v["session"].(map[string]any)["pendingTotpUser"], want) {
				t.Fatalf("%s continues the session %s started as %s", other.name, starter.name, r.body)
			}
			otherRest, expires := splitExpires(t, sessionSetCookie(t, r, "bd.sid"))
			if otherRest != startRest {
				t.Fatalf("Set-Cookie differs:\n%s: %s\n%s: %s", starter.name, startRest, other.name, otherRest)
			}
			if d := time.Until(expires) - 30*time.Minute; d < -5*time.Second || d > 5*time.Second {
				t.Fatalf("%s renews the cookie until %v, want 30 minutes from now", other.name, expires)
			}
			stored := readRow(t, db, sid)
			if !stored.active || stored.userID != nil || !strings.Contains(stored.json, `"originalMaxAge":1800000,`) {
				t.Fatalf("row: %+v", stored)
			}

			// The other server adds a field, the first reads both
			b.do(other, "POST", "/t/set", map[string]any{"key": "pendingTotpUserId", "value": 7})
			back := b.whoami(starter, "pendingTotpUser", "pendingTotpUserId")
			if back.sid != sid || back.session["pendingTotpUserId"] != json.Number("7") || !reflect.DeepEqual(back.session["pendingTotpUser"], want) {
				t.Fatalf("%s after %s wrote: %v", starter.name, other.name, back.session)
			}
		}
	})
}

// Scenario 3: ten alternating requests, each changing a field.
func TestInteropPingPong(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		b := newBrowser(t)
		sid := b.login(g.server, aliceUser)["sid"].(string)
		values := []string{`1`, `"text"`, `{"a":[1,2,{"b":null}],"z":"y"}`, `true`, `1.5`, `"ünï©ødé ☃ \"q\" <&>"`, `[]`, `null`, `12345678901`, `""`}
		var keys []string
		for i, value := range values {
			side := g.server
			if i%2 == 1 {
				side = node
			}
			key := fmt.Sprintf("f%d", i)
			keys = append(keys, key)
			if r := b.do(side, "POST", "/t/set", map[string]any{"key": key, "value": json.RawMessage(value)}); r.status != http.StatusOK {
				t.Fatalf("request %d on %s: %d %s", i, side.name, r.status, r.body)
			}
			if got := b.whoami(side).sid; got != sid {
				t.Fatalf("request %d on %s: session id %s, want %s", i, side.name, got, sid)
			}
		}
		for _, side := range []*server{g.server, node} {
			w := b.whoami(side, keys...)
			if w.userID != "7" {
				t.Fatalf("%s: user %s", side.name, w.userID)
			}
			for i, value := range values {
				var want any
				dec := json.NewDecoder(strings.NewReader(value))
				dec.UseNumber()
				if err := dec.Decode(&want); err != nil {
					t.Fatal(err)
				}
				got, there := w.session[keys[i]]
				if !there || !reflect.DeepEqual(got, want) {
					t.Errorf("%s: field %s is %#v (there: %v), want %s", side.name, keys[i], got, there, value)
				}
			}
		}
		var active, total int
		if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM panel_sessions WHERE is_active = `+db.Pick("1", "TRUE")).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM panel_sessions`).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if active != 1 || total != 1 {
			t.Fatalf("%d active rows of %d, want one row", active, total)
		}
	})
}

// Scenario 4: the per-account limit across both servers.
func TestInteropLimitAcrossServers(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), false)
		for _, order := range [][2]*server{{node, g.server}, {g.server, node}} {
			first, second := order[0], order[1]
			a := newBrowser(t)
			sidA := a.login(first, aliceUser)["sid"].(string)
			a.wantUser(true, g.server, node)
			time.Sleep(10 * time.Millisecond)

			b := newBrowser(t)
			b.headers["User-Agent"] = "Second/2.0"
			answer := b.login(second, aliceUser)
			if fmt.Sprint(answer["replaced"]) != "1" {
				t.Fatalf("sign-in on %s after one on %s replaced %v sessions, want 1", second.name, first.name, answer["replaced"])
			}
			old := readRow(t, db, sidA)
			if old.active || str(old.reason) != "replaced" || old.ended == nil {
				t.Fatalf("the session %s created, after a sign-in on %s: %+v", first.name, second.name, old)
			}
			if !strings.Contains(old.json, `"userId":7`) {
				t.Fatalf("a replaced session keeps its JSON, got %s", old.json)
			}
			a.wantUser(false, g.server, node)
			b.wantUser(true, g.server, node)
			if now := readRow(t, db, answer["sid"].(string)); !now.active || str(now.userAgent) != "Second/2.0" {
				t.Fatalf("the new session: %+v", now)
			}
			// Another account is left alone
			c := newBrowser(t)
			c.login(first, map[string]any{"id": 8, "username": "bob", "role": "viewer", "preferred_language": nil})
			b.wantUser(true, g.server, node)
		}
	})
}

// age moves a session's times in its row into the past.
func age(t *testing.T, db *store.DB, sid, column string, by time.Duration) {
	t.Helper()
	exec1(t, db, `UPDATE panel_sessions SET `+column+` = ? WHERE sid = ?`, time.Now().Add(-by).UnixMilli(), sid)
}

var lastActivityPattern = regexp.MustCompile(`"lastActivityAt":\d+`)

// ageJSON moves the last activity inside the stored session only, so the
// row passes and the session's own check decides.
func ageJSON(t *testing.T, db *store.DB, sid string, by time.Duration) {
	t.Helper()
	r := readRow(t, db, sid)
	text := lastActivityPattern.ReplaceAllString(r.json, fmt.Sprintf(`"lastActivityAt":%d`, time.Now().Add(-by).UnixMilli()))
	if text == r.json {
		t.Fatalf("no lastActivityAt in %s", r.json)
	}
	exec1(t, db, `UPDATE panel_sessions SET session_json = ? WHERE sid = ?`, text, sid)
}

// Scenario 5: idle and absolute expiry decided by one side is seen by the
// other. The shortest lifetimes the settings allow are a minute and five
// minutes, so the rows' times are moved instead of waiting.
func TestInteropExpiryDecidedByOneSide(t *testing.T) {
	cases := []struct {
		name, column string
		by           time.Duration
		reason       string
	}{
		{"idle", "last_activity_at", 61 * time.Second, "idle"},
		{"absolute", "created_at", 301 * time.Second, "max_age"},
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, shortLives(), true)
		for _, tc := range cases {
			for _, order := range [][2]*server{{g.server, node}, {node, g.server}} {
				decider, other := order[0], order[1]
				t.Run(tc.name+" decided by "+decider.name, func(t *testing.T) {
					b := newBrowser(t)
					sid := b.login(other, aliceUser)["sid"].(string)
					b.wantUser(true, decider, other)
					age(t, db, sid, tc.column, tc.by)

					// The store ends it on the way in. Node goes on signed
					// out and nothing clears the cookie; Go answers the
					// request as an ended session, clears the cookie and
					// empties the row (BUGS.md S7)
					probe := b.copy()
					r := probe.do(decider, "GET", "/api/t/whoami", nil)
					cleared := strings.Contains(strings.Join(r.setCookies, "\n"), "bd.sid=;")
					if decider == g.server {
						code := map[string]string{"idle": "session_idle", "max_age": "session_expired"}[tc.reason]
						if r.status != http.StatusUnauthorized || !strings.Contains(string(r.body), `"code":"`+code+`"`) || !cleared {
							t.Fatalf("%s: %d %s, Set-Cookie %q", decider.name, r.status, r.body, r.setCookies)
						}
					} else if r.status != http.StatusOK || r.json(t)["userId"] != nil || strings.Contains(strings.Join(r.setCookies, "\n"), "bd.sid=") {
						t.Fatalf("%s: %d %s, Set-Cookie %q", decider.name, r.status, r.body, r.setCookies)
					}
					ended := readRow(t, db, sid)
					if ended.active || str(ended.reason) != tc.reason || ended.ended == nil || (ended.json == "{}") != (decider == g.server) {
						t.Fatalf("row after %s decided: %+v", decider.name, ended)
					}
					b.wantUser(false, other, decider)
					if after := readRow(t, db, sid); str(after.reason) != tc.reason || *after.ended != *ended.ended {
						t.Fatalf("%s changed the ended row: %+v", other.name, after)
					}
				})
			}
		}
	})
}

// answerOf is what a client can see of an answer, without the parts that
// differ from request to request.
func answerOf(r *reply) string {
	return fmt.Sprintf("%d\nContent-Type: %s\nContent-Length: %s\nETag: %s\nLocation: %s\nVary: %s\nSet-Cookie: %s\n\n%s",
		r.status, r.header.Get("Content-Type"), r.header.Get("Content-Length"), r.header.Get("ETag"),
		r.header.Get("Location"), r.header.Get("Vary"), strings.Join(r.setCookies, "\nSet-Cookie: "), r.body)
}

// Scenario 5, second half: the session's own check (the middleware after
// the store) answers the request itself. Both servers must give the same
// answer, and the other side must see the session gone.
func TestInteropExpiryAnsweredByTheMiddleware(t *testing.T) {
	requests := []struct {
		name, path string
		headers    []string
	}{
		{"API", "/api/t/whoami", nil},
		{"page", "/t/whoami?keys=a", []string{"Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"}},
		{"page for a client that takes anything", "/t/whoami", nil},
		{"JSON by Accept", "/t/whoami", []string{"Accept", "application/json"}},
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, shortLives(), false)
		for _, rq := range requests {
			t.Run(rq.name, func(t *testing.T) {
				answers := map[string]string{}
				for _, order := range [][2]*server{{g.server, node}, {node, g.server}} {
					decider, other := order[0], order[1]
					b := newBrowser(t)
					sid := b.login(other, aliceUser)["sid"].(string)
					ageJSON(t, db, sid, 61*time.Second)
					keep := b.copy()
					r := b.do(decider, "GET", rq.path, nil, rq.headers...)
					answers[decider.name] = answerOf(r)
					ended := readRow(t, db, sid)
					if ended.active || str(ended.reason) != "idle" || ended.json != "{}" {
						t.Fatalf("row after %s answered: %+v", decider.name, ended)
					}
					if len(b.cookies["bd.sid"]) != 0 {
						t.Fatalf("%s did not clear the session cookie: %q", decider.name, r.setCookies)
					}
					keep.wantUser(false, other, decider)

					audits := newBrowser(t).do(decider, "GET", "/t/audits", nil)
					var list []audit
					if err := json.Unmarshal(audits.body, &list); err != nil {
						t.Fatal(err)
					}
					last := list[len(list)-1]
					if last.UserID != 7 || last.Action != "session_expired" || last.Details != "reason=idle" || !strings.HasSuffix(last.IP, "127.0.0.1") {
						t.Fatalf("%s wrote the audit entry %+v", decider.name, last)
					}
				}
				if answers["go"] != answers["node"] {
					t.Fatalf("the answers differ.\n--- Go:\n%s\n--- Node:\n%s", answers["go"], answers["node"])
				}
				if !strings.HasPrefix(answers["go"], "401") && !strings.HasPrefix(answers["go"], "302") {
					t.Fatalf("answer:\n%s", answers["go"])
				}
			})
		}
	})
}

// Scenario 6: sign-out on one side signs out on the other.
func TestInteropSignOut(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		for _, route := range []string{"POST /t/logout", "GET /t/logout"} {
			method, path, _ := strings.Cut(route, " ")
			answers := map[string]string{}
			for _, order := range [][2]*server{{g.server, node}, {node, g.server}} {
				outOn, inOn := order[0], order[1]
				b := newBrowser(t)
				sid := b.login(inOn, aliceUser)["sid"].(string)
				stolen := b.copy()
				b.wantUser(true, outOn, inOn)

				var body any
				if method == "POST" {
					body = map[string]any{}
				}
				r := b.do(outOn, method, path, body)
				// Without the token cookie a GET also sets, which is random,
				// and without the clearing header for a cookie called
				// "undefined" that Node sent and the Go console does not
				// (BUGS.md S1)
				var cleared []string
				for _, line := range r.setCookies {
					if !strings.HasPrefix(line, "__csrf") && !(outOn == node && strings.HasPrefix(line, "undefined=")) {
						cleared = append(cleared, line)
					}
				}
				if len(cleared) < 2 {
					t.Fatalf("%s on %s: Set-Cookie %q", route, outOn.name, r.setCookies)
				}
				answers[outOn.name] = fmt.Sprintf("%d %s\n%s", r.status, r.header.Get("Location"), strings.Join(cleared, "\n"))
				gone := readRow(t, db, sid)
				if gone.active || str(gone.reason) != "logout" || gone.json != "{}" || gone.ended == nil {
					t.Fatalf("%s on %s: row %+v", route, outOn.name, gone)
				}
				if *gone.userID != 7 || str(gone.username) != "alice" {
					t.Fatalf("the ended row lost its account: %+v", gone)
				}
				// The cookie from before sign-out is worth nothing on either side
				stolen.wantUser(false, inOn, outOn)
				if len(b.cookies["bd.sid"]) != 0 {
					t.Fatalf("%s on %s left the cookie: %q", route, outOn.name, r.setCookies)
				}
			}
			if answers["go"] != answers["node"] {
				t.Fatalf("%s answers differ.\n--- Go:\n%s\n--- Node:\n%s", route, answers["go"], answers["node"])
			}
		}
	})
}

// csrfToken asks a server for a token; the browser keeps the cookie.
func (b *browser) csrfToken(s *server) (token string, r *reply) {
	b.t.Helper()
	r = b.do(s, "GET", "/t/csrf", nil)
	token, _ = r.json(b.t)["token"].(string)
	if len(token) != 128 {
		b.t.Fatalf("%s handed out the token %q", s.name, token)
	}
	return token, r
}

func (b *browser) write(s *server, path string, body any, headers ...string) int {
	b.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	return b.do(s, "POST", path, body, headers...).status
}

// Scenario 7: CSRF tokens across both servers.
func TestInteropCSRF(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		for _, order := range [][2]*server{{node, g.server}, {g.server, node}} {
			giver, taker := order[0], order[1]
			b := newBrowser(t)
			token, first := b.csrfToken(giver)
			if len(first.setCookies) != 1 || !strings.HasPrefix(first.setCookies[0], "__csrf_h=") {
				t.Fatalf("%s set %q", giver.name, first.setCookies)
			}
			// The other server takes the pair for a write ...
			if got := b.write(taker, "/t/write", nil, "X-CSRF-Token", token); got != http.StatusOK {
				t.Fatalf("a token from %s was refused by %s: %d", giver.name, taker.name, got)
			}
			// ... in the body too
			if got := b.write(taker, "/t/write", map[string]any{"_csrf": token}); got != http.StatusOK {
				t.Fatalf("a token from %s in a JSON body was refused by %s: %d", giver.name, taker.name, got)
			}
			if got := b.write(taker, "/t/write", "a=1&_csrf="+token); got != http.StatusOK {
				t.Fatalf("a token from %s in a form body was refused by %s: %d", giver.name, taker.name, got)
			}
			// ... and hands the same token out again, with the same cookie text
			again, second := b.csrfToken(taker)
			if again != token || !reflect.DeepEqual(second.setCookies, first.setCookies) {
				t.Fatalf("%s gave %q with %q; %s gave %q with %q", giver.name, token, first.setCookies, taker.name, again, second.setCookies)
			}
			for _, s := range []*server{giver, taker} {
				if got := b.write(s, "/t/write", nil); got != http.StatusForbidden {
					t.Fatalf("%s took a write without a token: %d", s.name, got)
				}
				if got := b.write(s, "/t/write", nil, "X-CSRF-Token", strings.Repeat("0", 128)); got != http.StatusForbidden {
					t.Fatalf("%s took a write with a wrong token: %d", s.name, got)
				}
				if got := newBrowser(t).write(s, "/t/write", nil, "X-CSRF-Token", token); got != http.StatusForbidden {
					t.Fatalf("%s took a token without its cookie: %d", s.name, got)
				}
			}
		}

		// A pair made with another secret fails on both, and both replace
		// the cookie in the same way
		foreign, err := csrf.New(csrf.Options{Secret: "another-secret", SignedIn: func(*http.Request) bool { return false }})
		if err != nil {
			t.Fatal(err)
		}
		var foreignToken string
		rec := httptest.NewRecorder()
		foreign.Provider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { foreignToken = csrf.Token(r) })).
			ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		foreignCookie := strings.TrimPrefix(strings.Split(rec.Header().Get("Set-Cookie"), ";")[0], "__csrf_h=")
		for _, s := range []*server{g.server, node} {
			b := newBrowser(t)
			b.cookies["__csrf_h"] = foreignCookie
			if got := b.write(s, "/t/write", nil, "X-CSRF-Token", foreignToken); got != http.StatusForbidden {
				t.Fatalf("%s took a pair made with another secret: %d", s.name, got)
			}
			token, r := b.csrfToken(s)
			if token == foreignToken || len(r.setCookies) != 2 ||
				r.setCookies[0] != "__csrf_h=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax" {
				t.Fatalf("%s on a foreign cookie: token reused %v, Set-Cookie %q", s.name, token == foreignToken, r.setCookies)
			}
		}

		// The Node console's cookie parser reads a value j:<JSON> as the
		// JSON's value: text counts as the cookie, anything else as none
		pairBrowser := newBrowser(t)
		token, _ := pairBrowser.csrfToken(node)
		pair, err := url.PathUnescape(pairBrowser.cookies["__csrf_h"])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []*server{g.server, node} {
			b := newBrowser(t)
			b.cookies["__csrf_h"] = "j%3A%22" + url.QueryEscape(pair) + "%22"
			if got := b.write(s, "/t/write", nil, "X-CSRF-Token", token); got != http.StatusOK {
				t.Fatalf("%s refused the pair wrapped as JSON text: %d", s.name, got)
			}
			if again, r := b.csrfToken(s); again != token || len(r.setCookies) != 1 {
				t.Fatalf("%s on the pair wrapped as JSON text: same token %v, Set-Cookie %q", s.name, again == token, r.setCookies)
			}
			for _, value := range []string{"j%3A%7B%7D", "j%3A5", "j%3Atrue"} {
				b := newBrowser(t)
				b.cookies["__csrf_h"] = value
				if _, r := b.csrfToken(s); len(r.setCookies) != 1 {
					t.Fatalf("%s on the cookie %s: Set-Cookie %q, want a new pair and no clearing header", s.name, value, r.setCookies)
				}
			}
			for _, value := range []string{"j%3A%7B", "j%3Anull", "j%3A0", "j%3A%22%22", "garbage"} {
				b := newBrowser(t)
				b.cookies["__csrf_h"] = value
				if _, r := b.csrfToken(s); len(r.setCookies) != 2 {
					t.Fatalf("%s on the cookie %s: Set-Cookie %q, want a clearing header and a new pair", s.name, value, r.setCookies)
				}
			}
		}

		// The device API exception: without a session no token is asked for
		// under /api/bd/, with a session it is
		for _, s := range []*server{g.server, node} {
			if got := newBrowser(t).write(s, "/api/bd/t/write", nil); got != http.StatusOK {
				t.Fatalf("%s: a device request without a session got %d", s.name, got)
			}
			b := newBrowser(t)
			b.login(s, aliceUser)
			if got := b.write(s, "/api/bd/t/write", nil); got != http.StatusForbidden {
				t.Fatalf("%s: a signed-in browser wrote to /api/bd/ without a token: %d", s.name, got)
			}
			token, _ := b.csrfToken(s)
			if got := b.write(s, "/api/bd/t/write", nil, "X-CSRF-Token", token); got != http.StatusOK {
				t.Fatalf("%s: a signed-in browser with the token got %d", s.name, got)
			}
		}
	})
}

// Scenario 7, HTTPS mode: the cookie names and flags, and both directions
// again.
func TestInteropHTTPSMode(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		s := defaultSetup()
		s.https, s.trustProxy = true, "loopback"
		g, node := both(t, db, s, false)
		for _, order := range [][2]*server{{node, g.server}, {g.server, node}} {
			giver, taker := order[0], order[1]
			b := newBrowser(t)
			b.headers["X-Forwarded-Proto"] = "https"
			token, first := b.csrfToken(giver)
			if len(first.setCookies) != 1 || !strings.HasPrefix(first.setCookies[0], "__csrf=") ||
				!strings.HasSuffix(first.setCookies[0], "; Path=/; HttpOnly; Secure; SameSite=Lax") {
				t.Fatalf("%s set %q", giver.name, first.setCookies)
			}
			if got := b.write(taker, "/t/write", nil, "X-CSRF-Token", token); got != http.StatusOK {
				t.Fatalf("a token from %s was refused by %s: %d", giver.name, taker.name, got)
			}
			if again, second := b.csrfToken(taker); again != token || !reflect.DeepEqual(second.setCookies, first.setCookies) {
				t.Fatalf("%s set %q, %s set %q", giver.name, first.setCookies, taker.name, second.setCookies)
			}

			login := b.do(giver, "POST", "/t/login", map[string]any{"user": aliceUser})
			cookie := sessionSetCookie(t, login, "betterdesk.sid")
			if !strings.HasSuffix(cookie, "; HttpOnly; Secure; SameSite=Lax") {
				t.Fatalf("%s set %q", giver.name, cookie)
			}
			r := b.do(taker, "GET", "/t/whoami", nil)
			if fmt.Sprint(r.json(t)["userId"]) != "7" {
				t.Fatalf("%s does not see the session of %s: %s", taker.name, giver.name, r.body)
			}
			takerRest, _ := splitExpires(t, sessionSetCookie(t, r, "betterdesk.sid"))
			giverRest, _ := splitExpires(t, cookie)
			if takerRest != giverRest {
				t.Fatalf("Set-Cookie differs:\n%s: %s\n%s: %s", giver.name, giverRest, taker.name, takerRest)
			}
			// A session under the HTTP name is not read
			other := newBrowser(t)
			other.headers["X-Forwarded-Proto"] = "https"
			other.cookies["bd.sid"] = b.cookies["betterdesk.sid"]
			other.wantUser(false, giver, taker)

			// Over plain HTTP neither server sends the Secure session cookie,
			// and the token cookie keeps its Secure flag
			plain := newBrowser(t)
			if got := plain.do(giver, "POST", "/t/login", map[string]any{"user": aliceUser}); len(got.setCookies) != 0 {
				t.Fatalf("%s sent %q over plain HTTP", giver.name, got.setCookies)
			}
			if _, got := plain.csrfToken(giver); !strings.HasSuffix(got.setCookies[0], "; Secure; SameSite=Lax") {
				t.Fatalf("%s sent %q over plain HTTP", giver.name, got.setCookies)
			}
		}
	})
}

// The "sessions" page: both servers list an account's sessions alike.
func TestInteropSessionList(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		// A history: replaced, signed out, and the current one
		a := newBrowser(t)
		a.login(node, aliceUser)
		b := newBrowser(t)
		b.login(g.server, aliceUser)
		b.do(node, "POST", "/t/logout", map[string]any{})
		c := newBrowser(t)
		c.headers["User-Agent"] = ""
		c.login(node, aliceUser)
		d := newBrowser(t)
		d.login(g.server, aliceUser)

		decode := func(r *reply) map[string]any { return r.json(t) }
		fromGo := decode(d.copy().do(g.server, "GET", "/t/sessions", nil, "X-BD-Background", "1"))
		fromNode := decode(d.copy().do(node, "GET", "/t/sessions", nil, "X-BD-Background", "1"))
		sessions := fromGo["sessions"].([]any)
		if len(sessions) != 4 {
			t.Fatalf("Go lists %d sessions, want 4", len(sessions))
		}
		// expires_at moves with every answer; the rest must be the same
		for _, list := range []map[string]any{fromGo, fromNode} {
			for _, entry := range list["sessions"].([]any) {
				e := entry.(map[string]any)
				if _, isNumber := e["expiresAt"].(json.Number); !isNumber {
					t.Fatalf("expiresAt is %#v", e["expiresAt"])
				}
				if e["current"] == true {
					delete(e, "expiresAt")
				}
			}
		}
		if !reflect.DeepEqual(fromGo, fromNode) {
			goText, _ := json.MarshalIndent(fromGo, "", " ")
			nodeText, _ := json.MarshalIndent(fromNode, "", " ")
			t.Fatalf("the lists differ.\n--- Go:\n%s\n--- Node:\n%s", goText, nodeText)
		}
		// On PostgreSQL the Node console has always sent these times as
		// text, on SQLite as numbers
		created := fromNode["sessions"].([]any)[0].(map[string]any)["createdAt"]
		if _, isText := created.(string); isText != db.IsPostgres() {
			t.Fatalf("Node sends createdAt as %#v", created)
		}
	})
}

// A stored session neither server can read: Node answers the request with
// an error and leaves the row; Go ends the row and goes on without a
// session (BUGS.md S21), and Node then finds the row ended too.
func TestInteropBrokenStoredSession(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		g, node := both(t, db, defaultSetup(), true)
		for _, text := range []string{`{"userId":7}`, `not json`, `{"cookie":null,"userId":7}`} {
			b := newBrowser(t)
			sid := b.login(node, aliceUser)["sid"].(string)
			exec1(t, db, `UPDATE panel_sessions SET session_json = ? WHERE sid = ?`, text, sid)
			if r := b.copy().do(node, "GET", "/t/whoami", nil); r.status != http.StatusInternalServerError {
				t.Fatalf("node answered %d %s for the stored session %s", r.status, r.body, text)
			}
			if after := readRow(t, db, sid); !after.active || after.json != text {
				t.Fatalf("row after node answered: %+v", after)
			}
			if r := b.copy().do(g.server, "GET", "/t/whoami", nil); r.status != http.StatusOK || r.json(t)["userId"] != nil {
				t.Fatalf("go answered %d %s for the stored session %s", r.status, r.body, text)
			}
			if after := readRow(t, db, sid); after.active || str(after.reason) != "unreadable" || after.json != "{}" {
				t.Fatalf("row after go answered: %+v", after)
			}
			if r := b.copy().do(node, "GET", "/t/whoami", nil); r.status != http.StatusOK || r.json(t)["userId"] != nil {
				t.Fatalf("node answered %d %s after go ended the row", r.status, r.body)
			}
		}
	})
}

// schema describes panel_sessions: columns with type, null rule and
// default, and the indexes with their columns.
func schema(t *testing.T, db *store.DB) []string {
	t.Helper()
	queries := []string{
		`SELECT name || ' ' || type || ' notnull=' || "notnull" || ' default=' || COALESCE(dflt_value, '') || ' pk=' || pk FROM pragma_table_info('panel_sessions')`,
		`SELECT name || ': ' || sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'panel_sessions' AND sql IS NOT NULL`,
	}
	if db.IsPostgres() {
		queries = []string{
			`SELECT column_name || ' ' || data_type || ' null=' || is_nullable || ' default=' || COALESCE(column_default, '')
			 FROM information_schema.columns WHERE table_name = 'panel_sessions' AND table_schema = current_schema()`,
			`SELECT replace(indexdef, schemaname || '.', '') FROM pg_indexes WHERE tablename = 'panel_sessions' AND schemaname = current_schema()`,
		}
	}
	var lines []string
	for _, query := range queries {
		rows, err := db.Query(context.Background(), query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, strings.Join(strings.Fields(line), " "))
		}
		rows.Close()
	}
	sort.Strings(lines)
	return lines
}

// The table Go creates is the table Node creates.
func TestInteropSchema(t *testing.T) {
	storetest.Each(t, func(t *testing.T, byGo *store.DB) {
		var byNode *store.DB
		if byGo.IsPostgres() {
			byNode = storetest.Postgres(t)
		} else {
			byNode = storetest.SQLite(t)
		}
		startGo(t, byGo, defaultSetup())
		startNode(t, byNode, defaultSetup())
		goSchema, nodeSchema := schema(t, byGo), schema(t, byNode)
		if len(goSchema) < 17 || !reflect.DeepEqual(goSchema, nodeSchema) {
			t.Fatalf("the tables differ.\n--- Go:\n%s\n--- Node:\n%s", strings.Join(goSchema, "\n"), strings.Join(nodeSchema, "\n"))
		}
	})
}
