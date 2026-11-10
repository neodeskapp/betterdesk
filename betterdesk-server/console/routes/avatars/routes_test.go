package avatars_test

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	avatarsvc "github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/routes/avatars"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// world is the avatars group on a console database, with the session of
// one signed-in user.
type world struct {
	t      *testing.T
	db     *store.DB
	svc    *avatarsvc.Service
	steps  http.Handler
	cookie string
	limits *limits.Set
}

func newWorld(t *testing.T, db *store.DB, env map[string]string) *world {
	t.Helper()
	ctx := context.Background()
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "avatars-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: db, svc: avatarsvc.New(avatarsvc.Options{DB: db})}
	w.limits = limits.NewSet(limits.Options{
		UserKey: func(r *http.Request) string {
			if id := kit.UserID(r); id != 0 {
				return fmt.Sprint(id)
			}
			return ""
		},
		Getenv: func(name string) string { return env[name] },
	})
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		Avatars:  w.svc,
		Limits:   w.limits,
		Log:      kit.NewLogger("error", true),
	}
	avatars.Register(d)
	d.Router.Get("/signin/{id}", func(rw http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		var id int64
		_, _ = fmt.Sscan(r.PathValue("id"), &id)
		if _, err := s.Establish(session.User{ID: id, Username: "bob", Role: "viewer"}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	w.steps = sessions.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(rw, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(rw, r)
		}
	}))
	return w
}

func (w *world) signIn(id int64) {
	w.t.Helper()
	pair, _, _ := strings.Cut(w.send("", http.MethodGet, fmt.Sprintf("/signin/%d", id), nil).Header().Get("Set-Cookie"), ";")
	if !strings.HasPrefix(pair, "bd.sid=") {
		w.t.Fatalf("sign in set the cookie %q", pair)
	}
	w.cookie = pair
}

func (w *world) send(cookie, method, target string, headers http.Header) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/", nil)
	// The target as the client wrote it, also where it is no valid URL
	// for Go's parser
	req.RequestURI = target
	path, query, _ := strings.Cut(target, "?")
	req.URL.Path, req.URL.RawPath, req.URL.RawQuery = path, "", query
	if plain, err := unescapePath(path); err == nil {
		req.URL.Path, req.URL.RawPath = plain, path
	}
	req.RemoteAddr = "203.0.113.7:5555"
	for name, values := range headers {
		req.Header[name] = values
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w.steps.ServeHTTP(rec, req)
	return rec
}

func unescapePath(p string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "http://x"+p, nil)
	if err != nil {
		return "", err
	}
	return req.URL.Path, nil
}

