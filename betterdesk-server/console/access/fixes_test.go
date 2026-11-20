package access

import (
	"context"
	"testing"
)

// The vectors in testdata were recorded from the console's Node modules
// with Node's guard rules and catalog. The bug fixes changed both tables
// on purpose. The oracle tests therefore run on Node's tables
// (withNodeTables): they pin what the code does with a table. The changes
// of the tables are pinned here against Node's, each with the bug it
// fixes. Where a fix changed the code itself, the oracle test lists the
// vectors that fix changes.

// ruleFix is a change of the guard's rule table against Node's: a rule
// that goes (of two equal ones the later), a rule that comes, or both for
// a rule replaced in its place. Rules that come without replacing one are
// at the end of the table, in the order listed.
type ruleFix struct {
	bug         string
	remove, add Rule
}

var ruleFixes = []ruleFix{
	// The routes for people of the file transfer group answer at /api/bd
	// too.
	{bug: "FT8", add: Rule{Prefix: "/api/bd/transfer", Section: "files"}},
	{bug: "FT8", add: Rule{Prefix: "/api/bd/transfers", Section: "files"}},

	// The API rules of these sections named prefixes no route has.
	{bug: "PA4, G16", remove: Rule{Prefix: "/api/organizations", Section: "organizations"}, add: Rule{Prefix: "/api/panel/org", Section: "organizations"}},
	{bug: "PA4", remove: Rule{Prefix: "/api/scaling", Section: "scaling"}, add: Rule{Prefix: "/api/panel/scaling", Section: "scaling"}},
	{bug: "PA4", remove: Rule{Prefix: "/api/cross-platform", Section: "cross-platform"}, add: Rule{Prefix: "/api/panel/cross-platform", Section: "cross-platform"}},
	{bug: "PA4", remove: Rule{Prefix: "/api/languages", Section: "languages"}, add: Rule{Prefix: "/api/panel/languages", Section: "languages"}},
	{bug: "PA4", remove: Rule{Prefix: "/api/resource-control", Section: "resource-control"}, add: Rule{Prefix: "/api/panel/resource-control", Section: "resource-control"}},
	{bug: "PA4 (F2)", remove: Rule{Prefix: "/api/security-audit", Section: "security-audit"}, add: Rule{Prefix: "/api/panel/security-audit", Section: "security-audit"}},
	// fleet and chat had no API rule.
	{bug: "PA4", add: Rule{Prefix: "/api/panel/fleet", Section: "fleet"}},
	{bug: "PA4", add: Rule{Prefix: "/api/chat", Section: "chat"}},
	// The second address of the fleet, scaling and resource control routes.
	{bug: "PA4, I3", add: Rule{Prefix: "/api/bd/api/panel/fleet", Section: "fleet"}},
	{bug: "PA4, I3", add: Rule{Prefix: "/api/bd/api/panel/scaling", Section: "scaling"}},
	{bug: "PA4, I3", add: Rule{Prefix: "/api/bd/api/panel/resource-control", Section: "resource-control"}},

	// The Settings page is for every account, Client setup for every role;
	// Tokens, Policies and Client branding ask other permissions than
	// server.config.
	{bug: "PA3 (F1)", remove: Rule{Prefix: "/keys", Section: "settings"}},
	{bug: "PA3 (F1)", remove: Rule{Prefix: "/tokens", Section: "settings"}},
	{bug: "PA3 (F1)", remove: Rule{Prefix: "/policies", Section: "settings"}},
	{bug: "PA3 (F1)", remove: Rule{Prefix: "/client-branding", Section: "settings"}},
	{bug: "PA3 (F1)", remove: Rule{Prefix: "/settings", Section: "settings"}},
	// What the shell and the Settings page read for every account.
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/info"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/server-info"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/appearance"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/branding"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/themes"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/fonts"}},
	{bug: "PA3 (F1)", add: Rule{Prefix: "/api/settings/restart-status"}},
	// The audit trail asks audit.view, the security audit section's read
	// permission.
	{bug: "PA3 (F2)", add: Rule{Prefix: "/api/settings/audit", Section: "security-audit"}},
	// What every account reads of itself.
	{bug: "PA3 (F3)", add: Rule{Prefix: "/api/users/me"}},
	{bug: "PA3 (F3)", add: Rule{Prefix: "/api/users/avatars"}},
	{bug: "PA3 (F3)", add: Rule{Prefix: "/api/panel/access/effective"}},
	// The network tools belong to the toolkit.
	{bug: "PA3 (F4)", add: Rule{Prefix: "/api/network/ping", Section: "toolkit"}},
	{bug: "PA3 (F4)", add: Rule{Prefix: "/api/network/tcp", Section: "toolkit"}},
	{bug: "PA3 (F4)", add: Rule{Prefix: "/api/network/http", Section: "toolkit"}},
	{bug: "PA3 (F4)", add: Rule{Prefix: "/api/network/resolve", Section: "toolkit"}},
	// The server's clock, and the commercialization routes beside billing.
	{bug: "PA3 (F11)", add: Rule{Prefix: "/api/panel/billing/timesync"}},
	{bug: "PA3 (F11)", add: Rule{Prefix: "/api/panel/billing/clock"}},
	{bug: "PA3 (F11)", add: Rule{Prefix: "/api/panel/commercialization", Section: "commercialization"}},
	// The guest link routes authenticate by the link; remote targets are
	// what the remote section's permissions are about.
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/guest/access-links/validate"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/guest/access-links/peers"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/guest/mesh"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/remote/guest-info"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/remote/viewer"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/remote/login-info"}},
	{bug: "PA14 (F12)", add: Rule{Prefix: "/api/remote-targets", Section: "remote"}},
	// The reports page was listed twice.
	{bug: "PA3 (F6), RP1", remove: Rule{Prefix: "/reports", Section: "reports"}},
	// /attestation is the device identity page; the server attestation
	// page had no rule.
	{bug: "PA5 (F9)", remove: Rule{Prefix: "/attestation", Section: "server-attestation"}, add: Rule{Prefix: "/server-attestation", Section: "server-attestation"}},
	// The data guard's routes for people answer at /api/bd too.
	{bug: "DG6", add: Rule{Prefix: "/api/bd/policies", Section: "dataguard"}},
	{bug: "DG6", add: Rule{Prefix: "/api/bd/events", Section: "dataguard"}},
	{bug: "DG6", add: Rule{Prefix: "/api/bd/stats", Section: "dataguard"}},
}

