//go:build contract

package contract_test

import (
	"net/url"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The trends of the reports page: sessions and help requests of the last
// days. How many there are depends on the hour the demo data was seeded,
// so the two lists are free in their length; the number of days the route
// made of ?days= is not. An answer of a kilobyte and more is compressed,
// and a short period can fall on either side of that, so every case but
// the first asks for the plain answer.
func init() {
	lists := []string{"data.sessions[#]", "data.help[#]"}
	plain := map[string]string{"Accept-Encoding": "identity"}
	some := contract.Roles("viewer", "admin")
	days := func(name string, values ...string) contract.Case {
		return contract.Case{
			Name: name, Path: "/api/reports/trends", Query: url.Values{"days": values},
			Headers: plain, Volatile: lists, Roles: some,
		}
	}

	contract.Group("trends",
		contract.Case{Name: "default", Path: "/api/reports/trends", Volatile: lists},
		contract.Case{Name: "default-head", Method: "HEAD", Path: "/api/reports/trends", Roles: contract.Roles("signed-out", "guest_issuer", "admin")},
		contract.Case{Name: "default-no-compression", Path: "/api/reports/trends", Headers: plain, Volatile: lists},
		contract.Case{
			Name: "days-30", Path: "/api/reports/trends", Query: url.Values{"days": {"30"}},
			Headers: plain, Volatile: lists,
		},
		days("days-1", "1"),
		days("days-90", "90"),
		days("days-too-many", "365"),
		days("days-zero", "0"),
		days("days-negative", "-3"),
		days("days-text", "soon"),
		days("days-with-a-unit", "7d"),
		days("days-fraction", "2.9"),
		days("days-empty", ""),
		// A parameter sent twice is a list; its first number counts
		days("days-twice", "7", "30"),
		contract.Case{
			Name: "days-in-brackets", Path: "/api/reports/trends", Query: url.Values{"days[n]": {"5"}},
			Headers: plain, Volatile: lists, Roles: some,
		},
		contract.Case{
			Name: "days-in-empty-brackets", Path: "/api/reports/trends", Query: url.Values{"days[]": {"21"}},
			Headers: plain, Volatile: lists, Roles: some,
		},
	)
}
