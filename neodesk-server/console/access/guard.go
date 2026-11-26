package access

import (
	"fmt"
	"strings"
)

// Rule maps a console path to a panel section. A rule with Path matches
// that path only; a rule with Prefix matches the prefix itself and
// everything below it. A rule without a section exempts its paths from
// the rule of a shorter prefix: when it is the longest match, the path
// has no section.
type Rule struct {
	Path    string `json:"path,omitempty"`
	Prefix  string `json:"prefix,omitempty"`
	Section string `json:"section"`
}

// pattern is the text the rule matches on.
func (r Rule) pattern() string {
	if r.Path != "" {
		return r.Path
	}
	return r.Prefix
}

func (r Rule) matches(path string) bool {
	if r.Path != "" {
		return path == r.Path
	}
	return path == r.Prefix || strings.HasPrefix(path, r.Prefix+"/")
}

// RouteRules is the guard's table of pages and API paths, in the order the
// web app receives the page rules.
var RouteRules = []Rule{
	{Path: "/", Section: "dashboard"},
	{Prefix: "/inventory", Section: "devices"},
	{Prefix: "/tickets", Section: "tickets"},
	{Prefix: "/help-requests", Section: "help-requests"},
	{Prefix: "/automation", Section: "automation"},
	{Prefix: "/network", Section: "network"},
	{Prefix: "/registrations", Section: "registrations"},
	{Prefix: "/cdap", Section: "cdap"},
	{Prefix: "/fleet", Section: "fleet"},
	{Prefix: "/chat", Section: "chat"},
	{Prefix: "/scaling", Section: "scaling"},
	{Prefix: "/cross-platform", Section: "cross-platform"},
	{Prefix: "/reports", Section: "reports"},
	{Prefix: "/agents", Section: "agents"},
	{Prefix: "/toolkit", Section: "toolkit"},
	{Prefix: "/server-attestation", Section: "server-attestation"},
	{Prefix: "/dataguard", Section: "dataguard"},
	{Prefix: "/security-audit", Section: "security-audit"},
	{Prefix: "/permissions", Section: "permissions"},
	{Prefix: "/languages", Section: "languages"},
	{Prefix: "/resource-control", Section: "resource-control"},
	{Prefix: "/server-management", Section: "server-management"},
	{Prefix: "/cdap-studio", Section: "cdap"},
	{Prefix: "/users", Section: "users"},
	{Prefix: "/api/users", Section: "users"},
	{Prefix: "/api/panel/user-groups", Section: "users"},
	{Prefix: "/api/panel/access", Section: "users"},
	{Prefix: "/api/guest", Section: "guests"},
	{Prefix: "/api/folders", Section: "folders"},
	{Prefix: "/organizations", Section: "organizations"},
	{Prefix: "/api/panel/org", Section: "organizations"},
	{Prefix: "/devices", Section: "devices"},
	{Prefix: "/api/devices", Section: "devices"},
	{Prefix: "/api/device-groups", Section: "devices"},
	{Prefix: "/api/tags", Section: "devices"},
	{Prefix: "/api/panel/strategies", Section: "strategies"},
	{Prefix: "/strategies", Section: "strategies"},
	{Prefix: "/folders", Section: "folders"},
	{Prefix: "/remote", Section: "remote"},
	{Prefix: "/api/remote", Section: "remote"},
	{Prefix: "/api/tickets", Section: "tickets"},
	{Prefix: "/api/automation", Section: "automation"},
	{Prefix: "/api/network", Section: "network"},
	{Prefix: "/api/dataguard", Section: "dataguard"},
	{Prefix: "/api/reports", Section: "reports"},
	{Prefix: "/api/activity", Section: "activity"},
	{Prefix: "/api/files", Section: "files"},
	{Prefix: "/api/panel/scaling", Section: "scaling"},
	{Prefix: "/api/panel/cross-platform", Section: "cross-platform"},
	{Prefix: "/api/toolkit", Section: "toolkit"},
	{Prefix: "/api/panel/security-audit", Section: "security-audit"},
	{Prefix: "/api/panel/languages", Section: "languages"},
	{Prefix: "/api/panel/resource-control", Section: "resource-control"},
	{Prefix: "/api/server-management", Section: "server-management"},
	{Prefix: "/api/server-attestation", Section: "server-attestation"},
	{Prefix: "/api/registrations", Section: "registrations"},
	{Prefix: "/api/settings", Section: "settings"},
	{Prefix: "/api/panel/billing", Section: "commercialization"},
	{Prefix: "/commercialization", Section: "commercialization"},
	{Prefix: "/activity", Section: "activity"},
	// The file transfer group answers at /api/nd too, with the routes for
	// people beside the device routes.
	{Prefix: "/api/nd/transfer", Section: "files"},
	{Prefix: "/api/nd/transfers", Section: "files"},
	{Prefix: "/api/panel/fleet", Section: "fleet"},
	{Prefix: "/api/chat", Section: "chat"},
	// The fleet, scaling and resource control groups answer at
	// /api/nd + their path too.
	{Prefix: "/api/nd/api/panel/fleet", Section: "fleet"},
	{Prefix: "/api/nd/api/panel/scaling", Section: "scaling"},
	{Prefix: "/api/nd/api/panel/resource-control", Section: "resource-control"},
	// What the shell and the Settings page read for every account; the
	// branding writes ask branding.edit themselves.
	{Prefix: "/api/settings/info"},
	{Prefix: "/api/settings/server-info"},
	{Prefix: "/api/settings/appearance"},
	{Prefix: "/api/settings/branding"},
	{Prefix: "/api/settings/themes"},
	{Prefix: "/api/settings/fonts"},
	{Prefix: "/api/settings/restart-status"},
	// The audit trail the Security audit page reads.
	{Prefix: "/api/settings/audit", Section: "security-audit"},
	// A person's own profile, the profile pictures and the person's own
	// effective policy need a session only.
	{Prefix: "/api/users/me"},
	{Prefix: "/api/users/avatars"},
	{Prefix: "/api/panel/access/effective"},
	// The network page's four tools ask device.connect, as the toolkit does.
	{Prefix: "/api/network/ping", Section: "toolkit"},
	{Prefix: "/api/network/tcp", Section: "toolkit"},
	{Prefix: "/api/network/http", Section: "toolkit"},
	{Prefix: "/api/network/resolve", Section: "toolkit"},
	// The server's clock, which the billing page and the system settings
	// read; the routes check their permissions themselves.
	{Prefix: "/api/panel/billing/timesync"},
	{Prefix: "/api/panel/billing/clock"},
	{Prefix: "/api/panel/commercialization", Section: "commercialization"},
	// The routes a guest link holder calls authenticate by the link.
	{Prefix: "/api/guest/access-links/validate"},
	{Prefix: "/api/guest/access-links/peers"},
	{Prefix: "/api/guest/mesh"},
	{Prefix: "/api/remote/guest-info"},
	{Prefix: "/api/remote/viewer"},
	{Prefix: "/api/remote/login-info"},
	{Prefix: "/api/remote-targets", Section: "remote"},
	// The data guard group answers at /api/nd too, with the routes for
	// people beside the device routes.
	{Prefix: "/api/nd/policies", Section: "dataguard"},
	{Prefix: "/api/nd/events", Section: "dataguard"},
	{Prefix: "/api/nd/stats", Section: "dataguard"},
	// The registration routes for people answer at /api/nd too, beside
	// the two device routes.
	{Prefix: "/api/nd/api/registrations", Section: "registrations"},
	// The security checks and billing pages of the Activity section (plan
	// step 9 of L494) belong to the sections of their old addresses.
	{Prefix: "/security-checks", Section: "security-audit"},
	{Prefix: "/billing", Section: "commercialization"},
	// Devices › Add computers (plan step 8) is below /devices but for every
	// account: Manual setup needs a session only, the other tabs and their
	// calls ask their own permissions.
	{Prefix: "/devices/add"},
	// Where each server setting's value comes from, for the settings
	// pages (GET /api/panel/settings/sources).
	{Prefix: "/api/panel/settings", Section: "settings"},
}

