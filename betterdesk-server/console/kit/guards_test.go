package kit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// htmlEscaper escapes an address the way a redirect's HTML body shows it.
var htmlEscaper = strings.NewReplacer(`"`, "&quot;", "&", "&amp;", "'", "&#39;", "<", "&lt;", ">", "&gt;")

// guarded runs a guard in front of a handler for a request by the given
// account and returns the answer and whether the handler ran.
func guarded(d *Deps, guard Middleware, who *access.Context, method, target string, header ...string) (*httptest.ResponseRecorder, bool) {
	reached := false
	h := guard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	r = Begin(r, httpx.TrustProxy{}, nil)
	SetAccess(r, who)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, reached
}

func account(id int64, role string) *access.Context {
	c := &access.Context{UserID: id, Loaded: true}
	if id != 0 {
		c.HasUser, c.Role = true, role
	}
	return c
}

// withPermissions is an account whose role was loaded with these
// permissions.
func withPermissions(role string, permissions ...string) *access.Context {
	c := account(7, role)
	c.Permissions = access.PermissionSet{}
	for _, p := range permissions {
		c.Permissions[p] = struct{}{}
	}
	return c
}

func TestGuardsAnswerTheAPI(t *testing.T) {
	d := &Deps{}
	signedOut := account(0, "")
	cases := []struct {
		name   string
		guard  Middleware
		who    *access.Context
		status int
		body   string
	}{
		{"requireAuth signed out", d.RequireAuth, signedOut, 401, `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`},
		{"requireAuth pro", d.RequireAuth, account(7, "pro"), 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`},
		{"requireAuth viewer", d.RequireAuth, account(7, "viewer"), 0, ""},
		{"requireAdmin signed out", d.RequireAdmin, signedOut, 401, `{"success":false,"error":"Unauthorized"}`},
		{"requireAdmin operator", d.RequireAdmin, account(7, "operator"), 403, `{"success":false,"error":"Admin access required"}`},
		{"requireAdmin viewer", d.RequireAdmin, account(7, "viewer"), 403, `{"success":false,"error":"Admin access required"}`},
		{"requireAdmin pro", d.RequireAdmin, account(7, "pro"), 403, `{"success":false,"error":"Admin access required"}`},
		{"requireAdmin server_admin", d.RequireAdmin, account(7, "server_admin"), 0, ""},
		{"requireAdmin global_admin", d.RequireAdmin, account(7, "global_admin"), 0, ""},
		{"requireAdmin admin", d.RequireAdmin, account(7, "admin"), 0, ""},
		{"requireAdmin super_admin", d.RequireAdmin, account(7, "super_admin"), 0, ""},
		{"requireRole signed out", d.RequireRole("operator"), signedOut, 401, `{"success":false,"error":"Unauthorized"}`},
		{"requireRole other role", d.RequireRole("operator"), account(7, "viewer"), 403, `{"success":false,"error":"Forbidden"}`},
		{"requireRole same role", d.RequireRole("operator"), account(7, "operator"), 0, ""},
		{"requireRole admin", d.RequireRole("operator"), account(7, "admin"), 0, ""},
		{"requireRole server_admin is no super admin", d.RequireRole("operator"), account(7, "server_admin"), 403, `{"success":false,"error":"Forbidden"}`},
		{"requirePermission signed out", d.RequirePermission("server.config"), signedOut, 401, `{"success":false,"error":"Unauthorized"}`},
		{"requirePermission missing", d.RequirePermission("server.config"), withPermissions("viewer", "device.view"), 403, `{"success":false,"error":"Permission denied: server.config"}`},
		{"requirePermission held", d.RequirePermission("device.view"), withPermissions("viewer", "device.view"), 0, ""},
		{"requirePermission admin", d.RequirePermission("server.config"), account(7, "admin"), 0, ""},
		{"requirePermission lookup failed", d.RequirePermission("device.view"), &access.Context{UserID: 7, HasUser: true, Role: "viewer", Loaded: true, PermissionErr: http.ErrAbortHandler}, 503,
			`{"success":false,"error":"Permission service unavailable","code":"permission_service_unavailable"}`},
		{"requireAnyPermission none", d.RequireAnyPermission("user.edit", "user.create"), withPermissions("viewer", "device.view"), 403, `{"success":false,"error":"Permission denied: user.edit or user.create"}`},
		{"requireAnyPermission one", d.RequireAnyPermission("user.edit", "device.view"), withPermissions("viewer", "device.view"), 0, ""},
		{"requireRdClientAuth signed out", d.RequireRdClientAuth("device.connect"), signedOut, 401, `{"success":false,"error":"Unauthorized. Please log in."}`},
		{"requireRdClientAuth pro", d.RequireRdClientAuth(""), account(7, "pro"), 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`},
		{"requireRdClientAuth missing", d.RequireRdClientAuth("device.connect"), withPermissions("viewer", "device.view"), 403, `{"success":false,"error":"Permission denied: device.connect"}`},
		{"requireRdClientAuth no permission asked", d.RequireRdClientAuth(""), withPermissions("viewer"), 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Below a mount the address still starts with /api/
			rec, reached := guarded(d, c.guard, c.who, "POST", "/api/bd/thing?x=1")
			if c.status == 0 {
				if !reached || rec.Body.Len() != 0 {
					t.Fatalf("refused with %d %s, want the request to pass", rec.Code, rec.Body.String())
				}
				return
			}
			if reached {
				t.Fatal("the handler ran")
			}
			if rec.Code != c.status || rec.Body.String() != c.body {
				t.Errorf("got %d %s\nwant %d %s", rec.Code, rec.Body.String(), c.status, c.body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", got)
			}
			if rec.Header().Get("ETag") == "" {
				t.Error("no ETag on the JSON answer")
			}
		})
	}
}

