package db

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPeerMetricsSendShortFloats: a stored heartbeat metric goes out as the
// number it was, 12.3 and not the 12.300000190734863 of the four-byte REAL
// column PostgreSQL keeps it in (BUGS D18).
func TestPeerMetricsSendShortFloats(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		// The PostgreSQL test database is shared: a peer of this run only,
		// its metrics removed afterwards
		peerID := "D18-" + strings.ReplaceAll(t.Name(), "/", "-")
		if pg, ok := database.(*PostgresDB); ok {
			clean := func() { _, _ = pg.pool.Exec(pg.ctx, `DELETE FROM peer_metrics WHERE peer_id = $1`, peerID) }
			clean()
			t.Cleanup(clean)
		}
		if err := database.SavePeerMetric(peerID, 12.3, 45.6, 0.1); err != nil {
			t.Fatal(err)
		}
		metrics, err := database.GetPeerMetrics(peerID, 10)
		if err != nil || len(metrics) != 1 {
			t.Fatalf("GetPeerMetrics: %v %v", metrics, err)
		}
		latest, err := database.GetLatestPeerMetric(peerID)
		if err != nil || latest == nil {
			t.Fatalf("GetLatestPeerMetric: %v %v", latest, err)
		}
		for name, m := range map[string]*PeerMetric{"GetPeerMetrics": metrics[0], "GetLatestPeerMetric": latest} {
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if want := `"cpu_usage":12.3,"memory_usage":45.6,"disk_usage":0.1,`; !strings.Contains(string(raw), want) {
				t.Errorf("%s: %s, want it to hold %s", name, raw, want)
			}
		}
	})
}
