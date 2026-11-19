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

// stillDead are the API rules that still name a prefix no route has, with
// the reason.
var stillDead = map[string]string{
	// The routes are under /api/panel/security-audit. Moving the rule there
	// changes what the security audit routes' own tests pin (BUGS PA4).
	"/api/security-audit": "PA4",
}

// PA4: a rule for an API prefix no route has leaves the section's API
// unguarded while the page is hidden. Every API rule names routes.
func TestEveryAPIRuleMatchesARoute(t *testing.T) {
	routes := consoleRoutes(t)
	for _, rule := range RouteRules {
		if !strings.HasPrefix(rule.pattern(), apiPrefix) || stillDead[rule.pattern()] != "" {
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
