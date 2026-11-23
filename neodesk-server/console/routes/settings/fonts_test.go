package settings_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// google answers the font download in place of fonts.googleapis.com and
// fonts.gstatic.com, and records what it was asked. Nothing leaves the
// test.
type google struct {
	mu    sync.Mutex
	asked []string
	fail  error
}

const fakeCSS = `/* latin-ext */
@font-face { font-family: 'Inter'; font-weight: 400; src: url(https://fonts.gstatic.com/s/inter/ext-400.woff2) format('woff2'); unicode-range: U+0100; }
/* latin */
@font-face { font-family: 'Inter'; font-weight: 400; src: url(https://fonts.gstatic.com/s/inter/latin-400.woff2) format('woff2'); unicode-range: U+0000; }
/* latin */
@font-face { font-family: 'Inter'; font-weight: 700; src: url(https://fonts.gstatic.com/s/inter/latin-700.woff2) format('woff2'); unicode-range: U+0000; }
`

func (g *google) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.asked = append(g.asked, req.URL.String())
	fail := g.fail
	g.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	body := "woff2 of " + req.URL.Path
	if req.URL.Host == "fonts.googleapis.com" {
		body = fakeCSS
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// withGoogle gives the console a branding service whose font downloads go
// to g. The handlers of the settings group take the service at every
// request.
func withGoogle(t *testing.T, w *routetest.World, g *google) *branding.Service {
	t.Helper()
	cfg := w.Server.Deps().Config
	s, err := branding.New(branding.Options{
		DB: w.DB, UploadsDir: cfg.UploadsDir, FontsDir: cfg.FontsDir, TempDir: cfg.TempDir, ThemesDir: cfg.ThemesDir,
		PalettesFile: cfg.PalettesFile, FaviconFile: cfg.FaviconFile, HTTP: g,
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.LoadBranding(context.Background())
	w.Server.Deps().Branding = s
	return s
}

func TestDownloadFont(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		g := &google{}
		withGoogle(t, w, g)
		admin := w.As("admin")
		res := admin.Do("POST", "/api/settings/fonts/download", `{"family":"Inter","weights":["400","700","900"]}`)
		if res.Body != `{"success":true,"data":{"success":true,"path":"/fonts/inter","files":["inter-400.woff2","inter-700.woff2"],"cssPath":"/fonts/inter/font.css"}}` {
			t.Fatalf("download: %d %s", res.Status, res.Body)
		}
		if got := strings.Join(g.asked, " "); got != "https://fonts.googleapis.com/css2?family=Inter:wght@400;700&display=swap "+
			"https://fonts.gstatic.com/s/inter/latin-400.woff2 https://fonts.gstatic.com/s/inter/latin-700.woff2" {
			t.Errorf("requests: %s", got)
		}
		fonts := w.Server.Deps().Config.FontsDir
		if stored, _ := os.ReadFile(filepath.Join(fonts, "inter", "inter-700.woff2")); string(stored) != "woff2 of /s/inter/latin-700.woff2" {
			t.Errorf("stored file: %q", stored)
		}
		if got := lastAudit(t, w); got != "font_download|Downloaded font: Inter (2 files)|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		// weights that are empty in JavaScript's eyes are the four defaults
		g.asked = nil
		admin.Do("POST", "/api/settings/fonts/download", `{"family":"Inter","weights":0}`)
		if len(g.asked) == 0 || !strings.Contains(g.asked[0], ":wght@400;500;600;700&") {
			t.Errorf("default weights: %v", g.asked)
		}

		before := len(w.AuditRows())
		g.asked = nil
		for body, want := range map[string]string{
			`{}`:                                   `400 {"success":false,"error":"Font family is required"}`,
			`{"family":""}`:                        `400 {"success":false,"error":"Font family is required"}`,
			`{"family":12}`:                        `400 {"success":false,"error":"Font family is required"}`,
			`["Inter"]`:                            `400 {"success":false,"error":"Font family is required"}`,
			`{"family":"Inter","weights":"400"}`:   `500 {"success":false,"error":"weights.filter is not a function"}`,
			`{"family":"Other","weights":{"a":1}}`: `500 {"success":false,"error":"weights.filter is not a function"}`,
			`{"family":"你好"}`:                      `500 {"success":false,"error":"Invalid font family name"}`,
		} {
			res := admin.Do("POST", "/api/settings/fonts/download", body)
			if strconv.Itoa(res.Status)+" "+res.Body != want {
				t.Errorf("%s: %d %s", body, res.Status, res.Body)
			}
		}
		if len(g.asked) != 0 {
			t.Errorf("refused downloads asked Google: %v", g.asked)
		}
		// the connection's own error, without Go's "Get ...:" in front
		g.fail = errors.New("getaddrinfo ENOTFOUND fonts.googleapis.com")
		if res := admin.Do("POST", "/api/settings/fonts/download", `{"family":"Roboto"}`); res.Status != http.StatusInternalServerError ||
			res.Body != `{"success":false,"error":"getaddrinfo ENOTFOUND fonts.googleapis.com"}` {
			t.Errorf("offline: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != before {
			t.Errorf("failed downloads were audited: %v", rows[before:])
		}
	})
}

func TestDeleteFont(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db)
		admin := w.As("admin")
		fonts := w.Server.Deps().Config.FontsDir
		for _, name := range []string{"open-sans", "mono"} {
			if err := os.MkdirAll(filepath.Join(fonts, name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fonts, name, "font.css"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		// decoded once by the router and once more by the route
		if res := admin.Do("DELETE", "/api/settings/fonts/Open%2520Sans", ""); res.Body != `{"success":true,"deleted":true}` {
			t.Errorf("delete: %d %s", res.Status, res.Body)
		}
		if got := lastAudit(t, w); got != "font_delete|Deleted font: Open Sans|127.0.0.1|1" {
			t.Errorf("audit: %s", got)
		}
		if _, err := os.Stat(filepath.Join(fonts, "open-sans")); !os.IsNotExist(err) {
			t.Errorf("the folder is still there: %v", err)
		}
		before := len(w.AuditRows())
		if res := admin.Do("DELETE", "/api/settings/fonts/Open%20Sans", ""); res.Body != `{"success":true,"deleted":false}` {
			t.Errorf("delete again: %d %s", res.Status, res.Body)
		}
		for _, path := range []string{"%25E0%25A4", "%25", "%E4%BD%A0"} {
			if res := admin.Do("DELETE", "/api/settings/fonts/"+path, ""); res.Status != http.StatusInternalServerError || res.Body != `{"success":false,"error":"Failed to delete font"}` {
				t.Errorf("%s: %d %s", path, res.Status, res.Body)
			}
		}
		if res := admin.Do("DELETE", "/api/settings/fonts/%FF", ""); res.Status != http.StatusBadRequest {
			t.Errorf("a path that does not decode: %d %s", res.Status, res.Body)
		}
		if rows := w.AuditRows(); len(rows) != before {
			t.Errorf("nothing deleted, but audited: %v", rows[before:])
		}
		if _, err := os.Stat(filepath.Join(fonts, "mono", "font.css")); err != nil {
			t.Errorf("another font went: %v", err)
		}
	})
}
