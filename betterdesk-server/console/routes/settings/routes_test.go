package settings_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

var fixtures = filepath.Join("..", "..", "branding", "testdata", "console")

// world is a console with the branding files of the fixtures. The themes
// are copied into a folder of the test, which a test may add to; fonts and
// the temporary folder of font uploads are folders of the test too.
func world(t *testing.T, db *store.DB) *routetest.World {
	t.Helper()
	return worldWith(t, db, nil)
}

// worldWith is world with more environment variables.
func worldWith(t *testing.T, db *store.DB, env map[string]string) *routetest.World {
	t.Helper()
	themes := filepath.Join(t.TempDir(), "themes")
	if err := os.MkdirAll(themes, 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadDir(filepath.Join(fixtures, "themes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range list {
		raw, err := os.ReadFile(filepath.Join(fixtures, "themes", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(themes, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scratch := t.TempDir()
	return routetest.New(t, routetest.Options{DB: db, Env: env, Config: func(cfg *config.Config) {
		cfg.PalettesFile = filepath.Join(fixtures, "config", "theme-palettes.json")
		cfg.FaviconFile = filepath.Join(fixtures, "public", "favicon.svg")
		cfg.ThemesDir = themes
		cfg.FontsDir = filepath.Join(scratch, "fonts")
		cfg.TempDir = filepath.Join(scratch, "tmp")
	}})
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func keyOrder(t *testing.T, body string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s", body)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(keys, ",")
}

// The settings reads need a session, and the panel guard keeps roles out
// that may not read the settings area.
func TestReadsNeedASession(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		for _, path := range []string{
			"/api/settings/branding", "/api/settings/appearance", "/api/settings/branding/backgrounds",
			"/api/settings/branding/profiles", "/api/settings/themes", "/api/settings/fonts", "/api/settings/fonts/local",
		} {
			if res := w.SignedOut().Do("GET", path, ""); res.Status != http.StatusUnauthorized {
				t.Errorf("%s signed out: %d %s", path, res.Status, res.Body)
			}
			if res := w.As("admin").Do("GET", path, ""); res.Status != http.StatusOK || !strings.HasPrefix(res.Body, `{"success":true,`) {
				t.Errorf("%s as admin: %d %s", path, res.Status, res.Body)
			}
		}
	})
}

func TestBrandingAndAppearance(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		if got := keyOrder(t, admin.Do("GET", "/api/settings/branding", "").Body); got != "success,data,appearance,readability,revision" {
			t.Errorf("branding keys = %s", got)
		}
		if got := keyOrder(t, admin.Do("GET", "/api/settings/appearance", "").Body); got != "success,data,flat,readability,revision" {
			t.Errorf("appearance keys = %s", got)
		}
		exec(t, db, "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Stored', '2026-01-02 03:04:05')")
		w.Server.Deps().Branding.LoadBranding(context.Background())
		var got struct {
			Data struct {
				AppName string `json:"appName"`
			} `json:"data"`
			Appearance struct {
				Identity struct {
					AppName string `json:"appName"`
				} `json:"identity"`
			} `json:"appearance"`
			Revision string `json:"revision"`
		}
		if err := json.Unmarshal([]byte(admin.Do("GET", "/api/settings/branding", "").Body), &got); err != nil {
			t.Fatal(err)
		}
		if got.Data.AppName != "Stored" || got.Appearance.Identity.AppName != "Stored" || got.Revision != w.Server.Deps().Branding.Revision() {
			t.Errorf("after a load: %+v", got)
		}
	})
}

func TestProfilesThemesFontsBackgrounds(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		if res := admin.Do("GET", "/api/settings/branding/profiles", ""); res.Body != `{"success":true,"data":[]}` {
			t.Errorf("no profiles: %s", res.Body)
		}
		exec(t, db, "INSERT INTO branding_profiles (name, description, data) VALUES ('Zeta', NULL, '{}'), ('alpha', 'first', '{}')")
		res := admin.Do("GET", "/api/settings/branding/profiles", "")
		if !strings.Contains(res.Body, `"name":"alpha","description":"first"`) || !strings.Contains(res.Body, `"name":"Zeta","description":null`) {
			t.Errorf("profiles: %s", res.Body)
		}
		if strings.Index(res.Body, "alpha") > strings.Index(res.Body, "Zeta") && !db.IsPostgres() {
			t.Errorf("profiles not by name without regard to case: %s", res.Body)
		}
		themes := admin.Do("GET", "/api/settings/themes", "").Body
		for _, id := range []string{`"id":"default"`, `"id":"insolve"`, `"id":"light"`} {
			if !strings.Contains(themes, id) {
				t.Errorf("themes lack %s: %s", id, themes)
			}
		}
		if res := admin.Do("GET", "/api/settings/branding/backgrounds", ""); res.Body != `{"success":true,"data":[]}` {
			t.Errorf("backgrounds: %s", res.Body)
		}
		uploads := filepath.Join(w.DataDir, "uploads")
		if err := os.WriteFile(filepath.Join(uploads, "bg-0123456789abcdef.png"), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(uploads, "logo-0123456789abcdef.png"), []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := admin.Do("GET", "/api/settings/branding/backgrounds", ""); !strings.Contains(res.Body, `"name":"bg-0123456789abcdef.png","url":"/uploads/bg-0123456789abcdef.png","size":3`) || strings.Contains(res.Body, "logo-") {
			t.Errorf("backgrounds: %s", res.Body)
		}
		if res := admin.Do("GET", "/api/settings/fonts/local", ""); res.Body != `{"success":true,"data":[]}` {
			t.Errorf("local fonts: %s", res.Body)
		}
	})
}

func TestFontSearchReadsTheQueryAsExpressDid(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		admin := world(t, db).As("admin")
		search := func(query string) string { return admin.Do("GET", "/api/settings/fonts"+query, "").Body }
		all := search("")
		if !strings.Contains(all, `"family":"Inter"`) {
			t.Fatalf("curated fonts: %s", all)
		}
		if got := search("?q=inter"); !strings.Contains(got, `"family":"Inter"`) || strings.Contains(got, `"family":"Roboto"`) {
			t.Errorf("q=inter: %s", got)
		}
		// q=a&q=b is the list "a,b"; q[x]=inter is "[object Object]"
		if got := search("?q=inter&q=roboto"); got != `{"success":true,"data":[]}` {
			t.Errorf("a list: %s", got)
		}
		if got := search("?q[x]=inter"); got != `{"success":true,"data":[]}` {
			t.Errorf("an object: %s", got)
		}
		// A search past 100 characters is cut there
		if got := search("?q=inter" + strings.Repeat("%20", 95) + "zzz"); !strings.Contains(got, `"family":"Inter"`) {
			t.Errorf("search cut at 100 characters: %s", got)
		}
		if got := search("?q=inter" + strings.Repeat("x", 95)); got != `{"success":true,"data":[]}` {
			t.Errorf("long search: %s", got)
		}
		if got := search("?q=" + strings.Repeat("x", 100) + "inter"); got != `{"success":true,"data":[]}` {
			t.Errorf("cut search: %s", got)
		}
		// An object with a toString member cannot be made text
		if res := admin.Do("GET", "/api/settings/fonts?q[toString]=1", ""); res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Failed to search fonts"}` {
			t.Errorf("toString: %d %s", res.Status, res.Body)
		}
		if got := search("?category=monospace"); strings.Contains(got, `"category":"sans-serif"`) || !strings.Contains(got, `"category":"monospace"`) {
			t.Errorf("category: %s", got)
		}
	})
}