func (w *world) user(name string) int64 {
	w.t.Helper()
	id, err := w.db.Insert(context.Background(), `INSERT INTO users (username, password_hash, role) VALUES (?, 'x', 'viewer')`, name)
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

func (w *world) picture(userID int64, mime string, data []byte, version, source, sourceURL string) {
	w.t.Helper()
	_, err := w.db.Exec(context.Background(), `INSERT INTO user_avatars (user_id, mime_type, data, version, source, source_url) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, mime, data, version, source, sourceURL)
	if err != nil {
		w.t.Fatal(err)
	}
}

type recorded struct {
	Users    []string `json:"users"`
	Pictures []struct {
		UserID    int64  `json:"userId"`
		Mime      string `json:"mime"`
		Data      string `json:"data"`
		Version   string `json:"version"`
		Source    string `json:"source"`
		SourceURL string `json:"sourceUrl"`
	} `json:"pictures"`
	Requests []struct {
		As      string                     `json:"as"`
		Method  string                     `json:"method"`
		Target  string                     `json:"target"`
		Headers map[string]json.RawMessage `json:"headers"`
		Answer  struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		} `json:"answer"`
	} `json:"requests"`
}

// latin1Bytes turns the characters of a recorded header value back into
// the bytes that went over the wire.
func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

// TestOracleRoutes replays the requests the Node route answered with
// pictures in its database (testdata/nodeoracle/routes.js): status, the
// headers the route sets, and the body must be the same.
func TestOracleRoutes(t *testing.T) {
	f, err := os.Open("testdata/routes.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var rec recorded
	if err := json.NewDecoder(zr).Decode(&rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Requests) < 200 {
		t.Fatalf("%d requests recorded", len(rec.Requests))
	}

	w := newWorld(t, storetest.Migrated(t), nil)
	for i, name := range rec.Users {
		if id := w.user(name); id != int64(i+1) {
			t.Fatalf("user %s got the id %d", name, id)
		}
	}
	for _, p := range rec.Pictures {
		data, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			t.Fatal(err)
		}
		w.picture(p.UserID, p.Mime, data, p.Version, p.Source, p.SourceURL)
	}
	w.signIn(2)

	statuses := map[int]int{}
	for _, q := range rec.Requests {
		what := q.Method + " " + q.Target
		headers := http.Header{}
		for name, raw := range q.Headers {
			var one string
			var many []string
			if json.Unmarshal(raw, &one) == nil {
				many = []string{one}
			} else if err := json.Unmarshal(raw, &many); err != nil {
				t.Fatal(err)
			}
			for _, value := range many {
				headers[name] = append(headers[name], latin1Bytes(value))
			}
			what += fmt.Sprintf(" %s: %q", name, many)
		}
		cookie := w.cookie
		if q.As == "anon" {
			cookie = ""
		}
		got := w.send(cookie, q.Method, q.Target, headers)
		statuses[got.Code]++
		if got.Code != q.Answer.Status {
			t.Errorf("%s: status %d, Node %d", what, got.Code, q.Answer.Status)
			continue
		}
		if q.As == "anon" {
			// The 401 is the guard's, which has its own tests
			continue
		}
		want := q.Answer.Headers
		if got.Code == http.StatusNotModified {
			// Go's HTTP server takes Content-Type off every 304, so the
			// handler does not set it. Node sent it.
			if want["content-type"] == "" {
				t.Errorf("%s: the recorded 304 has no Content-Type", what)
			}
			delete(want, "content-type")
		}
		var names []string
		for name := range got.Header() {
			if lower := strings.ToLower(name); !strings.HasPrefix(lower, "ratelimit") && lower != "set-cookie" {
				names = append(names, lower)
			}
		}
		sort.Strings(names)
		var wantNames []string
		for name := range want {
			wantNames = append(wantNames, name)
		}
		sort.Strings(wantNames)
		if strings.Join(names, " ") != strings.Join(wantNames, " ") {
			t.Errorf("%s: headers %v, Node %v", what, names, wantNames)
		}
		for name, value := range want {
			if got.Header().Get(name) != value {
				t.Errorf("%s: %s = %q, Node %q", what, name, got.Header().Get(name), value)
			}
		}
		if body := base64.StdEncoding.EncodeToString(got.Body.Bytes()); body != q.Answer.Body {
			t.Errorf("%s: body %s, Node %s", what, body, q.Answer.Body)
		}
	}
	if statuses[200] < 100 || statuses[304] < 40 || statuses[404] < 20 || statuses[401] != 2 {
		t.Fatalf("the replay answered %v", statuses)
	}
}

func TestPicturesNeedASignedInUser(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), nil)
	alice := w.user("alice")
	w.picture(alice, "image/png", []byte("\x89PNG\r\n\x1a\n"), "0123456789abcdef", "oidc", "")
	for _, target := range []string{"/api/users/avatars", fmt.Sprintf("/api/users/%d/avatar", alice)} {
		rec := w.send("", http.MethodGet, target, nil)
		if rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "PNG") || strings.Contains(rec.Body.String(), "alice") {
			t.Errorf("%s without a session: %d %s", target, rec.Code, rec.Body.String())
		}
		// Nothing was counted: the guard answers before the limiter
		if rec.Header().Get("RateLimit-Limit") != "" {
			t.Errorf("%s without a session was counted by the limiter", target)
		}
	}
}

// Every signed-in read counts against the reader's budget of panel
// reads, and the read over the budget is refused.
func TestPicturesCountAgainstThePanelReadBudget(t *testing.T) {
	w := newWorld(t, storetest.Migrated(t), map[string]string{"PANEL_READ_RATE_LIMIT_MAX": "3"})
	w.user("alice")
	bob := w.user("bob")
	w.signIn(bob)
	for i, target := range []string{"/api/users/avatars", "/api/users/1/avatar", "/api/users/x/avatar"} {
		rec := w.send(w.cookie, http.MethodGet, target, nil)
		if rec.Code == http.StatusTooManyRequests || rec.Header().Get("RateLimit-Limit") != "3" || rec.Header().Get("RateLimit-Remaining") != fmt.Sprint(2-i) {
			t.Fatalf("%s: %d, limit %q, remaining %q", target, rec.Code, rec.Header().Get("RateLimit-Limit"), rec.Header().Get("RateLimit-Remaining"))
		}
	}
	rec := w.send(w.cookie, http.MethodGet, "/api/users/avatars", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Body.String() != `{"success":false,"error":"Too many panel read requests. Please slow down."}` {
		t.Fatalf("the fourth read: %d %s", rec.Code, rec.Body.String())
	}
}

func TestListOfPictures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		alice, bob, carol := w.user("alice"), w.user("bob"), w.user("carol")
		w.signIn(bob)
		rec := w.send(w.cookie, http.MethodGet, "/api/users/avatars", nil)
		if rec.Code != 200 || rec.Body.String() != `{"success":true,"data":{"users":[]}}` || rec.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("no pictures: %d %s, Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
		w.picture(carol, "image/gif", []byte("GIF89a"), "1f19970f056cd116", "profile", "http://pictures.example/c.gif")
		w.picture(alice, "image/png", []byte("\x89PNG\r\n\x1a\n"), "c47dd9465c00e9a0", "oidc", "")
		w.svc.ResetCache()
		rec = w.send(w.cookie, http.MethodGet, "/api/users/avatars", nil)
		want := fmt.Sprintf(`{"success":true,"data":{"users":[{"id":%d,"username":"alice","avatar_version":"c47dd9465c00e9a0"},{"id":%d,"username":"carol","avatar_version":"1f19970f056cd116"}]}}`, alice, carol)
		if rec.Code != 200 || rec.Body.String() != want {
			t.Fatalf("two pictures: %d %s\nwant %s", rec.Code, rec.Body.String(), want)
		}
		if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `W/"`) {
			t.Fatalf("the list has the ETag %q", etag)
		}
	})
}

