package dataguard_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestEventListHasADefaultLimit: the event list asked for without a limit
// gives the 500 newest events, not the whole table; a limit still narrows
// it (BUGS RV66, RV27).
func TestEventListHasADefaultLimit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range 501 {
			at := start.Add(time.Duration(i) * time.Second).Format(time.DateTime)
			if _, err := db.Exec(ctx, `INSERT INTO dlp_events (device_id, event_source, created_at) VALUES (?, 'usb', ?)`, fmt.Sprintf("D%d", i), at); err != nil {
				t.Fatal(err)
			}
		}
		count := func(target string) (int, string) {
			t.Helper()
			got := w.As("admin").Do("GET", target, "")
			var events []struct {
				DeviceID string `json:"device_id"`
			}
			if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &events) != nil {
				t.Fatalf("GET %s: %d %.300s", target, got.Status, got.Body)
			}
			last := ""
			if len(events) > 0 {
				last = events[len(events)-1].DeviceID
			}
			return len(events), last
		}
		if n, last := count("/api/dataguard/events"); n != 500 || last != "D1" {
			t.Errorf("events without a limit: %d, the oldest %s; want 500, D1", n, last)
		}
		if n, _ := count("/api/dataguard/events?limit=3"); n != 3 {
			t.Errorf("events with limit 3: %d", n)
		}
	})
}
