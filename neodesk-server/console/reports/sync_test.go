package reports

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A device report reads the console's device table after it was brought up
// to date: a device that arrives with the sync is in the report.
func TestDeviceReportSyncsTheDevicesFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		calls := 0
		e := New(Options{DB: db, SyncPeers: func(ctx context.Context) {
			calls++
			if calls > 1 {
				return
			}
			if _, err := db.Exec(ctx, "INSERT INTO peer (id) VALUES ('900100001')"); err != nil {
				t.Fatal(err)
			}
		}})
		report, err := e.Generate(context.Background(), valueOf(t, `"devices"`), Filters{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := json.Marshal(report.Data)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), `"total_devices":1,`) {
			t.Errorf("the device of the sync is not in the report: %s", text)
		}
		// getAllPeers and getPeerStats each synced
		if calls != 2 {
			t.Errorf("%d syncs, want 2", calls)
		}
	})
}
