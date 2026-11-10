//go:build contract

package contract_test

import (
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The language routes under /api/i18n. Nothing here writes a language file:
// the instances share the lang folder of the source tree, so uploads and
// deletes are sent only in forms the routes refuse. The choice of a
// language is stored for a signed-in account and cannot be taken back
// through the API, so only a signed-out visitor makes one.
func init() {
	refused := contract.Roles("signed-out", "pro", "guest_issuer", "viewer", "operator", "global_admin")
	jsonFile := func(name, content string) []contract.Part {
		return []contract.Part{{Field: "file", FileName: name, ContentType: "application/json", Content: []byte(content)}}
	}

	contract.Group("i18n",
		contract.Case{Name: "languages", Path: "/api/i18n/languages"},
		contract.Case{Name: "languages-head", Method: "HEAD", Path: "/api/i18n/languages", Roles: contract.Roles("signed-out", "admin")},
		contract.Case{
			Name: "languages-not-modified", Path: "/api/i18n/languages",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{
			Name: "languages-no-compression", Path: "/api/i18n/languages",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("signed-out", "admin"),
		},
		contract.Case{Name: "translations-de", Path: "/api/i18n/translations/de"},
		contract.Case{Name: "translations-zh-tw", Path: "/api/i18n/translations/zh-TW", Roles: contract.Roles("signed-out", "viewer")},
		contract.Case{Name: "translations-unknown", Path: "/api/i18n/translations/xx", Roles: contract.Roles("signed-out", "viewer")},
		contract.Case{Name: "translations-not-a-code", Path: "/api/i18n/translations/de.json", Roles: contract.Roles("signed-out", "viewer")},
		contract.Case{Name: "translations-inherited-name", Path: "/api/i18n/translations/constructor", Roles: contract.Roles("signed-out", "viewer")},
		contract.Case{
			Name: "translations-no-compression", Path: "/api/i18n/translations/de",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: contract.Roles("signed-out"),
		},

		contract.Case{
			Name: "set-de", Method: "POST", Path: "/api/i18n/set/de",
			Body: map[string]any{}, Roles: contract.Roles("signed-out", "pro"),
		},
		// The suite keeps the language cookie: put it back to the language
		// its Accept-Language header asks for anyway
		contract.Case{
			Name: "set-back-to-en", Method: "POST", Path: "/api/i18n/set/en",
			Body: map[string]any{}, Roles: contract.Roles("signed-out", "pro"),
		},
		contract.Case{
			Name: "set-unknown", Method: "POST", Path: "/api/i18n/set/xx",
			Body: map[string]any{},
		},
		contract.Case{
			Name: "set-not-a-code", Method: "POST", Path: "/api/i18n/set/DE",
			Body: map[string]any{},
		},
		contract.Case{
			Name: "set-without-csrf-token", Method: "POST", Path: "/api/i18n/set/de",
			Body: map[string]any{}, NoCSRF: true,
		},
		contract.Case{
			Name: "set-as-form", Method: "POST", Path: "/api/i18n/set/xx",
			RawBody: "a=1", ContentType: "application/x-www-form-urlencoded", Roles: contract.Roles("signed-out", "admin"),
		},

		contract.Case{Name: "validate-de", Path: "/api/i18n/validate/de"},
		contract.Case{Name: "validate-en", Path: "/api/i18n/validate/en", Roles: contract.Roles("viewer", "admin")},
		contract.Case{Name: "validate-unknown", Path: "/api/i18n/validate/xx", Roles: contract.Roles("signed-out", "viewer", "admin")},

		contract.Case{
			Name: "upload-refused", Method: "POST", Path: "/api/i18n/upload",
			Multipart: jsonFile("zz.json", `{"_meta":{"code":"zz","name":"Contract"},"nav":{"devices":"x"}}`),
			Roles:     refused,
		},
		contract.Case{
			Name: "upload-without-csrf-token", Method: "POST", Path: "/api/i18n/upload",
			Multipart: jsonFile("zz.json", `{"_meta":{"code":"zz"}}`), NoCSRF: true,
		},
		contract.Case{
			Name: "upload-without-a-file", Method: "POST", Path: "/api/i18n/upload",
			Multipart: []contract.Part{{Field: "code", Content: []byte("zz")}},
		},
		contract.Case{
			Name: "upload-as-json", Method: "POST", Path: "/api/i18n/upload",
			Body: map[string]any{"code": "zz"},
		},
		contract.Case{
			Name: "upload-file-that-is-not-json", Method: "POST", Path: "/api/i18n/upload",
			Multipart: jsonFile("zz.json", `{"_meta":{"code":"zz"}`),
		},
		contract.Case{
			Name: "upload-other-file-type", Method: "POST", Path: "/api/i18n/upload",
			Multipart: []contract.Part{{Field: "file", FileName: "zz.txt", ContentType: "text/plain", Content: []byte(`{"_meta":{"code":"zz"}}`)}},
		},
		contract.Case{
			Name: "upload-code-too-long", Method: "POST", Path: "/api/i18n/upload",
			Multipart: jsonFile("zz.json", `{"_meta":{"code":"contract"},"nav":{"devices":"x"}}`),
		},
		contract.Case{
			Name: "upload-file-over-the-limit", Method: "POST", Path: "/api/i18n/upload",
			Multipart: jsonFile("zz.json", `{"_meta":{"code":"zz"},"pad":"`+strings.Repeat("x", 1<<20)+`"}`),
			Roles:     contract.Roles("signed-out", "viewer", "server_admin", "admin"),
		},
		contract.Case{
			Name: "upload-in-another-field", Method: "POST", Path: "/api/i18n/upload",
			Multipart: []contract.Part{{Field: "other", FileName: "zz.json", ContentType: "application/json", Content: []byte(`{"_meta":{"code":"zz"}}`)}},
			Roles:     contract.Roles("viewer", "admin"),
		},

		contract.Case{Name: "delete-refused", Method: "DELETE", Path: "/api/i18n/zz", Roles: refused},
		contract.Case{Name: "delete-english", Method: "DELETE", Path: "/api/i18n/en"},
		contract.Case{Name: "delete-polish", Method: "DELETE", Path: "/api/i18n/pl", Roles: contract.Roles("viewer", "server_admin", "admin")},
		contract.Case{Name: "delete-without-csrf-token", Method: "DELETE", Path: "/api/i18n/en", NoCSRF: true},
	)
}
