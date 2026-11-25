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
	// The routes for people of the file transfer group answer at /api/nd
	// too.
	{bug: "FT8", add: Rule{Prefix: "/api/nd/transfer", Section: "files"}},
	{bug: "FT8", add: Rule{Prefix: "/api/nd/transfers", Section: "files"}},

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
	{bug: "PA4, I3", add: Rule{Prefix: "/api/nd/api/panel/fleet", Section: "fleet"}},
	{bug: "PA4, I3", add: Rule{Prefix: "/api/nd/api/panel/scaling", Section: "scaling"}},
	{bug: "PA4, I3", add: Rule{Prefix: "/api/nd/api/panel/resource-control", Section: "resource-control"}},

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
	// The data guard's routes for people answer at /api/nd too.
	{bug: "DG6", add: Rule{Prefix: "/api/nd/policies", Section: "dataguard"}},
	{bug: "DG6", add: Rule{Prefix: "/api/nd/events", Section: "dataguard"}},
	{bug: "DG6", add: Rule{Prefix: "/api/nd/stats", Section: "dataguard"}},
	// The registration routes for people answer at /api/nd too (I3).
	{bug: "PA15", add: Rule{Prefix: "/api/nd/api/registrations", Section: "registrations"}},
	// The Activity section's new pages (L494 plan step 9) belong to the
	// sections of their old addresses.
	{bug: "L494", add: Rule{Prefix: "/security-checks", Section: "security-audit"}},
	{bug: "L494", add: Rule{Prefix: "/billing", Section: "commercialization"}},
	{bug: "L494", add: Rule{Prefix: "/devices/add"}},
}

// sectionFix is a catalog section whose permissions differ from Node's,
// and its label when that changed too.
type sectionFix struct {
	bug         string
	key         string
	read, write Permission
	label       string
}

// sectionAdd is a catalog section Node did not have, placed after
// another.
type sectionAdd struct {
	bug, after string
	section    Section
}

var sectionAdds = []sectionAdd{
	// Team chat has its own section beside Support chat (A242).
	{bug: "A242", after: "chat", section: Section{"team-chat", "Team chat", "operations", "chat.team", "chat.team"}},
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
	// Chat is Support chat; Team chat is a section of its own (A242).
	{bug: "A242", key: "chat", read: "chat.support", write: "chat.support", label: "Support chat"},
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
	features := nodeFeatures(t)
	goRules, goCatalog, goFeatures := RouteRules, Catalog, ClientFeatures
	RouteRules, Catalog, ClientFeatures = rules, catalog, features
	t.Cleanup(func() { RouteRules, Catalog, ClientFeatures = goRules, goCatalog, goFeatures })
}

