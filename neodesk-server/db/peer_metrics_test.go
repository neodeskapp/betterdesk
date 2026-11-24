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

// TestPeerUsageKeepsUnknownValuesAndNetworkRates: a usage sample stores a
// value the device did not measure as NULL (null in JSON, not 0) and the
// network rates next to CPU, memory and disk (L480).
func TestPeerUsageKeepsUnknownValuesAndNetworkRates(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		peerID := "L480-" + strings.ReplaceAll(t.Name(), "/", "-")
		if pg, ok := database.(*PostgresDB); ok {
			clean := func() { _, _ = pg.pool.Exec(pg.ctx, `DELETE FROM peer_metrics WHERE peer_id = $1`, peerID) }
			clean()
			t.Cleanup(clean)
		}
		f := func(v float64) *float64 { return &v }
		if err := database.SavePeerUsage(peerID, PeerUsage{CPU: f(37.5), Memory: f(41.25), Disk: f(22.5), NetRx: f(1100), NetTx: f(30)}); err != nil {
			t.Fatal(err)
		}
		if err := database.SavePeerUsage(peerID, PeerUsage{Memory: f(30)}); err != nil {
			t.Fatal(err)
		}
		metrics, err := database.GetPeerMetrics(peerID, 10)
		if err != nil || len(metrics) != 2 {
			t.Fatalf("GetPeerMetrics: %v %v", metrics, err)
		}
		newest, _ := json.Marshal(metrics[0])
		oldest, _ := json.Marshal(metrics[1])
		if want := `"cpu_usage":null,"memory_usage":30,"disk_usage":null,"created_at"`; !strings.Contains(string(newest), want) {
			t.Errorf("newest %s, want it to hold %s", newest, want)
		}
		if want := `"cpu_usage":37.5,"memory_usage":41.25,"disk_usage":22.5,"net_rx_bps":1100,"net_tx_bps":30,`; !strings.Contains(string(oldest), want) {
			t.Errorf("oldest %s, want it to hold %s", oldest, want)
		}
	})
}
