package access

import (
	"errors"
	"strings"
	"testing"
)

// guardPaths lists paths with the section the guard must find; "" means no
// rule matches.
var guardPaths = []struct {
	path    string
	section string
}{
	// every rule at its own address
	{"/", "dashboard"}, {"/inventory", "devices"}, {"/tickets", "tickets"}, {"/help-requests", "help-requests"},
	{"/automation", "automation"}, {"/network", "network"}, {"/registrations", "registrations"}, {"/cdap", "cdap"},
	{"/fleet", "fleet"}, {"/chat", "chat"}, {"/scaling", "scaling"}, {"/cross-platform", "cross-platform"},
	{"/reports", "reports"}, {"/agents", "agents"}, {"/toolkit", "toolkit"}, {"/keys", ""},
	{"/tokens", ""}, {"/policies", ""}, {"/attestation", "server-attestation"},
	{"/dataguard", "dataguard"}, {"/security-audit", "security-audit"}, {"/permissions", "permissions"},
	{"/languages", "languages"}, {"/resource-control", "resource-control"}, {"/server-management", "server-management"},
	{"/client-branding", ""}, {"/cdap-studio", "cdap"}, {"/users", "users"}, {"/api/users", "users"},
	{"/api/panel/user-groups", "users"}, {"/api/panel/access", "users"}, {"/api/guest", "guests"},
	{"/api/folders", "folders"}, {"/organizations", "organizations"}, {"/api/panel/org", "organizations"},
	{"/devices", "devices"}, {"/api/devices", "devices"}, {"/api/device-groups", "devices"}, {"/api/tags", "devices"},
	{"/api/panel/strategies", "strategies"}, {"/strategies", "strategies"}, {"/folders", "folders"}, {"/remote", "remote"},
	{"/api/remote", "remote"},
	{"/api/tickets", "tickets"}, {"/api/automation", "automation"}, {"/api/network", "network"},
	{"/api/dataguard", "dataguard"}, {"/api/reports", "reports"}, {"/api/activity", "activity"},
	{"/api/files", "files"}, {"/api/panel/scaling", "scaling"}, {"/api/panel/cross-platform", "cross-platform"},
	{"/api/toolkit", "toolkit"}, {"/api/security-audit", "security-audit"}, {"/api/panel/languages", "languages"},
	{"/api/panel/resource-control", "resource-control"}, {"/api/server-management", "server-management"},
	{"/api/panel/fleet", "fleet"}, {"/api/chat", "chat"},
	{"/api/server-attestation", "server-attestation"}, {"/api/registrations", "registrations"},
	{"/api/settings", "settings"}, {"/settings", ""}, {"/api/panel/billing", "commercialization"},
	{"/commercialization", "commercialization"}, {"/activity", "activity"},

	// below a prefix, and with a trailing slash
	{"/users/", "users"}, {"/users/42", "users"}, {"/api/users/42", "users"}, {"/api/users/42/avatar", "users"},
	{"/strategies/new", "strategies"}, {"/strategiesx", ""},
	{"/api/panel/access/users/1", "users"}, {"/api/panel/user-groups/abc/members", "users"},
	{"/api/settings/server-info", ""}, {"/api/settings/audit", "settings"}, {"/api/reports/generate", "reports"},

	// what every account reads below /api/settings has no section (F1)
	{"/api/settings/info", ""}, {"/api/settings/appearance", ""}, {"/api/settings/branding", ""},
	{"/api/settings/branding/profiles/1/apply", ""}, {"/api/settings/themes", ""}, {"/api/settings/themes/x/apply", ""},
	{"/api/settings/fonts/local", ""}, {"/api/settings/restart-status", ""}, {"/api/settings/brandingx", "settings"},
	{"/api/settings/ldap", "settings"}, {"/api/settings/backup/stats", "settings"}, {"/api/settings/infos", "settings"},
	{"/api/devices/ABC/rename", "devices"}, {"/api/guest/links", "guests"}, {"/devices//x", "devices"},
	{"/api/remote/viewer/abc", "remote"}, {"/remote/login", "remote"}, {"/remote/abc", "remote"},
	{"/cdap/devices/1", "cdap"}, {"/server-management/", "server-management"},

	// the longest rule wins
	{"/cdap-studio/flows/1", "cdap"}, {"/api/panel/billing/packages", "commercialization"},
	{"/api/device-groups/g1/members", "devices"},

	// near misses: a prefix matches only whole path segments
	{"/devicesX", ""}, {"/usersX", ""}, {"/users-extra", ""}, {"/users.json", ""}, {"/api/usersX", ""},
	{"/api/panel/accessories", ""}, {"/api/remote-targets", ""}, {"/api/remote-targets/1", ""},
	{"/reportsX", ""}, {"/cdapX", ""}, {"/cdap-studioX", ""}, {"/setting", ""}, {"/api/setting", ""},
	{"/x/users", ""}, {"/app/users", ""}, {"users", ""}, {"/users%2Fx", ""}, {" /users", ""},
	{"/users ", ""}, {"/users?tab=1", ""}, {"", ""}, {"//", ""}, {"/index", ""},

	// letter case is exact
	{"/USERS", ""}, {"/Users", ""}, {"/API/users", ""}, {"/api/Users", ""}, {"/api/USERS/1", ""}, {"/Settings", ""},

	// the second mount below /api/bd of the file transfer routes (FT8)
	{"/api/bd/transfer", "files"}, {"/api/bd/transfers", "files"}, {"/api/bd/transfers/abc/download", "files"},
	{"/api/bd/file-transfer", ""}, {"/api/bd/transfersx", ""},

	// the real prefixes of the sections whose rules named none (PA4)
	{"/api/panel/org/1", "organizations"}, {"/api/panel/fleet/tasks", "fleet"}, {"/api/panel/scaling/relays", "scaling"},
	{"/api/panel/languages/de/missing", "languages"}, {"/api/panel/resource-control/policies", "resource-control"},
	{"/api/panel/cross-platform/protocols", "cross-platform"}, {"/api/chat/search", "chat"},
	{"/api/bd/api/panel/fleet/tasks", "fleet"}, {"/api/bd/api/panel/scaling/relays", "scaling"},
	{"/api/bd/api/panel/resource-control/devices", "resource-control"},
	{"/api/organizations", ""}, {"/api/scaling", ""}, {"/api/cross-platform", ""}, {"/api/languages", ""},
	{"/api/resource-control", ""}, {"/api/panel/orgx", ""}, {"/api/chatx", ""},

	// API paths without a rule
	{"/api", ""}, {"/api/", ""}, {"/api/panel", ""},
	{"/api/panel/roles", ""}, {"/api/panel/role-permissions", ""}, {"/api/panel/security-audit/events", ""},
	{"/api/cdap/devices", ""}, {"/api/bd/commands", ""}, {"/api/auth/login", ""}, {"/api/bd/api/registrations", ""},
	{"/api/console/bootstrap", ""}, {"/api/inventory", ""}, {"/api/keys", ""}, {"/api/tenants", ""},

	// pages without a rule
	{"/login", ""}, {"/logout", ""}, {"/pairing", ""}, {"/server-attestation", ""}, {"/generator", ""}, {"/portal", ""},
}

