package generator_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

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