func TestStylesheetsAndFavicon(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		visitor := w.SignedOut()
		css := visitor.Do("GET", "/css/branding.css", "")
		revision := w.Server.Deps().Branding.Revision()
		if css.Status != http.StatusOK || css.Header.Get("Content-Type") != "text/css; charset=utf-8" ||
			css.Header.Get("Cache-Control") != "public, max-age=60" || css.Header.Get("X-Branding-Revision") != revision ||
			!strings.HasPrefix(css.Body, ":root {") || css.Header.Get("ETag") == "" {
			t.Errorf("branding.css: %d %v %.40s", css.Status, css.Header, css.Body)
		}
		again := visitor.Do("GET", "/css/branding.css", "", "If-None-Match", css.Header.Get("ETag"))
		if again.Status != http.StatusNotModified || again.Body != "" || again.Header.Get("X-Branding-Revision") != revision {
			t.Errorf("branding.css with its ETag: %d %v", again.Status, again.Header)
		}
		theme := visitor.Do("GET", "/css/theme.css", "")
		if theme.Body != css.Body || theme.Header.Get("X-Branding-Revision") != "" || theme.Header.Get("Cache-Control") != "public, max-age=60" {
			t.Errorf("theme.css: %v", theme.Header)
		}
		icon := visitor.Do("GET", "/branding/favicon.svg", "")
		if icon.Header.Get("Content-Type") != "image/svg+xml; charset=utf-8" || icon.Header.Get("Cache-Control") != "public, max-age=300" || !strings.HasPrefix(icon.Body, "<svg") {
			t.Errorf("favicon: %v %.40s", icon.Header, icon.Body)
		}

		// A heading font whose name has no ASCII letter is no installed
		// font; the stylesheet names it and keeps everything else (BUGS BR2)
		exec(t, db, "INSERT INTO branding_config (key, value, updated_at) VALUES ('fontHeading', ?, '2026-01-02 03:04:05')", "你好")
		w.Server.Deps().Branding.LoadBranding(context.Background())
		withFont := visitor.Do("GET", "/css/branding.css", "")
		if withFont.Status != http.StatusOK || !strings.Contains(withFont.Body, "--font-heading: '你好', sans-serif;") || !strings.Contains(withFont.Body, "--bg-primary:") ||
			withFont.Header.Get("X-Branding-Revision") == "" {
			t.Errorf("branding.css with a font without ASCII letters: %d %v %.200q", withFont.Status, withFont.Header, withFont.Body)
		}
		if theme := visitor.Do("GET", "/css/theme.css", ""); theme.Body != withFont.Body {
			t.Errorf("theme.css with a font without ASCII letters: %.200q", theme.Body)
		}
	})
}
