package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// consoleRoutes returns the paths of the console's routes, from the route
// list of the contract suite, with every parameter written as "x".
func consoleRoutes(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "contract", "testdata", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Routes []struct {
			Path        string `json:"path"`
			App         string `json:"app"`
			Unreachable bool   `json:"unreachable"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, route := range list.Routes {
		if route.App != "console" || route.Unreachable {
			continue
		}
		parts := strings.Split(route.Path, "/")
		for i, part := range parts {
			if strings.HasPrefix(part, ":") || part == "*" {
				parts[i] = "x"
			}
		}
		paths = append(paths, strings.Join(parts, "/"))
	}
	if len(paths) < 500 {
		t.Fatalf("only %d console routes", len(paths))
	}
	return paths
}

// PA4: a rule for an API prefix no route has leaves the section's API
// unguarded while the page is hidden. Every API rule names routes.
func TestEveryAPIRuleMatchesARoute(t *testing.T) {
	routes := consoleRoutes(t)
	for _, rule := range RouteRules {
		if !strings.HasPrefix(rule.pattern(), apiPrefix) {
			continue
		}
		found := false
		for _, path := range routes {
			if rule.matches(path) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("rule %+v matches no console route", rule)
		}
	}
}

// PA4: the sections whose API rules named prefixes no route has now guard
// their routes, so a row that closes a section closes its API.
func TestClosedSectionsCloseTheirAPI(t *testing.T) {
	for _, c := range []struct{ method, path, section string }{
		{"GET", "/api/panel/org", "organizations"}, {"GET", "/api/panel/org/1/peer-credentials", "organizations"},
		{"POST", "/api/panel/org/1/devices", "organizations"},
		{"GET", "/api/panel/scaling/relays", "scaling"}, {"GET", "/api/bd/api/panel/scaling/relays", "scaling"},
		{"GET", "/api/panel/cross-platform/distribution", "cross-platform"},
		{"GET", "/api/panel/languages", "languages"}, {"POST", "/api/panel/languages/de/fix", "languages"},
		{"GET", "/api/panel/resource-control/devices", "resource-control"},
		{"GET", "/api/bd/api/panel/resource-control/devices", "resource-control"},
		{"GET", "/api/panel/fleet/tasks", "fleet"}, {"GET", "/api/bd/api/panel/fleet/tasks", "fleet"},
		{"POST", "/api/chat/upload", "chat"}, {"GET", "/api/chat/search", "chat"},
	} {
		c2 := &Context{UserID: 1, HasUser: true, Role: "super_admin", Policy: policyWith(c.section, LevelNone)}
		if got := c2.Guard(c.method, c.path); got.Verdict != VerdictDenied || got.Section != c.section {
			t.Errorf("%s %s with %s at none: %+v", c.method, c.path, c.section, got)
		}
	}
}

// PA15: the registration routes answer at /api/bd/api/registrations too
// (I3). A policy that closes the registrations section closes them there
// as at /api/registrations, and every default role gets the same answer
// at both mounts.
func TestRegistrationsAreGuardedAtBothMounts(t *testing.T) {
	routes := []struct{ method, path, permission string }{
		{"GET", "", "enrollment.approve"}, {"GET", "/count", "enrollment.approve"},
		{"GET", "/enrollment-ui", "enrollment.approve"}, {"GET", "/5", "enrollment.approve"},
		{"PUT", "/5/approve", "enrollment.approve"}, {"PUT", "/5/reject", "enrollment.approve"},
		{"DELETE", "/5", "enrollment.manage"},
	}
	for _, r := range routes {
		path := "/api/bd/api/registrations" + r.path
		closed := &Context{UserID: 1, HasUser: true, Role: "super_admin", Policy: policyWith("registrations", LevelNone)}
		if got := closed.Guard(r.method, path); got.Verdict != VerdictDenied || got.Section != "registrations" {
			t.Errorf("%s %s with registrations at none: %+v", r.method, path, got)
		}
		for _, role := range defaultRoles {
			first := routeAnswer(t, role, r.method, "/api/registrations"+r.path, r.permission)
			if second := routeAnswer(t, role, r.method, path, r.permission); second != first {
				t.Errorf("%s %s as %s: %s, at /api/registrations %s", r.method, path, role, second, first)
			}
		}
	}
}

// PA4 with A39 (G16): organizations are read with user.view, which the
// roles of the pages that read them have (organizations, billing,
// policies); the devices with a stored password and every write need
// org.manage_devices.
func TestOrganizationsNeedUserView(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/panel/org", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1/devices", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1/settings", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1/address-book", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1/device-groups", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/panel/org/1/peer-credentials", "org.manage_devices", [5]string{"guard", "ok", "guard", "ok", "route"}},
		{"POST", "/api/panel/org/1/devices", "org.manage_devices", [5]string{"guard", "ok", "guard", "ok", "guard"}},
		{"PUT", "/api/panel/org/1", "org.edit", [5]string{"guard", "route", "guard", "ok", "guard"}},
		{"GET", "/api/panel/cross-platform/protocols", "device.view", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/panel/scaling/relays", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"GET", "/api/panel/fleet/tasks", "org.manage_devices", [5]string{"guard", "ok", "guard", "ok", "guard"}},
		{"POST", "/api/chat/upload", "chat.access", [5]string{"ok", "ok", "guard", "ok", "guard"}},
	})
}

// PA3 (F1): the Settings page, its branding and the server facts the shell
// reads are for every account; the branding writes ask branding.edit,
// which global_admin has; the rest of /api/settings stays server.config.
func TestSettingsReadsAreForEveryAccount(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/settings/info", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/server-info", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/branding", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/appearance", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/themes", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/fonts/local", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/settings/restart-status", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"POST", "/api/settings/branding", "branding.edit", [5]string{"route", "route", "route", "ok", "route"}},
		{"POST", "/api/settings/themes/dark/apply", "branding.edit", [5]string{"route", "route", "route", "ok", "route"}},
		{"DELETE", "/api/settings/fonts/Inter", "branding.edit", [5]string{"route", "route", "route", "ok", "route"}},
		{"GET", "/api/settings/ldap", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"PUT", "/api/settings/ldap", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
	})
	for _, page := range []string{"/settings", "/keys", "/tokens", "/policies", "/client-branding"} {
		if rule, ok := FindRule(page); ok {
			t.Errorf("page %s has the rule %+v; its routes decide", page, rule)
		}
	}
	// A row that closes settings still closes the server settings, and
	// leaves what every account reads alone.
	c := &Context{UserID: 1, HasUser: true, Role: "super_admin", Policy: policyWith("settings", LevelNone)}
	if got := c.Guard("GET", "/api/settings/ldap"); got.Verdict != VerdictDenied {
		t.Errorf("ldap with settings at none: %+v", got)
	}
	if got := c.Guard("GET", "/api/settings/branding"); got.Verdict != VerdictNoRule {
		t.Errorf("branding with settings at none: %+v", got)
	}
}

// PA3 (F2): the audit trail of the Security audit page asks audit.view,
// the security audit section's read permission, not server.config.
func TestSettingsAuditIsTheSecurityAuditsTrail(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/settings/audit", "audit.view", [5]string{"ok", "ok", "guard", "ok", "ok"}},
	})
	c := &Context{UserID: 1, HasUser: true, Role: "super_admin", Policy: policyWith("security-audit", LevelNone)}
	if got := c.Guard("GET", "/api/settings/audit"); got.Verdict != VerdictDenied || got.Section != "security-audit" {
		t.Errorf("audit with security-audit at none: %+v", got)
	}
}

// PA4 (F2): the Security audit page's routes are under
// /api/panel/security-audit, and its section guards them: they ask
// audit.view, the section's read permission, so the roles with it pass
// and guest_issuer gets the guard's refusal; a row that closes the section
// closes them.
func TestSecurityAuditRoutesAreGuarded(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	var cases []routeCase
	for _, route := range []string{"overview", "events", "hardening", "vulnerabilities"} {
		cases = append(cases, routeCase{"GET", "/api/panel/security-audit/" + route, "audit.view", [5]string{"ok", "ok", "guard", "ok", "ok"}})
	}
	checkRouteCases(t, cases)
	c := &Context{UserID: 1, HasUser: true, Role: "super_admin", Policy: policyWith("security-audit", LevelNone)}
	if got := c.Guard("GET", "/api/panel/security-audit/overview"); got.Verdict != VerdictDenied || got.Section != "security-audit" {
		t.Errorf("overview with security-audit at none: %+v", got)
	}
}

// PA3 (F6) with the guard half of RP1: every reports route asks
// audit.view, the generating POSTs too, so the reports section is
// audit.view for reading and writing. global_admin, whom the save and
// delete routes let in, passes the guard.
func TestReportsAskAuditView(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/reports/types", "audit.view", [5]string{"ok", "ok", "guard", "ok", "ok"}},
		{"POST", "/api/reports/generate", "audit.view", [5]string{"ok", "ok", "guard", "ok", "ok"}},
		{"POST", "/api/reports/generate/csv", "audit.view", [5]string{"ok", "ok", "guard", "ok", "ok"}},
		{"POST", "/api/reports/save", "", [5]string{"ok", "ok", "guard", "ok", "ok"}},
		{"DELETE", "/api/reports/saved/1", "", [5]string{"ok", "ok", "guard", "ok", "ok"}},
	})
	if got := PageRules(); len(got) == 0 || func() int {
		n := 0
		for _, rule := range got {
			if rule.Prefix == "/reports" {
				n++
			}
		}
		return n
	}() != 1 {
		t.Errorf("the reports page must have one rule: %+v", got)
	}
}

// PA5 (F9): /attestation is the device identity page of the four admin
// roles, /server-attestation the server attestation page; every server
// attestation route asks server.attestation, so server_admin runs the
// benchmark and global_admin, who has branding.edit only, passes none.
func TestServerAttestationAsksItsPermission(t *testing.T) {
	if rule, ok := FindRule("/attestation"); ok {
		t.Errorf("/attestation has the rule %+v", rule)
	}
	if rule, ok := FindRule("/server-attestation"); !ok || rule.Section != "server-attestation" {
		t.Errorf("/server-attestation: %+v %v", rule, ok)
	}
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/server-attestation/status", "server.attestation", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"POST", "/api/server-attestation/run", "server.attestation", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"PUT", "/api/server-attestation/badge-config", "server.attestation", [5]string{"guard", "guard", "guard", "guard", "ok"}},
	})
}

// PA3 (F3): a person's own profile, the profile pictures and the person's
// own effective policy need a session only; another account's picture
// stays in the users section.
func TestOwnProfileIsForEveryAccount(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/users/me/profile", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"PUT", "/api/users/me/profile", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/users/avatars", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/panel/access/effective", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/users/3/avatar", "", [5]string{"guard", "ok", "guard", "ok", "ok"}},
		{"GET", "/api/users", "user.view", [5]string{"guard", "ok", "guard", "ok", "ok"}},
	})
}

// PA3 (F4): network targets and status are read with device.view and
// changed with server.config; the four tools ask device.connect, as the
// toolkit does. server_admin keeps no tools (DECISIONS A46).
func TestNetworkReadsAndTools(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/network/targets", "device.view", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/network/monitor/status", "device.view", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"POST", "/api/network/targets", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"POST", "/api/network/ping", "device.connect", [5]string{"guard", "ok", "guard", "ok", "guard"}},
		{"POST", "/api/network/resolve", "device.connect", [5]string{"guard", "ok", "guard", "ok", "guard"}},
		{"POST", "/api/network/targets/1/check", "device.connect", [5]string{"guard", "guard", "guard", "guard", "route"}},
	})
}

// PA3 (F5): the data guard's policies are read with device.view, its
// events and statistics with audit.view (the routes check that), and every
// write asks server.config, so the roles that see devices read what their
// permissions let them and only server_admin writes.
func TestDataGuardReadsAsItsRoutesSay(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/dataguard/policies", "device.view", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/dataguard/policies/1", "device.view", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/dataguard/events", "audit.view", [5]string{"ok", "ok", "route", "ok", "ok"}},
		{"GET", "/api/dataguard/stats", "audit.view", [5]string{"ok", "ok", "route", "ok", "ok"}},
		{"GET", "/api/bd/stats", "audit.view", [5]string{"ok", "ok", "route", "ok", "ok"}},
		{"POST", "/api/dataguard/policies", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"PATCH", "/api/bd/policies/1", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"DELETE", "/api/dataguard/policies/1", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
	})
}

// PA3 (F8): every automation route and the menu entry ask server.config,
// so server_admin writes rules, commands and the SMTP settings, and the
// roles that cannot use the page have no level there.
func TestAutomationAsksServerConfig(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/automation/rules", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"GET", "/api/automation/alerts", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"POST", "/api/automation/rules", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"POST", "/api/automation/commands", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
		{"PUT", "/api/automation/smtp", "server.config", [5]string{"guard", "guard", "guard", "guard", "ok"}},
	})
}

// PA3 (F7): the toolkit is one section that asks device.connect for
// reading and writing, as its page and wake-on-LAN route do.
func TestToolkitAsksDeviceConnect(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"POST", "/api/toolkit/wol", "device.connect", [5]string{"guard", "ok", "guard", "ok", "guard"}},
	})
	// An account with device.connect and without device.edit reaches write.
	c := &Context{UserID: 7, HasUser: true, Role: "operator", Loaded: true, Permissions: newPermissionSet([]string{"device.connect"})}
	if got := ceilingFor(c.Permissions)["toolkit"]; got != LevelWrite {
		t.Errorf("toolkit with device.connect alone: %s, want write", got)
	}
}

// PA3 (F11): an operator files work reports (billing.reports); the time
// sync and clock settings are the server's and answer as their routes
// say; the commercialization routes beside billing are guarded.
func TestCommercializationWritesAndTheClock(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"POST", "/api/panel/billing/sessions/1/report", "billing.reports", [5]string{"guard", "ok", "guard", "ok", "guard"}},
		{"POST", "/api/panel/billing/packages", "billing.manage", [5]string{"guard", "route", "guard", "ok", "guard"}},
		{"POST", "/api/panel/billing/timesync/check", "server.config", [5]string{"route", "route", "route", "route", "ok"}},
		{"PUT", "/api/panel/billing/clock/settings", "server.config", [5]string{"route", "route", "route", "route", "ok"}},
		{"GET", "/api/panel/commercialization/plans", "billing.view", [5]string{"guard", "ok", "guard", "ok", "guard"}},
	})
}

// PA14 (F12): the routes a guest link holder calls authenticate by the
// link, so a signed-in account is not refused there by its sections; the
// remote targets belong to the remote section.
func TestGuestLinkRoutesAreExempt(t *testing.T) {
	// viewer, operator, guest_issuer, global_admin, server_admin
	checkRouteCases(t, []routeCase{
		{"GET", "/api/guest/access-links/validate", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/guest/access-links/peers", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"POST", "/api/guest/mesh/devices/1/desktop", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/remote/guest-info", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/remote/viewer/D1", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/remote/login-info", "", [5]string{"ok", "ok", "ok", "ok", "ok"}},
		{"GET", "/api/guest/access-links", "guest.create", [5]string{"guard", "ok", "ok", "ok", "guard"}},
		{"GET", "/api/remote-targets", "remote_target.view", [5]string{"ok", "ok", "guard", "ok", "ok"}},
		{"POST", "/api/remote-targets", "remote_target.edit", [5]string{"guard", "route", "guard", "ok", "guard"}},
	})
}