func TestFindRule(t *testing.T) {
	for _, c := range guardPaths {
		rule, ok := FindRule(c.path)
		if (c.section == "") == ok || rule.Section != c.section {
			t.Errorf("FindRule(%q) = %q (found %v), want %q", c.path, rule.Section, ok, c.section)
		}
	}
	// Jest: panelAccessGuard.test.js, "maps page and API paths to the same section".
	for path, section := range map[string]string{"/users": "users", "/api/users/42": "users", "/api/settings/ldap": "settings"} {
		if rule, ok := FindRule(path); !ok || rule.Section != section {
			t.Errorf("FindRule(%q) = %+v", path, rule)
		}
	}
	if _, ok := FindRule("/unknown"); ok {
		t.Error("FindRule(/unknown) found a rule")
	}
}

// No rule of the table lies below another one today, so the longest-match
// and first-of-equals rules are checked on a table that has such rules.
func TestFindRuleLongestMatch(t *testing.T) {
	rules := []Rule{
		{Prefix: "/api/users", Section: "users"},
		{Path: "/api/users", Section: "exact, listed later"},
		{Prefix: "/api/users/me", Section: "self"},
		{Prefix: "/api/users/me/keys", Section: "keys"},
		{Path: "/api/users/me/keys/1", Section: "one key"},
		{Prefix: "/api", Section: "api"},
	}
	for path, want := range map[string]string{
		"/api/users":             "users",
		"/api/users/7":           "users",
		"/api/users/me":          "self",
		"/api/users/me/":         "self",
		"/api/users/mex":         "users",
		"/api/users/me/keys":     "keys",
		"/api/users/me/keys/2":   "keys",
		"/api/users/me/keys/1":   "one key",
		"/api/users/me/keys/1/x": "keys",
		"/api/other":             "api",
		"/api":                   "api",
		"/apix":                  "",
	} {
		rule, ok := findRuleIn(rules, path)
		if rule.Section != want || ok != (want != "") {
			t.Errorf("findRuleIn(%q) = %q, want %q", path, rule.Section, want)
		}
	}
}

