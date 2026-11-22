package settings_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// writes are the routes that change the branding, with a body that would
// pass their checks.
var writes = []struct{ method, path, body string }{
	{"POST", "/api/settings/branding", `{"appName":"x"}`},
	{"POST", "/api/settings/branding/upload-logo", ""},
	{"POST", "/api/settings/branding/upload-background", ""},
	{"POST", "/api/settings/branding/reset", "{}"},
	{"GET", "/api/settings/branding/export", ""},
	{"POST", "/api/settings/branding/import", "{}"},
	{"POST", "/api/settings/branding/profiles", `{"name":"x"}`},
	{"PUT", "/api/settings/branding/profiles/1", `{"name":"x"}`},
	{"POST", "/api/settings/branding/profiles/1/apply", "{}"},
	{"POST", "/api/settings/branding/profiles/1/duplicate", "{}"},
	{"DELETE", "/api/settings/branding/profiles/1", ""},
	{"POST", "/api/settings/themes/light/apply", "{}"},
	{"POST", "/api/settings/fonts/download", "{}"},
	{"DELETE", "/api/settings/fonts/Inter", ""},
	{"POST", "/api/settings/fonts/upload", ""},
}

// Every write needs a session and branding.edit. The branding routes have
// no panel section (PA3, F1): viewer and server_admin are refused by the
// permission.
func TestWritesNeedBrandingEdit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		out, viewer, server, admin := w.SignedOut(), w.As("viewer"), w.As("server_admin"), w.As("admin")
		for _, c := range writes {
			if res := out.Do(c.method, c.path, c.body); res.Status != http.StatusUnauthorized || res.Body != `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}` {
				t.Errorf("%s %s signed out: %d %s", c.method, c.path, res.Status, res.Body)
			}
			if res := viewer.Do(c.method, c.path, c.body); res.Status != http.StatusForbidden || res.Body != `{"success":false,"error":"Permission denied: branding.edit"}` {
				t.Errorf("%s %s as viewer: %d %s", c.method, c.path, res.Status, res.Body)
			}
			if res := server.Do(c.method, c.path, c.body); res.Status != http.StatusForbidden || res.Body != `{"success":false,"error":"Permission denied: branding.edit"}` {
				t.Errorf("%s %s as server_admin: %d %s", c.method, c.path, res.Status, res.Body)
			}
		}
		before := len(w.AuditRows())
		if before != 0 || w.Server.Deps().Branding.GetBranding().Text("appName") != "BetterDesk" {
			t.Errorf("refused writes changed something: %v, appName %q", w.AuditRows(), w.Server.Deps().Branding.GetBranding().Text("appName"))
		}
		for _, c := range writes {
			if res := admin.Do(c.method, c.path, c.body); res.Status == http.StatusUnauthorized || res.Status == http.StatusForbidden {
				t.Errorf("%s %s as admin: %d %s", c.method, c.path, res.Status, res.Body)
			}
		}
		// the refused requests changed nothing that is audited; admin's did
		if rows := w.AuditRows(); len(rows) == before {
			t.Errorf("admin's writes left no audit rows")
		}
		if got := w.Server.Deps().Branding.GetBranding().Text("appName"); got != "BetterDesk" {
			t.Errorf("appName after the writes (the last that changes it is a reset): %q", got)
		}
	})
}

