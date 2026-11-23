//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// POST /api/nd/remote-agent-token: an agent asks with its device token
// for a single-use token of its remote desktop socket
// /ws/remote-agent/<id>. The device is the instance's own: the token of
// 900100001 in /stack/secrets/device_token, that of 900100002 for another
// device. As a device (signed out, no cookies, no CSRF token) it gets a
// token, opens its socket with it once, and is refused the second time.
// Then what comes before the route: the device named in the body, the
// query or X-Device-Id, a device id that is not text, no token, a token
// nobody issued, Basic, and each role's session in front of a device's
// request, with and without the CSRF token (the device API sits before
// the CSRF gate).
func init() {
	const path = "/api/nd/remote-agent-token"
	device := map[string]string{"Authorization": "Bearer {secret:device_token}"}
	other := map[string]string{"Authorization": "Bearer {secret:device_token_other}"}
	asDevice := func(c contract.Case) contract.Case {
		c.NoCSRF, c.NoCookies, c.Roles = true, true, contract.Roles("signed-out")
		if c.Method == "" {
			c.Method = "POST"
		}
		if c.Path == "" {
			c.Path = path
		}
		if c.Headers == nil {
			c.Headers = device
		}
		return c
	}
	empty := map[string]any{}

	contract.Group("remote-agent-token",
		asDevice(contract.Case{Name: "issue", Body: empty, CaptureSecret: map[string]string{"agent_token": "token"}}),
		contract.Case{
			Name: "agent-with-token", Path: "/ws/remote-agent/900100001", Query: url.Values{"token": {"{secret:agent_token}"}},
			NoCookies: true, Roles: contract.Roles("signed-out"), Socket: &contract.Socket{Name: "a", Close: true},
		},
		contract.Case{
			Name: "agent-with-token-again", Path: "/ws/remote-agent/900100001", Query: url.Values{"token": {"{secret:agent_token}"}},
			NoCookies: true, Roles: contract.Roles("signed-out"), Socket: &contract.Socket{Name: "b", Close: true},
		},
		asDevice(contract.Case{Name: "issue-with-device-id", Body: map[string]any{"device_id": "900100001"}}),
		asDevice(contract.Case{Name: "issue-device-id-empty", Body: map[string]any{"device_id": ""}}),
		asDevice(contract.Case{Name: "issue-device-id-zero", Body: map[string]any{"device_id": 0}}),
		asDevice(contract.Case{Name: "issue-device-id-number", Body: map[string]any{"device_id": 900100001}}),
		asDevice(contract.Case{Name: "issue-device-id-list", Body: map[string]any{"device_id": []any{"900100001"}}}),
		asDevice(contract.Case{Name: "issue-device-id-other", Body: map[string]any{"device_id": "900100002"}}),
		asDevice(contract.Case{Name: "issue-device-id-invalid", Body: map[string]any{"device_id": "x"}}),
		asDevice(contract.Case{Name: "issue-query-other", Body: empty, Query: url.Values{"device_id": {"900100002"}}}),
		asDevice(contract.Case{
			Name: "issue-header-other", Body: empty,
			Headers: map[string]string{"Authorization": "Bearer {secret:device_token}", "X-Device-Id": "900100002"},
		}),
		asDevice(contract.Case{Name: "issue-other-device", Body: empty, Headers: other}),
		asDevice(contract.Case{Name: "issue-other-device-naming-first", Body: map[string]any{"device_id": "900100001"}, Headers: other}),
		asDevice(contract.Case{Name: "issue-without-token", Body: empty, Headers: map[string]string{}}),
		asDevice(contract.Case{Name: "issue-unknown-token", Body: empty, Headers: map[string]string{"Authorization": "Bearer contract-no-such-token"}}),
		asDevice(contract.Case{Name: "issue-basic", Body: empty, Headers: map[string]string{"Authorization": "Basic Y29udHJhY3Q6Y29udHJhY3Q="}}),
		asDevice(contract.Case{Name: "issue-device-header-alone", Body: empty, Headers: map[string]string{"X-Device-Id": "900100001"}}),
		asDevice(contract.Case{Name: "issue-form", RawBody: "device_id=900100001", ContentType: "application/x-www-form-urlencoded"}),
		asDevice(contract.Case{Name: "issue-bad-json", RawBody: `{"device_id":`, ContentType: "application/json"}),

		// A browser's session in front of a device's request
		contract.Case{Name: "issue-with-session", Method: "POST", Path: path, Headers: device, Body: empty},
		contract.Case{Name: "issue-with-session-without-csrf", Method: "POST", Path: path, Headers: device, Body: empty, NoCSRF: true},
		contract.Case{Name: "issue-session-only", Method: "POST", Path: path, Body: empty},
	)
}
