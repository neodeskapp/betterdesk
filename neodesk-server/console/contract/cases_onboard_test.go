//go:build contract

package contract_test

import (
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// Add a computer and Quick support (console/routes/onboard). These routes
// came with the Go console; their goldens were recorded from Go
// (CONTRACT_RECORD_NEW).
//
// The signed-in routes run for every role and signed out. The routes that
// take a code from outside share one limit of 20 a minute per address, and
// every request of the suite comes from one address, so they run in the
// admin's turn only, without the session (as a visitor or a client would
// send them): a code made in the turn is used, redeemed and refused, a
// wrong and a revoked code get the same answer, and the last requests run
// into the limit. A test instance has no client release, so the page says
// release_unavailable and a download 503. What a turn makes it removes
// again (the invite, the session, both devices); the two devices have ids
// the seed does not hold (900400001, 900400002), so the pending
// registrations of the seed stay for the groups after this one. An earlier
// group (automation) leaves an SMTP server saved that refuses connections,
// so in a whole run the mail check says mail is set up and sending an
// invite fails.
func init() {
	// The roles that make invites (enrollment.approve) and quick support
	// sessions (device.connect). Only their turns capture a code: a refusal
	// has a "code" field too, which is no secret.
	approvers := contract.Roles("global_admin", "super_admin", "admin")
	others := contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "operator", "server_admin")
	supporters := contract.Roles("operator", "global_admin", "super_admin", "admin")
	nonSupporters := contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "server_admin")
	visitor := func(c contract.Case) contract.Case {
		c.NoCookies, c.NoCSRF, c.Roles = true, true, contract.Roles("admin")
		return c
	}
	cases := []contract.Case{
		{Name: "status", Path: "/api/onboard/status"},
		{Name: "invites-empty", Path: "/api/onboard/invites"},
		{Name: "invite-bad-email", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"email": "not an address"}},
		{Name: "invite-bad-lifetime", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"lifetime_hours": 721}},
		{Name: "invite-bad-uses", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"uses": 0}},
		{Name: "invite-unknown-folder", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"folder_id": 999999999}},
		{Name: "invite-create-refused", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"name": "Contract desk"}, Roles: others},
		{
			Name: "invite-create", Method: "POST", Path: "/api/onboard/invites",
			Body:          map[string]any{"name": "Contract desk", "lifetime_hours": 24, "uses": 1},
			Capture:       map[string]string{"invite": "invite.id"},
			CaptureSecret: map[string]string{"invite_code": "code"},
			Roles:         approvers,
		},
		{Name: "invites-one", Path: "/api/onboard/invites"},
		{Name: "invite-mail-fails", Method: "POST", Path: "/api/onboard/invites/{invite}/mail", Body: map[string]any{"code": "{secret:invite_code}", "to": "ana@example.com"}, Roles: approvers},

		visitor(contract.Case{Name: "public-invite", Path: "/api/onboard/public/{secret:invite_code}"}),
		visitor(contract.Case{Name: "public-download-no-release", Path: "/api/onboard/public/{secret:invite_code}/download", Query: map[string][]string{"os": {"windows"}, "arch": {"x64"}, "format": {"exe"}}}),
		visitor(contract.Case{Name: "install-script", Path: "/join/{secret:invite_code}/install.sh"}),
		visitor(contract.Case{
			Name: "join", Method: "POST", Path: "/api/onboard/join",
			Body:          map[string]any{"code": "{secret:invite_code}", "device_id": "900400001", "hostname": "contract-desk", "platform": "Linux"},
			CaptureSecret: map[string]string{"joined_token": "device_token"},
		}),
		visitor(contract.Case{Name: "join-used", Method: "POST", Path: "/api/onboard/join", Body: map[string]any{"code": "{secret:invite_code}", "device_id": "900200003"}}),
		// The invite sheet follows the join: the used-up invite still answers
		// with the computer that joined
		{Name: "invite-show-joined", Path: "/api/onboard/invites/{invite}"},
		{Name: "invite-show-unknown", Path: "/api/onboard/invites/0123456789abcdef", Roles: approvers},
		// A join with another device's ID and no proof is refused: the ID is
		// enrolled now, and the join brings neither its key nor the proof of
		// it. The invite is not spent, and its sheet names the refused ID.
		{
			Name: "invite-second", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{"name": "Second desk"},
			Capture: map[string]string{"second": "invite.id"}, CaptureSecret: map[string]string{"second_code": "code"},
			Roles: approvers,
		},
		visitor(contract.Case{Name: "join-other-device-no-proof", Method: "POST", Path: "/api/onboard/join", Body: map[string]any{"code": "{secret:second_code}", "device_id": "900400001"}}),
		{Name: "invite-show-refused", Path: "/api/onboard/invites/{second}", Roles: approvers},
		{Name: "invite-second-cleanup", Method: "DELETE", Path: "/api/onboard/invites/{second}", Roles: approvers},
		visitor(contract.Case{Name: "public-used", Path: "/api/onboard/public/{secret:invite_code}"}),
		visitor(contract.Case{Name: "public-wrong", Path: "/api/onboard/public/ZZZZ-ZZZZ"}),
		visitor(contract.Case{Name: "join-bad-device", Method: "POST", Path: "/api/onboard/join", Body: map[string]any{"code": "ZZZZZZZZ", "device_id": "no/id"}}),

		{
			Name: "invite-to-revoke", Method: "POST", Path: "/api/onboard/invites", Body: map[string]any{},
			Capture: map[string]string{"revoked": "invite.id"}, CaptureSecret: map[string]string{"revoked_code": "code"},
			Roles: approvers,
		},
		{Name: "invite-revoke", Method: "DELETE", Path: "/api/onboard/invites/{revoked}"},
		{Name: "invite-revoke-again", Method: "DELETE", Path: "/api/onboard/invites/{revoked}"},
		visitor(contract.Case{Name: "public-revoked", Path: "/api/onboard/public/{secret:revoked_code}"}),

		{Name: "sessions-empty", Path: "/api/support/sessions"},
		{Name: "session-create-refused", Method: "POST", Path: "/api/support/sessions", Body: map[string]any{}, Roles: nonSupporters},
		{
			Name: "session-create", Method: "POST", Path: "/api/support/sessions", Body: map[string]any{"name": "Contract person"},
			Capture: map[string]string{"session": "session.id"}, CaptureSecret: map[string]string{"support_code": "code"},
			Roles: supporters,
		},
		{Name: "session-get", Path: "/api/support/sessions/{session}"},
		{Name: "sessions-one", Path: "/api/support/sessions"},
		{Name: "session-password-too-early", Method: "POST", Path: "/api/support/sessions/{session}/password", Body: map[string]any{}},
		visitor(contract.Case{Name: "public-support", Path: "/api/onboard/public/{secret:support_code}"}),
		visitor(contract.Case{Name: "run-script", Path: "/s/{secret:support_code}/run.sh"}),
		visitor(contract.Case{
			Name: "support-redeem", Method: "POST", Path: "/api/support/redeem", Body: map[string]any{"code": "{secret:support_code}"},
			CaptureSecret: map[string]string{"session_token": "session_token"},
		}),
		visitor(contract.Case{Name: "support-redeem-again", Method: "POST", Path: "/api/support/redeem", Body: map[string]any{"code": "{secret:support_code}"}}),
	}
	app := func(name, method, path string, body any) contract.Case {
		return visitor(contract.Case{Name: name, Method: method, Path: path, Body: body,
			Headers: map[string]string{"Authorization": "Bearer {secret:session_token}"}})
	}
	cases = append(cases,
		app("app-session", "GET", "/api/support/session", nil),
		app("app-consent-too-early", "POST", "/api/support/session/consent", map[string]any{"allowed": true}),
		app("app-device", "POST", "/api/support/session/device", map[string]any{"device_id": "900400002", "hostname": "contract-laptop", "platform": "Windows"}),
		app("app-consent", "POST", "/api/support/session/consent", map[string]any{"allowed": true}),
		app("app-password", "POST", "/api/support/session/password", map[string]any{"password": "Contract-pw1"}),
		contract.Case{Name: "session-allowed", Path: "/api/support/sessions/{session}", Roles: contract.Roles("admin")},
		contract.Case{Name: "session-password", Method: "POST", Path: "/api/support/sessions/{session}/password", Body: map[string]any{}, Roles: contract.Roles("admin")},
		contract.Case{Name: "session-password-twice", Method: "POST", Path: "/api/support/sessions/{session}/password", Body: map[string]any{}, Roles: contract.Roles("admin")},
		contract.Case{Name: "device-password-none", Method: "POST", Path: "/api/support/devices/900400002/password", Body: map[string]any{}},
		app("app-end", "POST", "/api/support/session/end", map[string]any{}),
		app("app-after-end", "GET", "/api/support/session", nil),
		visitor(contract.Case{Name: "app-made-up-token", Path: "/api/support/session", Headers: map[string]string{"Authorization": "Bearer " + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}),
		contract.Case{Name: "session-ended", Path: "/api/support/sessions/{session}"},
		contract.Case{Name: "session-end-again", Method: "DELETE", Path: "/api/support/sessions/{session}"},
		contract.Case{
			Name: "session-to-end", Method: "POST", Path: "/api/support/sessions", Body: map[string]any{},
			Capture: map[string]string{"waiting": "session.id"},
		},
		contract.Case{Name: "session-end-waiting", Method: "DELETE", Path: "/api/support/sessions/{waiting}"},
	)
	// Use the rest of the limit: fourteen requests of this turn went through
	// it (the visitor cases above, the app's session routes do not count)
	for i := 1; i <= 6; i++ {
		cases = append(cases, visitor(contract.Case{Name: fmt.Sprintf("limit-%02d", i), Path: "/api/onboard/public/ZZZZZZZZ"}))
	}
	cases = append(cases,
		visitor(contract.Case{Name: "limit-reached", Method: "POST", Path: "/api/onboard/join", Body: map[string]any{"code": "ZZZZZZZZ", "device_id": "900200004"}}),
		contract.Case{Name: "invite-cleanup", Method: "DELETE", Path: "/api/onboard/invites/{invite}"},
		contract.Case{Name: "cleanup-joined", Method: "DELETE", Path: "/api/devices/900400001", Query: map[string][]string{"hard": {"true"}}, As: "admin", Roles: contract.Roles("admin")},
		contract.Case{Name: "cleanup-supported", Method: "DELETE", Path: "/api/devices/900400002", Query: map[string][]string{"hard": {"true"}}, As: "admin", Roles: contract.Roles("admin")},
	)
	contract.Group("onboard", cases...)
}
