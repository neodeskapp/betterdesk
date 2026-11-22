//go:build contract

package contract_test

import (
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The routes of the settings group that only read the branding: the
// settings with their appearance model, the profiles, the uploaded
// backgrounds, the built-in themes, the font search and the installed
// fonts for anyone signed in, and the stylesheet and favicon for everyone.
//
// What the same routes answer after a save is in the group
// branding-saved-in-node.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	contract.Group("settings-branding",
		contract.Case{Name: "branding", Path: "/api/settings/branding"},
		contract.Case{Name: "branding-head", Method: "HEAD", Path: "/api/settings/branding", Roles: some},
		contract.Case{
			Name: "branding-german", Path: "/api/settings/branding",
			Headers: map[string]string{"Accept-Language": "de"}, Roles: some,
		},
		contract.Case{
			Name: "branding-not-modified", Path: "/api/settings/branding",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{Name: "appearance", Path: "/api/settings/appearance"},
		contract.Case{
			Name: "appearance-no-compression", Path: "/api/settings/appearance",
			Headers: map[string]string{"Accept-Encoding": "identity"}, Roles: some,
		},
		contract.Case{Name: "profiles", Path: "/api/settings/branding/profiles"},
		contract.Case{Name: "backgrounds", Path: "/api/settings/branding/backgrounds"},
		contract.Case{Name: "themes", Path: "/api/settings/themes"},
		contract.Case{Name: "themes-other-spelling", Path: "/API/Settings/Themes/", Roles: some},

		contract.Case{Name: "fonts", Path: "/api/settings/fonts"},
		contract.Case{Name: "fonts-search", Path: "/api/settings/fonts", Query: url.Values{"q": {"inter"}}, Roles: some},
		contract.Case{Name: "fonts-search-upper-case", Path: "/api/settings/fonts", Query: url.Values{"q": {"ROBOTO"}}, Roles: some},
		contract.Case{Name: "fonts-category", Path: "/api/settings/fonts", Query: url.Values{"category": {"serif"}}, Roles: some},
		contract.Case{Name: "fonts-category-unknown", Path: "/api/settings/fonts", Query: url.Values{"category": {"nothing-like-it"}}, Roles: some},
		contract.Case{
			Name: "fonts-search-in-category", Path: "/api/settings/fonts",
			Query: url.Values{"q": {"mono"}, "category": {"monospace"}}, Roles: some,
		},
		contract.Case{Name: "fonts-google", Path: "/api/settings/fonts", Query: url.Values{"source": {"google"}}, Roles: some},
		contract.Case{
			Name: "fonts-google-search", Path: "/api/settings/fonts",
			Query: url.Values{"source": {"google"}, "q": {"open"}}, Roles: some,
		},
		contract.Case{Name: "fonts-other-source", Path: "/api/settings/fonts", Query: url.Values{"source": {"GOOGLE"}}, Roles: some},
		contract.Case{
			Name: "fonts-long-search", Path: "/api/settings/fonts",
			Query: url.Values{"q": {"inter" + strings.Repeat("x", 120)}}, Roles: some,
		},
		contract.Case{
			Name: "fonts-long-category", Path: "/api/settings/fonts",
			Query: url.Values{"category": {"sans-serif" + strings.Repeat("y", 30)}}, Roles: some,
		},
		contract.Case{Name: "fonts-search-as-list", Path: "/api/settings/fonts", Query: url.Values{"q": {"inter", "roboto"}}, Roles: some},
		contract.Case{Name: "fonts-search-as-object", Path: "/api/settings/fonts", Query: url.Values{"q[a]": {"inter"}}, Roles: some},
		contract.Case{
			Name: "fonts-empty-search", Path: "/api/settings/fonts",
			Query: url.Values{"q": {""}, "category": {""}, "source": {""}}, Roles: some,
		},
		contract.Case{Name: "fonts-local", Path: "/api/settings/fonts/local"},

		contract.Case{Name: "stylesheet", Path: "/css/branding.css", VolatileHeaders: []string{"x-branding-revision"}},
		contract.Case{Name: "stylesheet-head", Method: "HEAD", Path: "/css/branding.css", VolatileHeaders: []string{"x-branding-revision"}, Roles: some},
		contract.Case{
			Name: "stylesheet-not-modified", Path: "/css/branding.css",
			Headers: map[string]string{"If-None-Match": "*"}, VolatileHeaders: []string{"x-branding-revision"}, Roles: some,
		},
		contract.Case{
			Name: "stylesheet-other-tag", Path: "/css/branding.css",
			Headers: map[string]string{"If-None-Match": `W/"0-0"`}, VolatileHeaders: []string{"x-branding-revision"}, Roles: some,
		},
		contract.Case{
			Name: "stylesheet-no-compression", Path: "/css/branding.css",
			Headers: map[string]string{"Accept-Encoding": "identity"}, VolatileHeaders: []string{"x-branding-revision"}, Roles: some,
		},
		contract.Case{
			Name: "stylesheet-with-a-query", Path: "/css/branding.css", Query: url.Values{"v": {"123"}},
			VolatileHeaders: []string{"x-branding-revision"}, Roles: some,
		},
		contract.Case{Name: "stylesheet-other-spelling", Path: "/CSS/Branding.css", VolatileHeaders: []string{"x-branding-revision"}, Roles: some},
		contract.Case{Name: "theme-stylesheet", Path: "/css/theme.css"},
		contract.Case{
			Name: "theme-stylesheet-not-modified", Path: "/css/theme.css",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{Name: "favicon", Path: "/branding/favicon.svg"},
		contract.Case{Name: "favicon-head", Method: "HEAD", Path: "/branding/favicon.svg", Roles: some},
		contract.Case{
			Name: "favicon-not-modified", Path: "/branding/favicon.svg",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
	)
}
