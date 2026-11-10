package console_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// fixtures is the copy of the old console's branding files the branding
// package tests with.
var fixtures = filepath.Join("..", "..", "branding", "testdata", "console")

// world is a console with three small language files (English, German,
// Arabic), the branding fixtures and a product version of 9.8.7.
// nodeBehind makes it a console with the Node console behind it.
func world(t *testing.T, db *store.DB, nodeBehind bool) *routetest.World {
	t.Helper()
	return worldWithLanguages(t, db, nodeBehind, smallLangDir(t))
}

// smallLangDir is a folder with three language files of a few texts.
func smallLangDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for code, content := range map[string]string{
		"en": `{"meta":{"name":"English"},"errors":{"server_error":"Server error"}}`,
		"de": `{"meta":{"name":"German"},"errors":{"server_error":"Serverfehler"}}`,
		"ar": `{"meta":{"name":"Arabic"}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, code+".json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func worldWithLanguages(t *testing.T, db *store.DB, nodeBehind bool, langDir string) *routetest.World {
	t.Helper()
	consoleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(consoleDir, "VERSION"), []byte("9.8.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := routetest.New(t, routetest.Options{DB: db, Config: func(cfg *config.Config) {
		cfg.LangDir = langDir
		cfg.ConsoleDir = filepath.Join(consoleDir, "web-nodejs")
		cfg.PalettesFile = filepath.Join(fixtures, "config", "theme-palettes.json")
		cfg.SupportersFile = filepath.Join(fixtures, "config", "supporters.json")
		cfg.FaviconFile = filepath.Join(fixtures, "public", "favicon.svg")
		cfg.ThemesDir = filepath.Join(fixtures, "themes")
		if nodeBehind {
			cfg.NodeUpstream = &url.URL{Scheme: "http", Host: "127.0.0.1:9"}
		}
	}})
	w.Server.Router().Get("/__test/emergency", func(rw http.ResponseWriter, r *http.Request) {
		kit.Session(r).Set("emergencyMode", true)
		kit.OK(rw, r, nil)
	})
	// viewer, signed in with German as the preferred language
	w.Server.Router().Get("/__test/signin-german", func(rw http.ResponseWriter, r *http.Request) {
		sess := kit.Session(r)
		if err := sess.Regenerate(); err != nil {
			t.Error(err)
		}
		user := session.User{ID: routetest.RoleIDs["viewer"], Username: "e2e_viewer", Role: "viewer", PreferredLanguage: "de"}
		if _, err := sess.Establish(user, w.Server.Deps().Sessions.ClientMeta(r, "")); err != nil {
			t.Error(err)
		}
		kit.OK(rw, r, nil)
	})
	return w
}

// sourceLangDir is web-nodejs/lang of the source tree, "" when the tree
// around the server is not there.
func sourceLangDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "web-nodejs", "lang")
		if _, err := os.Stat(filepath.Join(candidate, "en.json")); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("not JSON: %v: %s", err, body)
	}
	return out
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// The navigation and the panel access of the bootstrap under an account
// policy are what the Node console answered under the same policy
// (testdata/bootstrap_with_policy.json, from its contract recording).
func TestBootstrapUnderAPolicyAsNodeAnswered(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "bootstrap_with_policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Policy [][2]string `json:"policy"`
		Roles  map[string]struct {
			Access      json.RawMessage `json:"access"`
			Nav         json.RawMessage `json:"nav"`
			Permissions json.RawMessage `json:"permissions"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var sections []map[string]string
	for _, s := range fixture.Policy {
		sections = append(sections, map[string]string{"resource_key": s[0], "access_level": s[1]})
	}
	body, _ := json.Marshal(map[string]any{"sections": sections, "features": []any{}})
	// The navigation's texts are compared when the language files of the
	// source tree are there; without them everything else is
	langDir, texts := sourceLangDir(t), true
	if langDir == "" {
		langDir, texts = smallLangDir(t), false
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := worldWithLanguages(t, db, false, langDir)
		for role, want := range fixture.Roles {
			// One session per account: a new sign-in as admin ends the last one
			admin := w.As("admin")
			id := routetest.RoleIDs[role]
			if res := admin.Do("PUT", "/api/panel/access/users/"+itoa(id), string(body)); res.Status != http.StatusOK {
				t.Fatalf("policy for %s: %d %s", role, res.Status, res.Body)
			}
			reader := admin
			if role != "admin" {
				reader = w.As(role)
			}
			res := reader.Do("GET", "/api/console/bootstrap", "")
			if res.Status != http.StatusOK {
				t.Fatalf("bootstrap as %s: %d %s", role, res.Status, res.Body)
			}
			var got struct {
				Access      json.RawMessage `json:"access"`
				Nav         json.RawMessage `json:"nav"`
				Permissions json.RawMessage `json:"permissions"`
			}
			if err := json.Unmarshal([]byte(res.Body), &got); err != nil {
				t.Fatal(err)
			}
			for _, part := range []struct {
				name      string
				want, got json.RawMessage
			}{{"access", want.Access, got.Access}, {"nav", want.Nav, got.Nav}, {"permissions", want.Permissions, got.Permissions}} {
				if part.name == "nav" && !texts {
					part.want, part.got = withoutTexts(part.want), withoutTexts(part.got)
				}
				if !sameJSON(part.want, part.got) {
					t.Errorf("%s of %s differs\n  node: %s\n    go: %s", part.name, role, part.want, part.got)
				}
			}
		}
	})
}

