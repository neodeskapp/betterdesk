package devices_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestDeviceUsageHistoryHasNetworkRates: the server writes NeoDesk
// clients' telemetry into peer_metrics with the network rates and with
// NULL for a CPU value the client did not measure (L480). The device
// answers both: the rates as net_rx and net_tx, the unknown CPU as null.
// A database whose peer_metrics has no rate columns yet (a console that
// runs before the server added them) answers the history without them.
func TestDeviceUsageHistoryHasNetworkRates(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		insert := `INSERT INTO peer_metrics (peer_id, cpu_usage, memory_usage, disk_usage, created_at) VALUES (?, ?, ?, ?, ?)`
		if _, err := db.Exec(ctx, insert, "A", 37.5, 41.25, 22.5, "2026-11-24 22:00:00"); err != nil {
			t.Fatal(err)
		}
		w, _ := world(t, db, nil, nil)
		admin := w.As("admin")
		got := admin.Do("GET", "/api/devices/A", "")
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"metrics_history":[{"cpu":37.5,"memory":41.25,"disk":22.5,"time":`) {
			t.Fatalf("without the rate columns: %d %s", got.Status, got.Body)
		}

		for _, column := range []string{"net_rx_bps", "net_tx_bps"} {
			if _, err := db.Exec(ctx, `ALTER TABLE peer_metrics ADD COLUMN `+column+` REAL DEFAULT NULL`); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, `UPDATE peer_metrics SET net_rx_bps = 1100, net_tx_bps = 30 WHERE peer_id = 'A'`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO peer_metrics (peer_id, cpu_usage, memory_usage, disk_usage, created_at) VALUES (?, NULL, ?, ?, ?)`,
			"A", 30.0, 22.5, "2026-11-24 22:01:30"); err != nil {
			t.Fatal(err)
		}
		got = admin.Do("GET", "/api/devices/A", "")
		body := got.Body
		for _, want := range []string{
			`{"cpu":null,"memory":30,"disk":22.5,"time":`,
			`{"cpu":37.5,"memory":41.25,"disk":22.5,"time":`,
			`"net_rx":1100,"net_tx":30}`,
			`"metrics":{"cpu_usage":null,"memory_usage":30,`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("device %s\nwant it to hold %s", body, want)
			}
		}
	})
}
