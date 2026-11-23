//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The support client generator. A test instance runs in download mode
// without templates and without a signing seed, so the generator is never
// ready: creating, changing and rebuilding a bundle answer "not ready",
// and there is no bundle for the download portal to show. Those paths,
// the builds and the downloads are pinned by the tests of
// console/routes/generator, which replay what the Node routes answered on
// a database with bundles and a module with templates.
//
// The template download from GitHub (POST /api/generator/module/install)
// is only sent while the terms are not accepted, where it stops before any
// request leaves the console. The terms are accepted in the last turn
// (admin), after the install cases of that turn; an instance keeps them
// accepted afterwards, as the matrix leaves it too.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	admins := contract.Roles("signed-out", "operator", "global_admin", "admin")
	last := contract.Roles("admin")
	tarball := []byte("not really a tar.gz")
	contract.Group("generator",
		contract.Case{Name: "old-address", Path: "/generator", Roles: some},
		contract.Case{Name: "old-address-html", Path: "/generator", Headers: map[string]string{"Accept": "text/html"}, Roles: some},
		contract.Case{Name: "old-address-head", Method: "HEAD", Path: "/generator", Roles: some},

		contract.Case{Name: "module-status", Path: "/api/generator/module/status"},
		contract.Case{Name: "install-before-terms", Method: "POST", Path: "/api/generator/module/install", Body: map[string]any{}},
		contract.Case{
			Name: "install-before-terms-repo", Method: "POST", Path: "/api/generator/module/install",
			Body: map[string]any{"repo": "  example/repo ", "tag": "v1"}, Roles: admins,
		},
		contract.Case{
			Name: "install-local-without-file", Method: "POST", Path: "/api/generator/module/install-local",
			Body: map[string]any{"version": "1.0"}, Roles: admins,
		},
		contract.Case{
			Name: "install-local-before-terms", Method: "POST", Path: "/api/generator/module/install-local",
			Multipart: []contract.Part{
				{Field: "version", Content: []byte("  2.0  ")},
				{Field: "archive", FileName: "templates.tar.gz", ContentType: "application/gzip", Content: tarball},
			},
			Roles: admins,
		},
		contract.Case{
			Name: "install-local-wrong-field", Method: "POST", Path: "/api/generator/module/install-local",
			Multipart: []contract.Part{{Field: "file", FileName: "templates.tar.gz", ContentType: "application/gzip", Content: tarball}},
			Roles:     admins,
		},
		contract.Case{
			Name: "install-local-form-without-file", Method: "POST", Path: "/api/generator/module/install-local",
			Multipart: []contract.Part{{Field: "version", Content: []byte("x")}}, Roles: admins,
		},
		contract.Case{Name: "accept-terms", Method: "POST", Path: "/api/generator/module/accept-terms", Body: map[string]any{}, Roles: last},
		contract.Case{Name: "module-status-after-terms", Path: "/api/generator/module/status", Roles: last},
		contract.Case{
			Name: "install-local-without-seed", Method: "POST", Path: "/api/generator/module/install-local",
			Multipart: []contract.Part{{Field: "archive", FileName: "ä b/../c.tar.gz", ContentType: "application/gzip", Content: tarball}},
			Roles:     last,
		},
		contract.Case{Name: "module-status-after-failed-install", Path: "/api/generator/module/status", Roles: last},

		contract.Case{Name: "bundles", Path: "/api/generator/bundles"},
		contract.Case{Name: "bundles-with-revoked-and-legacy", Path: "/api/generator/bundles", Query: url.Values{"includeRevoked": {"1"}, "includeLegacy": {"1"}}, Roles: admins},
		contract.Case{Name: "bundles-query-twice", Path: "/api/generator/bundles", Query: url.Values{"includeLegacy": {"1", "1"}}, Roles: last},
		contract.Case{Name: "bundle-unknown", Path: "/api/generator/bundles/{nobody}"},
		contract.Case{Name: "bundle-unknown-escaped", Path: "/api/generator/bundles/a%2Fb", Roles: last},
		contract.Case{Name: "defaults", Path: "/api/generator/defaults", Volatile: []string{"data.public_key"}},

		contract.Case{Name: "create-not-ready", Method: "POST", Path: "/api/generator/bundles", Body: map[string]any{"name": "Acme", "branding": map[string]any{"server_host": "desk.example.com"}}},
		contract.Case{Name: "create-not-ready-empty", Method: "POST", Path: "/api/generator/bundles", Body: map[string]any{}, Roles: admins},
		contract.Case{Name: "update-not-ready", Method: "PUT", Path: "/api/generator/bundles/{nobody}", Body: map[string]any{"name": "Acme"}, Roles: admins},
		contract.Case{Name: "rebuild-unknown", Method: "POST", Path: "/api/generator/bundles/{nobody}/rebuild", Body: map[string]any{}, Roles: admins},
		contract.Case{Name: "rebuild-platform-unknown", Method: "POST", Path: "/api/generator/bundles/{nobody}/rebuild/windows/x64/portable", Body: map[string]any{}, Roles: admins},
		contract.Case{Name: "revoke-unknown", Method: "POST", Path: "/api/generator/bundles/{nobody}/revoke", Body: map[string]any{"revoked": false}, Roles: admins},
		contract.Case{Name: "delete-unknown", Method: "DELETE", Path: "/api/generator/bundles/{nobody}", Roles: admins},
		contract.Case{Name: "build-status", Path: "/api/generator/build-status"},
		contract.Case{Name: "platforms", Path: "/api/generator/platforms", Roles: admins},

		contract.Case{Name: "preview-empty", Method: "POST", Path: "/api/generator/preview", Body: map[string]any{}, Volatile: []string{"data.branding.server.public_key"}},
		contract.Case{
			Name: "preview-full", Method: "POST", Path: "/api/generator/preview", Roles: last,
			Body: map[string]any{"branding": map[string]any{
				"appName": "  Acme Remote  ", "companyName": "Acme GmbH", "server_host": "https://Desk.Example.com:8443/path",
				"use_https": false, "api_port": 21114, "relay_host": " relay.example.com ",
				"short_text": strings.Repeat("x", 600), "product_label": " Remote help ", "hide_product_type": 1,
				"contact_email": "help@example.com", "contactUrl": "example.com/help", "contact_phone": "+49 30 1",
				"primaryColor": "#ABCDEF", "accent_color": "red", "textMutedColor": "#123456", "logo_data_url": "data:image/png;base64,AAAA",
				"allow_unattended": "yes", "autostart": true, "capabilities": map[string]any{"desktop": false, "files": nil, "audio": 0},
				"default_lang": "zh-TW", "disable_settings": false, "server": map[string]any{"address": "a", "api_url": 5, "public_key": "k"},
				"enrollment_token": "never", "public_key": "client-key",
			}},
			Volatile: []string{"data.branding.server.public_key"},
		},
		contract.Case{
			Name: "preview-invalid", Method: "POST", Path: "/api/generator/preview", Roles: last,
			Body: map[string]any{"branding": map[string]any{
				"server_host": "bad host!", "contact_url": "javascript:alert(1)", "logo_data_url": "http://x/logo.png",
				"primary_color": "#12345", "background_color": 5, "default_lang": "xx",
			}},
			Volatile: []string{"data.branding.server.public_key"},
		},
		contract.Case{
			Name: "preview-color-list", Method: "POST", Path: "/api/generator/preview", Roles: last,
			Body: map[string]any{"branding": map[string]any{"server_host": "h.example", "primary_color": []any{"#aabbcc"}}},
		},
		contract.Case{
			Name: "preview-text-branding", Method: "POST", Path: "/api/generator/preview", Roles: last,
			Body:     map[string]any{"branding": "abc"},
			Volatile: []string{"data.branding.server.public_key"},
		},

		contract.Case{Name: "manifest-unknown", Path: "/api/d/{nobody}/manifest"},
		contract.Case{Name: "page-unknown", Path: "/api/d/{nobody}/page", Roles: some},
		contract.Case{Name: "download-unknown", Path: "/api/d/{nobody}/download/windows/x64/portable", Roles: some},

		contract.Case{Name: "legacy-config", Path: "/api/generator/config", Volatile: []string{"data.publicKey", "data.defaults.key"}},
		contract.Case{Name: "generate-config-no-host", Method: "POST", Path: "/api/generator/generate-config", Body: map[string]any{}, Roles: some},
		contract.Case{
			Name: "generate-config", Method: "POST", Path: "/api/generator/generate-config", Roles: some,
			Body:     map[string]any{"serverHost": "desk.example.com", "relayHost": "relay.example.com", "relayPort": 0, "clientName": []any{"a", "b"}},
			Volatile: []string{"data.config"},
		},
		contract.Case{
			Name: "generate-config-ports", Method: "POST", Path: "/api/generator/generate-config", Roles: last,
			Body:     map[string]any{"serverHost": 5, "serverPort": "21200"},
			Volatile: []string{"data.config"},
		},
	)
}
