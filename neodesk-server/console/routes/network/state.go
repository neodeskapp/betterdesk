package network

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/netmonitor"
)

// monitorKey is the row of the settings table that keeps whether the
// monitor runs and its interval, so a restart of the console resumes it.
const monitorKey = "network_monitor"

type monitorSaved struct {
	Running    bool              `json:"running"`
	IntervalMs netmonitor.Number `json:"interval_ms"`
}

// saveMonitor writes the monitor's state into the settings table. A write
// that fails is logged; the monitor runs as it was told all the same.
func (h *handlers) saveMonitor(ctx context.Context) {
	value, err := json.Marshal(monitorSaved{Running: h.monitor.Running(), IntervalMs: h.monitor.PollInterval()})
	if err != nil {
		h.logError("save the network monitor's state:", err)
		return
	}
	query := h.db.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = NOW()`,
	)
	if _, err := h.db.Exec(ctx, query, monitorKey, string(value)); err != nil {
		h.logError("save the network monitor's state:", err)
	}
}

// resumeMonitor starts the monitor when the settings table says it ran
// before the console stopped, with the interval it had.
func (h *handlers) resumeMonitor() {
	if h.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var value sql.NullString
	err := h.db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", monitorKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		h.logError("read the network monitor's state:", err)
		return
	}
	var saved monitorSaved
	if json.Unmarshal([]byte(value.String), &saved) != nil || !saved.Running {
		return
	}
	if saved.IntervalMs < minPollMs || saved.IntervalMs > maxMs {
		saved.IntervalMs = 0
	}
	h.monitor.Start(saved.IntervalMs)
}
