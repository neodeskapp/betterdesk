//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The security audit page. Nothing here writes. What the API server's
// audit log holds depends on everything that ran on the instance before
// (other groups sign in, fail to, change settings), so the counts made
// from it and the list of events are free; the shape, the checks and the
// way the query reaches the API server are not.
func init() {
	some := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	readers := contract.Roles("viewer", "admin")
	overview := []string{"~score", "~stats.failed_logins", "~stats.config_changes", "checks[2].pass"}
	list := []string{"events[#]", "~total", "~count"}
	events := func(name string, query url.Values) contract.Case {
		return contract.Case{Name: name, Path: "/api/panel/security-audit/events", Query: query, Volatile: list, Roles: readers}
	}

	contract.Group("security-audit",
		contract.Case{Name: "overview", Path: "/api/panel/security-audit/overview", Volatile: overview},
		contract.Case{Name: "overview-head", Method: "HEAD", Path: "/api/panel/security-audit/overview", Roles: some},
		contract.Case{Name: "events", Path: "/api/panel/security-audit/events", Volatile: list},
		events("events-two", url.Values{"limit": {"2"}}),
		events("events-two-from-the-second", url.Values{"limit": {"2"}, "offset": {"1"}}),
		events("events-of-an-action", url.Values{"action": {"org_policy_changed"}, "limit": {"3"}}),
		events("events-of-an-unknown-action", url.Values{"action": {"nothing happened & more"}}),
		events("events-limit-no-number", url.Values{"limit": {"many"}, "offset": {"few"}}),
		events("events-limit-zero", url.Values{"limit": {"0"}}),
		events("events-limit-negative", url.Values{"limit": {"-1"}}),
		events("events-limit-hexadecimal", url.Values{"limit": {"0x2"}}),
		events("events-limit-too-large", url.Values{"limit": {"99999999999999999999999"}}),
		events("events-limit-twice", url.Values{"limit": {"1", "3"}}),
		events("events-action-twice", url.Values{"action": {"peer_deleted", "peer_banned"}}),
		events("events-action-in-brackets", url.Values{"action[name]": {"peer_deleted"}}),
		contract.Case{Name: "hardening", Path: "/api/panel/security-audit/hardening"},
		contract.Case{
			Name: "vulnerabilities", Path: "/api/panel/security-audit/vulnerabilities",
			Volatile: []string{"dependencies.nodejs.total"},
		},
	)
}
