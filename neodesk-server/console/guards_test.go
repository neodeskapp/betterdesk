package console

import (
	"strings"
	"testing"
)

// guardWorld is a console with one API route and one page route behind
// each guard. None of the paths belongs to a panel section, so the role
// map alone decides.
func guardWorld(t *testing.T, o setup) *world {
	t.Helper()
	w := newWorld(t, o)
	d := w.s.Deps()
	r := w.s.Router()
	r.Get("/api/probe", probe)
	for _, path := range []string{"/api/g/auth", "/g/auth"} {
		r.Get(path, probe, d.RequireAuth)
	}
	for _, path := range []string{"/api/g/admin", "/g/admin"} {
		r.Get(path, probe, d.RequireAdmin)
	}
	for _, path := range []string{"/api/g/role", "/g/role"} {
		r.Get(path, probe, d.RequireRole("operator"))
	}
	for _, path := range []string{"/api/g/perm", "/g/perm"} {
		r.Get(path, probe, d.RequirePermission("server.config"))
	}
	for _, path := range []string{"/api/g/rd", "/g/rd"} {
		r.Get(path, probe, d.RequireRdClientAuth("device.connect"))
	}
	// Mounted twice, as several groups are: the guard still answers JSON
	// below /api/nd
	d.At("/api/nd").Router.Get("/g/auth", probe, d.RequireAuth)
	return w
}

// expectation is what a guard answers one account: pass, or the API body
// and status with the page's status and redirect target.
type expectation struct {
	pass     bool
	status   int
	api      string
	pageCode int
	location string
	text     string
}

var (
	pass         = expectation{pass: true}
	unauthorized = expectation{status: 401, api: `{"success":false,"error":"Unauthorized"}`, pageCode: 302, location: "/login"}
	proAPI       = `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`
)

func forbidden(message string) expectation {
	return expectation{status: 403, api: `{"success":false,"error":"` + message + `"}`, pageCode: 403, text: "Forbidden"}
}

var guardRoles = []string{"", "admin", "super_admin", "server_admin", "global_admin", "operator", "viewer", "guest_issuer", "pro"}

// guardTable is what each guard answers each default role, from the old
// console's middleware and role map.
var guardTable = map[string]map[string]expectation{
	"auth": {
		"":      {status: 401, api: `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`, pageCode: 302, location: "/login?return=%2Fg%2Fauth"},
		"pro":   {status: 403, api: proAPI, pageCode: 302, location: "/login"},
		"admin": pass, "super_admin": pass, "server_admin": pass, "global_admin": pass, "operator": pass, "viewer": pass, "guest_issuer": pass,
	},
	"admin": {
		"":      unauthorized,
		"admin": pass, "super_admin": pass, "server_admin": pass, "global_admin": pass,
		"operator": forbidden("Admin access required"), "viewer": forbidden("Admin access required"),
		"guest_issuer": forbidden("Admin access required"), "pro": forbidden("Admin access required"),
	},
	"role": {
		"":      unauthorized,
		"admin": pass, "super_admin": pass, "operator": pass,
		"server_admin": forbidden("Forbidden"), "global_admin": forbidden("Forbidden"), "viewer": forbidden("Forbidden"),
		"guest_issuer": forbidden("Forbidden"), "pro": forbidden("Forbidden"),
	},
	"perm": {
		"":      unauthorized,
		"admin": pass, "super_admin": pass, "server_admin": pass,
		"global_admin": forbidden("Permission denied: server.config"), "operator": forbidden("Permission denied: server.config"),
		"viewer": forbidden("Permission denied: server.config"), "guest_issuer": forbidden("Permission denied: server.config"),
		"pro": forbidden("Permission denied: server.config"),
	},
	"rd": {
		"":      {status: 401, api: `{"success":false,"error":"Unauthorized. Please log in."}`, pageCode: 302, location: "/remote/login?return=%2Fremote"},
		"pro":   {status: 403, api: proAPI, pageCode: 302, location: "/remote/login?return=%2Fremote"},
		"admin": pass, "super_admin": pass, "global_admin": pass, "operator": pass,
		"server_admin": forbidden("Permission denied: device.connect"), "viewer": forbidden("Permission denied: device.connect"),
		"guest_issuer": forbidden("Permission denied: device.connect"),
	},
}