const apiPrefix = "/api/"

// PageRules returns the rules for pages, without the API paths. The
// bootstrap sends them so the web app can tell which page belongs to which
// section.
func PageRules() []Rule {
	out := make([]Rule, 0, len(RouteRules))
	for _, rule := range RouteRules {
		if !strings.HasPrefix(rule.pattern(), apiPrefix) && rule.Section != "" {
			out = append(out, rule)
		}
	}
	return out
}

// FindRule returns the rule for a path: of all rules that match, the one
// with the longest pattern, and of several equally long ones the first in
// the table. The path is compared as rulePath gives it.
func FindRule(path string) (Rule, bool) {
	return findRuleIn(RouteRules, path)
}

// rulePath is the path as the rules see it. The console's routes match
// their fixed parts in any ASCII letter case, so the path is lower-cased:
// every rule is.
func rulePath(path string) string {
	return asciiLower(path)
}

func findRuleIn(rules []Rule, path string) (Rule, bool) {
	path = rulePath(path)
	var best Rule
	found := false
	for _, rule := range rules {
		if !rule.matches(path) {
			continue
		}
		if !found || len(rule.pattern()) > len(best.pattern()) {
			best, found = rule, true
		}
	}
	if best.Section == "" {
		return Rule{}, false
	}
	return best, found
}

// MsgTokenSection is the refusal of a panel section whose level the
// request's API token does not reach with its permissions.
func MsgTokenSection(section string, level Level) string {
	return "Panel access denied: " + section + " needs " + string(level) + " access, which this API token's permissions do not give"
}

// RequiredLevel is the level a request needs in its section: read for GET,
// HEAD and OPTIONS, write for everything else.
func RequiredLevel(method string) Level {
	switch method {
	case "GET", "HEAD", "OPTIONS":
		return LevelRead
	}
	return LevelWrite
}