func TestSaveBranding(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		res := admin.Do("POST", "/api/settings/branding", `{"themeMode":"light","colors":{"accentBlue":"#123456"}}`)
		if res.Status != http.StatusOK || keyOrder(t, res.Body) != "success,data,appearance,readability,revision" {
			t.Fatalf("theme save: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "theme_toggle|Theme switched to light|127.0.0.1|1" {
			t.Errorf("theme save audit: %s", got)
		}
		res = admin.Do("POST", "/api/settings/branding", `{"appName":"Saved","logoSvg":"<svg onload=\"x()\"><script>x()</script><rect/></svg>","bgImageUrl":"javascript:alert(1)","themeMode":"dark"}`)
		data := decode(t, res.Body)["data"].(map[string]any)
		if res.Status != http.StatusOK || data["appName"] != "Saved" || strings.Contains(data["logoSvg"].(string), "script") || data["bgImageUrl"] != "" {
			t.Errorf("save: %d %v", res.Status, data)
		}
		if got := lastAudit(t, w); got != "branding_update|Updated branding configuration|127.0.0.1|1" {
			t.Errorf("save audit: %s", got)
		}
		if got := decode(t, res.Body)["revision"]; got != w.Server.Deps().Branding.Revision() {
			t.Errorf("revision %v, the service has %s", got, w.Server.Deps().Branding.Revision())
		}
		// a theme switch with another key, an empty theme, a list and an
		// empty object are all a branding update
		for _, body := range []string{`{"themeMode":"light","appName":"Saved"}`, `{"themeMode":""}`, `["themeMode"]`, `{}`} {
			before := len(w.AuditRows())
			if res := admin.Do("POST", "/api/settings/branding", body); res.Status != http.StatusOK {
				t.Errorf("%s: %d %s", body, res.Status, res.Body)
			}
			if rows := w.AuditRows(); len(rows) != before+1 || !strings.HasPrefix(rows[len(rows)-1], "branding_update|") {
				t.Errorf("%s: audit %v", body, rows[before:])
			}
		}
		// a value that cannot be made text, and a theme mode that cannot
		for _, body := range []string{`{"appName":{"toString":1}}`, `{"themeMode":{"toString":1}}`} {
			if res := admin.Do("POST", "/api/settings/branding", body); res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Server error"}` {
				t.Errorf("%s: %d %s", body, res.Status, res.Body)
			}
		}
		if res := admin.Do("POST", "/api/settings/branding", "appName=x", "Content-Type", "application/x-www-form-urlencoded"); res.Status != http.StatusUnsupportedMediaType {
			t.Errorf("a form: %d %s", res.Status, res.Body)
		}
	})
}

