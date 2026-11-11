//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// Inventory: the two routes people read, the list of devices with their
// hardware in short and one device's full report with its last telemetry.
// The three routes of the desktop agent (inventory and telemetry upload,
// reading its own report) take a device token and are not part of these
// cases.
//
// The demo data has reports for 14 devices. Their times count back from
// the moment an instance starts, and so do the install dates of the
// programs in a report.
func init() {
	times := []string{"devices[*].last_seen", "devices[*].collected_at"}
	report := []string{"collected_at", "received_at", "telemetry.timestamp", "telemetry.received_at", "software.apps[*].installed_at"}
	few := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	page := func(name string, query url.Values, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/inventory", Query: query, Volatile: times, Roles: roles}
	}

	contract.Group("inventory",
		// The list and its pages
		page("list", nil),
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/inventory"},
		contract.Case{Name: "list-other-spelling", Path: "/API/Inventory/", Volatile: times, Roles: few},
		page("list-first-page-of-five", url.Values{"page": {"1"}, "limit": {"5"}}),
		page("list-second-page-of-five", url.Values{"page": {"2"}, "limit": {"5"}}, few...),
		page("list-last-page-of-five", url.Values{"page": {"3"}, "limit": {"5"}}, few...),
		page("list-page-after-the-last", url.Values{"page": {"4"}, "limit": {"5"}}, few...),
		page("list-page-only", url.Values{"page": {"2"}}, few...),
		page("list-limit-only", url.Values{"limit": {"3"}}, few...),
		page("list-limit-one-hundred", url.Values{"limit": {"100"}}, few...),
		page("list-limit-above-the-cap", url.Values{"limit": {"1000000"}}, few...),
		page("list-limit-beyond-integers", url.Values{"limit": {"99999999999999999999"}}, few...),
		page("list-page-with-zeros-in-front", url.Values{"page": {"0002"}, "limit": {"5"}}, few...),
		page("list-largest-page", url.Values{"page": {"9007199254740991"}}, few...),
		page("list-page-beyond-integers", url.Values{"page": {"9007199254740992"}}, few...),
		page("list-page-zero", url.Values{"page": {"0"}}, few...),
		page("list-limit-zero", url.Values{"limit": {"0"}}, few...),
		page("list-page-negative", url.Values{"page": {"-1"}}, few...),
		page("list-limit-fraction", url.Values{"limit": {"1.5"}}, few...),
		page("list-limit-not-a-number", url.Values{"limit": {"abc"}}, few...),
		page("list-limit-exponent", url.Values{"limit": {"1e1"}}, few...),
		page("list-limit-with-space", url.Values{"limit": {" 5"}}, few...),
		page("list-page-empty", url.Values{"page": {""}}, few...),
		page("list-limit-empty", url.Values{"limit": {""}}, few...),
		page("list-page-twice", url.Values{"page": {"1", "2"}}, few...),
		page("list-limit-in-brackets", url.Values{"limit[]": {"5"}}, few...),
		page("list-page-object", url.Values{"page[n]": {"1"}}, few...),
		page("list-bad-page-good-limit", url.Values{"page": {"x"}, "limit": {"5"}}, few...),

		// One device: a Windows desktop, a Mac, a Linux server, a
		// Windows server and a Raspberry Pi
		contract.Case{Name: "detail", Path: "/api/inventory/900100001", Volatile: report},
		contract.Case{Name: "detail-head", Method: "HEAD", Path: "/api/inventory/900100001", Roles: few},
		contract.Case{Name: "detail-other-spelling", Path: "/API/Inventory/900100001/", Volatile: report, Roles: few},
		contract.Case{Name: "detail-mac", Path: "/api/inventory/900100005", Volatile: report, Roles: few},
		contract.Case{Name: "detail-linux-server", Path: "/api/inventory/900100012", Volatile: report, Roles: few},
		contract.Case{Name: "detail-windows-server", Path: "/api/inventory/900100026", Volatile: report, Roles: few},
		contract.Case{Name: "detail-small-computer", Path: "/api/inventory/900100024", Volatile: report, Roles: few},
		contract.Case{Name: "detail-offline-with-a-query", Path: "/api/inventory/900100016", Query: url.Values{"page": {"x"}, "limit": {"0"}}, Volatile: report, Roles: few},
		contract.Case{Name: "detail-device-without-report", Path: "/api/inventory/900100007"},
		contract.Case{Name: "detail-unknown-device", Path: "/api/inventory/no%20such%2Fdevice"},
		// The agent's routes are POST /api/inventory/inventory and
		// /telemetry and GET /api/inventory/inventory/:id; read without an
		// id, the two words are device ids
		contract.Case{Name: "detail-of-the-upload-address", Path: "/api/inventory/inventory"},
		contract.Case{Name: "detail-of-the-telemetry-address", Path: "/api/inventory/telemetry"},
	)
}
