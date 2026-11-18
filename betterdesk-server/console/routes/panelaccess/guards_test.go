package panelaccess_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/panelaccess"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestGuards checks the permission each route asks for itself. In the
// console the panel access guard of the pipeline stands in front and
// closes the users section to the same accounts, so this test runs the
// group's routes without the pipeline: the guards then decide by the
// role's built-in permissions.
func TestGuards(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	ids := map[string]int64{"admin": 1, "operator": 5, "viewer": 6}
	for role, id := range ids {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, "e2e_"+role, role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO user_groups (guid, name) VALUES ('team-1', 'Team')"); err != nil {
		t.Fatal(err)
	}
	sessions, err := session.New(ctx, session.Options{DB: db, Secret: "panel-access-test-secret-0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	d := &kit.Deps{
		Router:   kit.NewRouter(),
		Store:    db,
		Sessions: sessions,
		Access:   access.New(db, access.NewDBRoleSource(nil), access.Options{}),
		Audit:    auditlog.New(db),
		Log:      kit.NewLogger("error", true),
	}
	panelaccess.Register(d)
	d.Router.Get("/signin/{role}", func(w http.ResponseWriter, r *http.Request) {
		s := kit.Session(r)
		if err := s.Regenerate(); err != nil {
			t.Error(err)
		}
		role := r.PathValue("role")
		if _, err := s.Establish(session.User{ID: ids[role], Username: "e2e_" + role, Role: role}, session.Meta{}); err != nil {
			t.Error(err)
		}
	})
	steps := middleware.Bodies(middleware.BodyOptions{})(sessions.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Router.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			http.NotFound(w, r)
		}
	})))
	call := func(cookie, method, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		body := ""
		if method == http.MethodPut {
			body = `{"sections":[{"resource_key":"devices","access_level":"write"}]}`
		}
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		steps.ServeHTTP(rec, req)
		return rec
	}
	signIn := func(role string) string {
		pair, _, _ := strings.Cut(call("", "GET", "/signin/"+role).Header().Get("Set-Cookie"), ";")
		if !strings.HasPrefix(pair, "bd.sid=") {
			t.Fatalf("sign in as %s set the cookie %q", role, pair)
		}
		return pair
	}
	admin, operator, viewer := signIn("admin"), signIn("operator"), signIn("viewer")

	signedOut := `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	view := `{"success":false,"error":"Permission denied: user.view"}`
	edit := `{"success":false,"error":"Permission denied: user.edit"}`
	for _, tc := range []struct {
		method, target string
		viewer         string // "" when the viewer may
		operator       string // "" when the operator may
		admin          int    // the status the admin gets past the guards
	}{
		{"GET", "/api/panel/access/catalog", view, "", 200},
		{"GET", "/api/panel/access/effective", "", "", 200},
		{"GET", "/api/panel/access/users/6", view, "", 200},
		{"PUT", "/api/panel/access/users/6", edit, edit, 200},
		{"GET", "/api/panel/access/groups/team-1", view, "", 200},
		{"PUT", "/api/panel/access/groups/team-1", edit, edit, 500}, // a group's policy cannot be saved (BUGS UM2)
	} {
		name := tc.method + " " + tc.target
		if rec := call("", tc.method, tc.target); rec.Code != 401 || rec.Body.String() != signedOut {
			t.Errorf("%s signed out: %d %s", name, rec.Code, rec.Body.String())
		}
		for cookie, want := range map[string]string{viewer: tc.viewer, operator: tc.operator} {
			rec := call(cookie, tc.method, tc.target)
			if want == "" && rec.Code != 200 {
				t.Errorf("%s: %d %s, want 200", name, rec.Code, rec.Body.String())
			}
			if want != "" && (rec.Code != 403 || rec.Body.String() != want) {
				t.Errorf("%s: %d %s, want 403 %s", name, rec.Code, rec.Body.String(), want)
			}
		}
		var stored int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM panel_access_policies").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != 0 {
			t.Fatalf("%s stored %d rows for accounts that may not", name, stored)
		}
		if rec := call(admin, tc.method, tc.target); rec.Code != tc.admin {
			t.Errorf("%s as admin: %d %s, want %d", name, rec.Code, rec.Body.String(), tc.admin)
		}
		if _, err := db.Exec(ctx, "DELETE FROM panel_access_policies"); err != nil {
			t.Fatal(err)
		}
	}
}
