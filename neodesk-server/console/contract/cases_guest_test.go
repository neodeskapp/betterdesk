//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Guest access links under /api/guest: an operator makes a link for some
// devices, lists the links and takes one back; the holder of a link asks
// whether it is good and which devices it reaches, without a session.
//
// Each turn makes one link and deletes it; admin deletes what a turn
// could not. The requests of a link holder go out without cookies, so the
// guest cookie a good link sets stays out of the role's cookie jar and of
// the groups after this one.
//
// A query whose token has a toString member (?guest[toString]=x on the
// peers and desktop routes) is left out: it stops the Node console
// (BUGS.md MC1).
func init() {
	const links = "/api/guest/access-links"
	const desktop = "/api/guest/mesh/devices/900100001/desktop"
	few := contract.Roles("signed-out", "viewer", "operator", "admin")
	ends := contract.Roles("signed-out", "admin")
	admin := contract.Roles("admin")
	issuers := contract.Roles("guest_issuer", "operator", "admin")
	empty := map[string]any{}
	link := map[string]any{"peer_ids": []any{"900100001", "900100002"}, "ttl_minutes": 5, "view_only": true, "label": "Contract link", "max_uses": 3}
	// The demo devices are RustDesk devices, for which the API server
	// refuses a view_only link (RV209): the link the turns make and use
	// gives control.
	controlLink := map[string]any{"peer_ids": []any{"900100001", "900100002"}, "ttl_minutes": 5, "view_only": false, "label": "Contract link", "max_uses": 3}
	create := func(name string, body any, roles ...contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: links, Body: body, Roles: roles}
	}

	contract.Group("guest",
		contract.Case{Name: "list-empty", Path: links},
		contract.Case{Name: "list-head", Method: "HEAD", Path: links, Roles: ends},
		contract.Case{Name: "list-other-spelling", Path: "/API/Guest/Access-Links/", Roles: few},

		// What a link needs
		create("create-without-peers", empty),
		create("create-empty-peers", map[string]any{"peer_ids": []any{}}, issuers...),
		create("create-peers-as-text", map[string]any{"peer_ids": "900100001"}, issuers...),
		contract.Case{
			Name: "create-list-body", Method: "POST", Path: links,
			RawBody: `[1, 2]`, ContentType: "application/json", Roles: admin,
		},
		create("create-unknown-ttl", map[string]any{"peer_ids": []any{"900100001"}, "ttl_minutes": "soon"}, issuers...),
		create("create-peer-that-cannot-be-text", map[string]any{"peer_ids": []any{map[string]any{"toString": 1}}}, ends...),
		contract.Case{
			Name: "create-without-csrf-token", Method: "POST", Path: links,
			Body: link, NoCSRF: true, Roles: few,
		},
		contract.Case{
			Name: "create-as-form", Method: "POST", Path: links,
			RawBody: "peer_ids=900100001", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},

		// make, read back, ask as the holder, take back
		contract.Case{
			Name: "create", Method: "POST", Path: links, Body: controlLink,
			Capture: map[string]string{"link": "id", "token": "token"},
		},
		contract.Case{Name: "list-with-one", Path: links},
		contract.Case{
			Name: "validate", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token": {"{token}"}, "peer_id": {"900100001"}},
		},
		contract.Case{
			Name: "validate-without-peer", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token": {"{token}"}}, Roles: issuers,
		},
		contract.Case{
			Name: "validate-another-device", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token": {"{token}"}, "peer_id": {"900100009"}}, Roles: issuers,
		},
		contract.Case{
			Name: "validate-signed-in", Path: links + "/validate",
			Query: url.Values{"token": {"{token}"}, "peer_id": {"900100002"}},
		},
		contract.Case{
			Name: "peers", Path: links + "/peers", NoCookies: true,
			Query: url.Values{"t": {"{token}"}},
		},
		contract.Case{
			Name: "peers-guest-before-t", Path: links + "/peers", NoCookies: true,
			Query: url.Values{"guest": {" {token} "}, "t": {"other"}}, Roles: issuers,
		},
		contract.Case{
			Name: "peers-from-cookie", Path: links + "/peers", NoCookies: true,
			Headers: map[string]string{"Cookie": "nd.guest={token}"}, Roles: issuers,
		},
		contract.Case{
			Name: "peers-query-before-cookie", Path: links + "/peers", NoCookies: true,
			Query:   url.Values{"t": {"unknown"}},
			Headers: map[string]string{"Cookie": "nd.guest={token}"}, Roles: issuers,
		},
		contract.Case{
			Name: "desktop-guest", Method: "POST", Path: desktop,
			Query: url.Values{"guest": {"{token}"}}, Body: empty, Roles: issuers,
		},
		contract.Case{
			Name: "desktop-guest-signed-in", Method: "POST", Path: desktop,
			Query: url.Values{"t": {"{token}"}, "view_only": {"0"}, "extra": {"a", "b"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-guest-of-another-device", Method: "POST", Path: "/api/guest/mesh/devices/900100009/desktop",
			Query: url.Values{"guest": {"{token}"}}, Body: empty, Roles: issuers,
		},
		contract.Case{Name: "delete", Method: "DELETE", Path: links + "/{link}"},
		contract.Case{Name: "delete-as-admin", Method: "DELETE", Path: links + "/{link}", As: "admin"},
		contract.Case{Name: "delete-again", Method: "DELETE", Path: links + "/{link}", Roles: issuers},
		contract.Case{Name: "list-after-delete", Path: links, Roles: issuers},
		contract.Case{
			Name: "validate-after-delete", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token": {"{token}"}, "peer_id": {"900100001"}}, Roles: issuers,
		},
		contract.Case{
			Name: "peers-after-delete", Path: links + "/peers", NoCookies: true,
			Query: url.Values{"t": {"{token}"}}, Roles: issuers,
		},

		// The values of a link as the API server takes them
		contract.Case{
			Name: "create-number-peers", Method: "POST", Path: links,
			Body:    map[string]any{"peer_ids": []any{900100003, true, nil, []any{1, 2}}, "view_only": "yes", "label": 7, "max_uses": "2"},
			Capture: map[string]string{"odd": "id"}, Roles: issuers,
		},
		contract.Case{Name: "create-number-peers-deleted", Method: "DELETE", Path: links + "/{odd}", As: "admin", Roles: issuers},
		contract.Case{
			Name: "create-plain", Method: "POST", Path: links,
			Body:    map[string]any{"peer_ids": []any{"900100004"}},
			Capture: map[string]string{"plain": "id"}, Roles: issuers,
		},
		contract.Case{Name: "create-plain-deleted", Method: "DELETE", Path: links + "/{plain}", As: "admin", Roles: issuers},

		// Without a link
		contract.Case{Name: "validate-nothing", Path: links + "/validate"},
		contract.Case{
			Name: "validate-unknown", Path: links + "/validate",
			Query: url.Values{"token": {"abc"}, "peer_id": {"900100001"}},
		},
		contract.Case{
			Name: "validate-lists", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token[]": {"abc"}, "peer_id[x]": {"1"}}, Roles: ends,
		},
		contract.Case{
			Name: "validate-query-that-cannot-be-text", Path: links + "/validate", NoCookies: true,
			Query: url.Values{"token[toString]": {"x"}}, Roles: ends,
		},
		contract.Case{Name: "validate-head", Method: "HEAD", Path: links + "/validate", Query: url.Values{"token": {"abc"}}, Roles: ends},
		contract.Case{Name: "peers-nothing", Path: links + "/peers"},
		contract.Case{Name: "peers-unknown", Path: links + "/peers", Query: url.Values{"guest": {"abc"}}},
		contract.Case{
			Name: "peers-blank", Path: links + "/peers", NoCookies: true,
			Query: url.Values{"guest": {" "}, "t": {""}}, Roles: ends,
		},
		contract.Case{
			Name: "peers-token-as-list", Path: links + "/peers", NoCookies: true,
			Query: url.Values{"guest": {"a", "b"}}, Roles: ends,
		},
		contract.Case{
			Name: "peers-unknown-cookie", Path: links + "/peers", NoCookies: true,
			Headers: map[string]string{"Cookie": "nd.guest=abc%20def"}, Roles: ends,
		},
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: links + "/no-such-link"},
		contract.Case{Name: "delete-id-two-dots", Method: "DELETE", Path: links + "/%2e%2e", Roles: few},
		contract.Case{Name: "delete-id-with-slash", Method: "DELETE", Path: links + "/a%2Fb%3Fc", Roles: admin},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: links + "/no-such-link", NoCSRF: true, Roles: few},

		contract.Case{Name: "desktop-nothing", Method: "POST", Path: desktop, Body: empty},
		contract.Case{
			Name: "desktop-unknown-guest", Method: "POST", Path: desktop,
			Query: url.Values{"guest": {"abc"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-unknown-share", Method: "POST", Path: desktop,
			Query: url.Values{"mesh_share": {"abc"}}, Body: empty,
		},
		contract.Case{
			Name: "desktop-guest-before-share", Method: "POST", Path: desktop,
			Query: url.Values{"mesh_share": {"abc"}, "t": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-blank-tokens", Method: "POST", Path: desktop,
			Query: url.Values{"mesh_share": {" "}, "guest": {" "}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-share-as-list", Method: "POST", Path: desktop,
			Query: url.Values{"mesh_share[]": {"abc"}}, Body: empty, Roles: ends,
		},
		contract.Case{
			Name: "desktop-without-csrf-token", Method: "POST", Path: desktop, NoCSRF: true,
			Query: url.Values{"guest": {"abc"}}, Body: empty, Roles: few,
		},
		contract.Case{
			Name: "desktop-as-form", Method: "POST", Path: desktop,
			RawBody: "a=1", ContentType: "application/x-www-form-urlencoded", Roles: ends,
		},
	)
}