func TestPageRules(t *testing.T) {
	pages := PageRules()
	if len(pages) != 32 {
		t.Fatalf("%d page rules, want 32", len(pages))
	}
	if pages[0] != (Rule{Path: "/", Section: "dashboard"}) {
		t.Errorf("first page rule is %+v", pages[0])
	}
	for _, rule := range pages {
		if strings.HasPrefix(rule.pattern(), "/api/") {
			t.Errorf("page rules hold the API rule %+v", rule)
		}
	}
	// The JSON the bootstrap sends: "path" for the exact rule, "prefix" for the rest.
	if got := jsJSON(t, pages[:2]); got != `[{"path":"/","section":"dashboard"},{"prefix":"/inventory","section":"devices"}]` {
		t.Errorf("page rules as JSON: %s", got)
	}
}

// Jest: panelAccessGuard.test.js, "requires read for reads and write for mutations".
func TestRequiredLevel(t *testing.T) {
	for method, want := range map[string]Level{
		"GET": LevelRead, "HEAD": LevelRead, "OPTIONS": LevelRead,
		"POST": LevelWrite, "PUT": LevelWrite, "PATCH": LevelWrite, "DELETE": LevelWrite,
		"get": LevelWrite, "": LevelWrite, "PROPFIND": LevelWrite,
	} {
		if got := RequiredLevel(method); got != want {
			t.Errorf("RequiredLevel(%q) = %q, want %q", method, got, want)
		}
	}
}

