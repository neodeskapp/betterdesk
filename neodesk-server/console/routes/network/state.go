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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if saved, ok := h.savedMonitor(ctx); ok && saved.Running {
		h.monitor.Start(saved.IntervalMs)
	}
}

// savedMonitor reads the monitor's saved state, with an interval out of
// range as 0 (the default). ok is false when there is none.
func (h *handlers) savedMonitor(ctx context.Context) (saved monitorSaved, ok bool) {
	if h.db == nil {
		return saved, false
	}
	var value sql.NullString
	err := h.db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", monitorKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return saved, false
	}
	if err != nil {
		h.logError("read the network monitor's state:", err)
		return saved, false
	}
	if json.Unmarshal([]byte(value.String), &saved) != nil {
		return saved, false
	}
	if saved.IntervalMs < minPollMs || saved.IntervalMs > maxMs {
		saved.IntervalMs = 0
	}
	return saved, true
}

// followEvery is how often the instance that polls for the installation
// reads the monitor's saved state.
const followEvery = 30 * time.Second

// followMonitor keeps the monitor of the instance that holds the lease in
// step with the saved state, until ctx ends. With several instances, a
// start or stop reaches one of them, which saves it; the lease holder,
// which does the polls, follows within followEvery.
func (h *handlers) followMonitor(ctx context.Context, held func() bool) {
	tick := time.NewTicker(followEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if held() {
			h.syncMonitor(ctx)
		}
	}
}

// syncMonitor starts, stops or restarts the monitor as the saved state
// says.
func (h *handlers) syncMonitor(ctx context.Context) {
	saved, ok := h.savedMonitor(ctx)
	if !ok {
		return
	}
	running := h.monitor.Running()
	switch {
	case saved.Running && running && h.monitor.PollInterval() != saved.IntervalMs.Or(netmonitor.DefaultPollIntervalMs):
		h.monitor.Stop()
		h.monitor.Start(saved.IntervalMs)
	case saved.Running && !running:
		h.monitor.Start(saved.IntervalMs)
	case !saved.Running && running:
		h.monitor.Stop()
	}
}