// sameJSON compares two JSON texts by value, keys sorted (the fixture was
// written with sorted keys).
func sameJSON(a, b json.RawMessage) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestBootstrap(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, false)
		ctx := context.Background()
		if res := w.SignedOut().Do("GET", "/api/console/bootstrap", ""); res.Status != http.StatusUnauthorized {
			t.Errorf("signed out: %d %s", res.Status, res.Body)
		}

		viewer := w.As("viewer")
		res := viewer.Do("GET", "/api/console/bootstrap", "")
		if res.Status != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("viewer: %d, Cache-Control %q: %s", res.Status, res.Header.Get("Cache-Control"), res.Body)
		}
		keys := keyOrder(t, res.Body)
		if keys != "success,user,permissions,access,isSuperAdmin,emergencyMode,nav,lang,branding,palettes,sessionPolicy,csrfToken,appVersion" {
			t.Errorf("keys = %s", keys)
		}
		got := decode(t, res.Body)
		user, _ := json.Marshal(got["user"])
		if string(user) != `{"avatarVersion":null,"id":6,"preferredLanguage":null,"role":"viewer","username":"e2e_viewer"}` {
			t.Errorf("user = %s", user)
		}
		if got["appVersion"] != "9.8.7" || got["emergencyMode"] != false || got["isSuperAdmin"] != false {
			t.Errorf("appVersion %v, emergencyMode %v, isSuperAdmin %v", got["appVersion"], got["emergencyMode"], got["isSuperAdmin"])
		}
		if token, _ := got["csrfToken"].(string); len(token) < 32 {
			t.Errorf("csrfToken = %q", token)
		}
		palettes, _ := json.Marshal(got["palettes"])
		if !strings.Contains(string(palettes), `"light":{`) || !strings.Contains(string(palettes), `"dark":{`) || strings.Contains(string(palettes), `null`) {
			t.Errorf("palettes = %s", palettes)
		}
		policy, _ := json.Marshal(got["sessionPolicy"])
		if string(policy) != `{"idleTimeoutMs":1800000,"maxAgeMs":86400000,"maxSessionsPerUser":1,"warnBeforeMs":60000}` {
			t.Errorf("sessionPolicy = %s", policy)
		}
		branding, _ := got["branding"].(map[string]any)
		if branding["appName"] != "BetterDesk" || branding["revision"] != "0" || branding["showPoweredBy"] != true {
			t.Errorf("branding = %v", branding)
		}

		// A picture, a preferred language and the emergency flag of the session
		exec(t, db, "INSERT INTO user_avatars (user_id, mime_type, data, version, source, source_url, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			6, "image/png", []byte{0x89, 'P', 'N', 'G'}, "0123456789abcdef", "profile", "", "2026-01-01 00:00:00")
		w.Server.Deps().Avatars.ResetCache()
		viewer.Do("GET", "/__test/emergency", "")
		got = decode(t, viewer.Do("GET", "/api/console/bootstrap", "").Body)
		if user := got["user"].(map[string]any); user["avatarVersion"] != "0123456789abcdef" || got["emergencyMode"] != true {
			t.Errorf("avatarVersion %v, emergencyMode %v", user["avatarVersion"], got["emergencyMode"])
		}

		// The branding as stored, once loaded
		exec(t, db, "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Stored Name', '2026-01-02 03:04:05'), ('showPoweredBy', 'false', '2026-01-02 03:04:05')")
		w.Server.Deps().Branding.LoadBranding(ctx)
		branding = decode(t, viewer.Do("GET", "/api/console/bootstrap", "").Body)["branding"].(map[string]any)
		if branding["appName"] != "Stored Name" || branding["showPoweredBy"] != false {
			t.Errorf("branding after a load = %v", branding)
		}
		if !db.IsPostgres() && branding["revision"] != "2026-01-02030405" {
			t.Errorf("revision = %v", branding["revision"])
		}

		german := w.SignedOut()
		german.Do("GET", "/__test/signin-german", "")
		if user := decode(t, german.Do("GET", "/api/console/bootstrap", "").Body)["user"].(map[string]any); user["preferredLanguage"] != "de" {
			t.Errorf("preferredLanguage = %v", user["preferredLanguage"])
		}

		admin := decode(t, w.As("admin").Do("GET", "/api/console/bootstrap", "").Body)
		if admin["isSuperAdmin"] != true || len(admin["permissions"].([]any)) != 43 {
			t.Errorf("admin: isSuperAdmin %v, %d permissions", admin["isSuperAdmin"], len(admin["permissions"].([]any)))
		}
	})
}

