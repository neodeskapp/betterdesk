//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// The cross-platform page: the seeded devices counted by platform,
// architecture and client version, the codecs and features of the devices
// that are online, and one device as the API server describes it. Nothing
// here writes.
func init() {
	some := contract.Roles("signed-out", "guest_issuer", "viewer", "admin")
	readers := contract.Roles("viewer", "admin")

	contract.Group("cross-platform",
		contract.Case{Name: "distribution", Path: "/api/panel/cross-platform/distribution"},
		contract.Case{Name: "distribution-head", Method: "HEAD", Path: "/api/panel/cross-platform/distribution", Roles: some},
		contract.Case{Name: "protocols", Path: "/api/panel/cross-platform/protocols"},
		contract.Case{Name: "capabilities", Path: "/api/panel/cross-platform/capabilities/900100001"},
		contract.Case{Name: "capabilities-second-device", Path: "/api/panel/cross-platform/capabilities/900100014", Roles: readers},
		contract.Case{Name: "capabilities-unknown-device", Path: "/api/panel/cross-platform/capabilities/999999999", Roles: some},
		contract.Case{Name: "capabilities-id-with-spaces-around", Path: "/api/panel/cross-platform/capabilities/%20900100014%20", Roles: readers},
		contract.Case{Name: "capabilities-id-with-a-slash", Path: "/api/panel/cross-platform/capabilities/9001%2F00001", Roles: some},
		contract.Case{Name: "capabilities-id-with-a-space", Path: "/api/panel/cross-platform/capabilities/9001%2000001", Roles: readers},
		contract.Case{Name: "capabilities-id-of-dots", Path: "/api/panel/cross-platform/capabilities/%2E%2E", Roles: readers},
		contract.Case{Name: "capabilities-id-with-two-dots", Path: "/api/panel/cross-platform/capabilities/a..b", Roles: readers},
		contract.Case{Name: "capabilities-id-with-a-percent-sign", Path: "/api/panel/cross-platform/capabilities/a%25b", Roles: readers},
		contract.Case{Name: "capabilities-id-too-long", Path: "/api/panel/cross-platform/capabilities/" + longID(257), Roles: readers},
	)
}

// longID is a device id of n letters.
func longID(n int) string {
	id := make([]byte, n)
	for i := range id {
		id[i] = 'x'
	}
	return string(id)
}