func TestGuardsOnTheWire(t *testing.T) {
	for _, build := range []bool{false, true} {
		name := "without a build"
		if build {
			name = "with the web app"
		}
		t.Run(name, func(t *testing.T) {
			w := guardWorld(t, setup{build: build})
			for _, role := range guardRoles {
				for guard, byRole := range guardTable {
					want, ok := byRole[role]
					if !ok {
						t.Fatalf("no expectation for %s as %q", guard, role)
					}
					who := role
					if who == "" {
						who = "signed out"
					}
					t.Run(guard+" as "+who, func(t *testing.T) {
						checkGuard(t, w, role, guard, want, build)
					})
				}
			}
		})
	}
}

func checkGuard(t *testing.T, w *world, role, guard string, want expectation, build bool) {
	// A refused pro account loses its session, so every check signs in anew
	fresh := func() *agent {
		a := w.agent()
		if role != "" {
			a.signIn(role)
		}
		return a
	}

	api := fresh().get("/api/g/" + guard)
	page := fresh().get("/g/"+guard, "Accept", "text/html")
	if want.pass {
		if api.status != 200 || page.status != 200 {
			t.Errorf("API %d, page %d; want both to pass", api.status, page.status)
		}
		return
	}

	if api.status != want.status || api.body != want.api {
		t.Errorf("API: %d %s\nwant %d %s", api.status, api.body, want.status, want.api)
	}
	if got := api.header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("API Content-Type = %q", got)
	}
	if api.header.Get("Cache-Control") != "no-cache, no-store, must-revalidate" || api.header.Get("ETag") == "" {
		t.Errorf("API headers: Cache-Control %q, ETag %q", api.header.Get("Cache-Control"), api.header.Get("ETag"))
	}
	wantSecurityHeaders(t, api)

	if page.status != want.pageCode {
		t.Fatalf("page: %d %.200s, want %d", page.status, page.body, want.pageCode)
	}
	switch {
	case want.pageCode == 302:
		wantBody := "<p>Found. Redirecting to " + strings.ReplaceAll(want.location, "&", "&amp;") + "</p>"
		if page.header.Get("Location") != want.location || page.body != wantBody || page.header.Get("Vary") != "Accept" {
			t.Errorf("page: Location %q, Vary %q, body %q; want a redirect to %q", page.header.Get("Location"), page.header.Get("Vary"), page.body, want.location)
		}
	case build:
		if !strings.Contains(page.body, `<meta name="bd-error" content="403">`) || !strings.Contains(page.body, "console app") ||
			page.header.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("page: want the app's 403 page, got %q %.200s", page.header.Get("Content-Type"), page.body)
		}
		if !strings.HasPrefix(page.header.Get("Content-Security-Policy"), "default-src 'self'; script-src 'self' 'sha256-abc='") {
			t.Errorf("the error page carries the policy %q, want the build's", page.header.Get("Content-Security-Policy"))
		}
	default:
		if page.body != want.text || page.header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("page: %q as %q, want %q as plain text", page.body, page.header.Get("Content-Type"), want.text)
		}
	}
}

func TestGuardBelowAMountAnswersJSON(t *testing.T) {
	w := guardWorld(t, setup{})
	res := w.agent().get("/api/nd/g/auth")
	if res.status != 401 || res.body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
		t.Errorf("got %d %s", res.status, res.body)
	}
	if res := w.agent().signIn("viewer").get("/api/nd/g/auth"); res.status != 200 {
		t.Errorf("signed in: %d", res.status)
	}
}

