package scaling

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
)

// roleIDs gives each test account its own id: an account has one session
// at a time.
var roleIDs = map[string]int64{"admin": 1, "super_admin": 2, "server_admin": 3, "global_admin": 4, "operator": 5, "viewer": 6, "guest_issuer": 7, "pro": 8}

// site is the group behind the steps of the pipeline its routes need: the
// body parsers, the session and the request accessors. Without the
// pipeline's permission step the guards decide by the built-in role map.
type site struct {
	t     *testing.T
	d     *kit.Deps
	steps http.Handler
}

func newSite(t *testing.T, db *store.DB, o Options) *site {
	t.Helper()
	sessions, err := session.New(context.Background(), session.Options{DB: db, Secret: "scaling-secret-0123456789abcdef0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		Log:      kit.NewLogger("error", true),
		Limits:   limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }}),
	}
	// Both addresses the old console had the group under
	RegisterWith(d, o)
	RegisterWith(d.At("/api/bd"), o)
	d.Router.Get("/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		role := r.PathValue("role")
		if _, err := s.Establish(session.User{ID: roleIDs[role], Username: "e2e_" + role, Role: role}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	steps := middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(w, r)
		}
	})))
	return &site{t: t, d: d, steps: steps}
}

func (s *site) call(cookie, method, target, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	s.steps.ServeHTTP(rec, req)
	return rec
}

// signIn returns the session cookie of a test account with the role.
func (s *site) signIn(role string) string {
	s.t.Helper()
	pair, _, _ := strings.Cut(s.call("", "GET", "/signin/"+role, "").Header().Get("Set-Cookie"), ";")
	if !strings.HasPrefix(pair, "bd.sid=") {
		s.t.Fatalf("sign in as %s set the cookie %q", role, pair)
	}
	return pair
}

// post sends a JSON body as the account of the cookie and returns status
// and body.
func (s *site) post(cookie, path, body string) (int, string) {
	s.t.Helper()
	rec := s.call(cookie, "POST", path, body)
	return rec.Code, rec.Body.String()
}

// send sends a request with an optional JSON body as the account of the
// cookie and returns status and body.
func (s *site) send(cookie, method, path, body string) (int, string) {
	s.t.Helper()
	rec := s.call(cookie, method, path, body)
	return rec.Code, rec.Body.String()
}
