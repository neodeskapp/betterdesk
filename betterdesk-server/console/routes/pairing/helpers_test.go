package pairing_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/pairing"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// roleIDs gives every test account its users row id.
var roleIDs = map[string]int64{
	"admin": 1, "super_admin": 2, "server_admin": 3, "global_admin": 4,
	"operator": 5, "viewer": 6, "guest_issuer": 7,
}

// call is one request the fake API server received.
type call struct {
	method, path, body string
}

// fakeAPI stands in for the API server. answer decides what it says;
// without one it knows no route.
type fakeAPI struct {
	mu     sync.Mutex
	calls  []call
	answer func(c call) (status int, body string)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c := call{method: r.Method, path: r.URL.Path, body: string(raw)}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	answer := f.answer
	f.mu.Unlock()
	status, body := http.StatusNotFound, `{"error":"not found"}`
	if answer != nil {
		status, body = answer(c)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// seen returns the calls so far and forgets them.
func (f *fakeAPI) seen() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

// world is the pairing group on a console database, with a fake API
// server behind it and one signed-in session per role.
type world struct {
	t       *testing.T
	db      *store.DB
	api     *fakeAPI
	router  *kit.Router
	steps   http.Handler
	cookies map[string]string
}

func newWorld(t *testing.T, db *store.DB) *world {
	t.Helper()
	ctx := context.Background()
	if db == nil {
		db = storetest.Migrated(t)
	}
	for role, id := range roleIDs {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "pairing-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: db, api: &fakeAPI{}, cookies: map[string]string{}}
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		GoAPI:    goapi.New(w.api),
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("error", true),
	}
	pairing.Register(d)
	w.router = d.Router
	d.Router.Get("/signin/{role}", func(rw http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		role := r.PathValue("role")
		if _, err := s.Establish(session.User{ID: roleIDs[role], Username: "e2e_" + role, Role: role}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	w.steps = middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(rw, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(rw, r)
		}
	})))
	return w
}

// do sends a request as the account of a role, "" for nobody signed in.
// A body is sent as JSON.
func (w *world) do(role, method, target, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	cookie := ""
	if role != "" {
		cookie = w.cookies[role]
		if cookie == "" {
			pair, _, _ := strings.Cut(w.send("", http.MethodGet, "/signin/"+role, "").Header().Get("Set-Cookie"), ";")
			if !strings.HasPrefix(pair, "bd.sid=") {
				w.t.Fatalf("sign in as %s set the cookie %q", role, pair)
			}
			cookie = pair
			w.cookies[role] = cookie
		}
	}
	return w.send(cookie, method, target, body)
}

func (w *world) send(cookie, method, target, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w.steps.ServeHTTP(rec, req)
	return rec
}

// entry is one row of the audit log.
type entry struct {
	userID              int64
	action, details, ip string
}

// audit returns the audit log, oldest entry first.
func (w *world) audit() []entry {
	w.t.Helper()
	rows, err := w.db.Query(context.Background(), "SELECT user_id, action, details, ip_address FROM audit_log ORDER BY id")
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.userID, &e.action, &e.details, &e.ip); err != nil {
			w.t.Fatal(err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	return entries
}

// want fails unless the answer has the status and exactly the body.
func want(t *testing.T, name string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s\nwant %d %s", name, rec.Code, rec.Body.String(), status, body)
	}
}
