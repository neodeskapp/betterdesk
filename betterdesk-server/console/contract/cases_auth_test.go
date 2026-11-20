//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The sign-in routes (routes/auth.routes.js): what a sign-in refuses, the
// session of every role, sign-out through the API and the page (GET /logout only explains that a POST is needed), single
// sign-on without a provider, and one account of its own that signs in, is
// signed in a second time, changes its password and signs out.
//
// No case locks an account. A lockout takes ten failed sign-ins, and every
// failed sign-in of the run counts against the limit of 30 per address in
// 15 minutes, which then refuses every sign-in of the suite, the roles'
// own included. The lockout is in the flow test of console/routes/auth and
// in the recorded Node answers there (testdata/lockout_*.json).
//
// The two-factor routes change the caller's account, so the roles only send
// what is refused before anything is written. The whole enrolment, the
// lockout counters and the audit entries are in the flow test of
// console/routes/auth, which computes the codes.
//
// The account "contract_auth_tmp" and its two passwords exist only for
// these cases. The admin creates it in the signed-out turn and deletes it
// at the end of that turn.
const (
	authTmpUser        = "contract_auth_tmp"
	authTmpPassword    = "Contract-Auth-1!x"
	authTmpNewPassword = "Contract-Auth-2!y"
)

func init() {
	signedOut := contract.Roles("signed-out")
	guests := contract.Roles("signed-out", "pro")
	some := contract.Roles("signed-out", "viewer", "admin")
	login := func(name string, body any) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/auth/login", Body: body, Roles: signedOut}
	}

	contract.Group("auth",
		// The session of every role.
		contract.Case{Name: "verify", Path: "/api/auth/verify"},
		contract.Case{Name: "sessions", Path: "/api/auth/sessions"},
		contract.Case{Name: "totp-status", Path: "/api/auth/totp/status"},

		// A sign-in that is refused before or by the password check.
		login("login-empty-body", map[string]any{}),
		login("login-without-username", map[string]any{"password": "not-a-password"}),
		login("login-empty-password", map[string]any{"username": "e2e_viewer", "password": ""}),
		login("login-long-username", map[string]any{"username": strings.Repeat("a", 129), "password": "not-a-password"}),
		login("login-long-password", map[string]any{"username": "e2e_viewer", "password": strings.Repeat("p", 129)}),
		login("login-unknown-user", map[string]any{"username": "contract-nobody", "password": "not-a-password"}),
		login("login-wrong-password", map[string]any{"username": "e2e_viewer", "password": "not-the-password"}),
		login("login-number-as-username", map[string]any{"username": 12345, "password": "not-a-password"}),
		login("login-number-as-password", map[string]any{"username": "contract-nobody", "password": 12345}),
		contract.Case{
			Name: "login-list-as-body", Method: "POST", Path: "/api/auth/login",
			RawBody: `["e2e_viewer","not-the-password"]`, ContentType: "application/json", Roles: signedOut,
		},
		contract.Case{
			Name: "login-broken-json", Method: "POST", Path: "/api/auth/login",
			RawBody: `{"username":"e2e_viewer",`, ContentType: "application/json", Roles: signedOut,
		},
		contract.Case{
			Name: "login-as-form", Method: "POST", Path: "/api/auth/login",
			RawBody: "username=e2e_viewer&password=not-the-password", ContentType: "application/x-www-form-urlencoded", Roles: signedOut,
		},
		contract.Case{
			Name: "login-as-text", Method: "POST", Path: "/api/auth/login",
			RawBody: `{"username":"e2e_viewer","password":"not-the-password"}`, ContentType: "text/plain", Roles: signedOut,
		},
		contract.Case{
			Name: "login-without-csrf", Method: "POST", Path: "/api/auth/login",
			Body:   map[string]any{"username": "e2e_viewer", "password": "not-the-password"},
			NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "login-wrong-csrf", Method: "POST", Path: "/api/auth/login",
			Body:    map[string]any{"username": "e2e_viewer", "password": "not-the-password"},
			Headers: map[string]string{"X-CSRF-Token": strings.Repeat("0", 128)}, Roles: some,
		},
		// A signed-in browser that sends a refused sign-in keeps its session.
		contract.Case{
			Name: "login-refused-while-signed-in", Method: "POST", Path: "/api/auth/login",
			Body: map[string]any{"username": "e2e_viewer"}, Roles: contract.Roles("viewer"),
		},

		// The second factor: only what is refused before anything changes.
		contract.Case{
			Name: "totp-verify-without-sign-in", Method: "POST", Path: "/api/auth/totp/verify",
			Body: map[string]any{"code": "123456"}, Roles: some,
		},
		contract.Case{
			Name: "totp-verify-without-csrf", Method: "POST", Path: "/api/auth/totp/verify",
			Body: map[string]any{"code": "123456"}, NoCSRF: true, Roles: signedOut,
		},
		contract.Case{Name: "totp-setup-refused", Method: "POST", Path: "/api/auth/totp/setup", Body: map[string]any{}, Roles: guests},
		contract.Case{
			Name: "totp-enable-short-code", Method: "POST", Path: "/api/auth/totp/enable",
			Body: map[string]any{"code": "123"},
		},
		contract.Case{Name: "totp-enable-without-code", Method: "POST", Path: "/api/auth/totp/enable", Body: map[string]any{}},
		contract.Case{
			Name: "totp-enable-not-set-up", Method: "POST", Path: "/api/auth/totp/enable",
			Body: map[string]any{"code": "123456"},
		},
		contract.Case{Name: "totp-disable-without-password", Method: "POST", Path: "/api/auth/totp/disable", Body: map[string]any{}},
		contract.Case{
			Name: "totp-disable-wrong-password", Method: "POST", Path: "/api/auth/totp/disable",
			Body: map[string]any{"password": "not-the-password"}, Roles: contract.Roles("signed-out", "pro", "viewer", "admin"),
		},
		// A password change counts against a limit of three in five minutes
		// per address, also when it is refused: only who is refused before
		// the limit sends one here.
		contract.Case{
			Name: "password-refused", Method: "POST", Path: "/api/auth/password",
			Body: map[string]any{"currentPassword": "a", "newPassword": "b", "confirmPassword": "b"}, Roles: guests,
		},

		// Single sign-on on an instance without an identity provider.
		contract.Case{Name: "oidc-status", Path: "/api/auth/oidc/status", Roles: some},
		contract.Case{Name: "oidc-authorize", Path: "/api/auth/oidc/authorize", Roles: some},
		contract.Case{
			Name: "oidc-authorize-with-return", Path: "/api/auth/oidc/authorize",
			Query: url.Values{"return_url": {"/devices?tab=1"}}, Roles: signedOut,
		},
		contract.Case{Name: "oidc-callback-without-query", Path: "/api/auth/oidc/callback", Roles: some},
		contract.Case{
			Name: "oidc-callback-unknown-state", Path: "/api/auth/oidc/callback",
			Query: url.Values{"code": {"contract-code"}, "state": {"contract-state"}}, Roles: signedOut,
		},
		contract.Case{
			Name: "oidc-callback-provider-error", Path: "/api/auth/oidc/callback",
			Query: url.Values{"error": {"access_denied"}, "state": {"contract-state"}}, Roles: signedOut,
		},
		contract.Case{
			Name: "oidc-short-callback-unknown-state", Path: "/api/oidc/callback",
			Query: url.Values{"code": {"contract-code"}, "state": {"contract-state"}}, Roles: some,
		},

		// One account of its own, in the signed-out turn.
		contract.Case{
			Name: "account-create", Method: "POST", Path: "/api/users", As: "admin", Roles: signedOut,
			Body:    map[string]any{"username": authTmpUser, "password": authTmpPassword, "role": "operator"},
			Capture: map[string]string{"account": "data.id"},
		},
		login("account-sign-in", map[string]any{
			"username": authTmpUser, "password": authTmpPassword,
			"return": "/devices?tab=1", "clientFingerprint": "contract-fingerprint-01",
		}),
		contract.Case{Name: "account-verify", Path: "/api/auth/verify", Roles: signedOut},
		contract.Case{Name: "account-sessions", Path: "/api/auth/sessions", Roles: signedOut},
		contract.Case{Name: "account-totp-status", Path: "/api/auth/totp/status", Roles: signedOut},
		// A second sign-in from another browser (no session cookie, and the
		// new one is not kept) ends the first: one session per account.
		contract.Case{
			Name: "account-sign-in-elsewhere", Method: "POST", Path: "/api/auth/login", Roles: signedOut, NoSession: true,
			Body: map[string]any{"username": authTmpUser, "password": authTmpPassword, "return": "//evil.example/"},
		},
		contract.Case{Name: "account-first-session-ended", Path: "/api/auth/verify", Roles: signedOut},
		login("account-sign-in-third", map[string]any{"username": authTmpUser, "password": authTmpPassword, "fingerprint": "contract-fingerprint-03"}),
		contract.Case{Name: "account-sessions-after", Path: "/api/auth/sessions", Roles: signedOut},
		contract.Case{
			Name: "account-password-change", Method: "POST", Path: "/api/auth/password", Roles: signedOut,
			Body: map[string]any{"currentPassword": authTmpPassword, "newPassword": authTmpNewPassword, "confirmPassword": authTmpNewPassword},
		},
		contract.Case{Name: "account-logout", Method: "POST", Path: "/api/auth/logout", Body: map[string]any{}, Roles: signedOut},
		contract.Case{Name: "account-verify-after-logout", Path: "/api/auth/verify", Roles: signedOut},
		login("account-old-password", map[string]any{"username": authTmpUser, "password": authTmpPassword}),
		login("account-new-password", map[string]any{"username": authTmpUser, "password": authTmpNewPassword}),
		contract.Case{Name: "account-logout-page", Path: "/logout", Roles: signedOut},
		contract.Case{Name: "account-verify-after-logout-page", Path: "/api/auth/verify", Roles: signedOut},
		// Signing out through the page is a POST with the CSRF token.
		contract.Case{Name: "account-logout-form-without-csrf", Method: "POST", Path: "/logout", NoCSRF: true, Roles: signedOut},
		contract.Case{Name: "account-verify-after-refused-logout-form", Path: "/api/auth/verify", Roles: signedOut},
		contract.Case{Name: "account-logout-form", Method: "POST", Path: "/logout", Roles: signedOut},
		contract.Case{Name: "account-verify-after-logout-form", Path: "/api/auth/verify", Roles: signedOut},
		contract.Case{Name: "account-delete", Method: "DELETE", Path: "/api/users/{account}", As: "admin", Roles: signedOut},

		// Sign-out, at the end: it ends the role's session. Half the roles
		// sign out through the API, the others through the page: the page
		// refuses a GET and keeps the session, the form's POST signs out.
		contract.Case{
			Name: "logout-without-csrf", Method: "POST", Path: "/api/auth/logout", Body: map[string]any{},
			NoCSRF: true, Roles: contract.Roles("signed-out", "viewer"),
		},
		// A write to the API without a body has no content type.
		contract.Case{Name: "logout-without-body", Method: "POST", Path: "/api/auth/logout", Roles: contract.Roles("signed-out", "viewer")},
		contract.Case{
			Name: "logout", Method: "POST", Path: "/api/auth/logout", Body: map[string]any{},
			Roles: contract.Roles("signed-out", "pro", "viewer", "operator", "admin"),
		},
		contract.Case{
			Name: "logout-page", Path: "/logout",
			Roles: contract.Roles("signed-out", "pro", "guest_issuer", "global_admin", "server_admin", "super_admin"),
		},
		contract.Case{Name: "verify-after-logout", Path: "/api/auth/verify"},
		contract.Case{
			Name: "logout-form", Method: "POST", Path: "/logout",
			Roles: contract.Roles("signed-out", "pro", "guest_issuer", "global_admin", "server_admin", "super_admin"),
		},
		contract.Case{
			Name: "verify-after-logout-form", Path: "/api/auth/verify",
			Roles: contract.Roles("signed-out", "pro", "guest_issuer", "global_admin", "server_admin", "super_admin"),
		},
		contract.Case{Name: "logout-again", Method: "POST", Path: "/api/auth/logout", Body: map[string]any{}, Roles: contract.Roles("viewer", "admin")},
	)

	// GET /api/auth/oidc/session finishes a single sign-on. Without a code
	// the Go server knows, it only redirects.
	contract.Group("auth-oidc-session",
		contract.Case{Name: "without-code", Path: "/api/auth/oidc/session", Roles: some},
		contract.Case{
			Name: "unknown-code", Path: "/api/auth/oidc/session",
			Query: url.Values{"code": {"contract-code"}}, Roles: some,
		},
		contract.Case{
			Name: "code-twice", Path: "/api/auth/oidc/session",
			Query: url.Values{"code": {"a", "b"}}, Roles: signedOut,
		},
	)
}