func TestExportImportReset(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		admin.Do("POST", "/api/settings/branding", `{"appName":"Exported"}`)
		res := admin.Do("GET", "/api/settings/branding/export", "")
		if res.Status != http.StatusOK || res.Header.Get("Content-Type") != "application/json" ||
			res.Header.Get("Content-Disposition") != `attachment; filename="betterdesk-theme.json"` ||
			keyOrder(t, res.Body) != "version,type,appearance,branding" || decode(t, res.Body)["branding"].(map[string]any)["appName"] != "Exported" {
			t.Errorf("export: %d %v %.80s", res.Status, res.Header, res.Body)
		}
		exported := res.Body
		if res := admin.Do("POST", "/api/settings/branding/reset", "{}"); res.Body != `{"success":true,"message":"Branding reset to defaults"}` {
			t.Errorf("reset: %s", res.Body)
		}
		if got := lastAudit(t, w); got != "branding_reset|Reset branding to defaults|127.0.0.1|1" {
			t.Errorf("reset audit: %s", got)
		}
		if got := w.Server.Deps().Branding.GetBranding().Text("appName"); got != "BetterDesk" {
			t.Errorf("after the reset: %s", got)
		}
		if res := admin.Do("POST", "/api/settings/branding/import", exported); res.Body != `{"success":true,"message":"Theme imported successfully"}` {
			t.Errorf("import: %s", res.Body)
		}
		if got := lastAudit(t, w); got != "branding_import|Imported branding preset|127.0.0.1|1" {
			t.Errorf("import audit: %s", got)
		}
		// the import brings the exported settings back (BUGS BR5)
		if got := w.Server.Deps().Branding.GetBranding().Text("appName"); got != "Exported" {
			t.Errorf("after the import: %s", got)
		}
		before := len(w.AuditRows())
		for _, c := range []struct{ body, contentType string }{
			{`{"type":"other","branding":{"appName":"x"}}`, ""},
			{`{"type":"betterdesk-theme"}`, ""},
			{`[]`, ""},
			{"not json", "text/plain"},
		} {
			header := []string{}
			if c.contentType != "" {
				header = []string{"Content-Type", c.contentType}
			}
			if res := admin.Do("POST", "/api/settings/branding/import", c.body, header...); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"Invalid theme preset file"}` {
				t.Errorf("import %s: %d %s", c.body, res.Status, res.Body)
			}
		}
		if res := admin.Do("POST", "/api/settings/branding/import", `{"type":"betterdesk-theme","branding":{"colors":"red"}}`); res.Status != http.StatusInternalServerError {
			t.Errorf("colours as text: %d %s", res.Status, res.Body)
		}
		// a form is read: branding "x" is a theme with nothing in it
		if res := admin.Do("POST", "/api/settings/branding/import", "type=betterdesk-theme&branding=x", "Content-Type", "application/x-www-form-urlencoded"); res.Status != http.StatusOK {
			t.Errorf("a form: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != before+1 {
			t.Errorf("refused imports were audited: %v", rows[before:])
		}
	})
}

func TestApplyTheme(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		if res := admin.Do("POST", "/api/settings/themes/insolve/apply", "{}"); res.Body != `{"success":true,"message":"Theme \"insolve\" applied"}` {
			t.Errorf("apply: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "theme_apply|Applied theme: insolve|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		if got := w.Server.Deps().Branding.GetBranding().Text("appName"); got != "insolve Core Server" {
			t.Errorf("after the theme: %s", got)
		}
		before := len(w.AuditRows())
		for path, want := range map[string]string{
			"/api/settings/themes/bad.id/apply":  `400 {"success":false,"error":"Invalid theme id"}`,
			"/api/settings/themes/nothing/apply": `404 {"success":false,"error":"Theme not found"}`,
		} {
			if res := admin.Do("POST", path, "{}"); strconv.Itoa(res.Status)+" "+res.Body != want {
				t.Errorf("%s: %d %s", path, res.Status, res.Body)
			}
		}
		// a file that is JSON but no theme, and one that is no JSON
		themes := w.Server.Deps().Config.ThemesDir
		for name, content := range map[string]string{"notatheme.json": `{"type":"other"}`, "broken.json": `{`} {
			if err := os.WriteFile(filepath.Join(themes, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if res := admin.Do("POST", "/api/settings/themes/notatheme/apply", "{}"); res.Status != http.StatusBadRequest || res.Body != `{"success":false,"error":"Invalid theme format"}` {
			t.Errorf("no theme: %d %s", res.Status, res.Body)
		}
		if res := admin.Do("POST", "/api/settings/themes/broken/apply", "{}"); res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Failed to apply theme"}` {
			t.Errorf("no JSON: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != before {
			t.Errorf("refused themes were audited: %v", rows[before:])
		}
	})
}

