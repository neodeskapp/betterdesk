//go:build contract

package contract_test

import (
	"bytes"
	"encoding/base64"
	"strings"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The routes of the settings group that write the branding: save, export,
// import and reset, the profiles, the built-in themes, the logo and
// background uploads and the fonts. What the reading routes answer after a
// save is in the group branding-saved-in-node.
//
// Every turn ends with the branding reset and its profiles and fonts
// deleted. Two things stay after the group: the uploaded pictures (no
// route deletes one; they are in the instance's DATA_DIR/uploads) and the
// profile "Contract two", which admin applies in its turn (the active
// profile cannot be deleted, and nothing makes it inactive again). Font
// uploads go to the source tree's web-nodejs/public/fonts and are deleted
// by the cases after them.
//
// No case downloads a font: that fetches from fonts.googleapis.com. The
// download cases stop at a check before the fetch; the tests of
// console/branding cover the fetch against a fake server.
//
// Profile ids are marked by hand: the ids count the profiles every group
// before this one created.
func init() {
	editors := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	admin := contract.Roles("admin")
	id := []string{"data.id"}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		panic(err)
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script><rect width="4" height="4"/></svg>`)
	font := []byte("not really a font, nothing looks at it\n")
	file := func(field, name, contentType string, content []byte) contract.Part {
		return contract.Part{Field: field, FileName: name, ContentType: contentType, Content: content}
	}
	field := func(name, value string) contract.Part {
		return contract.Part{Field: name, Content: []byte(value)}
	}
	const (
		save     = "/api/settings/branding"
		export   = "/api/settings/branding/export"
		imports  = "/api/settings/branding/import"
		reset    = "/api/settings/branding/reset"
		profiles = "/api/settings/branding/profiles"
		logo     = "/api/settings/branding/upload-logo"
		wall     = "/api/settings/branding/upload-background"
		fonts    = "/api/settings/fonts"
	)

	contract.Group("settings-branding-writes",
		// Saving
		contract.Case{Name: "save-theme", Method: "POST", Path: save, Body: map[string]any{"themeMode": "light"}, Roles: editors},
		contract.Case{
			Name: "save-theme-and-colors", Method: "POST", Path: save, Roles: admin,
			Body: map[string]any{"themeMode": "dark", "colors": map[string]any{"accentBlue": "#123456"}, "glassColor": "#ffffff"},
		},
		contract.Case{
			Name: "save", Method: "POST", Path: save, Roles: editors,
			Body: map[string]any{
				"appName":         "Writes Console",
				"logoType":        "svg",
				"logoSvg":         string(svg),
				"bgType":          "image",
				"bgImageUrl":      "javascript:alert(1)",
				"loginBgImageUrl": "/wallpapers/b.jpg",
				"colors":          map[string]any{"accentBlue": "expression(alert(1))", "bgPrimary": "#0a0a0a", "unknown": "#fff"},
				"customCss":       "@import url(//evil.example/x.css); .x { color: red; }",
				"bgSize":          "huge",
				"loginPromoMode":  "nonsense",
				"notASetting":     "ignored",
				"fontHeading":     "Inter",
			},
		},
		contract.Case{
			Name: "save-nested", Method: "POST", Path: save, Roles: admin,
			Body: map[string]any{"appearance": map[string]any{"identity": map[string]any{"appName": "Nested Name"}, "palette": map[string]any{"mode": "light"}}},
		},
		contract.Case{Name: "save-list", Method: "POST", Path: save, Body: []any{"appName"}, Roles: admin},
		contract.Case{Name: "save-empty", Method: "POST", Path: save, Body: map[string]any{}, Roles: admin},
		contract.Case{
			Name: "save-numbers", Method: "POST", Path: save, Roles: admin,
			Body: map[string]any{"appName": 42, "footerText": true, "loginSlides": "not a list"},
		},
		contract.Case{
			Name: "save-value-without-text", Method: "POST", Path: save, Roles: admin,
			Body: map[string]any{"appName": map[string]any{"toString": 1}},
		},
		contract.Case{Name: "save-form", Method: "POST", Path: save, RawBody: "appName=Form", ContentType: "application/x-www-form-urlencoded", Roles: admin},

		// Export and import
		contract.Case{Name: "export", Path: export, Roles: editors},
		contract.Case{
			Name: "import", Method: "POST", Path: imports, Roles: editors,
			Body: map[string]any{
				"version": "2.0", "type": "betterdesk-theme",
				"appearance": map[string]any{"identity": map[string]any{"appName": "Imported"}},
				"branding":   map[string]any{"appName": "Flat part, not used"},
			},
		},
		contract.Case{
			Name: "import-flat", Method: "POST", Path: imports, Roles: admin,
			Body: map[string]any{"type": "betterdesk-theme", "branding": map[string]any{
				"appName": "Imported flat", "themeMode": "light", "colors": map[string]any{"accentBlue": "#00ff00"}, "customCss": strings.Repeat("a", 20010),
			}},
		},
		contract.Case{Name: "export-after-import", Path: export, Roles: admin},
		contract.Case{Name: "import-other-type", Method: "POST", Path: imports, Body: map[string]any{"type": "other", "branding": map[string]any{"appName": "x"}}, Roles: admin},
		contract.Case{Name: "import-without-parts", Method: "POST", Path: imports, Body: map[string]any{"type": "betterdesk-theme"}, Roles: admin},
		contract.Case{Name: "import-list", Method: "POST", Path: imports, Body: []any{}, Roles: admin},
		contract.Case{Name: "import-text", Method: "POST", Path: imports, RawBody: "not json", ContentType: "text/plain", Roles: admin},
		contract.Case{
			Name: "import-form", Method: "POST", Path: imports, Roles: admin,
			RawBody: "type=betterdesk-theme&branding=x", ContentType: "application/x-www-form-urlencoded",
		},
		contract.Case{
			Name: "import-colors-as-text", Method: "POST", Path: imports, Roles: admin,
			Body: map[string]any{"type": "betterdesk-theme", "branding": map[string]any{"colors": "red"}},
		},
		contract.Case{Name: "reset", Method: "POST", Path: reset, Body: map[string]any{}, Roles: editors},
		contract.Case{Name: "export-after-reset", Path: export, Roles: admin},

		// Profiles
		contract.Case{
			Name: "profile-setup-one", Method: "POST", Path: profiles, As: "admin", Roles: editors,
			Body: map[string]any{"name": "Contract one", "description": "  first  "}, Capture: map[string]string{"one": "data.id"}, Volatile: id,
		},
		contract.Case{
			Name: "profile-setup-two", Method: "POST", Path: profiles, As: "admin", Roles: editors,
			Body:    map[string]any{"name": "Contract two", "branding": map[string]any{"appName": "From profile", "themeMode": "light"}},
			Capture: map[string]string{"two": "data.id"}, Volatile: id,
		},
		contract.Case{
			Name: "profile-create", Method: "POST", Path: profiles, Roles: editors,
			Body: map[string]any{"name": "Contract three", "description": 7}, Capture: map[string]string{"three": "data.id"}, Volatile: id,
		},
		contract.Case{Name: "profile-create-no-name", Method: "POST", Path: profiles, Body: map[string]any{"name": "   "}, Roles: admin},
		contract.Case{Name: "profile-create-name-zero", Method: "POST", Path: profiles, Body: map[string]any{"name": 0}, Roles: admin},
		contract.Case{Name: "profile-create-taken", Method: "POST", Path: profiles, Body: map[string]any{"name": "Contract one"}, Roles: admin},
		contract.Case{
			Name: "profile-create-long", Method: "POST", Path: profiles, Roles: admin,
			Body: map[string]any{"name": strings.Repeat("L", 100), "description": strings.Repeat("d", 300)}, Capture: map[string]string{"long": "data.id"}, Volatile: id,
		},
		contract.Case{
			Name: "profile-update", Method: "PUT", Path: profiles + "/{one}", Roles: editors,
			Body: map[string]any{"name": "Contract one renamed", "description": "changed"},
		},
		contract.Case{Name: "profile-update-branding", Method: "PUT", Path: profiles + "/{one}", Body: map[string]any{"branding": map[string]any{"appName": "Updated"}}, Roles: admin},
		contract.Case{Name: "profile-update-suffix", Method: "PUT", Path: profiles + "/{one}abc", Body: map[string]any{"description": "by a prefix"}, Roles: admin},
		contract.Case{Name: "profile-update-bad-id", Method: "PUT", Path: profiles + "/abc", Body: map[string]any{"name": "x"}, Roles: admin},
		contract.Case{Name: "profile-update-missing", Method: "PUT", Path: profiles + "/999999", Body: map[string]any{"name": "x"}, Roles: admin},
		contract.Case{
			Name: "profile-update-name-without-text", Method: "PUT", Path: profiles + "/{one}", Roles: admin,
			Body: map[string]any{"name": map[string]any{"toString": 1}},
		},
		contract.Case{Name: "profile-apply", Method: "POST", Path: profiles + "/{two}/apply", Body: map[string]any{}, Roles: editors},
		contract.Case{Name: "profile-apply-missing", Method: "POST", Path: profiles + "/999999/apply", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "profile-apply-bad-id", Method: "POST", Path: profiles + "/x/apply", Body: map[string]any{}, Roles: admin},
		contract.Case{
			Name: "profile-duplicate", Method: "POST", Path: profiles + "/{one}/duplicate", Body: map[string]any{}, Roles: editors,
			Capture: map[string]string{"copy": "data.id"}, Volatile: id,
		},
		contract.Case{
			Name: "profile-duplicate-taken-name", Method: "POST", Path: profiles + "/{one}/duplicate", Body: map[string]any{"name": "Contract two"}, Roles: admin,
			Capture: map[string]string{"copy2": "data.id"}, Volatile: id,
		},
		contract.Case{Name: "profile-duplicate-name-number", Method: "POST", Path: profiles + "/{one}/duplicate", Body: map[string]any{"name": 5}, Roles: admin},
		contract.Case{Name: "profile-duplicate-missing", Method: "POST", Path: profiles + "/999999/duplicate", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "profile-delete-active", Method: "DELETE", Path: profiles + "/{two}", Roles: admin},
		contract.Case{Name: "profile-delete-missing", Method: "DELETE", Path: profiles + "/999999", Roles: admin},
		contract.Case{Name: "profile-delete-bad-id", Method: "DELETE", Path: profiles + "/x", Roles: admin},
		contract.Case{Name: "profile-delete", Method: "DELETE", Path: profiles + "/{one}", Roles: editors},
		contract.Case{Name: "profile-cleanup-one", Method: "DELETE", Path: profiles + "/{one}", As: "admin", Roles: editors},
		contract.Case{Name: "profile-cleanup-three", Method: "DELETE", Path: profiles + "/{three}", As: "admin", Roles: editors},
		contract.Case{Name: "profile-cleanup-copy", Method: "DELETE", Path: profiles + "/{copy}", As: "admin", Roles: editors},
		contract.Case{Name: "profile-cleanup-copy2", Method: "DELETE", Path: profiles + "/{copy2}", As: "admin", Roles: editors},
		contract.Case{Name: "profile-cleanup-long", Method: "DELETE", Path: profiles + "/{long}", As: "admin", Roles: editors},
		contract.Case{Name: "profile-cleanup-two", Method: "DELETE", Path: profiles + "/{two}", As: "admin", Roles: editors},
		contract.Case{Name: "reset-after-profiles", Method: "POST", Path: reset, Body: map[string]any{}, As: "admin", Roles: editors},

		// Built-in themes
		contract.Case{Name: "theme-apply", Method: "POST", Path: "/api/settings/themes/light/apply", Body: map[string]any{}, Roles: editors},
		contract.Case{Name: "export-after-theme", Path: export, Roles: admin},
		contract.Case{Name: "theme-apply-insolve", Method: "POST", Path: "/api/settings/themes/insolve/apply", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "theme-apply-bad-id", Method: "POST", Path: "/api/settings/themes/bad.id/apply", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "theme-apply-missing", Method: "POST", Path: "/api/settings/themes/nothing-here/apply", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "reset-after-themes", Method: "POST", Path: reset, Body: map[string]any{}, As: "admin", Roles: editors},

		// Logo uploads
		contract.Case{Name: "upload-logo", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "logo.png", "image/png", png)}, Roles: editors},
		contract.Case{Name: "upload-logo-svg", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "mark.SVG", "image/svg+xml", svg)}, Roles: admin},
		contract.Case{Name: "upload-logo-no-extension", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "logo", "image/webp", png)}, Roles: admin},
		contract.Case{Name: "upload-logo-type-with-parameter", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "a.gif", "image/gif; charset=x", png)}, Roles: admin},
		contract.Case{Name: "upload-logo-wrong-type", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "page.png", "text/html", png)}, Roles: admin},
		contract.Case{Name: "upload-logo-no-file", Method: "POST", Path: logo, Multipart: []contract.Part{field("note", "no file")}, Roles: admin},
		contract.Case{Name: "upload-logo-other-field", Method: "POST", Path: logo, Multipart: []contract.Part{file("picture", "logo.png", "image/png", png)}, Roles: admin},
		contract.Case{
			Name: "upload-logo-two-files", Method: "POST", Path: logo, Roles: admin,
			Multipart: []contract.Part{file("logo", "one.png", "image/png", png), file("logo", "two.png", "image/png", png)},
		},
		contract.Case{Name: "upload-logo-dots", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "..", "image/png", png)}, Roles: admin},
		contract.Case{Name: "upload-logo-exact-size", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "exact.png", "image/png", bytes.Repeat([]byte("a"), 2<<20))}, Roles: admin},
		contract.Case{Name: "upload-logo-too-large", Method: "POST", Path: logo, Multipart: []contract.Part{file("logo", "over.png", "image/png", bytes.Repeat([]byte("a"), 2<<20+1))}, Roles: admin},
		contract.Case{Name: "upload-logo-json", Method: "POST", Path: logo, Body: map[string]any{}, Roles: admin},

		// Background uploads
		contract.Case{
			Name: "upload-background", Method: "POST", Path: wall, Roles: editors,
			Multipart: []contract.Part{field("target", "bgImageUrl"), file("background", "wall.png", "image/png", png)},
		},
		contract.Case{
			Name: "upload-background-target-after-file", Method: "POST", Path: wall, Roles: admin,
			Multipart: []contract.Part{file("background", "wall.jpeg", "image/jpeg", png), field("target", " loginBgImageUrl ")},
		},
		contract.Case{
			Name: "upload-background-unknown-target", Method: "POST", Path: wall, Roles: admin,
			Multipart: []contract.Part{field("target", "logoUrl"), file("background", "wall.png", "image/png", png)},
		},
		contract.Case{
			Name: "upload-background-inherited-target", Method: "POST", Path: wall, Roles: admin,
			Multipart: []contract.Part{field("target", "constructor"), file("background", "wall.png", "image/png", png)},
		},
		contract.Case{Name: "upload-background-no-target", Method: "POST", Path: wall, Multipart: []contract.Part{file("background", "wall", "image/jpeg", png)}, Roles: admin},
		contract.Case{Name: "upload-background-wrong-type", Method: "POST", Path: wall, Multipart: []contract.Part{file("background", "wall.bmp", "image/bmp", png)}, Roles: admin},
		contract.Case{
			Name: "upload-background-too-large", Method: "POST", Path: wall, Roles: admin,
			Multipart: []contract.Part{file("background", "over.jpg", "image/jpeg", bytes.Repeat([]byte("a"), 8<<20+1))},
		},
		contract.Case{Name: "reset-after-uploads", Method: "POST", Path: reset, Body: map[string]any{}, As: "admin", Roles: editors},

		// Fonts
		contract.Case{
			Name: "font-upload", Method: "POST", Path: fonts + "/upload", Roles: editors,
			Multipart: []contract.Part{field("family", "Contract Upload"), file("font", "Contract.ttf", "font/ttf", font)},
		},
		contract.Case{
			Name: "font-upload-by-type", Method: "POST", Path: fonts + "/upload", Roles: admin,
			Multipart: []contract.Part{field("family", "  Contract Woff  "), file("font", "file.bin", "application/font-woff2", font)},
		},
		contract.Case{Name: "font-upload-wrong-type", Method: "POST", Path: fonts + "/upload", Multipart: []contract.Part{file("font", "x.png", "image/png", png)}, Roles: admin},
		contract.Case{Name: "font-upload-no-file", Method: "POST", Path: fonts + "/upload", Multipart: []contract.Part{field("family", "Nothing")}, Roles: admin},
		contract.Case{
			Name: "font-upload-too-large", Method: "POST", Path: fonts + "/upload", Roles: admin,
			Multipart: []contract.Part{field("family", "Too Big"), file("font", "big.ttf", "font/ttf", bytes.Repeat([]byte("a"), 5<<20+1))},
		},
		contract.Case{Name: "font-delete", Method: "DELETE", Path: fonts + "/Contract%20Upload", Roles: editors},
		contract.Case{Name: "font-delete-again", Method: "DELETE", Path: fonts + "/Contract%20Upload", Roles: admin},
		contract.Case{Name: "font-delete-encoded-twice", Method: "DELETE", Path: fonts + "/Contract%2520Woff", Roles: admin},
		contract.Case{Name: "font-delete-malformed", Method: "DELETE", Path: fonts + "/%25E0%25A4", Roles: admin},
		contract.Case{Name: "font-delete-no-letters", Method: "DELETE", Path: fonts + "/%E4%BD%A0", Roles: admin},
		contract.Case{Name: "font-download-no-family", Method: "POST", Path: fonts + "/download", Body: map[string]any{}, Roles: editors},
		contract.Case{Name: "font-download-family-number", Method: "POST", Path: fonts + "/download", Body: map[string]any{"family": 12}, Roles: admin},
		contract.Case{Name: "font-download-family-empty", Method: "POST", Path: fonts + "/download", Body: map[string]any{"family": ""}, Roles: admin},
		contract.Case{Name: "font-download-list", Method: "POST", Path: fonts + "/download", Body: []any{"Inter"}, Roles: admin},
		contract.Case{
			Name: "font-download-weights-text", Method: "POST", Path: fonts + "/download", Roles: admin,
			Body: map[string]any{"family": "Contract Font", "weights": "400"},
		},
		contract.Case{
			Name: "font-download-weights-object", Method: "POST", Path: fonts + "/download", Roles: admin,
			Body: map[string]any{"family": "Inter", "weights": map[string]any{"0": "400"}},
		},
		contract.Case{
			Name: "font-download-no-letters", Method: "POST", Path: fonts + "/download", Roles: admin,
			Body: map[string]any{"family": "你好", "weights": []any{"400"}},
		},
	)
}
