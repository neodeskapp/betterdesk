package netmonitor

import (
	"context"
	"fmt"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Checks of the same second come newest first, so a limit cuts the oldest
// of them (BUGS.md NM8).
func TestHistoryOfOneSecondKeepsItsOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, "INSERT INTO network_targets (id, name, host, check_type) VALUES (1, 't', 'h', 'ping')"); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 3; i++ {
			if _, err := db.Exec(ctx, "INSERT INTO network_checks (id, target_id, status, checked_at) VALUES (?, 1, 'up', ?)", i, "2026-01-01 00:00:00"); err != nil {
				t.Fatal(err)
			}
		}
		limit := Num(2)
		checks, err := CheckHistory(ctx, db, Num(1), HistoryFilter{Limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, c := range checks {
			ids = append(ids, c.ID.V)
		}
		if fmt.Sprint(ids) != "[3 2]" {
			t.Errorf("checks %v, want the newest two: [3 2]", ids)
		}
	})
}