func TestSignedOutPageRedirectKnowsAnEndedSession(t *testing.T) {
	w := guardWorld(t, setup{})
	res := w.agent().get("/g/auth?tab=1", "Cookie", "nd.sid=s%3Agone.sig", "Accept", "text/html")
	if res.status != 302 || res.header.Get("Location") != "/login?expired=1&return=%2Fg%2Fauth%3Ftab%3D1" {
		t.Errorf("got %d to %q", res.status, res.header.Get("Location"))
	}
	// Only GET and HEAD get a way back
	a := w.agent()
	w.s.Router().Post("/g/auth", probe, w.s.Deps().RequireAuth)
	res = a.do("POST", "/g/auth", "a=1", "Content-Type", "application/x-www-form-urlencoded", "X-CSRF-Token", a.csrf())
	if res.status != 302 || res.header.Get("Location") != "/login" {
		t.Errorf("POST: %d to %q, want /login without a way back", res.status, res.header.Get("Location"))
	}
}

func TestProAccountLosesItsSessionAtTheGuard(t *testing.T) {
	w := guardWorld(t, setup{})
	for _, path := range []string{"/api/g/auth", "/g/auth", "/api/g/rd", "/g/rd"} {
		a := w.agent().signIn("pro")
		if res := a.get("/api/probe"); res.json(t)["user"] != float64(roleIDs["pro"]) {
			t.Fatalf("pro is not signed in: %s", res.body)
		}
		res := a.get(path)
		if res.status != 403 && res.status != 302 {
			t.Fatalf("%s: %d", path, res.status)
		}
		if res.cookie("nd.sid") != "" {
			t.Errorf("%s: the refusal renews the session cookie: %q", path, res.cookie("nd.sid"))
		}
		// The browser still sends the cookie; the session behind it is gone
		if res := a.get("/api/probe"); res.json(t)["user"] != float64(0) {
			t.Errorf("%s: the session survived the refusal: %s", path, res.body)
		}
	}
	// The other guards refuse a pro account without ending its session
	a := w.agent().signIn("pro")
	a.get("/api/g/admin")
	a.get("/api/g/perm")
	if res := a.get("/api/probe"); res.json(t)["user"] != float64(roleIDs["pro"]) {
		t.Errorf("requireAdmin or requirePermission ended the session: %s", res.body)
	}
}

func TestPanelAccessGuardAnswers(t *testing.T) {
	for _, build := range []bool{false, true} {
		w := guardWorld(t, setup{build: build})
		w.s.Router().Post("/users/import", probe)
		w.s.Router().Post("/api/users/import", probe)
		a := w.agent().signIn("viewer")

		res := a.post("/api/users/import", "{}")
		if res.status != 403 || res.body != `{"success":false,"error":"Panel access denied: users","code":"panel_access_denied"}` {
			t.Errorf("API write: %d %s", res.status, res.body)
		}
		res = a.do("POST", "/users/import", "a=1", "Content-Type", "application/x-www-form-urlencoded", "Accept", "text/html", "X-CSRF-Token", a.csrf())
		if res.status != 403 {
			t.Fatalf("page write: %d", res.status)
		}
		const message = "You do not have permission to access this panel section"
		if build {
			if !strings.Contains(res.body, `<meta name="bd-error" content="403"`) || !strings.Contains(res.body, `data-message="`+message+`"`) {
				t.Errorf("page write with a build: %.300s", res.body)
			}
		} else if res.body != message || res.header.Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("page write without a build: %q as %q", res.body, res.header.Get("Content-Type"))
		}
		// Reading a page passes the guard: the app shows its own view
		w.s.Router().Get("/users/list", probe)
		if res := a.get("/users/list"); res.status != 200 || res.json(t)["section"] != "users" {
			t.Errorf("page read: %d %s", res.status, res.body)
		}
	}
}
