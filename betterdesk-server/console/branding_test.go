package console

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The console loads the stored branding at start, before it listens,
// with or without the Node console behind it. A palettes file that cannot
// be read stops the start, as it stopped the Node console.
func TestBrandingAtStart(t *testing.T) {
	for _, nodeBehind := range []bool{false, true} {
		db := storetest.Migrated(t)
		ctx := context.Background()
		if _, err := db.Exec(ctx, "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Stored', '2026-01-02 03:04:05')"); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{
			Host: "127.0.0.1", DataDir: t.TempDir(), UploadsDir: t.TempDir(),
			LangDir: "static/files/lang", WebUIDir: t.TempDir(), DefaultLanguage: "en",
			PalettesFile: filepath.Join("branding", "testdata", "console", "config", "theme-palettes.json"),
			Getenv: func(key string) string {
				if key == "SESSION_SECRET" {
					return testSecret
				}
				return ""
			},
		}
		if nodeBehind {
			cfg.NodeUpstream = &url.URL{Scheme: "http", Host: "127.0.0.1:9"}
		}
		s, err := New(ctx, Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Deps().Branding.GetBranding().Text("appName"); got != "BetterDesk" {
			t.Errorf("appName before Start = %q", got)
		}
		if err := s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		if got, rev := s.Deps().Branding.GetBranding().Text("appName"), s.Deps().Branding.Revision(); got != "Stored" || rev != "8d6cfd59" {
			t.Errorf("after Start: appName %q, revision %q", got, rev)
		}
		s.Stop()
	}

	cfg := &config.Config{Host: "127.0.0.1", DataDir: t.TempDir(), PalettesFile: filepath.Join(t.TempDir(), "missing.json"),
		Getenv: func(string) string { return testSecret }}
	if _, err := New(context.Background(), Options{Config: cfg, Store: storetest.Migrated(t), API: http.NotFoundHandler()}); err == nil {
		t.Error("the console was built without its palettes file")
	}
}
