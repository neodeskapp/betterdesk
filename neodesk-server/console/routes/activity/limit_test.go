package activity_test

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

// TestListGivesTheNewestSummariesOnly: the list of all devices' summaries
// gives the 500 received last, not every summary of the window (BUGS
// RV66).
func TestListGivesTheNewestSummariesOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := range 501 {
			at := start.Add(time.Duration(i) * time.Second).Format(time.DateTime)
			if _, err := db.Exec(ctx, `INSERT INTO activity_summaries (device_id, reported_at, received_at) VALUES (?, ?, ?)`, fmt.Sprintf("D%d", i), at, at); err != nil {
				t.Fatal(err)
			}
		}
		got := w.As("admin").Do("GET", "/api/activity", "")
		var list struct {
			Summaries []struct {
				DeviceID string `json:"device_id"`
			} `json:"summaries"`
			Total int `json:"total"`
		}
		if got.Status != http.StatusOK || json.Unmarshal([]byte(got.Body), &list) != nil {
			t.Fatalf("GET /api/activity: %d %.300s", got.Status, got.Body)
		}
		if n := len(list.Summaries); n != 500 || list.Total != 500 || list.Summaries[0].DeviceID != "D500" || list.Summaries[n-1].DeviceID != "D1" {
			t.Errorf("the list: %d summaries, total %d, want the 500 newest, D500 to D1", n, list.Total)
		}
	})
}