// TestGuardVerdicts runs every path of guardPaths with every method for an
// account whose level is read in each section, and for one without a
// session.
func TestGuardVerdicts(t *testing.T) {
	methods := []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"}
	checked := 0
	for _, c := range guardPaths {
		api := strings.HasPrefix(c.path, "/api/")
		for _, method := range methods {
			label := method + " " + c.path
			read := method == "GET" || method == "HEAD" || method == "OPTIONS"

			signedOut := &Context{Policy: policyWith(c.section, LevelNone)}
			if got := signedOut.Guard(method, c.path); got.Verdict != VerdictNoRule || got.Section != "" || signedOut.Section != "" || !got.Decision.Passed() {
				t.Errorf("%s without a session: %+v", label, got)
			}

			ctx := &Context{UserID: 7, HasUser: true, Role: "viewer", Policy: policyWith(c.section, LevelRead)}
			got := ctx.Guard(method, c.path)
			checked++
			if got.Section != c.section || ctx.Section != c.section {
				t.Errorf("%s: section %q, want %q", label, got.Section, c.section)
				continue
			}
			var want Verdict
			switch {
			case c.section == "":
				want = VerdictNoRule
			case !api && (method == "GET" || method == "HEAD"):
				want = VerdictPage
			case read:
				want = VerdictAllowed
			default:
				want = VerdictDenied
			}
			if got.Verdict != want {
				t.Errorf("%s: verdict %d, want %d", label, got.Verdict, want)
				continue
			}
			if want != VerdictDenied {
				if !got.Decision.Passed() {
					t.Errorf("%s: must pass, got %+v", label, got.Decision)
				}
				continue
			}
			if api {
				wantBody := `{"success":false,"error":"Panel access denied: ` + c.section + `","code":"panel_access_denied"}`
				if got.Decision.Answer != AnswerJSON || got.Decision.Status != 403 || jsJSON(t, got.Decision.Body) != wantBody {
					t.Errorf("%s: %+v body %s", label, got.Decision, jsJSON(t, got.Decision.Body))
				}
			} else {
				page := ErrorPage{Kind: 403, Message: "You do not have permission to access this panel section", Text: "You do not have permission to access this panel section"}
				if got.Decision.Answer != AnswerErrorPage || got.Decision.Status != 403 || got.Decision.Page != page {
					t.Errorf("%s: %+v", label, got.Decision)
				}
			}
		}
	}
	if checked < 150 {
		t.Fatalf("only %d method and path combinations ran", checked)
	}
}

// Jest: panelAccessGuard.test.js, "allows read-only access while denying
// mutations" and "serves a page load, so the app shows its own no-access
// view; the API stays refused".
func TestGuardLevels(t *testing.T) {
	viewer := func(level Level) *Context {
		return &Context{UserID: 7, HasUser: true, Role: "viewer", Policy: policyWith("users", level)}
	}
	cases := []struct {
		level  Level
		method string
		path   string
		want   Verdict
		status int
	}{
		{LevelRead, "GET", "/api/users", VerdictAllowed, 0},
		{LevelRead, "POST", "/api/users", VerdictDenied, 403},
		{LevelWrite, "POST", "/api/users", VerdictAllowed, 0},
		{LevelWrite, "DELETE", "/api/users/3", VerdictAllowed, 0},
		{LevelNone, "GET", "/users", VerdictPage, 0},
		{LevelNone, "HEAD", "/users", VerdictPage, 0},
		{LevelNone, "GET", "/api/users", VerdictDenied, 403},
		{LevelNone, "OPTIONS", "/api/users", VerdictDenied, 403},
		{LevelNone, "POST", "/users", VerdictDenied, 403},
		{LevelRead, "OPTIONS", "/users", VerdictAllowed, 0},
		{LevelNone, "OPTIONS", "/users", VerdictDenied, 403},
	}
	for _, c := range cases {
		got := viewer(c.level).Guard(c.method, c.path)
		if got.Verdict != c.want || got.Decision.Status != c.status || got.Section != "users" {
			t.Errorf("%s %s at %s: verdict %d status %d, want %d %d", c.method, c.path, c.level, got.Verdict, got.Decision.Status, c.want, c.status)
		}
	}
}

func TestGuardSessionStates(t *testing.T) {
	policy := policyWith("users", LevelNone)
	for name, ctx := range map[string]*Context{
		"no session":           {Policy: policy},
		"user id without user": {UserID: 7, Policy: policy},
		"user without user id": {HasUser: true, Role: "viewer", Policy: policy},
	} {
		if got := ctx.Guard("POST", "/api/users"); got.Verdict != VerdictNoRule || ctx.Section != "" {
			t.Errorf("%s: the guard must not act, got %+v", name, got)
		}
	}
}

