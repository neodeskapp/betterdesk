package access

import "strings"

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
	{Prefix: "/attestation", Section: "server-attestation"},
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
	{Prefix: "/api/security-audit", Section: "security-audit"},
	{Prefix: "/api/panel/languages", Section: "languages"},
	{Prefix: "/api/panel/resource-control", Section: "resource-control"},
	{Prefix: "/api/server-management", Section: "server-management"},
	{Prefix: "/api/server-attestation", Section: "server-attestation"},
	{Prefix: "/api/registrations", Section: "registrations"},
	{Prefix: "/api/settings", Section: "settings"},
	{Prefix: "/api/panel/billing", Section: "commercialization"},
	{Prefix: "/commercialization", Section: "commercialization"},
	{Prefix: "/activity", Section: "activity"},
	// The file transfer group answers at /api/bd too, with the routes for
	// people beside the device routes.
	{Prefix: "/api/bd/transfer", Section: "files"},
	{Prefix: "/api/bd/transfers", Section: "files"},
	{Prefix: "/api/panel/fleet", Section: "fleet"},
	{Prefix: "/api/chat", Section: "chat"},
	// The fleet, scaling and resource control groups answer at
	// /api/bd + their path too.
	{Prefix: "/api/bd/api/panel/fleet", Section: "fleet"},
	{Prefix: "/api/bd/api/panel/scaling", Section: "scaling"},
	{Prefix: "/api/bd/api/panel/resource-control", Section: "resource-control"},
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
// the table. The comparison is exact, also in letter case.
func FindRule(path string) (Rule, bool) {
	return findRuleIn(RouteRules, path)
}

func findRuleIn(rules []Rule, path string) (Rule, bool) {
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

// Guard maps the request to its panel section and checks the account's
// level there. path is the request path as it was sent, without the query
// and without decoding. Guard stores the section in the context, where
// HasPermission reads it.
func (c *Context) Guard(method, path string) GuardResult {
	rule, ok := FindRule(path)
	if !ok || c.UserID == 0 || !c.HasUser {
		return GuardResult{Verdict: VerdictNoRule, Decision: pass()}
	}
	c.Section = rule.Section
	api := strings.HasPrefix(path, apiPrefix)
	if !api && (method == "GET" || method == "HEAD") {
		return GuardResult{Verdict: VerdictPage, Section: rule.Section, Decision: pass()}
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
		return GuardResult{Verdict: VerdictUnavailable, Section: rule.Section, Decision: d}
	}
	if AccessAllows(c.Policy, rule.Section, RequiredLevel(method)) {
		return GuardResult{Verdict: VerdictAllowed, Section: rule.Section, Decision: pass()}
	}
	d := Decision{
		Answer: AnswerErrorPage,
		Status: statusForbidden,
		Page:   ErrorPage{Kind: errorPageForbidden, Message: MsgPanelSectionDenied, Text: MsgPanelSectionDenied},
	}
	if api {
		d = refuseJSON(statusForbidden, MsgPanelAccessDenied(rule.Section), CodePanelAccessDenied)
	}
	return GuardResult{Verdict: VerdictDenied, Section: rule.Section, Decision: d}
}