// A save through the route reaches every route that reads the branding
// at once, in every group: the cache of the branding service is the one
// copy they all read.
func TestASaveReachesEveryReader(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin, visitor := w.As("admin"), w.SignedOut()
		ctx := context.Background()
		bundle, err := w.Server.Deps().Generator.CreateBundle(ctx, gen.NewBundle{BundleID: "readerbundle01", Name: "Readers", Branding: "{}", BrandingHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		res := admin.Do("POST", "/api/settings/branding", `{"appName":"Reached Everywhere","themeMode":"light","agentBgType":"color","agentBgColor":"#123456",`+
			`"faviconSvg":"<svg xmlns=\"http://www.w3.org/2000/svg\"><circle r=\"9\"/></svg>","customCss":".reached-everywhere{color:red}","logoType":"text"}`)
		if res.Status != http.StatusOK {
			t.Fatalf("save: %d %s", res.Status, res.Body)
		}
		revision := w.Server.Deps().Branding.Revision()
		readers := []struct {
			who, path string
			wants     []string
		}{
			{"admin", "/api/settings/branding", []string{`"appName":"Reached Everywhere"`, `"revision":"` + revision + `"`}},
			{"admin", "/api/settings/appearance", []string{`"appName":"Reached Everywhere"`, `"revision":"` + revision + `"`}},
			{"admin", "/api/console/bootstrap", []string{`"appName":"Reached Everywhere"`}},
			{"visitor", "/console-boot.js", []string{`"themeMode":"light"`, `"brandingRevision":"` + revision + `"`}},
			{"visitor", "/api/auth/login-page", []string{`"appName":"Reached Everywhere"`}},
			{"visitor", "/api/bd/branding", []string{`"company_name":"Reached Everywhere"`}},
			{"visitor", "/api/bd/appearance", []string{`"appName":"Reached Everywhere"`}},
			{"visitor", "/api/bd/server-info", []string{`"panel_name":"Reached Everywhere"`, `"revision":"` + revision + `"`}},
			{"visitor", "/css/branding.css", []string{".reached-everywhere{color:red}"}},
			{"visitor", "/css/theme.css", []string{".reached-everywhere{color:red}"}},
			{"visitor", "/branding/favicon.svg", []string{`<circle r="9"/>`}},
			{"visitor", "/api/d/" + bundle.PublicID() + "/page", []string{`"companyName":"Reached Everywhere"`, "#123456"}},
			{"admin", "/api/server-attestation/badge-config", []string{"Reached Everywhere"}},
			{"admin", "/api/settings/backup", []string{"Reached Everywhere"}},
		}
		for _, reader := range readers {
			agent := visitor
			if reader.who == "admin" {
				agent = admin
			}
			res := agent.Do("GET", reader.path, "")
			if strings.HasPrefix(res.Body, "\x1f\x8b") {
				// the backup is a gzip file
				unzipped, err := gzip.NewReader(strings.NewReader(res.Body))
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(unzipped)
				if err != nil {
					t.Fatal(err)
				}
				res.Body = string(raw)
			}
			for _, want := range reader.wants {
				if res.Status != http.StatusOK || !strings.Contains(res.Body, want) {
					t.Errorf("%s: %d, %q not in %.300s", reader.path, res.Status, want, res.Body)
				}
			}
		}
		if got := visitor.Do("GET", "/css/branding.css", "").Header.Get("X-Branding-Revision"); got != revision {
			t.Errorf("X-Branding-Revision %q, want %q", got, revision)
		}
	})
}

// The routes that change the settings by other means than a save reach
// the readers as well: a reset, an import, a profile, a theme.
func TestEveryWriteReachesTheReaders(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin, visitor := w.As("admin"), w.SignedOut()
		readName := func() string {
			body := visitor.Do("GET", "/api/bd/branding", "").Body
			m := regexp.MustCompile(`"company_name":"([^"]*)"`).FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no company_name in %s", body)
			}
			return m[1]
		}
		profile := decode(t, admin.Do("POST", "/api/settings/branding/profiles", `{"name":"P","branding":{"appName":"From profile"}}`).Body)["data"].(map[string]any)["id"].(float64)
		steps := []struct{ method, path, body, want string }{
			{"POST", "/api/settings/branding/import", `{"type":"betterdesk-theme","branding":{"appName":"Imported"}}`, "Imported"},
			{"POST", "/api/settings/branding/profiles/" + strconv.Itoa(int(profile)) + "/apply", "{}", "From profile"},
			{"POST", "/api/settings/themes/insolve/apply", "{}", "insolve Core Server"},
			{"POST", "/api/settings/branding/reset", "{}", "BetterDesk"},
		}
		for _, s := range steps {
			if res := admin.Do(s.method, s.path, s.body); res.Status != http.StatusOK {
				t.Fatalf("%s: %d %s", s.path, res.Status, res.Body)
			}
			if got := readName(); got != s.want {
				t.Errorf("after %s the device API says %q, want %q", s.path, got, s.want)
			}
		}
	})
}
