package settings_test

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The appearance routes refuse in the caller's language (BUGS L419): a
// theme file that is none, a font or picture of the wrong type, a profile
// without a name. A console without the texts answers the fixed English
// refusals as before.
func TestAppearanceRefusalsInTheCallersLanguage(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		scratch := t.TempDir()
		w := routetest.New(t, routetest.Options{DB: db, Config: func(cfg *config.Config) {
			cfg.LangDir = filepath.Join("..", "..", "static", "files", "lang")
			cfg.FontsDir = filepath.Join(scratch, "fonts")
			cfg.TempDir = filepath.Join(scratch, "tmp")
		}})
		admin := w.As("admin")
		font := part{field: "font", name: "x.png", contentType: "image/png", content: pngBytes}
		logo := part{field: "logo", name: "page.png", contentType: "text/html", content: pngBytes}
		for _, lang := range []struct{ query, theme, profile, fontType, imageType string }{
			{"", `{"success":false,"error":"This is not an appearance file of the console."}`, `{"success":false,"error":"Enter a name for the appearance."}`,
				`{"success":false,"error":"This is not a font file. Upload a .woff2 or .ttf file."}`,
				`{"success":false,"error":"This is not a picture. Upload a PNG, JPG, GIF or WebP file."}`},
			{"?lang=de", `{"success":false,"error":"Das ist keine Darstellungsdatei der Konsole."}`, `{"success":false,"error":"Gib einen Namen für die Darstellung ein."}`,
				`{"success":false,"error":"Das ist keine Schriftdatei. Lade eine .woff2- oder .ttf-Datei hoch."}`,
				`{"success":false,"error":"Das ist kein Bild. Lade eine PNG-, JPG-, GIF- oder WebP-Datei hoch."}`},
		} {
			if res := admin.Do("POST", "/api/settings/branding/import"+lang.query, `{"type":"other"}`); res.Status != http.StatusBadRequest || res.Body != lang.theme {
				t.Errorf("import %q: %d %s", lang.query, res.Status, res.Body)
			}
			if res := admin.Do("POST", "/api/settings/branding/profiles"+lang.query, `{"name":" "}`); res.Status != http.StatusBadRequest || res.Body != lang.profile {
				t.Errorf("profile %q: %d %s", lang.query, res.Status, res.Body)
			}
			if res := upload(t, admin, "/api/settings/fonts/upload"+lang.query, font); res.Body != lang.fontType {
				t.Errorf("font %q: %d %s", lang.query, res.Status, res.Body)
			}
			if res := upload(t, admin, "/api/settings/branding/upload-logo"+lang.query, logo); res.Body != lang.imageType {
				t.Errorf("logo %q: %d %s", lang.query, res.Status, res.Body)
			}
		}
	})
}