func TestGuardPolicyUnavailable(t *testing.T) {
	broken := func() *Context {
		return &Context{UserID: 7, HasUser: true, Role: "viewer", PolicyErr: errors.New("database is down")}
	}
	got := broken().Guard("GET", "/api/users")
	wantBody := `{"success":false,"error":"Panel access policy unavailable","code":"panel_access_policy_unavailable"}`
	if got.Verdict != VerdictUnavailable || got.Decision.Answer != AnswerJSON || got.Decision.Status != 503 || jsJSON(t, got.Decision.Body) != wantBody {
		t.Errorf("API: %+v body %s", got, jsJSON(t, got.Decision.Body))
	}
	got = broken().Guard("POST", "/users")
	page := ErrorPage{Kind: 500, Message: "Panel access policy unavailable", Text: "Panel access policy unavailable"}
	if got.Verdict != VerdictUnavailable || got.Decision.Answer != AnswerErrorPage || got.Decision.Status != 503 || got.Decision.Page != page {
		t.Errorf("page write: %+v", got)
	}
	// A page load passes even then, and a path without a rule is not looked at.
	if got := broken().Guard("GET", "/users"); got.Verdict != VerdictPage {
		t.Errorf("page load: %+v", got)
	}
	if got := broken().Guard("POST", "/api/cdap/devices"); got.Verdict != VerdictNoRule {
		t.Errorf("no rule: %+v", got)
	}
	// The error wins over a policy that would allow.
	ctx := broken()
	ctx.Policy = policyWith("users", LevelWrite)
	if got := ctx.Guard("GET", "/api/users"); got.Verdict != VerdictUnavailable {
		t.Errorf("error with a policy: %+v", got)
	}
}

// The guard has no exception for super admins: a row that lowers a section
// locks them out of its API.
func TestGuardAppliesToSuperAdmins(t *testing.T) {
	for _, role := range []string{"admin", "super_admin"} {
		ctx := &Context{UserID: 1, HasUser: true, Role: role, Policy: policyWith("users", LevelNone)}
		if got := ctx.Guard("GET", "/api/users"); got.Verdict != VerdictDenied {
			t.Errorf("%s with users at none: %+v", role, got)
		}
		// Their permission checks still pass on that path.
		if !ctx.HasPermission("user.view") {
			t.Errorf("%s lost user.view", role)
		}
	}
}

// A signed-in account without a loaded policy is refused on guarded API
// paths.
func TestGuardWithoutPolicy(t *testing.T) {
	ctx := &Context{UserID: 7, HasUser: true, Role: "admin"}
	if got := ctx.Guard("GET", "/api/users"); got.Verdict != VerdictDenied {
		t.Errorf("no policy: %+v", got)
	}
}

// FT8: the file transfer routes for people that answer a second time
// below /api/bd keep the section of their first address.
func TestGuardCoversTheBDMounts(t *testing.T) {
	closed := func() *Context {
		return &Context{UserID: 7, HasUser: true, Role: "global_admin", Policy: policyWith("files", LevelNone)}
	}
	for _, c := range []struct{ method, path, section string }{
		{"POST", "/api/bd/transfer", "files"}, {"GET", "/api/bd/transfers", "files"},
		{"GET", "/api/bd/transfers/t1", "files"}, {"POST", "/api/bd/transfers/t1/cancel", "files"},
		{"GET", "/api/bd/transfers/t1/download", "files"},
	} {
		got := closed().Guard(c.method, c.path)
		if got.Verdict != VerdictDenied || got.Section != c.section || got.Decision.Status != 403 {
			t.Errorf("%s %s: %+v, want refused in %s", c.method, c.path, got, c.section)
		}
	}
	// The device routes beside them have no section: devices call them
	// without a session.
	for _, path := range []string{"/api/bd/file-transfer", "/api/bd/file-transfer/t1", "/api/bd/commands", "/api/bd/inventory"} {
		if got := closed().Guard("POST", path); got.Verdict != VerdictNoRule {
			t.Errorf("POST %s: %+v, want no rule", path, got)
		}
	}
}
