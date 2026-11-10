package access

import (
	"errors"
	"testing"
)

func signedIn(role string) *Context {
	return &Context{UserID: 5, HasUser: true, Role: role}
}

// wantJSON checks a refusal's status and exact body.
func wantJSON(t *testing.T, label string, d Decision, status int, body string) {
	t.Helper()
	if d.Answer != AnswerJSON || d.Status != status || d.Body == nil || jsJSON(t, d.Body) != body {
		got := "<no body>"
		if d.Body != nil {
			got = jsJSON(t, d.Body)
		}
		t.Errorf("%s: answer %d status %d body %s\n want status %d body %s", label, d.Answer, d.Status, got, status, body)
	}
}

func wantRedirect(t *testing.T, label string, d Decision, location string) {
	t.Helper()
	if d.Answer != AnswerRedirect || d.Status != 302 || d.Location != location {
		t.Errorf("%s: %+v, want a 302 to %s", label, d, location)
	}
}

func wantForbiddenPage(t *testing.T, label string, d Decision) {
	t.Helper()
	if d.Answer != AnswerErrorPage || d.Status != 403 || d.Page != (ErrorPage{Kind: 403, Text: "Forbidden"}) {
		t.Errorf("%s: %+v, want the 403 page", label, d)
	}
}

// Jest: middleware.auth.test.js "requireAuth", authGuards.apiMount.test.js.
func TestRequireAuth(t *testing.T) {
	none := &Context{}
	wantJSON(t, "API without a session", none.RequireAuth("/api/test"), 401,
		`{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`)
	// Inside a router mounted under /api the original URL still decides.
	wantJSON(t, "mounted API route", none.RequireAuth("/api/thing/open"), 401,
		`{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`)
	for _, page := range []string{"/dashboard", "/pages/open", "/apix", "/api", "/API/users", ""} {
		if d := none.RequireAuth(page); d.Answer != AnswerLoginRedirect || d.Status != 302 || d.DestroySession {
			t.Errorf("page %q without a session: %+v, want the sign-in redirect with the way back", page, d)
		}
	}
	if d := signedIn("admin").RequireAuth("/api/test"); !d.Passed() {
		t.Errorf("signed in: %+v", d)
	}
	if d := (&Context{UserID: 5}).RequireAuth("/api/test"); !d.Passed() {
		t.Errorf("a session without its user record passes this check: %+v", d)
	}

	// Pro accounts lose their session.
	d := signedIn("pro").RequireAuth("/api/test")
	wantJSON(t, "pro on the API", d, 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`)
	if !d.DestroySession {
		t.Error("pro on the API: the session must be destroyed")
	}
	d = signedIn("pro").RequireAuth("/dashboard")
	wantRedirect(t, "pro on a page", d, "/login")
	if !d.DestroySession {
		t.Error("pro on a page: the session must be destroyed")
	}
}

// Jest: middleware.auth.test.js "requireRole", permissions.superAdmin.test.js.
func TestRequireRole(t *testing.T) {
	cases := []struct {
		role     string
		required string
		pass     bool
	}{
		{"admin", "admin", true},
		{"operator", "admin", false},
		{"admin", "operator", true},
		{"super_admin", "operator", true},
		{"operator", "operator", true},
		{"viewer", "operator", false},
		{"global_admin", "operator", false},
		// Changing role overrides is for super admins; the older admin passes too.
		{"super_admin", "super_admin", true},
		{"admin", "super_admin", true},
		{"server_admin", "super_admin", false},
		{"global_admin", "super_admin", false},
		{"operator", "super_admin", false},
	}
	for _, c := range cases {
		label := c.role + " for " + c.required
		d := signedIn(c.role).RequireRole(c.required, "/api/admin")
		if c.pass {
			if !d.Passed() {
				t.Errorf("%s: %+v, want pass", label, d)
			}
			continue
		}
		wantJSON(t, label, d, 403, `{"success":false,"error":"Forbidden"}`)
		wantForbiddenPage(t, label+" (page)", signedIn(c.role).RequireRole(c.required, "/admin"))
	}
	none := &Context{}
	wantJSON(t, "no session", none.RequireRole("admin", "/api/admin"), 401, `{"success":false,"error":"Unauthorized"}`)
	wantRedirect(t, "no session (page)", none.RequireRole("admin", "/admin"), "/login")
}

// Jest: authGuards.apiMount.test.js, policies.reads.test.js (the role rule).
func TestRequireAdmin(t *testing.T) {
	for role, pass := range map[string]bool{
		"super_admin": true, "admin": true, "global_admin": true, "server_admin": true,
		"operator": false, "viewer": false, "guest_issuer": false, "pro": false, "": false,
	} {
		ctx := &Context{UserID: 5, HasUser: role != "", Role: role}
		d := ctx.RequireAdmin("/api/thing/admin")
		if pass {
			if !d.Passed() {
				t.Errorf("%s: %+v, want pass", role, d)
			}
			continue
		}
		wantJSON(t, role, d, 403, `{"success":false,"error":"Admin access required"}`)
		wantForbiddenPage(t, role+" (page)", ctx.RequireAdmin("/policies"))
	}
	none := &Context{}
	wantJSON(t, "no session", none.RequireAdmin("/api/x"), 401, `{"success":false,"error":"Unauthorized"}`)
	wantRedirect(t, "no session (page)", none.RequireAdmin("/policies"), "/login")
}

// Jest: authGuards.apiMount.test.js, reports.permission.test.js and
// automation.access.test.js (the permission rule), security/03-auth-bypass
// (the effective set wins).
func TestRequirePermission(t *testing.T) {
	wantJSON(t, "viewer needs server.config", signedIn("viewer").RequirePermission("server.config", "/api/thing/config"), 403,
		`{"success":false,"error":"Permission denied: server.config"}`)
	wantForbiddenPage(t, "viewer needs server.config (page)", signedIn("viewer").RequirePermission("server.config", "/pages/config"))
	if d := signedIn("server_admin").RequirePermission("server.config", "/api/thing/config"); !d.Passed() {
		t.Errorf("server_admin: %+v", d)
	}

	// Reports need audit.view: guest issuers and pro accounts are refused.
	for role, pass := range map[string]bool{"viewer": true, "operator": true, "server_admin": true, "guest_issuer": false, "pro": false} {
		if d := signedIn(role).RequirePermission("audit.view", "/api/reports/types"); d.Passed() != pass {
			t.Errorf("%s and audit.view: passed %v, want %v", role, d.Passed(), pass)
		}
	}

	none := &Context{}
	wantJSON(t, "no session", none.RequirePermission("device.view", "/api/x"), 401, `{"success":false,"error":"Unauthorized"}`)
	wantRedirect(t, "no session (page)", none.RequirePermission("device.view", "/devices"), "/login")

	// A failed lookup answers 503 before anything else is looked at.
	failed := func(role string) *Context {
		c := signedIn(role)
		c.Loaded, c.PermissionErr = true, errors.New("lookup failed")
		return c
	}
	wantJSON(t, "failed lookup", failed("operator").RequirePermission("device.view", "/api/devices"), 503,
		`{"success":false,"error":"Permission service unavailable","code":"permission_service_unavailable"}`)
	d := failed("operator").RequirePermission("device.view", "/devices")
	page := ErrorPage{Kind: 500, Message: "Permission service unavailable", Text: "Permission service unavailable"}
	if d.Answer != AnswerErrorPage || d.Status != 503 || d.Page != page {
		t.Errorf("failed lookup (page): %+v", d)
	}
	// The same error on a context that was never loaded is not a 503.
	unloaded := signedIn("operator")
	unloaded.PermissionErr = errors.New("lookup failed")
	if d := unloaded.RequirePermission("device.view", "/api/devices"); !d.Passed() {
		t.Errorf("error without a load: %+v", d)
	}

	// The effective set decides, not the built-in map.
	withOverride := signedIn("viewer")
	withOverride.Loaded, withOverride.Permissions = true, newPermissionSet([]string{"server.config"})
	if d := withOverride.RequirePermission("server.config", "/api/x"); !d.Passed() {
		t.Errorf("override grants: %+v", d)
	}
	wantJSON(t, "override takes away", withOverride.RequirePermission("device.view", "/api/x"), 403,
		`{"success":false,"error":"Permission denied: device.view"}`)
}

func TestRequireAnyPermission(t *testing.T) {
	if d := signedIn("operator").RequireAnyPermission("user.edit", "user.view"); !d.Passed() {
		t.Errorf("operator has user.view: %+v", d)
	}
	wantJSON(t, "viewer has neither", signedIn("viewer").RequireAnyPermission("user.edit", "user.view"), 403,
		`{"success":false,"error":"Permission denied: user.edit or user.view"}`)
	failed := signedIn("operator")
	failed.Loaded, failed.PermissionErr = true, errors.New("lookup failed")
	wantJSON(t, "failed lookup", failed.RequireAnyPermission("user.edit", "user.view"), 503,
		`{"success":false,"error":"Permission service unavailable","code":"permission_service_unavailable"}`)
}

// Jest: middleware.auth.test.js "requireRdClientAuth".
func TestRequireRdClientAuth(t *testing.T) {
	none := &Context{}
	wantRedirect(t, "page without a session", none.RequireRdClientAuth("device.connect", "/remote", ""), "/remote/login?return=%2Fremote")
	wantRedirect(t, "viewer page with a query", none.RequireRdClientAuth("", "/remote/abc?transport=cdap", ""),
		"/remote/login?return=%2Fremote%2Fabc%3Ftransport%3Dcdap")
	wantRedirect(t, "another page falls back to its return parameter", none.RequireRdClientAuth("", "/pairing", "/remote/dev1"),
		"/remote/login?return=%2Fremote%2Fdev1")
	wantRedirect(t, "an unsafe return parameter is dropped", none.RequireRdClientAuth("", "/pairing", "https://evil.example/remote"),
		"/remote/login?return=%2Fremote")
	wantRedirect(t, "the sign-in page is no way back", none.RequireRdClientAuth("", "/remote/login", ""), "/remote/login?return=%2Fremote")
	wantJSON(t, "API without a session", none.RequireRdClientAuth("", "/api/remote/sessions", ""), 401,
		`{"success":false,"error":"Unauthorized. Please log in."}`)

	if d := signedIn("operator").RequireRdClientAuth("device.connect", "/remote", ""); !d.Passed() {
		t.Errorf("operator with device.connect: %+v", d)
	}
	if d := signedIn("viewer").RequireRdClientAuth("", "/remote", ""); !d.Passed() {
		t.Errorf("no permission asked: %+v", d)
	}
	wantForbiddenPage(t, "viewer lacks device.connect", signedIn("viewer").RequireRdClientAuth("device.connect", "/remote", ""))
	wantJSON(t, "viewer lacks device.connect (API)", signedIn("viewer").RequireRdClientAuth("device.connect", "/api/remote/x", ""), 403,
		`{"success":false,"error":"Permission denied: device.connect"}`)

	// A failed lookup is a plain refusal here, not a 503.
	failed := signedIn("operator")
	failed.Loaded, failed.PermissionErr = true, errors.New("lookup failed")
	wantJSON(t, "failed lookup", failed.RequireRdClientAuth("device.connect", "/api/remote/x", ""), 403,
		`{"success":false,"error":"Permission denied: device.connect"}`)

	d := signedIn("pro").RequireRdClientAuth("", "/remote/abc", "")
	wantRedirect(t, "pro on a page", d, "/remote/login?return=%2Fremote%2Fabc")
	if !d.DestroySession {
		t.Error("pro: the session must be destroyed")
	}
	d = signedIn("pro").RequireRdClientAuth("", "/api/remote/x", "")
	wantJSON(t, "pro on the API", d, 403, `{"success":false,"error":"Pro accounts can only access the RustDesk desktop client API"}`)
	if !d.DestroySession {
		t.Error("pro on the API: the session must be destroyed")
	}
}

// Jest: middleware.auth.test.js "isSafeRdClientReturnUrl".
func TestRdClientReturnURL(t *testing.T) {
	for input, want := range map[string]bool{
		"/remote": true, "/remote/abc123": true, "/remote?transport=cdap": true,
		"/login": false, "/remoteevil": false, "/remote/login": false, "//evil.com/remote": false, "/remote/%0aevil": false,
		"": false, "remote": false, "/remote/login/x": false, "/remote/x/../login": false, "/remote/..": false,
		"/remote\\x": false, "/remote/%5Cx": false, "/remote/%00": false, "/\t/evil.example/remote": false,
		"/REMOTE": false,
	} {
		if got := IsSafeRdClientReturnURL(input); got != want {
			t.Errorf("IsSafeRdClientReturnURL(%q) = %v, want %v", input, got, want)
		}
	}
	for input, want := range map[string]string{
		"/remote/dev1?transport=cdap": "/remote/dev1?transport=cdap",
		"/remote/a/../b":              "/remote/b",
		"/remote/./a":                 "/remote/a",
		"/remote/a b?q=c d#e f":       "/remote/a%20b?q=c%20d#e%20f",
		"/remote/é":                   "/remote/%C3%A9",
		"/remote?":                    "/remote",
		"/remote#":                    "/remote",
		"/re\tmote":                   "/remote",
		"/remote/x ":                  "/remote/x",
	} {
		if got, ok := NormalizeRdClientReturnURL(input); !ok || got != want {
			t.Errorf("NormalizeRdClientReturnURL(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"https://evil.example/remote", "/remote/login?return=%2Fremote"} {
		if got, ok := NormalizeRdClientReturnURL(input); ok {
			t.Errorf("NormalizeRdClientReturnURL(%q) = %q, want a refusal", input, got)
		}
	}
}

func TestIsAPIRequest(t *testing.T) {
	for url, want := range map[string]bool{
		"/api/x": true, "/api/": true, "/api/files/1?x=1": true,
		"/api": false, "/apix": false, "/API/x": false, "/users": false, "": false, "api/x": false, "/x/api/y": false,
	} {
		if got := IsAPIRequest(url); got != want {
			t.Errorf("IsAPIRequest(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestErrorStatus(t *testing.T) {
	if got := ErrorStatus(&StatusError{Status: 404, Message: "x"}); got != 404 {
		t.Errorf("status error: %d", got)
	}
	if got := ErrorStatus(errors.New("plain")); got != 500 {
		t.Errorf("plain error: %d", got)
	}
}
