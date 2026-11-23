//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The data the web remote client asks for: the sign-in page's data, a
// guest link's page data, the remote desk, the viewer's data for one
// device and the old addresses that redirect to the new pages, and the
// list and state of the native remote sessions.
//
// Each turn makes a guest link for 900100001 (as admin) and deletes it.
// The requests of a link holder go out without cookies, so the guest
// cookie a good link sets stays out of the role's cookie jar.
//
// The guest link is made and deleted by routes the Go console answers
// (cases/guest pins them as Node answered); those two steps were recorded
// with CONTRACT_RECORD_NEW=1.
//
// The native sessions live in the console's memory and are never
// removed: the first turn (signed out) sees none, then its agent socket
// for 900100001 makes one, which every later turn sees.
func init() {
	const viewer = "/api/remote/viewer/"
	const links = "/api/guest/access-links"
	ends := contract.Roles("signed-out", "admin")
	few := contract.Roles("signed-out", "viewer", "operator", "admin")
	agent := url.Values{"token": {"{secret:device_token}"}}
	guest := url.Values{"guest": {"{secret:glink}"}}

	contract.Group("remote",
		// The sign-in page of the remote client
		contract.Case{Name: "login-info", Path: "/api/remote/login-info"},
		contract.Case{Name: "login-info-return", Path: "/api/remote/login-info", Query: url.Values{"return": {"/remote/900100001?transport=cdap"}}},
		contract.Case{Name: "login-info-return-other-host", Path: "/api/remote/login-info", Query: url.Values{"return": {"//evil.example/remote"}}, Roles: few},
		contract.Case{Name: "login-info-return-outside", Path: "/api/remote/login-info", Query: url.Values{"return": {"/devices"}}, Roles: few},
		contract.Case{Name: "login-info-return-twice", Path: "/api/remote/login-info", Query: url.Values{"return": {"/remote/a", "/remote/b"}}, Roles: ends},
		contract.Case{Name: "login-info-head", Method: "HEAD", Path: "/api/remote/login-info", Roles: ends},

		// The guest page: a token in the query or the guest cookie
		contract.Case{Name: "guest-info-without-token", Path: "/api/remote/guest-info"},
		contract.Case{Name: "guest-info-unknown-token", Path: "/api/remote/guest-info", Query: url.Values{"t": {"contract-no-such-link"}}, NoCookies: true},
		contract.Case{
			Name: "guest-link", Method: "POST", Path: links, As: "admin",
			Body:          map[string]any{"peer_ids": []any{"900100001"}, "ttl_minutes": 10, "label": "Contract remote"},
			Capture:       map[string]string{"link": "id"},
			CaptureSecret: map[string]string{"glink": "token"},
		},
		contract.Case{Name: "guest-info", Path: "/api/remote/guest-info", Query: url.Values{"t": {"{secret:glink}"}}, NoCookies: true},
		contract.Case{Name: "guest-info-guest-parameter", Path: "/api/remote/guest-info", Query: url.Values{"guest": {"{secret:glink}"}}, NoCookies: true, Roles: ends},
		contract.Case{
			Name: "guest-info-from-cookie", Path: "/api/remote/guest-info", NoCookies: true, Roles: ends,
			Headers: map[string]string{"Cookie": "nd.guest={secret:glink}"},
		},
		contract.Case{Name: "viewer-guest", Path: viewer + "900100001", Query: guest, NoCookies: true},
		contract.Case{Name: "viewer-guest-other-device", Path: viewer + "900100002", Query: guest, NoCookies: true},
		contract.Case{
			Name: "viewer-guest-cookie-other-device", Path: viewer + "900100002", NoCookies: true, Roles: ends,
			Headers: map[string]string{"Cookie": "nd.guest={secret:glink}"},
		},
		contract.Case{
			Name: "viewer-guest-cookie", Path: viewer + "900100001", NoCookies: true, Roles: ends,
			Headers: map[string]string{"Cookie": "nd.guest={secret:glink}"},
		},
		// A console session with device.connect wins over the link. Only roles
		// with device.connect: for every other role the link wins and sets the
		// guest cookie in the role's cookie jar, which every later group would
		// send (the sockets of ws-rendezvous and ws-relay then refuse a
		// signed-out browser with 403 instead of 401)
		contract.Case{Name: "viewer-guest-with-session", Path: viewer + "900100001", Query: guest, Roles: contract.Roles("operator", "global_admin", "super_admin", "admin")},
		contract.Case{Name: "guest-link-deleted", Method: "DELETE", Path: links + "/{link}", As: "admin"},
		contract.Case{Name: "guest-info-after-delete", Path: "/api/remote/guest-info", Query: url.Values{"t": {"{secret:glink}"}}, NoCookies: true, Roles: ends},
		contract.Case{Name: "viewer-guest-after-delete", Path: viewer + "900100001", Query: guest, NoCookies: true, Roles: ends},

		// The remote desk
		contract.Case{Name: "desk", Path: "/api/remote/desk"},

		// Old addresses
		contract.Case{Name: "dashboard-redirect", Path: "/remote-dashboard"},
		contract.Case{Name: "dashboard-redirect-head", Method: "HEAD", Path: "/remote-dashboard", Roles: ends},
		contract.Case{Name: "cdap-redirect", Path: "/remote-cdap/900100001"},
		contract.Case{Name: "cdap-redirect-short-id", Path: "/remote-cdap/ab", Roles: few},
		contract.Case{Name: "cdap-redirect-odd-id", Path: "/remote-cdap/abc%20def", Roles: few},
		contract.Case{Name: "desktop-redirect", Path: "/remote-desktop/900100001"},
		contract.Case{Name: "desktop-redirect-odd-id", Path: "/remote-desktop/x.y.z", Roles: few},
		contract.Case{Name: "desktop-redirect-encoded-id", Path: "/remote-desktop/9001%2D00001", Roles: few},

		// The viewer's data
		contract.Case{Name: "viewer", Path: viewer + "900100001"},
		contract.Case{Name: "viewer-other-device", Path: viewer + "900100002"},
		contract.Case{Name: "viewer-transport-cdap", Path: viewer + "900100001", Query: url.Values{"transport": {"CDAP"}}},
		contract.Case{Name: "viewer-transport-unknown", Path: viewer + "900100001", Query: url.Values{"transport": {"vnc"}}, Roles: few},
		contract.Case{Name: "viewer-mock", Path: viewer + "900100001", Query: url.Values{"mock": {"1"}, "scenario": {"slow"}}, Roles: few},
		contract.Case{Name: "viewer-unknown", Path: viewer + "900199999"},
		contract.Case{Name: "viewer-short-id", Path: viewer + "ab"},
		contract.Case{Name: "viewer-login", Path: viewer + "login"},
		contract.Case{Name: "viewer-remote-target-unknown", Path: viewer + "rt_contract-none"},
		contract.Case{Name: "viewer-mesh-share", Path: viewer + "900100001", Query: url.Values{"mesh_share": {"contract-no-such-share"}}},
		contract.Case{Name: "viewer-mesh-share-signed-out", Path: viewer + "900100001", Query: url.Values{"mesh_share": {"contract-no-such-share"}}, NoCookies: true, Roles: ends},

		// The native remote sessions, before and while an agent is there
		contract.Case{Name: "sessions-before", Path: "/api/remote/sessions"},
		contract.Case{Name: "session-before", Path: "/api/remote/session/900100001"},
		contract.Case{Name: "agent", Path: "/ws/remote-agent/900100001", Query: agent, NoCookies: true, Socket: &contract.Socket{Name: "agent"}},
		contract.Case{Name: "sessions-with-agent", Path: "/api/remote/sessions"},
		contract.Case{Name: "session-with-agent", Path: "/api/remote/session/900100001"},
		contract.Case{Name: "session-unknown", Path: "/api/remote/session/900199999"},
		contract.Case{Name: "agent-closed", Path: "/ws/remote-agent/900100001", Query: agent, NoCookies: true, Socket: &contract.Socket{Name: "agent", Close: true}},
	)
}