func TestGuardsAnswerPagesWithoutABuild(t *testing.T) {
	d := &Deps{}
	signedOut := account(0, "")
	cases := []struct {
		name     string
		guard    Middleware
		who      *access.Context
		target   string
		cookie   string
		status   int
		location string
		body     string
	}{
		{"requireAuth signed out", d.RequireAuth, signedOut, "/tickets/7?tab=notes", "", 302, "/login?return=%2Ftickets%2F7%3Ftab%3Dnotes", ""},
		{"requireAuth signed out with an old cookie", d.RequireAuth, signedOut, "/tickets", "bd.sid=gone", 302, "/login?expired=1&return=%2Ftickets", ""},
		{"requireAuth pro", d.RequireAuth, account(7, "pro"), "/tickets", "", 302, "/login", ""},
		{"requireAdmin signed out", d.RequireAdmin, signedOut, "/tickets", "bd.sid=gone", 302, "/login", ""},
		{"requireAdmin viewer", d.RequireAdmin, account(7, "viewer"), "/tickets", "", 403, "", "Forbidden"},
		{"requireRole signed out", d.RequireRole("operator"), signedOut, "/tickets", "", 302, "/login", ""},
		{"requireRole viewer", d.RequireRole("operator"), account(7, "viewer"), "/tickets", "", 403, "", "Forbidden"},
		{"requirePermission signed out", d.RequirePermission("device.view"), signedOut, "/tickets", "", 302, "/login", ""},
		{"requirePermission missing", d.RequirePermission("server.config"), withPermissions("viewer"), "/tickets", "", 403, "", "Forbidden"},
		{"requirePermission lookup failed", d.RequirePermission("device.view"), &access.Context{UserID: 7, HasUser: true, Role: "viewer", Loaded: true, PermissionErr: http.ErrAbortHandler}, "/tickets", "", 503, "", "Permission service unavailable"},
		{"requireRdClientAuth signed out", d.RequireRdClientAuth(""), signedOut, "/remote/ABC?x=1", "", 302, "/remote/login?return=%2Fremote%2FABC%3Fx%3D1", ""},
		{"requireRdClientAuth signed out elsewhere", d.RequireRdClientAuth(""), signedOut, "/other?return=/remote/files", "", 302, "/remote/login?return=%2Fremote%2Ffiles", ""},
		{"requireRdClientAuth return given twice", d.RequireRdClientAuth(""), signedOut, "/other?return=/remote/a&return=/remote/b", "", 302, "/remote/login?return=%2Fremote", ""},
		{"requireRdClientAuth pro", d.RequireRdClientAuth(""), account(7, "pro"), "/remote/ABC", "", 302, "/remote/login?return=%2Fremote%2FABC", ""},
		{"requireRdClientAuth missing", d.RequireRdClientAuth("device.connect"), withPermissions("viewer"), "/remote/ABC", "", 403, "", "Forbidden"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			header := []string{"Accept", "text/html"}
			if c.cookie != "" {
				header = append(header, "Cookie", c.cookie)
			}
			rec, reached := guarded(d, c.guard, c.who, "GET", c.target, header...)
			if reached {
				t.Fatal("the handler ran")
			}
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, c.status, rec.Body.String())
			}
			if c.status == 302 {
				if got := rec.Header().Get("Location"); got != c.location {
					t.Errorf("Location = %q, want %q", got, c.location)
				}
				if want := "<p>Found. Redirecting to " + htmlEscaper.Replace(c.location) + "</p>"; rec.Body.String() != want {
					t.Errorf("body = %q, want %q", rec.Body.String(), want)
				}
				if rec.Header().Get("Vary") != "Accept" || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
					t.Errorf("headers = %v", rec.Header())
				}
				return
			}
			if rec.Body.String() != c.body || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Errorf("got %q as %q, want %q as plain text", rec.Body.String(), rec.Header().Get("Content-Type"), c.body)
			}
		})
	}
}

