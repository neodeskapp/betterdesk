package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTelemetryMetricsKeepAHistory: each verified heartbeat with metrics
// adds a sample to the device's usage history, the one the device page
// draws. Before, NeoDesk clients' metrics went only into the latest
// snapshot and the history stayed empty (L480). A CPU value without the
// time it covers (clients up to 1.5.0-rc.7 read one refresh: 100 % on
// Windows, 0 % on Linux) is stored as unknown, and metrics the client
// could not collect add nothing.
func TestTelemetryMetricsKeepAHistory(t *testing.T) {
	srv, deviceID, deviceUUID, devicePriv, responsePub, _ := newConnectionModeFixture(t)
	send := func(seq uint64, metrics map[string]any, status string) {
		t.Helper()
		payload := desktopIdentity(deviceID, deviceUUID, "")
		payload["telemetry"] = map[string]any{
			"telemetry_schema": 1,
			"sample_id":        "s" + itoa(seq),
			"collected_at":     "2026-11-24T22:00:00Z",
			"metrics":          metrics,
			"status":           map[string]any{"metrics": status},
		}
		postHeartbeat(t, srv, deviceID, deviceUUID, sealClientHeartbeat(t, srv, deviceID, devicePriv, seq, payload, responsePub))
	}
	history := func() []string {
		t.Helper()
		rows, err := srv.db.GetPeerMetrics(deviceID, 10)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(rows))
		for i, row := range rows {
			raw, _ := json.Marshal(row)
			out[i] = string(raw)
		}
		return out
	}

	send(1, map[string]any{
		"cpu_percent": 37.5, "cpu_window_seconds": 60, "memory_percent": 41.25, "disk_percent": 22.5, "gpu_percent": nil,
		"network": map[string]any{"received_bytes": 66000, "transmitted_bytes": 1800, "received_per_second": 1100, "transmitted_per_second": 30, "window_seconds": 60},
	}, "ok")
	rows := history()
	if len(rows) != 1 {
		t.Fatalf("after one heartbeat with metrics the history has %d samples, want 1", len(rows))
	}
	if want := `"cpu_usage":37.5,"memory_usage":41.25,"disk_usage":22.5,"net_rx_bps":1100,"net_tx_bps":30`; !strings.Contains(rows[0], want) {
		t.Errorf("sample %s, want it to hold %s", rows[0], want)
	}

	// rc.7: one refresh, no window, a network count of the same moment
	send(2, map[string]any{
		"cpu_percent": 100, "memory_percent": 30, "disk_percent": 22.5, "gpu_percent": nil,
		"network": map[string]any{"received_bytes": 0, "transmitted_bytes": 0},
	}, "ok")
	rows = history()
	if len(rows) != 2 {
		t.Fatalf("after two heartbeats the history has %d samples, want 2", len(rows))
	}
	if want := `"cpu_usage":null,"memory_usage":30,"disk_usage":22.5,"created_at"`; !strings.Contains(rows[0], want) {
		t.Errorf("an old client's sample %s, want it to hold %s", rows[0], want)
	}

	// Collected nothing, or not allowed to: no sample
	send(3, map[string]any{"cpu_percent": nil, "memory_percent": nil, "disk_percent": nil}, "ok")
	send(4, map[string]any{"cpu_percent": 12, "cpu_window_seconds": 60, "memory_percent": 12}, "permission_denied")
	if rows = history(); len(rows) != 2 {
		t.Errorf("metrics without values or not ok added samples: %v", rows)
	}
}
