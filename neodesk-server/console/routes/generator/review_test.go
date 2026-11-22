package generator_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestRebuildPlatformQueuesTheCurrentGeneration queues the build of the
// generation the bundle's profile and client commit belong to, and
// leaves the ready build of the generation it hands out, while a newer
// generation is not promoted (BUGS RV52: the build of the handed-out
// generation was queued, no worker took it, and its download answered
// build_pending for good).
func TestRebuildPlatformQueuesTheCurrentGeneration(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var dataDir string
		w := replay.NewWorld(t, db, func(cfg *config.Config) { dataDir = cfg.DataDir })
		ready(t, w, dataDir)
		w.Exec(`INSERT INTO agent_bundles (bundle_id, slug, name, branding, branding_hash, active_generation_hash)
			VALUES ('twogen00001', 'twogen', 'Two', '{"server_host":"h.example"}', 'newgen', 'oldgen')`, nil)
		w.Exec(`INSERT INTO agent_bundle_builds (branding_hash, platform, arch, format, status, artifact_path, artifact_size)
			VALUES ('oldgen', 'linux', 'x64', 'portable', 'ready', '/made/up/old.tar.gz', 10),
			       ('newgen', 'linux', 'x64', 'portable', 'failed', NULL, 0)`, nil)
		got := w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/generator/bundles/twogen00001/rebuild/linux/x64/portable", Body: replay.Text("{}")})
		if got.Status != 200 || !strings.Contains(got.Body, `"branding_hash":"newgen"`) || strings.Contains(got.Body, `"branding_hash":"oldgen"`) {
			t.Errorf("rebuild: %d %s", got.Status, got.Body)
		}
		rows := w.Rows(`SELECT branding_hash, status, artifact_path FROM agent_bundle_builds
			WHERE platform = 'linux' AND arch = 'x64' AND format = 'portable' ORDER BY branding_hash`, nil)
		if want := `[{"branding_hash":"newgen","status":"queued","artifact_path":null},{"branding_hash":"oldgen","status":"ready","artifact_path":"/made/up/old.tar.gz"}]`; rows != want {
			t.Errorf("builds:\n %s\nwant\n %s", rows, want)
		}
	})
}

// TestPreviewTakesOnlyAnObject answers a preview whose profile is a long
// text as one with an empty profile, without building an object of one
// key per character (BUGS RV53: 15 MB of text took 2.6 GB of memory).
func TestPreviewTakesOnlyAnObject(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		preview := func(body string) replay.Answer {
			return w.Do(replay.Request{Role: "admin", Method: "POST", Path: "/api/generator/preview", Body: replay.Text(body)})
		}
		empty := preview(`{"branding":{}}`)
		if empty.Status != 200 {
			t.Fatalf("empty profile: %d %s", empty.Status, empty.Body)
		}
		for _, body := range []string{`{"branding":"abc"}`, `{"branding":["x","y"]}`, `{"branding":5}`} {
			if got := preview(body); got.Status != empty.Status || got.Body != empty.Body {
				t.Errorf("%s: %d %s, want the empty profile's answer", body, got.Status, got.Body)
			}
		}
		long := `{"branding":"` + strings.Repeat("a", 2<<20) + `"}`
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		got := preview(long)
		runtime.ReadMemStats(&after)
		if got.Status != empty.Status || got.Body != empty.Body {
			t.Errorf("long text: %d %.200s", got.Status, got.Body)
		}
		if used := after.TotalAlloc - before.TotalAlloc; used > 100<<20 {
			t.Errorf("a preview with 2 MB of text allocated %d MB", used>>20)
		}
	})
}
