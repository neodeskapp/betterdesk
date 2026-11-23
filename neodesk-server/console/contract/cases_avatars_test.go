//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Profile pictures: the list of who has one, and the picture of a user.
//
// A test instance has no pictures: only a single sign-on or a fetch from
// another host stores one. So the cases cover the list as it is without
// pictures and every way a picture is not found. What a stored picture
// looks like on the wire (the headers, the 304, the ?v= address) is pinned
// by the tests of console/routes/avatars, which replay what the Node route
// answered with pictures in its database.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	contract.Group("avatars",
		contract.Case{Name: "list", Path: "/api/users/avatars"},
		contract.Case{Name: "list-head", Method: "HEAD", Path: "/api/users/avatars", Roles: some},
		contract.Case{Name: "list-other-spelling", Path: "/API/Users/Avatars/", Roles: some},
		contract.Case{
			Name: "list-not-modified", Path: "/api/users/avatars",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{
			Name: "list-no-compression", Path: "/api/users/avatars",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: some,
		},
		contract.Case{Name: "list-with-a-query", Path: "/api/users/avatars", Query: url.Values{"v": {"1"}}, Roles: some},

		// User 1 is the first administrator: the account exists and has no picture
		contract.Case{Name: "picture-of-a-user-without-one", Path: "/api/users/1/avatar"},
		contract.Case{Name: "picture-of-a-user-without-one-head", Method: "HEAD", Path: "/api/users/1/avatar", Roles: some},
		contract.Case{
			Name: "picture-with-a-version", Path: "/api/users/1/avatar",
			Query: url.Values{"v": {"0123456789abcdef"}}, Roles: some,
		},
		contract.Case{
			Name: "picture-with-a-validator", Path: "/api/users/1/avatar",
			Headers: map[string]string{"If-None-Match": `"0123456789abcdef"`}, Roles: some,
		},
		contract.Case{Name: "picture-other-spelling", Path: "/API/Users/1/Avatar/", Roles: some},
		contract.Case{Name: "picture-of-no-user", Path: "/api/users/{nobody}/avatar"},
		contract.Case{Name: "picture-id-zero", Path: "/api/users/0/avatar", Roles: some},
		contract.Case{Name: "picture-id-zeros", Path: "/api/users/0000000000/avatar", Roles: some},
		contract.Case{Name: "picture-id-with-leading-zeros", Path: "/api/users/0000000001/avatar", Roles: some},
		contract.Case{Name: "picture-id-ten-digits", Path: "/api/users/9999999999/avatar", Roles: some},
		contract.Case{Name: "picture-id-eleven-digits", Path: "/api/users/00000000001/avatar", Roles: some},
		contract.Case{Name: "picture-id-not-a-number", Path: "/api/users/x1/avatar", Roles: some},
		contract.Case{Name: "picture-id-with-a-sign", Path: "/api/users/-1/avatar", Roles: some},
		contract.Case{Name: "picture-id-with-a-fraction", Path: "/api/users/1.0/avatar", Roles: some},
		contract.Case{Name: "picture-id-in-hex", Path: "/api/users/0x1/avatar", Roles: some},
		contract.Case{Name: "picture-id-escaped", Path: "/api/users/%31/avatar", Roles: some},
		contract.Case{Name: "picture-id-with-a-line-break", Path: "/api/users/1%0A/avatar", Roles: some},
		contract.Case{Name: "picture-id-with-a-space", Path: "/api/users/1%20/avatar", Roles: some},
	)
}
