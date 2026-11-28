// Package housekeeping runs the console's periodic clean-up of its own
// tables: once an hour the old heartbeat metrics, client audit rows, audit
// log rows, pending registrations, alerts, remote commands, app usage,
// data loss prevention events and network checks are deleted, and every
// few seconds devices whose last contact is too old are set offline in the
// console's copy of the device list.
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

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// IntegrationInterval is how often the hourly clean-up (Integration)
// runs.
const IntegrationInterval = time.Hour

// PendingRegistrationDays is how many days a registration request waits
// for an approver before the hourly run deletes it. Approved and rejected
// rows stay. A device whose request expired is listed again when it asks
// again.
const PendingRegistrationDays = 30

// RetentionDays is how many days the hourly run keeps the rows of the
// audit log, the client audit tables, the alert history, the remote
// commands, the app usage sessions and summaries, the data loss
// prevention events and the network checks.
const RetentionDays = 90

// sqliteOlder is the SQLite condition that created_at is more than days
// old, compared as a time: a time in any form datetime() reads (with a
// space or a T, a date alone), digits alone as Unix seconds (the column
// keeps a number as text). Text that datetime() cannot read is compared
// as text, as the column's own format is.
func sqliteOlder(days int) string {
	return "COALESCE(CASE WHEN created_at NOT GLOB '*[^0-9]*' THEN datetime(created_at, 'unixepoch') ELSE datetime(created_at) END, created_at)" +
		" < datetime('now', '-" + strconv.Itoa(days) + " days')"
}

// expiring are the tables the hourly run keeps RetentionDays of, each with
// the column that holds the time the row was written or last arrived. The
// database fills these columns with its own clock (datetime('now') on
// SQLite), so on SQLite they compare as text, by the index on the column
// where there is one, as the audit log does.
var expiring = []struct{ table, column string }{
	{"alert_history", "created_at"},
	{"remote_commands", "created_at"},
	{"activity_sessions", "created_at"},
	{"activity_summaries", "received_at"},
	{"dlp_events", "created_at"},
	{"network_checks", "checked_at"},
}

// integrationStatements delete heartbeat metrics older than 7 days, client
// audit rows and audit log rows older than RetentionDays, pending
// registrations older than PendingRegistrationDays and then the rows of
// the expiring tables older than RetentionDays, in this order. The
// console writes the audit log only in the column's own format, so its
// rows compare as text, by the index on created_at.
var integrationStatements = func() map[store.Dialect][]string {
	days := strconv.Itoa(RetentionDays)
	sqliteCutoff := "datetime('now', '-" + days + " days')"
	postgresCutoff := "NOW() - INTERVAL '" + days + " days'"
	out := map[store.Dialect][]string{
		store.SQLite: {
			`DELETE FROM peer_metrics WHERE ` + sqliteOlder(7),
			`DELETE FROM audit_connections WHERE ` + sqliteOlder(RetentionDays),
			`DELETE FROM audit_files WHERE ` + sqliteOlder(RetentionDays),
			`DELETE FROM audit_alarms WHERE ` + sqliteOlder(RetentionDays),
			`DELETE FROM audit_log WHERE created_at < ` + sqliteCutoff,
			`DELETE FROM pending_registrations WHERE status = 'pending' AND ` + sqliteOlder(PendingRegistrationDays),
		},
		store.Postgres: {
			`DELETE FROM peer_metrics WHERE created_at < NOW() - INTERVAL '7 days'`,
			`DELETE FROM audit_connections WHERE created_at < ` + postgresCutoff,
			`DELETE FROM audit_files WHERE created_at < ` + postgresCutoff,
			`DELETE FROM audit_alarms WHERE created_at < ` + postgresCutoff,
			`DELETE FROM audit_log WHERE created_at < ` + postgresCutoff,
			`DELETE FROM pending_registrations WHERE status = 'pending' AND created_at < NOW() - INTERVAL '` + strconv.Itoa(PendingRegistrationDays) + ` days'`,
		},
	}
	for _, e := range expiring {
		out[store.SQLite] = append(out[store.SQLite], `DELETE FROM `+e.table+` WHERE `+e.column+` < `+sqliteCutoff)
		out[store.Postgres] = append(out[store.Postgres], `DELETE FROM `+e.table+` WHERE `+e.column+` < `+postgresCutoff)
	}
	return out
}()