// sectionFix is a catalog section whose permissions differ from Node's.
type sectionFix struct {
	bug         string
	key         string
	read, write Permission
}

var sectionFixes = []sectionFix{
	// Organizations are read with user.view, their devices are assigned
	// with org.manage_devices (DECISIONS A39).
	{bug: "PA4, G16", key: "organizations", read: "user.view", write: "org.manage_devices"},
	// The cross-platform routes and menu entry ask device.view.
	{bug: "PA4", key: "cross-platform", read: "device.view", write: "device.edit"},
	// Reading targets and status asks device.view, changing them
	// server.config.
	{bug: "PA3 (F4)", key: "network", read: "device.view", write: "server.config"},
	// The toolkit page and its wake-on-LAN route ask device.connect.
	{bug: "PA3 (F7)", key: "toolkit", read: "device.connect", write: "device.connect"},
	// Filing a work report asks billing.reports; the other writes check
	// billing.manage themselves.
	{bug: "PA3 (F11)", key: "commercialization", read: "billing.view", write: "billing.reports"},
	// Every reports route asks audit.view, also the writes.
	{bug: "PA3 (F6), RP1", key: "reports", read: "audit.view", write: "audit.view"},
	// All server attestation routes ask server.attestation.
	{bug: "PA5 (F9)", key: "server-attestation", read: "server.attestation", write: "server.attestation"},
	// Listing, approving and rejecting registrations ask
	// enrollment.approve, so enrollment.manage alone gives no level.
	{bug: "UM12 (F10)", key: "registrations", read: "enrollment.approve", write: "enrollment.approve"},
	// The data guard's policies are read with device.view; its events and
	// statistics ask audit.view themselves; the writes ask server.config.
	{bug: "PA3 (F5)", key: "dataguard", read: "device.view", write: "server.config"},
	// Every automation route and the menu entry ask server.config.
	{bug: "PA3 (F8)", key: "automation", read: "server.config", write: "server.config"},
}

// nodeTables returns the guard's rules and the catalog's sections as the
// Node console had them.
func nodeTables(t *testing.T) ([]Rule, []Section) {
	t.Helper()
	var rules struct {
		Rules []Rule `json:"rules"`
	}
	loadVectors(t, "find_rule", &rules)
	var normalize struct {
		Catalog struct {
			Sections []struct {
				Key   string  `json:"key"`
				Label string  `json:"label"`
				Group string  `json:"group"`
				Read  *string `json:"readPermission"`
				Write *string `json:"writePermission"`
			} `json:"sections"`
		} `json:"catalog"`
	}
	loadVectors(t, "normalize", &normalize)
	permission := func(p *string) Permission {
		if p == nil {
			return ""
		}
		return Permission(*p)
	}
	catalog := make([]Section, 0, len(normalize.Catalog.Sections))
	for _, s := range normalize.Catalog.Sections {
		catalog = append(catalog, Section{s.Key, s.Label, s.Group, permission(s.Read), permission(s.Write)})
	}
	if len(rules.Rules) < 60 || len(catalog) < 30 {
		t.Fatalf("Node's tables look empty: %d rules, %d sections", len(rules.Rules), len(catalog))
	}
	return rules.Rules, catalog
}

