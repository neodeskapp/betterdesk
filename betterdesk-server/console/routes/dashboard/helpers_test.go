package dashboard

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/i18n"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// validKey is a public key a client accepts: 32 bytes in base64.
var validKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

// roleIDs gives every test account its users row id.
var roleIDs = map[string]int64{
	"admin": 1, "super_admin": 2, "server_admin": 3, "global_admin": 4,
	"operator": 5, "viewer": 6, "guest_issuer": 7,
}

// languages loads the console's language files once for all tests.
var languages = sync.OnceValue(func() *i18n.Bundle {
	bundle := i18n.New(filepath.Join("..", "..", "static", "files", "lang"), "en")
	bundle.Logf = func(string, ...any) {}
	bundle.Load()
	return bundle
})

// reply is what the fake API server answers on one path.
type reply struct {
	status int
	body   string
}

// fakeAPI stands in for the API server: it answers by path and remembers
// the requests. A path it has no reply for is 404.
type fakeAPI struct {
	mu      sync.Mutex
	replies map[string]reply
	calls   []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	answer, ok := f.replies[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		answer = reply{http.StatusNotFound, `{"error":"not found"}`}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.status)
	_, _ = io.WriteString(w, answer.body)
}

// set makes the fake answer 200 with body on a path.
func (f *fakeAPI) set(path, body string) {
	f.setStatus(path, http.StatusOK, body)
}

func (f *fakeAPI) setStatus(path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[path] = reply{status, body}
}

// seen returns the requests so far and forgets them.
func (f *fakeAPI) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

// world is the dashboard group on a console database, with a fake API
// server behind it, key files in a folder and one session per role.
type world struct {
	t       *testing.T
	db      *store.DB
	api     *fakeAPI
	cfg     *config.Config
	h       *handlers
	router  *kit.Router
	steps   http.Handler
	cookies map[string]string
}

// newWorld sets the group up on db, or on a new SQLite database for nil.
// files are written into the keys folder (id_ed25519.pub, .api_key).
func newWorld(t *testing.T, db *store.DB, files map[string]string) *world {
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
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "dashboard-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, db: db, api: &fakeAPI{replies: map[string]reply{}}, cookies: map[string]string{}}
	w.cfg = &config.Config{
		Port:       5000,
		APIURL:     "http://127.0.0.1:21114/api",
		GoAPIPort:  21114,
		APIPort:    21121,
		APIEnabled: true,
		SignalHost: "localhost",
		SignalPort: 21116,
		RelayHost:  "localhost",
		RelayPort:  21117,
	}
	api := goapi.New(w.api)
	log := kit.NewLogger("error", true)
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Config:   w.cfg,
		Store:    db,
		Sessions: sessions,
		GoAPI:    api,
		Audit:    auditlog.New(db),
		Log:      log,
		ServerInfo: serverinfo.New(serverinfo.Options{
			PubKeyPath: filepath.Join(dir, "id_ed25519.pub"),
			APIKeyPath: filepath.Join(dir, ".api_key"),
			APIURL:     w.cfg.APIURL,
			GoAPIPort:  w.cfg.GoAPIPort,
			APIPort:    w.cfg.APIPort,
			DataDir:    dir,
			EnvPath:    filepath.Join(dir, ".env"),
			Getenv:     func(string) string { return "" },
			GoAPI:      api,
			QR:         func(text string) (string, error) { return "qr:" + text, nil },
			Logger:     log,
		}),
	}
	w.h = newHandlers(d)
	w.h.register(d.Router)
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
		r = kit.Begin(r, httpx.TrustProxy{}, languages())
		kit.SetLanguage(r, "en")
		if !d.Router.Serve(rw, r) {
			http.NotFound(rw, r)
		}
	})))
	return w
}

// get sends a GET as the account of a role, "" for nobody signed in.
func (w *world) get(role, target string) *httptest.ResponseRecorder {
	w.t.Helper()
	return w.do(role, http.MethodGet, target)
}

func (w *world) do(role, method, target string) *httptest.ResponseRecorder {
	w.t.Helper()
	cookie := ""
	if role != "" {
		cookie = w.cookies[role]
		if cookie == "" {
			pair, _, _ := strings.Cut(w.send("", http.MethodGet, "/signin/"+role).Header().Get("Set-Cookie"), ";")
			if !strings.HasPrefix(pair, "bd.sid=") {
				w.t.Fatalf("sign in as %s set the cookie %q", role, pair)
			}
			cookie = pair
			w.cookies[role] = cookie
		}
	}
	return w.send(cookie, method, target)
}

func (w *world) send(cookie, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Host = "console.test:5000"
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	w.steps.ServeHTTP(rec, req)
	return rec
}

// want fails unless the answer has the status and exactly the body.
func want(t *testing.T, name string, rec *httptest.ResponseRecorder, status int, body string) {
	t.Helper()
	if rec.Code != status || rec.Body.String() != body {
		t.Errorf("%s: %d %s\nwant %d %s", name, rec.Code, rec.Body.String(), status, body)
	}
}