// Integration deletes heartbeat metrics older than 7 days, the client
// audit rows (connections, file transfers, alarms) and the rows of the
// console's audit log older than RetentionDays, the pending registrations
// older than PendingRegistrationDays, and the alert history, remote
// commands, app usage sessions and summaries, data loss prevention events
// and network checks older than RetentionDays, by the database's clock.
// The first statement that fails ends the run with its error; the tables
// after it are left for the next run.
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
	// between two runs of StalePeers, 15 when it is no number, zero, or
	// outside 1 to 86400.
	StatusSyncInterval string
	// HeartbeatStaleThreshold is HEARTBEAT_STALE_THRESHOLD as written:
	// the seconds after which a device counts as gone, 90 when it is no
	// number, zero, or outside 1 second to 30 days.
	HeartbeatStaleThreshold string
	// NewTicker makes the clock of the jobs; time.NewTicker when nil.
	NewTicker func(d time.Duration) (<-chan time.Time, func())
	// Logf gets the warnings about settings out of range; nil writes to
	// the console's log.
	Logf func(format string, args ...any)
	// IntegrationLease and StatusSyncLease report whether this instance
	// runs RunIntegration and RunStatusSync for the installation; a tick
	// while one is false runs nothing. Nil always runs.
	IntegrationLease, StatusSyncLease func() bool
}

// The longest stale threshold the sweep takes: a larger one (or one below
// a second) sets every device offline or none, or fails on PostgreSQL.
const maxStaleThreshold = 30 * 24 * 3600

// The longest time between two stale device sweeps.
const maxStatusSyncInterval = 24 * 3600

// Jobs are the two periodic clean-ups.
type Jobs struct {
	opts      Options
	interval  time.Duration
	threshold float64
}

// New returns the jobs, with the intervals read the way the old console
// read them (parseInt(value, 10) || default), and a value out of range
// replaced by the default with a warning.
func New(opts Options) *Jobs {
	if opts.NewTicker == nil {
		opts.NewTicker = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	if opts.Logf == nil {
		opts.Logf = func(format string, args ...any) { logging.Logf("console", format, args...) }
	}
	seconds := parseIntOr(opts.StatusSyncInterval, 15)
	if !(seconds >= 1 && seconds <= maxStatusSyncInterval) {
		opts.Logf("WARN: STATUS_SYNC_INTERVAL=%q is outside 1 to %d seconds; using 15", opts.StatusSyncInterval, maxStatusSyncInterval)
		seconds = 15
	}
	threshold := parseIntOr(opts.HeartbeatStaleThreshold, 90)
	if !(threshold >= 1 && threshold <= maxStaleThreshold) {
		opts.Logf("WARN: HEARTBEAT_STALE_THRESHOLD=%q is outside 1 to %d seconds; using 90", opts.HeartbeatStaleThreshold, maxStaleThreshold)
		threshold = 90
	}
	return &Jobs{
		opts:      opts,
		interval:  time.Duration(seconds) * time.Second,
		threshold: threshold,
	}
}

// StatusSyncInterval is the time between two runs of the stale device
// clean-up.
func (j *Jobs) StatusSyncInterval() time.Duration { return j.interval }

// StaleThreshold is the number of seconds after which a device counts as
// gone.
func (j *Jobs) StaleThreshold() float64 { return j.threshold }

// RunIntegration runs Integration once an hour, first an hour after the
// start, until ctx ends.
func (j *Jobs) RunIntegration(ctx context.Context) {
	j.every(ctx, IntegrationInterval, j.opts.IntegrationLease, func() { _ = Integration(ctx, j.opts.DB) })
}

// RunStatusSync sets stale devices offline every StatusSyncInterval,
// first one interval after the start, until ctx ends.
//
// The old console also asked its server backend to sync the online
// status at each run and once five seconds after the start. With the
// signal server owning the device list that call did nothing and logged
// nothing (neodeskApi.syncOnlineStatus answered {synced: 0}), so there
// is nothing to run for it here.
func (j *Jobs) RunStatusSync(ctx context.Context) {
	j.every(ctx, j.interval, j.opts.StatusSyncLease, func() { _, _ = j.opts.StalePeers(ctx, j.threshold) })
}

// every calls fn at each tick of a ticker with the interval until ctx
// ends, but not while lease (nil: never) is false. A run that is still
// going when the next tick comes delays it.
func (j *Jobs) every(ctx context.Context, interval time.Duration, lease func() bool, fn func()) {
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
			if lease != nil && !lease() {
				continue
			}
			fn()
		}
	}
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
