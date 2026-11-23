//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// routes/phase4_5.routes.js: the operator's own profile and what the
// consent popup shows of them, agent templates and enrollment with a
// template, the generic Support Agent's releases, its pairing, its
// downloads and the portal's installer list.
//
// The profile cases save the profile of the account whose turn it is and
// empty it again at the end of the turn (saving {} empties every field,
// BUGS C3). A test instance has imported no release (that needs GitHub),
// so the release, download and installer cases cover the empty cache; the
// tests of console/routes/phase45 replay what the Node routes answered
// with releases in the cache. Pairing is sent for devices the API server
// does not know, so no seeded device is renamed.
//
// A download address with a "." or ".." segment is left out: the Go
// console's route table matches no such address, so the Node console
// answers it while it runs behind Go (see the tests of
// console/routes/phase45).
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	writers := contract.Roles("global_admin", "admin")
	profile := map[string]any{
		"first_name": "Ada", "last_name": "Lovelace", "email": "ada@example.test", "phone": "+49 30 1234",
		"role_display": "Engineer", "avatar_url": "not-an-address",
	}
	put := func(name string, body any, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "PUT", Path: "/api/users/me/profile", Body: body, Roles: roles}
	}
	get := func(name string, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/users/me/profile", Roles: roles}
	}
	templateDelete := func(name, id string) contract.Case {
		return contract.Case{Name: name, Method: "DELETE", Path: "/api/agent-templates/" + id, Roles: writers}
	}
	enroll := func(name string, body any) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/nd/enroll", Body: body, Roles: some}
	}
	activate := func(name string, body any) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/agent-pairing/activate", Body: body, Roles: writers}
	}
	toString := map[string]any{"toString": 1}

	contract.Group("phase4-5",
		// The profile
		get("profile", nil),
		contract.Case{Name: "profile-head", Method: "HEAD", Path: "/api/users/me/profile", Roles: some},
		contract.Case{Name: "operator-info", Path: "/api/nd/operator-info"},
		put("profile-save", profile, nil),
		get("profile-after-save", writers),
		contract.Case{Name: "operator-info-after-save", Path: "/api/nd/operator-info", Query: url.Values{"session_id": {"remote-42"}}, Roles: writers},
		put("profile-save-the-same-address", profile, writers),
		put("profile-save-one-field", map[string]any{"phone": "555"}, writers),
		get("profile-after-one-field", writers),
		contract.Case{Name: "operator-info-after-one-field", Path: "/api/nd/operator-info", Roles: writers},
		put("profile-save-other-types", map[string]any{
			"first_name": 42, "last_name": true, "email": 1e21, "phone": nil, "role_display": []any{"a", 1, nil}, "avatar_url": "",
		}, writers),
		get("profile-after-other-types", writers),
		put("profile-save-objects", map[string]any{"first_name": map[string]any{"a": 1}, "role_display": []any{}}, writers),
		get("profile-after-objects", writers),
		put("profile-save-long-values", map[string]any{
			"first_name": strings.Repeat("x", 250), "last_name": strings.Repeat("ü", 199) + "😀", "phone": "  padded  ",
		}, writers),
		get("profile-after-long-values", writers),
		contract.Case{Name: "operator-info-after-long-values", Path: "/api/nd/operator-info", Roles: writers},
		put("profile-save-email-too-long", map[string]any{"email": strings.Repeat("e", 201)}, writers),
		put("profile-save-email-200", map[string]any{"email": strings.Repeat("e", 200)}, writers),
		put("profile-save-email-number", map[string]any{"email": 123456}, writers),
		put("profile-save-email-that-cannot-be-text", map[string]any{"email": toString}, writers),
		put("profile-save-name-that-cannot-be-text", map[string]any{"last_name": toString}, writers),
		get("profile-after-failed-saves", writers),
		contract.Case{
			Name: "profile-save-a-list", Method: "PUT", Path: "/api/users/me/profile",
			RawBody: `["first_name"]`, ContentType: "application/json", Roles: writers,
		},
		contract.Case{Name: "profile-save-without-csrf-token", Method: "PUT", Path: "/api/users/me/profile", Body: profile, NoCSRF: true, Roles: some},
		put("profile-empty", map[string]any{}, nil),
		get("profile-after-empty", writers),

		// The consent popup's query
		contract.Case{Name: "operator-info-long-session", Path: "/api/nd/operator-info", Query: url.Values{"session_id": {strings.Repeat("s", 200)}}, Roles: some},
		contract.Case{Name: "operator-info-session-list", Path: "/api/nd/operator-info", Query: url.Values{"session_id": {"a", "b"}}, Roles: some},
		contract.Case{Name: "operator-info-session-object", Path: "/api/nd/operator-info", Query: url.Values{"session_id[x]": {"a"}}, Roles: some},
		contract.Case{Name: "operator-info-session-that-cannot-be-text", Path: "/api/nd/operator-info", Query: url.Values{"session_id[toString]": {"a"}}, Roles: some},
		contract.Case{Name: "operator-info-other-spelling", Path: "/API/ND/Operator-Info/", Roles: some},

		// Agent templates
		contract.Case{Name: "templates", Path: "/api/agent-templates"},
		contract.Case{Name: "template-create", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{"name": "Kiosk", "description": "front desk", "config": map[string]any{"tags": []any{"kiosk"}}}},
		contract.Case{Name: "template-create-without-name", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{}, Roles: writers},
		contract.Case{Name: "template-create-blank-name", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{"name": " \t "}, Roles: writers},
		contract.Case{Name: "template-create-name-number", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{"name": 7}, Roles: writers},
		contract.Case{Name: "template-create-name-that-cannot-be-text", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{"name": toString}, Roles: writers},
		contract.Case{
			Name: "template-create-description-that-cannot-be-text", Method: "POST", Path: "/api/agent-templates",
			Body: map[string]any{"name": "x", "description": toString}, Roles: writers,
		},
		contract.Case{Name: "template-create-without-csrf-token", Method: "POST", Path: "/api/agent-templates", Body: map[string]any{"name": "x"}, NoCSRF: true, Roles: some},
		contract.Case{Name: "template-delete", Method: "DELETE", Path: "/api/agent-templates/1"},
		templateDelete("template-delete-zero", "0"),
		templateDelete("template-delete-negative", "-1"),
		templateDelete("template-delete-not-a-number", "abc"),
		templateDelete("template-delete-number-with-letters-behind", "12abc"),
		templateDelete("template-delete-hexadecimal", "0x10"),
		templateDelete("template-delete-huge", "99999999999999999999999"),
		templateDelete("template-delete-with-a-space", "%205"),
		templateDelete("template-delete-fraction", "0.5"),

		// Enrollment with a template token, without a session
		enroll("enroll-without-fields", map[string]any{}),
		enroll("enroll-without-device", map[string]any{"enrollment_token": "t"}),
		enroll("enroll-unknown-token", map[string]any{"enrollment_token": "t", "device_id": "d"}),
		enroll("enroll-numbers", map[string]any{"enrollment_token": 1, "device_id": 2}),
		enroll("enroll-zeros", map[string]any{"enrollment_token": 0, "device_id": "d"}),
		enroll("enroll-token-that-cannot-be-text", map[string]any{"enrollment_token": toString, "device_id": "d"}),
		contract.Case{Name: "enroll-without-csrf-token", Method: "POST", Path: "/api/nd/enroll", Body: map[string]any{"enrollment_token": "t", "device_id": "d"}, NoCSRF: true, Roles: some},

		// Releases of the generic agent, its downloads and the portal
		contract.Case{Name: "releases", Path: "/api/agent-releases"},
		contract.Case{Name: "signing-status", Path: "/api/agent-releases/signing-status"},
		contract.Case{Name: "download-unknown", Path: "/downloads/generic-agent/support-v1.0.0/agent.zip", Roles: some},
		contract.Case{Name: "download-head", Method: "HEAD", Path: "/downloads/generic-agent/support-v1.0.0/agent.zip", Roles: some},
		contract.Case{Name: "download-name-with-a-space", Path: "/downloads/generic-agent/support-v1.0.0/agent%20x.zip", Roles: some},
		contract.Case{Name: "download-escaped-slash", Path: "/downloads/generic-agent/..%2F..%2Fetc/passwd", Roles: some},
		contract.Case{Name: "installers", Path: "/api/portal/installers"},
		contract.Case{Name: "installers-head", Method: "HEAD", Path: "/api/portal/installers", Roles: some},

		// Pairing a generic agent
		activate("activate-without-device", map[string]any{}),
		activate("activate-short-device", map[string]any{"device_id": "ab"}),
		activate("activate-device-with-a-slash", map[string]any{"device_id": "abc/def"}),
		activate("activate-unknown-device", map[string]any{"device_id": "contract-unknown-1", "display_name": "Front desk"}),
		activate("activate-unknown-device-in-a-folder", map[string]any{"device_id": " contract-unknown-2 ", "display_name": 5, "folder_id": "3"}),
		activate("activate-device-number", map[string]any{"device_id": 123456}),
		contract.Case{Name: "activate", Method: "POST", Path: "/api/agent-pairing/activate", Body: map[string]any{"device_id": "contract-unknown-3"}},
	)
}