// withNodeTables puts Node's rule table and catalog in place of the Go
// ones until the test ends. The tests of this package do not run in
// parallel, so nothing else reads the tables meanwhile.
func withNodeTables(t *testing.T) {
	t.Helper()
	rules, catalog := nodeTables(t)
	goRules, goCatalog := RouteRules, Catalog
	RouteRules, Catalog = rules, catalog
	t.Cleanup(func() { RouteRules, Catalog = goRules, goCatalog })
}

func TestTablesAreNodesWithTheFixes(t *testing.T) {
	rules, catalog := nodeTables(t)
	want := append([]Rule(nil), rules...)
	for _, fix := range ruleFixes {
		if fix.remove == (Rule{}) {
			want = append(want, fix.add)
			continue
		}
		found := false
		for i := len(want) - 1; i >= 0 && !found; i-- {
			if want[i] != fix.remove {
				continue
			}
			found = true
			if fix.add == (Rule{}) {
				want = append(want[:i:i], want[i+1:]...)
			} else {
				want[i] = fix.add
			}
		}
		if !found {
			t.Fatalf("%s: Node has no rule %+v", fix.bug, fix.remove)
		}
	}
	for i := 0; i < len(RouteRules) || i < len(want); i++ {
		var got, wanted Rule
		if i < len(RouteRules) {
			got = RouteRules[i]
		}
		if i < len(want) {
			wanted = want[i]
		}
		if got != wanted {
			t.Errorf("rule %d is %+v, Node's table with the fixes has %+v", i, got, wanted)
		}
	}

	wantCatalog := append([]Section(nil), catalog...)
	for _, fix := range sectionFixes {
		found := false
		for i := range wantCatalog {
			if wantCatalog[i].Key == fix.key {
				wantCatalog[i].ReadPermission, wantCatalog[i].WritePermission = fix.read, fix.write
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: Node has no section %q", fix.bug, fix.key)
		}
	}
	if len(Catalog) != len(wantCatalog) {
		t.Fatalf("%d catalog sections, Node's catalog with the fixes has %d", len(Catalog), len(wantCatalog))
	}
	for i := range Catalog {
		if Catalog[i] != wantCatalog[i] {
			t.Errorf("section %d is %+v, Node's catalog with the fixes has %+v", i, Catalog[i], wantCatalog[i])
		}
	}
}

// defaultRoles are the roles below the super admins, whose answers the
// panel access fixes change.
var defaultRoles = []string{"viewer", "operator", "guest_issuer", "global_admin", "server_admin"}

// roleContext is the access context of a signed-in account with a default
// role and no policy rows: its built-in permissions and its role's ceiling
// in every section.
func roleContext(t *testing.T, role string) *Context {
	t.Helper()
	permissions := defaultPermissions(role)
	s := fakeService(&fakeData{}, &fakeRoles{permissions: map[string][]string{role: permissions}})
	policy, err := s.ResolveForUser(context.Background(), PolicyUser{ID: 7, Role: role})
	if err != nil {
		t.Fatalf("policy of %s: %v", role, err)
	}
	return &Context{UserID: 7, HasUser: true, Role: role, Loaded: true, Permissions: newPermissionSet(permissions), Policy: policy}
}

// routeAnswer is what a default role gets from the guard and a route's own
// permission check: "ok", "guard" (the guard refuses) or "route" (the
// guard lets it through and the route refuses).
func routeAnswer(t *testing.T, role, method, path, permission string) string {
	t.Helper()
	c := roleContext(t, role)
	if got := c.Guard(method, path); !got.Decision.Passed() {
		return "guard"
	}
	if permission != "" && !c.HasPermission(permission) {
		return "route"
	}
	return "ok"
}

// routeCase is a route with its own check and the answer every default
// role gets, in the order of defaultRoles.
type routeCase struct {
	method, path, permission string
	answers                  [5]string
}

func checkRouteCases(t *testing.T, cases []routeCase) {
	t.Helper()
	for _, c := range cases {
		for i, role := range defaultRoles {
			if got := routeAnswer(t, role, c.method, c.path, c.permission); got != c.answers[i] {
				t.Errorf("%s %s (%s) as %s: %s, want %s", c.method, c.path, c.permission, role, got, c.answers[i])
			}
		}
	}
}
