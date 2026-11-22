package branding

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// steppingClock moves on by one second at every reading, so a revision
// taken from the clock differs at every load.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func cacheService(t *testing.T, db *store.DB) *Service {
	t.Helper()
	opts := fixtureOptions(t.TempDir())
	opts.DB = db
	opts.Now = (&steppingClock{now: fixedNow}).Now
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.LoadBranding(context.Background())
	return s
}

// The cache is the one copy every route reads, so each write has to
// leave it as the database now is: the settings and a new revision.
func TestWritesReachTheCache(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := cacheService(t, db)
		profile, err := s.CreateProfile(ctx, "Cached", "", object("type", "betterdesk-theme", "branding", object("appName", "From a profile")))
		if err != nil {
			t.Fatal(err)
		}
		writes := []struct {
			name, appName string
			write         func() error
		}{
			{"save", "Saved", func() error { return s.SaveBranding(ctx, object("appName", "Saved")) }},
			{"import", "Imported", func() error {
				_, err := s.ImportPreset(ctx, object("type", "betterdesk-theme", "branding", object("appName", "Imported")))
				return err
			}},
			{"profile", "From a profile", func() error {
				applied, err := s.ApplyProfile(ctx, profile)
				if !applied && err == nil {
					t.Error("the profile was not applied")
				}
				return err
			}},
			{"theme", "BetterDesk", func() error {
				result, err := s.ApplyTheme(ctx, "light")
				if result != ThemeApplied && err == nil {
					t.Errorf("the theme was not applied: %d", result)
				}
				return err
			}},
			{"background", "BetterDesk", func() error {
				_, applied, err := s.ApplyBackground(ctx, "bgImageUrl", "/uploads/bg-0123456789abcdef.png")
				if !applied && err == nil {
					t.Error("the background was not applied")
				}
				return err
			}},
			{"reset", "BetterDesk", func() error { return s.ResetBranding(ctx) }},
		}
		for _, w := range writes {
			before := s.Revision()
			if err := w.write(); err != nil {
				t.Fatalf("%s: %v", w.name, err)
			}
			if got := s.GetBranding().Text("appName"); got != w.appName {
				t.Errorf("after %s the cache has appName %q, want %q", w.name, got, w.appName)
			}
			if s.Revision() == before {
				t.Errorf("after %s the revision is still %q", w.name, before)
			}
			if w.name == "background" && s.GetBranding().Text("bgImageUrl") != "/uploads/bg-0123456789abcdef.png" {
				t.Errorf("after %s bgImageUrl = %q", w.name, s.GetBranding().Text("bgImageUrl"))
			}
		}
	})
}

// A row written past the service (the restore of a full backup, another
// program) shows after the next load, which the next write or the next
// start does, not before: the Node console's cache worked the same way
// (BUGS BR14).
func TestRowsWrittenElsewhereShowAfterTheNextLoad(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := cacheService(t, db)
		before := s.Revision()
		if _, err := db.Exec(ctx, "INSERT INTO branding_config (key, value, updated_at) VALUES ('appName', 'Written elsewhere', '2026-01-02 03:04:05')"); err != nil {
			t.Fatal(err)
		}
		if got := s.GetBranding().Text("appName"); got != "BetterDesk" || s.Revision() != before {
			t.Errorf("before a load: appName %q, revision %q (was %q)", got, s.Revision(), before)
		}
		if err := s.SaveBranding(ctx, object("footerText", "x")); err != nil {
			t.Fatal(err)
		}
		if got := s.GetBranding().Text("appName"); got != "Written elsewhere" {
			t.Errorf("after the next save: appName %q", got)
		}
	})
}

// With no stored setting the revision is the one of no rows, the same at
// every load: the stylesheet is the same too (BUGS BR9).
func TestRevisionOfAnEmptyTable(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := cacheService(t, db)
		first := s.Revision()
		if first == "0" || first == "" {
			t.Fatalf("revision after the first load = %q", first)
		}
		if got := s.Revision(); got != first {
			t.Errorf("revision changed from %q to %q without a load", first, got)
		}
		s.LoadBranding(ctx)
		if got := s.Revision(); got != first {
			t.Errorf("LoadBranding changed the revision of an empty table from %q to %q", first, got)
		}
	})
}

func TestNoPalettesFile(t *testing.T) {
	opts := fixtureOptions(t.TempDir())
	opts.PalettesFile = ""
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := Stringify(s.BootPalettes()); got != `{"light":{},"dark":{}}` {
		t.Errorf("palettes without a file = %s", got)
	}
	opts.PalettesFile = opts.ThemesDir + "/missing.json"
	if _, err := New(opts); err == nil {
		t.Error("New took a palettes file that does not exist")
	}
}
