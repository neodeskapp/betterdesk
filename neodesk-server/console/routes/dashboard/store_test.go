package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The counts of the console's device table are taken after the table was
// brought up to date with the server's.
func TestDeviceCountsSyncFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		counts := deviceCounts{db: db, sync: func(ctx context.Context) {
			if _, err := db.Exec(ctx, "INSERT INTO peer (id) VALUES ('900100001')"); err != nil {
				t.Fatal(err)
			}
		}}
		stats, err := counts.GetStats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		text, _ := json.Marshal(stats)
		if want := `{"total":1,"online":0,"banned":0,"offline":1}`; string(text) != want {
			t.Errorf("got %s, want %s", text, want)
		}
	})
}