// keyOrder is the keys of a JSON object in the order they were written.
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

// With the Node console behind this one, a setting saved behind the Go
// console's back shows in the next answer; without it the cache keeps
// what it loaded, as the Node console did.
func TestBrandingSavedElsewhere(t *testing.T) {
	for _, nodeBehind := range []bool{true, false} {
		storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
			w := world(t, db, nodeBehind)
			w.Server.Deps().Branding.LoadBranding(context.Background())
			viewer := w.As("viewer")
			before := bootRevision(t, viewer.Do("GET", "/console-boot.js", "").Body)
			exec(t, db, "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Saved by Node', '2026-01-02 03:04:05')")
			name := decode(t, viewer.Do("GET", "/api/console/bootstrap", "").Body)["branding"].(map[string]any)["appName"]
			after := bootRevision(t, viewer.Do("GET", "/console-boot.js", "").Body)
			if (name == "Saved by Node") != nodeBehind || (after != before) != nodeBehind {
				t.Errorf("Node behind %v: appName %v, revision %q before and %q after the save", nodeBehind, name, before, after)
			}
		})
	}
}

// bootRevision is the branding revision a boot script carries.
func bootRevision(t *testing.T, script string) string {
	t.Helper()
	_, rest, ok := strings.Cut(script, `"brandingRevision":"`)
	revision, _, ok2 := strings.Cut(rest, `"`)
	if !ok || !ok2 {
		t.Fatalf("no revision in the boot script: %s", tailOf(script))
	}
	return revision
}

func tailOf(s string) string {
	if len(s) > 300 {
		return s[len(s)-300:]
	}
	return s
}

func TestSupportersAndBootScript(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, false)
		if res := w.SignedOut().Do("GET", "/api/console/supporters", ""); res.Status != http.StatusUnauthorized {
			t.Errorf("supporters signed out: %d", res.Status)
		}
		res := w.As("viewer").Do("GET", "/api/console/supporters", "")
		if res.Status != http.StatusOK || keyOrder(t, res.Body) != "success,honorary,individuals,links" || !strings.HasPrefix(res.Body, `{"success":true,`) {
			t.Errorf("supporters: %d %s", res.Status, res.Body)
		}
		boot := w.SignedOut().Do("GET", "/console-boot.js", "", "Accept-Language", "ar")
		if boot.Status != http.StatusOK || boot.Header.Get("Content-Type") != "application/javascript; charset=utf-8" ||
			boot.Header.Get("Cache-Control") != "no-store" || boot.Header.Get("ETag") == "" {
			t.Errorf("boot script: %d %v", boot.Status, boot.Header)
		}
		for _, part := range []string{`{"lang":"ar","dir":"rtl","themeMode":"system","palettes":{"light":{`, `"brandingRevision":"0","stringsUrl":"/api/console/strings/ar?v=`, `"publicPaths":["/login","/portal","/d","/remote"]});` + "\n"} {
			if !strings.Contains(boot.Body, part) {
				t.Errorf("boot script lacks %s:\n%s", part, tailOf(boot.Body))
			}
		}
		if again := w.SignedOut().Do("GET", "/console-boot.js", "", "Accept-Language", "ar", "If-None-Match", boot.Header.Get("ETag")); again.Status != http.StatusNotModified {
			t.Errorf("boot script with its ETag: %d", again.Status)
		}
	})
}

// withoutTexts is a navigation without its translated texts.
func withoutTexts(nav json.RawMessage) json.RawMessage {
	var sections []map[string]any
	if json.Unmarshal(nav, &sections) != nil {
		return nav
	}
	for _, s := range sections {
		delete(s, "text")
		pages, _ := s["pages"].([]any)
		for _, p := range pages {
			if page, ok := p.(map[string]any); ok {
				delete(page, "text")
				delete(page, "group")
			}
		}
	}
	out, _ := json.Marshal(sections)
	return out
}
