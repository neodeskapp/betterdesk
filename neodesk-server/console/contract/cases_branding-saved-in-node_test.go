//go:build contract

package contract_test

import "github.com/neodeskapp/neodesk/neodesk-server/console/contract"

// Branding saved through the settings routes, then read by every route
// that only reads it: the bootstrap, the sign-in page's data, the boot
// script, the settings and appearance, the stylesheets, the favicon and
// the profile list. admin saves, makes a profile, deletes it and resets
// the branding in every turn, so the instance ends as it began.
//
// The writes are routes of the settings group, which the Node console
// answers until that group moves; the reads are answered by the Go
// console. So these cases show that the Go console reads what the Node
// console saved at once: its branding has no cache of its own while the
// Node console runs behind it. With CONTRACT_REQUIRE_GO the four writes
// fail for as long as Node answers them, which is why the cases are a
// group of their own.
func init() {
	roles := contract.Roles("signed-out", "guest_issuer", "viewer", "operator", "admin")
	saved := map[string]any{
		"appName":        "Contract Console",
		"appDescription": "Saved by the settings route",
		"themeMode":      "dark",
		"logoType":       "text",
		"logoIcon":       "hub",
		"logoText":       "Contract",
		"logoTextAccent": "Co",
		"logoSvg":        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><rect width="4" height="4"/></svg>`,
		"faviconSvg":     `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 4 4"><circle cx="2" cy="2" r="2"/></svg>`,
		"footerText":     "Contract footer",
		"showPoweredBy":  "false",
		"loginTitle":     "Welcome back",
		"loginSubtitle":  "Sign in to the contract console",
		"loginPromoMode": "slides",
		"loginSlides": []any{
			map[string]any{"id": "s1", "slideKey": "one", "imageUrl": "/wallpapers/a.jpg", "title": "First", "body": "Body one", "overlay": 0.4},
			map[string]any{"id": "s2", "title": "Second", "overlay": "0.7"},
			map[string]any{"title": "Third"},
		},
		"colors":       map[string]any{"accentBlue": "#ff5500", "bgPrimary": "#101010"},
		"bgType":       "gradient",
		"bgGradient":   "linear-gradient(90deg, #111111 0%, #222222 100%)",
		"glassEnabled": "true",
		"customCss":    ".contract-note { color: #c00; }",
	}
	css := []string{"x-branding-revision"}
	contract.Group("branding-saved-in-node",
		contract.Case{Name: "save", Method: "POST", Path: "/api/settings/branding", Body: saved, As: "admin", Roles: roles},
		contract.Case{Name: "bootstrap", Path: "/api/console/bootstrap", Roles: roles},
		contract.Case{Name: "login-page", Path: "/api/auth/login-page", Roles: roles},
		contract.Case{Name: "boot-script", Path: "/console-boot.js", Roles: roles},
		contract.Case{Name: "settings", Path: "/api/settings/branding", Roles: roles},
		contract.Case{Name: "appearance", Path: "/api/settings/appearance", Roles: roles},
		contract.Case{Name: "stylesheet", Path: "/css/branding.css", VolatileHeaders: css, Roles: roles},
		contract.Case{Name: "theme-stylesheet", Path: "/css/theme.css", Roles: roles},
		contract.Case{Name: "favicon", Path: "/branding/favicon.svg", Roles: roles},
		contract.Case{
			Name: "profile-create", Method: "POST", Path: "/api/settings/branding/profiles",
			Body:    map[string]any{"name": "Contract profile", "description": "made by the contract suite"},
			Capture: map[string]string{"profile": "data.id"}, As: "admin", Roles: roles,
		},
		contract.Case{Name: "profiles", Path: "/api/settings/branding/profiles", Roles: roles},
		contract.Case{Name: "profile-delete", Method: "DELETE", Path: "/api/settings/branding/profiles/{profile}", As: "admin", Roles: roles},
		contract.Case{Name: "reset", Method: "POST", Path: "/api/settings/branding/reset", Body: map[string]any{}, As: "admin", Roles: roles},
		contract.Case{Name: "bootstrap-after-reset", Path: "/api/console/bootstrap", Roles: roles},
		contract.Case{Name: "stylesheet-after-reset", Path: "/css/branding.css", VolatileHeaders: css, Roles: roles},
	)
}
