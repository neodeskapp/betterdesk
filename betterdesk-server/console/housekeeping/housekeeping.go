// Package housekeeping runs the console's periodic clean-up of its own
// tables: once an hour the old heartbeat metrics, client audit rows and
// audit log rows are deleted, and every few seconds devices whose last
// contact is too old are set offline in the console's copy of the device
// list.
//
// Both jobs swallow every error, as the old console did: a failed run
// leaves no log line and the next run comes at its time.
package housekeeping

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// IntegrationInterval is how often the old metrics, client audit rows and
// audit log rows are deleted.
const IntegrationInterval = time.Hour

// timeoutMax is the longest delay a JavaScript timer takes, in
// milliseconds (2^31 - 1). A longer one, or one below a millisecond,
// becomes one millisecond.
const timeoutMax = 1<<31 - 1

// integrationStatements delete heartbeat metrics older than 7 days, client
// audit rows older than 90 days and audit log rows older than 90 days, in
// this order.
var integrationStatements = map[store.Dialect][]string{
	store.SQLite: {
		`DELETE FROM peer_metrics WHERE created_at < datetime('now', '-7 days')`,
		`DELETE FROM audit_connections WHERE created_at < datetime('now', '-90 days')`,
		`DELETE FROM audit_files WHERE created_at < datetime('now', '-90 days')`,
		`DELETE FROM audit_alarms WHERE created_at < datetime('now', '-90 days')`,
		`DELETE FROM audit_log WHERE created_at < datetime('now', '-90 days')`,
	},
	store.Postgres: {
		`DELETE FROM peer_metrics WHERE created_at < NOW() - INTERVAL '7 days'`,
		`DELETE FROM audit_connections WHERE created_at < NOW() - INTERVAL '90 days'`,
		`DELETE FROM audit_files WHERE created_at < NOW() - INTERVAL '90 days'`,
		`DELETE FROM audit_alarms WHERE created_at < NOW() - INTERVAL '90 days'`,
		`DELETE FROM audit_log WHERE created_at < NOW() - INTERVAL '90 days'`,
	},
}

// Integration deletes heartbeat metrics older than 7 days, the client
// audit rows (connections, file transfers, alarms) older than 90 days and
// the rows of the console's audit log older than 90 days. The first
// statement that fails ends the run with its error; the tables after it
// are left for the next run.
func Integration(ctx context.Context, db *store.DB) error {
	for _, query := range integrationStatements[db.Dialect()] {
		if _, err := db.Exec(ctx, query); err != nil {
			return fmt.Errorf("integration housekeeping: %w", err)
		}
	}
	return nil
}

// Options are the settings of the jobs.
type Options struct {
	// DB is the console's database.
	DB *store.DB
	// StalePeers sets the console's copies of devices offline whose last
	// contact is more than the given number of seconds ago
	// (devicegroups.Service.CleanupStaleOnlinePeers).
	StalePeers func(ctx context.Context, thresholdSeconds float64) (int64, error)
	// StatusSyncInterval is STATUS_SYNC_INTERVAL as written: seconds
	// between two runs of StalePeers, 15 when it is no number or zero.
	StatusSyncInterval string
	// HeartbeatStaleThreshold is HEARTBEAT_STALE_THRESHOLD as written:
	// the seconds after which a device counts as gone, 90 when it is no
	// number or zero.
	HeartbeatStaleThreshold string
	// NewTicker makes the clock of the jobs; time.NewTicker when nil.
	NewTicker func(d time.Duration) (<-chan time.Time, func())
}

// Jobs are the two periodic clean-ups.
type Jobs struct {
	opts      Options
	interval  time.Duration
	threshold float64
}

// New returns the jobs, with the intervals read the way the old console
// read them (parseInt(value, 10) || default).
func New(opts Options) *Jobs {
	if opts.NewTicker == nil {
		opts.NewTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	seconds := parseIntOr(opts.StatusSyncInterval, 15)
	return &Jobs{
		opts:      opts,
		interval:  timerDelay(seconds * 1000),
		threshold: parseIntOr(opts.HeartbeatStaleThreshold, 90),
	}
}

// StatusSyncInterval is the time between two runs of the stale device
// clean-up.
func (j *Jobs) StatusSyncInterval() time.Duration { return j.interval }

// StaleThreshold is the number of seconds after which a device counts as
// gone.
func (j *Jobs) StaleThreshold() float64 { return j.threshold }

// RunIntegration deletes old metrics, client audit rows and audit log rows
// once an hour, first an hour after the start, until ctx ends.
func (j *Jobs) RunIntegration(ctx context.Context) {
	j.every(ctx, IntegrationInterval, func() { _ = Integration(ctx, j.opts.DB) })
}

// RunStatusSync sets stale devices offline every StatusSyncInterval,
// first one interval after the start, until ctx ends.
//
// The old console also asked its server backend to sync the online
// status at each run and once five seconds after the start. With the
// signal server owning the device list that call did nothing and logged
// nothing (betterdeskApi.syncOnlineStatus answered {synced: 0}), so there
// is nothing to run for it here.
func (j *Jobs) RunStatusSync(ctx context.Context) {
	j.every(ctx, j.interval, func() { _, _ = j.opts.StalePeers(ctx, j.threshold) })
}

// every calls fn at each tick of a ticker with the interval until ctx
// ends. A run that is still going when the next tick comes delays it.
func (j *Jobs) every(ctx context.Context, interval time.Duration, fn func()) {
	ticks, stop := j.opts.NewTicker(interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if ctx.Err() != nil {
				return
			}
			fn()
		}
	}
}

// timerDelay is the delay a JavaScript timer waits for ms milliseconds:
// one millisecond for anything below one or above 2^31 - 1.
func timerDelay(ms float64) time.Duration {
	if !(ms >= 1 && ms <= timeoutMax) {
		ms = 1
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// parseIntOr is parseInt(text, 10) || fallback: white space in front is
// skipped, a sign is read, and the decimal digits up to the first other
// character are the number. No digits, or a value of zero, give the
// fallback.
func parseIntOr(text string, fallback float64) float64 {
	s := strings.TrimLeftFunc(text, isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return fallback
	}
	value, _ := strconv.ParseFloat(s[:end], 64)
	if value == 0 || math.IsNaN(value) {
		return fallback
	}
	if negative {
		value = -value
	}
	return value
}

// isJSSpace is JavaScript's white space and line terminators, what
// parseInt skips in front of a number.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