func TestGuardsAnswerPagesWithTheWebApp(t *testing.T) {
	d := &Deps{WebUI: webui.New(webui.Options{Dir: "../webui/testdata/site", Production: true})}

	rec, _ := guarded(d, d.RequireAdmin, account(7, "viewer"), "GET", "/tickets", "Accept", "text/html")
	page := rec.Body.String()
	if rec.Code != 403 || !strings.Contains(page, `<meta name="bd-error" content="403">`) || !strings.Contains(page, "console app") {
		t.Errorf("status %d, want the app's page with the 403 mark: %.200s", rec.Code, page)
	}
	if strings.Contains(page, "data-message") {
		t.Errorf("the 403 page carries a message: %.200s", page)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	failed := &access.Context{UserID: 7, HasUser: true, Role: "viewer", Loaded: true, PermissionErr: http.ErrAbortHandler}
	rec, _ = guarded(d, d.RequirePermission("device.view"), failed, "GET", "/tickets", "Accept", "text/html")
	page = rec.Body.String()
	if rec.Code != 503 || !strings.Contains(page, `<meta name="bd-error" content="500"`) || !strings.Contains(page, `data-message="Permission service unavailable"`) {
		t.Errorf("status %d, want the 500 page with the message and status 503: %.300s", rec.Code, page)
	}

	// The panel access guard's refusal of a page write goes the same way
	denied := access.Decision{Answer: access.AnswerErrorPage, Status: 403,
		Page: access.ErrorPage{Kind: 403, Message: access.MsgPanelSectionDenied, Text: access.MsgPanelSectionDenied}}
	out := httptest.NewRecorder()
	d.Refuse(out, httptest.NewRequest("POST", "/users", nil), denied)
	if out.Code != 403 || !strings.Contains(out.Body.String(), `data-message="You do not have permission to access this panel section"`) {
		t.Errorf("status %d body %.300s", out.Code, out.Body.String())
	}
}

func TestRedirectBodies(t *testing.T) {
	for accept, want := range map[string]string{
		"":                            "Found. Redirecting to /login?a=1&b='x'",
		"text/html":                   "<p>Found. Redirecting to /login?a=1&amp;b=&#39;x&#39;</p>",
		"application/json":            "",
		"*/*":                         "Found. Redirecting to /login?a=1&b='x'",
		"text/html, text/plain;q=0.5": "<p>Found. Redirecting to /login?a=1&amp;b=&#39;x&#39;</p>",
	} {
		r := httptest.NewRequest("GET", "/x", nil)
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		rec.Header().Set("Vary", "Accept-Encoding")
		Redirect(rec, r, "/login?a=1&b='x'")
		if rec.Code != 302 || rec.Body.String() != want || rec.Header().Get("Location") != "/login?a=1&b='x'" {
			t.Errorf("Accept %q: %d %q", accept, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != "Accept-Encoding, Accept" {
			t.Errorf("Vary = %q, want one header with both names", got)
		}
	}
	rec := httptest.NewRecorder()
	Redirect(rec, httptest.NewRequest("HEAD", "/x", nil), "/login")
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "28" {
		t.Errorf("HEAD: body %q, Content-Length %q", rec.Body.String(), rec.Header().Get("Content-Length"))
	}
}

func TestSingleQueryValue(t *testing.T) {
	for query, want := range map[string]string{
		"return=/remote/a":            "/remote/a",
		"x=1&return=%2Fremote%2Fa":    "/remote/a",
		"return=a+b":                  "a b",
		"return=/a&return=/b":         "",
		"return[]=/a":                 "",
		"return[x]=/a&return=/b":      "",
		"other=/a":                    "",
		"":                            "",
		"return":                      "",
		"return=%zz":                  "%zz",
		"%72eturn=/remote/a":          "/remote/a",
		"returned=/remote/a&return=x": "x",
	} {
		if got := singleQueryValue(query, "return"); got != want {
			t.Errorf("singleQueryValue(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestCookie(t *testing.T) {
	cases := []struct {
		header string
		want   string
		ok     bool
	}{
		{"bd_ui_shell=ux36", "ux36", true},
		{"a=1; bd_ui_shell=ux36; b=2", "ux36", true},
		{"a=1;bd_ui_shell = ux36 ;b=2", "ux36", true},
		{"bd_ui_shell=first; bd_ui_shell=second", "first", true},
		{`bd_ui_shell="ux36"`, "ux36", true},
		{`bd_ui_shell="`, "", true},
		{"bd_ui_shell=ux%33%36", "ux36", true},
		{"bd_ui_shell=ux%zz", "ux%zz", true},
		{"bd_ui_shell=%ff", "%ff", true},
		{`bd_ui_shell=j:"ux36"`, "ux36", true},
		{`bd_ui_shell=j%3A%22ux36%22`, "ux36", true},
		{`bd_ui_shell=j:{"a":1}`, "", false},
		{`bd_ui_shell=j:0`, "j:0", true},
		{`bd_ui_shell=j:{broken`, "j:{broken", true},
		{"flag; bd_ui_shell=ux36", "ux36", true},
		{"other=ux36", "", false},
		{"xbd_ui_shell=ux36", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if c.header != "" {
			r.Header.Set("Cookie", c.header)
		}
		got, ok := Cookie(r, "bd_ui_shell")
		if got != c.want || ok != c.ok {
			t.Errorf("Cookie(%q) = %q, %v; want %q, %v", c.header, got, ok, c.want, c.ok)
		}
	}
}

func TestSetCookie(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	rec := httptest.NewRecorder()
	SetCookie(rec, "bd_ui_shell", "ux36", CookieOptions{MaxAge: 365 * 24 * time.Hour, SameSite: "lax", Now: now})
	SetCookie(rec, "s", "a b|c", CookieOptions{HTTPOnly: true, Secure: true, SameSite: "strict", Path: "/api"})
	got := rec.Header().Values("Set-Cookie")
	want := []string{
		"bd_ui_shell=ux36; Max-Age=31536000; Path=/; Expires=Wed, 10 Nov 2027 12:00:00 GMT; SameSite=Lax",
		"s=a%20b%7Cc; Path=/api; HttpOnly; Secure; SameSite=Strict",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Set-Cookie = %q\nwant %q", got, want)
	}
}

func TestAccessorsWithoutThePipeline(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/x?y=1", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	if Session(r) != nil || User(r) != nil || UserID(r) != 0 || Lang(r) != "" || T(r, "nav.devices", nil) != "nav.devices" {
		t.Error("accessors on a bare request must answer empty values")
	}
	if ClientIP(r) != "203.0.113.9" || OriginalURL(r) != "/api/x?y=1" || HasPermission(r, "device.view") {
		t.Errorf("ClientIP %q, OriginalURL %q", ClientIP(r), OriginalURL(r))
	}
}