// A failing database is a 500 with a fixed word, for both routes. On
// PostgreSQL an id beyond the integer range is such a failure, on SQLite
// it finds nothing.
func TestDatabaseFailures(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		w.signIn(w.user("bob"))
		rec := w.send(w.cookie, http.MethodGet, "/api/users/9999999999/avatar", nil)
		wantStatus, wantBody, wantCache := 404, `{"success":false,"error":"not_found"}`, "private, no-cache"
		if db.IsPostgres() {
			wantStatus, wantBody, wantCache = 500, `{"success":false,"error":"server_error"}`, ""
		}
		if rec.Code != wantStatus || rec.Body.String() != wantBody || rec.Header().Get("Cache-Control") != wantCache {
			t.Fatalf("an id beyond integer: %d %s, Cache-Control %q", rec.Code, rec.Body.String(), rec.Header().Get("Cache-Control"))
		}
		if _, err := db.Exec(context.Background(), `ALTER TABLE user_avatars RENAME TO user_avatars_gone`); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"/api/users/avatars", "/api/users/1/avatar"} {
			rec := w.send(w.cookie, http.MethodGet, target, nil)
			if rec.Code != 500 || rec.Body.String() != `{"success":false,"error":"server_error"}` {
				t.Errorf("%s without the table: %d %s", target, rec.Code, rec.Body.String())
			}
		}
	})
}