// Verdict is what the guard found for a request.
type Verdict int

// The guard's verdicts.
const (
	// VerdictNoRule: no rule matches the path, or nobody is signed in. The
	// request goes on and has no section.
	VerdictNoRule Verdict = iota
	// VerdictPage: a GET or HEAD of a page. The request goes on with its
	// section; the web app shows its own no-access view.
	VerdictPage
	// VerdictAllowed: the account's level in the section is enough.
	VerdictAllowed
	// VerdictUnavailable: the account's policy could not be loaded.
	VerdictUnavailable
	// VerdictDenied: the account's level in the section is too low.
	VerdictDenied
)

// GuardResult is the guard's verdict with the answer to send when the
// request is refused.
type GuardResult struct {
	Verdict Verdict
	// Section is the section of the path, "" for VerdictNoRule.
	Section string
	// Decision passes, or holds the 503 or 403 answer: JSON for paths under
	// /api/, the console's error page for the rest.
	Decision Decision
}

// RouteSection is what a route says about its own panel section. It
// stands in place of the path rules, for a route they cannot name: one
// for every account below a guarded prefix, or one that belongs to
// another section than its neighbours.
type RouteSection struct {
	// Section is the key of a catalog section, "" for a route without a
	// section.
	Section string
	// Level is the level the route needs in its section, "" for the level
	// of its method (RequiredLevel).
	Level Level
}

// Valid reports what is wrong with the declaration: a section the catalog
// does not have, a level without a section, or a level the guard cannot
// ask for.
func (s RouteSection) Valid() error {
	if s.Section == "" {
		if s.Level != "" {
			return fmt.Errorf("level %q without a section", s.Level)
		}
		return nil
	}
	if _, ok := CatalogEntry(s.Section); !ok {
		return fmt.Errorf("section %q is not in the catalog", s.Section)
	}
	switch s.Level {
	case "", LevelRead, LevelWrite:
		return nil
	}
	return fmt.Errorf("level %q is not read or write", s.Level)
}

// Guard maps the request to its panel section by the path rules and
// checks the account's level there. path is the request path as it was
// sent, without the query and without decoding; letters count in either
// case, as for the routes. Guard stores the section in the context, where
// HasPermission reads it.
func (c *Context) Guard(method, path string) GuardResult {
	return c.GuardRoute(method, path, nil)
}

// GuardRoute is Guard for a request whose route is known: what the route
// declares about its section decides, and the path rules only when it
// declares nothing (declared is nil).
func (c *Context) GuardRoute(method, path string, declared *RouteSection) GuardResult {
	section, level := "", RequiredLevel(method)
	if declared != nil {
		section = declared.Section
		if declared.Level != "" {
			level = declared.Level
		}
	} else if rule, ok := FindRule(path); ok {
		section = rule.Section
	}
	if section == "" || c.UserID == 0 || !c.HasUser {
		return GuardResult{Verdict: VerdictNoRule, Decision: pass()}
	}
	c.Section = section
	api := strings.HasPrefix(rulePath(path), apiPrefix)
	if !api && (method == "GET" || method == "HEAD") {
		return GuardResult{Verdict: VerdictPage, Section: section, Decision: pass()}
	}
	if c.PolicyErr != nil {
		d := Decision{
			Answer: AnswerErrorPage,
			Status: statusUnavailable,
			Page:   ErrorPage{Kind: errorPageServerError, Message: MsgPolicyUnavailable, Text: MsgPolicyUnavailable},
		}
		if api {
			d = refuseJSON(statusUnavailable, MsgPolicyUnavailable, CodePolicyUnavailable)
		}
		return GuardResult{Verdict: VerdictUnavailable, Section: section, Decision: d}
	}
	if c.Cap != nil && rank(LegacySectionCeiling(section, c.Cap.Has, false)) < rank(level) {
		d := Decision{
			Answer: AnswerErrorPage,
			Status: statusForbidden,
			Page:   ErrorPage{Kind: errorPageForbidden, Message: MsgPanelSectionDenied, Text: MsgPanelSectionDenied},
		}
		if api {
			d = refuseJSON(statusForbidden, MsgTokenSection(section, level), CodeTokenPermission)
		}
		return GuardResult{Verdict: VerdictDenied, Section: section, Decision: d}
	}
	if AccessAllows(c.Policy, section, level) {
		return GuardResult{Verdict: VerdictAllowed, Section: section, Decision: pass()}
	}
	d := Decision{
		Answer: AnswerErrorPage,
		Status: statusForbidden,
		Page:   ErrorPage{Kind: errorPageForbidden, Message: MsgPanelSectionDenied, Text: MsgPanelSectionDenied},
	}
	if api {
		d = refuseJSON(statusForbidden, MsgPanelAccessDenied(section), CodePanelAccessDenied)
	}
	return GuardResult{Verdict: VerdictDenied, Section: section, Decision: d}
}