// nodeNavSections is the navigation as Node had it: five sections, the
// rules, server and billing pages under Settings, and the server terminal
// after the server status page (A194 removed it). The console's own table
// moved on (plan steps 6, 7 and 9 of L494, nav_test.go); the oracle replays
// Node's answers, so it runs with this table.
var nodeNavSections = []NavSection{
	{ID: "devices", Label: "nav.devices", Icon: "devices", Pages: []NavPage{
		navPage("dashboard", "home dashboard start", "/", "nav.dashboard", withRule(navAlways), core),
		navPage("devices", "computers peers machines connect", "/devices", "folders.all_devices", withPermission("device.view"), core),
		navPage("registrations", "enrollment approve new device registration pending", "/registrations", "nav.registrations", withPermission("enrollment.approve"), core, countID("reg-sidebar-badge")),
		navPage("activate-agent", "pair agent code activation", "/?activateAgent=1", "nav.pair_agent", withPermission("enrollment.approve"),
			func(p *NavPage) { p.Access, p.ID = "registrations", "nav-activate-agent" }),
		navPage("remote", "web client browser remote control", "/remote", "nav.remote", withPermission("device.connect"), core, external),
		navPage("inventory", "hardware software", "/inventory", "nav.inventory", withPermission("device.view")),
		navPage("network", "ping monitor latency targets", "/network", "nav.network", withPermission("device.view")),
		navPage("fleet", "fleet bulk tasks workflows compliance", "/fleet", "nav.fleet", withPermission("org.manage_devices")),
		navPage("cross-platform", "cross-platform matrix features os", "/cross-platform", "nav.cross_platform", withPermission("device.view")),
		navPage("cdap", "cdap iot scada sensors gateway", "/cdap", "nav.cdap", withPermission("cdap.view")),
	}},
	{ID: "support", Label: "nav.section_support", Icon: "support_agent", Pages: []NavPage{
		navPage("help-requests", "help support requests", "/help-requests", "nav.help_requests", withPermission("device.view"), core),
		navPage("tickets", "issues", "/tickets", "nav.tickets", withPermission("device.view"), core),
		// Node's chat.access is Support chat and Team chat since A242
		navPage("chat", "messages", "/chat", "nav.chat", withPermission("chat.support", "chat.team"), core),
		navPage("toolkit", "toolkit ping dns port ssl diagnostics", "/toolkit", "nav.toolkit", withPermission("device.connect")),
	}},
	{ID: "people", Label: "nav.section_people", Icon: "group", Pages: []NavPage{
		navPage("users", "accounts people operators", "/users", "nav.users", withPermission("user.view"), core),
		navPage("organizations", "tenants customers", "/organizations", "nav.organizations", withPermission("org.create", "org.edit", "org.manage_users")),
		navPage("permissions", "roles rights access", "/permissions", "nav.permissions", withRule(navSuperAdmin)),
	}},
	{ID: "activity", Label: "nav.activity", Icon: "history", Pages: []NavPage{
		navPage("security-audit", "audit log who banned security events", "/security-audit", "nav.security_audit", withPermission("audit.view"), core),
		navPage("activity", "app usage activity monitoring", "/activity", "activity.title", withPermission("audit.view")),
		navPage("reports", "reports export trends", "/reports", "nav.reports", withPermission("audit.view"), core),
		navPage("commercialization:sessions", "sessions billing minutes work reports", "/commercialization?tab=sessions", "commercialization.tabs.sessions", withPermission("billing.view")),
	}},
	{ID: "settings", Label: "nav.settings", Icon: "settings", Pages: []NavPage{
		navPage("settings:general", "settings relay address public endpoints connection strategy meshcentral", "/settings", "settings.tab_general", withRule(navAlways), core, inGroup("nav.group_console")),
		navPage("settings:branding", "branding theme colours colors logo dark mode", "/settings?tab=branding", "settings.tab_branding", withRule(navAlways), inGroup("nav.group_console")),
		navPage("settings:auth", "authentication 2fa two-factor totp password ldap sso oidc", "/settings?tab=auth", "settings.tab_auth", withRule(navAlways), inGroup("nav.group_console")),
		navPage("settings:backup", "backup restore", "/settings?tab=backup", "settings.tab_backup", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("settings:updates", "updates version upgrade", "/settings?tab=updates", "updates.tab_title", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("settings:email", "email smtp mail", "/settings?tab=email", "settings.tab_email", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("settings:time", "time zone timezone ntp clock", "/settings?tab=time", "settings.tab_time", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("settings:advanced", "advanced config files restart", "/settings?tab=advanced", "settings.tab_advanced", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("languages", "languages translations i18n", "/languages", "nav.languages", withPermission("server.config"), inGroup("nav.group_console")),
		navPage("keys", "keys public key qr client setup rustdesk config", "/keys", "nav.keys", withRule(navAlways), core, inGroup("nav.group_clients")),
		navPage("tokens", "tokens enrollment mode", "/tokens", "nav.tokens", withPermission("enrollment.manage"), inGroup("nav.group_clients")),
		navPage("pairing", "pairing code", "/pairing", "nav.pairing", withPermission("enrollment.approve"), inGroup("nav.group_clients")),
		navPage("agents", "agents installers generator", "/agents", "nav.agents", withRule(navAdminRole), inGroup("nav.group_clients"),
			func(p *NavPage) { p.AlsoActiveFor = []string{"generator"} }),
		navPage("client-branding", "client branding white label", "/client-branding", "nav.client_branding", withPermission("branding.edit"), inGroup("nav.group_clients")),
		navPage("policies", "policies rules", "/policies", "nav.policies", withRule(navAdminRole), inGroup("nav.group_rules")),
		navPage("automation", "automation alerts commands", "/automation", "nav.automation", withPermission("server.config"), inGroup("nav.group_rules")),
		navPage("resource-control", "resource control usb drives disks", "/resource-control", "nav.resource_control", withPermission("server.config"), inGroup("nav.group_rules")),
		navPage("dataguard", "dataguard dlp data loss", "/dataguard", "nav.dataguard", withPermission("server.config"), inGroup("nav.group_rules")),
		navPage("attestation", "attestation fingerprint hardware identity", "/attestation", "nav.attestation", withRule(navAdminRole), inGroup("nav.group_rules")),
		navPage("server-management:overview", "server management status health cpu memory uptime", "/server-management?tab=overview", "nav.server_status", withPermission("server.config"), core, inGroup("nav.group_server")),
		navPage("server-management:terminal", "shell console ssh", "/server-management?tab=terminal", "server_mgmt.tab_terminal", withPermission("server.config"), inGroup("nav.group_server")),
		navPage("server-management:files", "file browser", "/server-management?tab=files", "server_mgmt.tab_files", withPermission("server.config"), inGroup("nav.group_server")),
		navPage("server-management:services", "systemd services", "/server-management?tab=services", "server_mgmt.tab_services", withPermission("server.config"), inGroup("nav.group_server")),
		navPage("scaling", "scaling relays", "/scaling", "nav.scaling", withPermission("server.config"), inGroup("nav.group_server")),
		navPage("server-attestation", "server attestation benchmark load test badge", "/server-attestation", "nav.server_attestation", withPermission("server.attestation"), inGroup("nav.group_server")),
		navPage("commercialization:packages", "packages contracts billing", "/commercialization?tab=packages", "commercialization.tabs.packages", withPermission("billing.view"), inGroup("nav.group_billing")),
		navPage("cdap-studio", "sdk studio flows widgets", "/cdap-studio", "nav.sdk_studio", withRole("operator"), inGroup("nav.group_developer")),
	}},
}

// withNodeNav puts Node's navigation in place of the console's until the
// test ends, so that BuildNav and the bootstrap are compared with Node's.
func withNodeNav(t *testing.T) {
	t.Helper()
	goSections := NavSections
	NavSections = nodeNavSections
	t.Cleanup(func() { NavSections = goSections })
}

// withNodeFeatures puts Node's client features in place of the Go ones
// until the test ends.
func withNodeFeatures(t *testing.T) {
	t.Helper()
	features := nodeFeatures(t)
	goFeatures := ClientFeatures
	ClientFeatures = features
	t.Cleanup(func() { ClientFeatures = goFeatures })
}

// nodeFeatures returns the client features as the Node console had them.
func nodeFeatures(t *testing.T) []ClientFeature {
	t.Helper()
	var normalize struct {
		Catalog struct {
			Features []ClientFeature `json:"features"`
		} `json:"catalog"`
	}
	loadVectors(t, "normalize", &normalize)
	if len(normalize.Catalog.Features) < 5 {
		t.Fatalf("Node's client features look empty: %d", len(normalize.Catalog.Features))
	}
	return normalize.Catalog.Features
}

// featureFix is a change of the client features against Node's: a
// feature that goes, or one whose label changes.
type featureFix struct {
	bug, key string
	// label is the new label; "" removes the feature.
	label string
}

var featureFixes = []featureFix{
	// RustDesk has one permission for tunnels and port forwarding, and
	// nothing reads Client actions (DECISIONS A162).
	{bug: "L41 (A162)", key: "tunnel", label: "Tunnels and port forwarding"},
	{bug: "L41 (A162)", key: "port_forwarding"},
	{bug: "L41 (A162)", key: "client_actions"},
}

func TestClientFeaturesAreNodesWithTheFixes(t *testing.T) {
	want := append([]ClientFeature(nil), nodeFeatures(t)...)
	for _, fix := range featureFixes {
		found := false
		for i := range want {
			if want[i].Key != fix.key {
				continue
			}
			found = true
			if fix.label == "" {
				want = append(want[:i:i], want[i+1:]...)
			} else {
				want[i].Label = fix.label
			}
			break
		}
		if !found {
			t.Fatalf("%s: Node has no client feature %q", fix.bug, fix.key)
		}
	}
	if len(ClientFeatures) != len(want) {
		t.Fatalf("%d client features, Node's with the fixes has %d: %+v", len(ClientFeatures), len(want), ClientFeatures)
	}
	for i := range want {
		if ClientFeatures[i] != want[i] {
			t.Errorf("client feature %d is %+v, Node's with the fixes has %+v", i, ClientFeatures[i], want[i])
		}
	}
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
				if fix.label != "" {
					wantCatalog[i].Label = fix.label
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: Node has no section %q", fix.bug, fix.key)
		}
	}
	for _, add := range sectionAdds {
		at := -1
		for i := range wantCatalog {
			if wantCatalog[i].Key == add.after {
				at = i + 1
			}
		}
		if at < 0 {
			t.Fatalf("%s: no section %q to add %q after", add.bug, add.after, add.section.Key)
		}
		wantCatalog = append(wantCatalog[:at:at], append([]Section{add.section}, wantCatalog[at:]...)...)
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
