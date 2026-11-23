//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The security audit page. Nothing here writes. What the API server's
// audit log holds depends on everything that ran on the instance before
// (other groups sign in, fail to, change settings), so the counts made
// from it and what each event says are free; the shape, the checks and
// the way the query reaches the API server are not. The seed alone leaves
// 41 events, eight of them org_policy_changed, so a case whose limit is
// lower than that gets exactly that many events and keeps the number: a
// console that ignored the limit or the filter fails. Cases that ask for
// the default of 50 or more get as many as there are, so their number is
// free, and each event is still compared with the golden's sample.
func init() {
	some := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	readers := contract.Roles("viewer", "admin")
	overview := []string{"~score", "~stats.failed_logins", "~stats.config_changes", "checks[2].pass"}
	// What an event says, beside its keys; only some events have a target
	// and details (the start of the server has neither)
	said := []string{"events[*].timestamp", "~events[*].actor", contract.Optional + "events[*].target", contract.Optional + "events[*].details"}
	free := append([]string{"~events[*].action"}, said...)
	all := append([]string{"events[#]", "~total", "~count"}, free...)
	limited := append([]string{"~total"}, free...)
	events := func(name string, query url.Values, volatile []string) contract.Case {
		return contract.Case{Name: name, Path: "/api/panel/security-audit/events", Query: query, Volatile: volatile, Roles: readers}
	}

	contract.Group("security-audit",
		contract.Case{Name: "overview", Path: "/api/panel/security-audit/overview", Volatile: overview},
		contract.Case{Name: "overview-head", Method: "HEAD", Path: "/api/panel/security-audit/overview", Roles: some},
		contract.Case{Name: "events", Path: "/api/panel/security-audit/events", Volatile: all},
		events("events-two", url.Values{"limit": {"2"}}, limited),
		events("events-two-from-the-second", url.Values{"limit": {"2"}, "offset": {"1"}}, limited),
		// The filter keeps the action
		events("events-of-an-action", url.Values{"action": {"org_policy_changed"}, "limit": {"3"}}, append([]string{"~total"}, said...)),
		events("events-of-an-unknown-action", url.Values{"action": {"nothing happened & more"}}, []string{"~total"}),
		events("events-limit-no-number", url.Values{"limit": {"many"}, "offset": {"few"}}, all),
		events("events-limit-zero", url.Values{"limit": {"0"}}, all),
		events("events-limit-negative", url.Values{"limit": {"-1"}}, limited),
		events("events-limit-hexadecimal", url.Values{"limit": {"0x2"}}, all),
		events("events-limit-too-large", url.Values{"limit": {"99999999999999999999999"}}, all),
		events("events-limit-twice", url.Values{"limit": {"1", "3"}}, all),
		events("events-action-twice", url.Values{"action": {"peer_deleted", "peer_banned"}}, []string{"~total"}),
		events("events-action-in-brackets", url.Values{"action[name]": {"peer_deleted"}}, all),
		contract.Case{Name: "hardening", Path: "/api/panel/security-audit/hardening"},
	)
}
