package housekeeping_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/housekeeping"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is what testdata/nodeoracle/housekeeping.js wrote.
type recording struct {
	Integration []struct {
		Seed  []string          `json:"seed"`
		Error json.RawMessage   `json:"error"`
		Left  []json.RawMessage `json:"left"`
	} `json:"integration"`
	Stale []struct {
		Value     string            `json:"value"`
		Threshold string            `json:"threshold"`
		Seed      []string          `json:"seed"`
		Changes   *int64            `json:"changes"`
		Error     *string           `json:"error"`
		Rows      []json.RawMessage `json:"rows"`
	} `json:"stale"`
	Settings []struct {
		Value     string  `json:"value"`
		DelayMs   float64 `json:"delayMs"`
		Threshold string  `json:"threshold"`
	} `json:"settings"`
}

func load(t *testing.T, dialect store.Dialect) recording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "housekeeping_"+dialect.String()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func run(t *testing.T, db *store.DB, statements []string) {
	t.Helper()
	for _, sql := range statements {
		if _, err := db.Exec(context.Background(), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
}

// rows reads a query as one line per row, the columns in order, the
// values as text: what the recording holds after the same treatment.
func rows(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	res, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer res.Close()
	columns, _ := res.Columns()
	var out []string
	for res.Next() {
		cells := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := res.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(columns))
		for i, cell := range cells {
			if b, ok := cell.([]byte); ok {
				cell = string(b)
			}
			parts[i] = fmt.Sprint(cell)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

func recorded(t *testing.T, raw []json.RawMessage) []string {
	t.Helper()
	out := make([]string, len(raw))
	for i, row := range raw {
		dec := json.NewDecoder(bytes.NewReader(row))
		dec.UseNumber()
		var fields []any
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			t.Fatal(err)
		}
		// Keep the column order of the object as written
		keys := orderedKeys(t, row)
		for _, k := range keys {
			fields = append(fields, obj[k])
		}
		parts := make([]string, len(fields))
		for j, f := range fields {
			parts[j] = fmt.Sprint(f)
		}
		out[i] = strings.Join(parts, "|")
	}
	return out
}

func orderedKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	var keys []string
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// The seeds of the recording place rows relative to the time of the run.
// On SQLite Node compared times as text, so where a time written as a
// date alone ("2026-11-12") or with a T ("2026-11-12T23:00:00.000Z") fell
// against a cut-off ("2026-11-12 13:00:00") depended on the hour of the
// day Node ran in, and a number always sorted before it. The console
// compares such rows as times (BUGS JB11); the tests work out their
// outcome for the moment they run in (timeRows).

// readableTime is a time text SQLite's datetime() reads: a date, with an
// optional time after a space or a T.
var readableTime = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2})(?:[ T]([0-9]{2}:[0-9]{2}:[0-9]{2})(?:\.[0-9]+)?Z?)?$`)

// unixSeconds are digits alone: a number, which the text column keeps as
// text.
var unixSeconds = regexp.MustCompile(`^[0-9]+$`)

// timeRows lists the rows of a query (table, id, time; or id, online
// flag, time) whose time the clean-ups read as a time on SQLite, with the
// time as datetime() writes it: text datetime() reads, and digits alone
// as Unix seconds (BUGS JB11). Their outcome is the time comparison with
// the cut-off; that of the other rows is Node's text comparison, which
// the recording holds.
func timeRows(t *testing.T, db *store.DB, query string) []textRow {
	t.Helper()
	if db.Dialect() != store.SQLite {
		return nil
	}
	var out []textRow
	for _, line := range rows(t, db, query) {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		switch m := readableTime.FindStringSubmatch(parts[2]); {
		case unixSeconds.MatchString(parts[2]):
			n, _ := strconv.ParseInt(parts[2], 10, 64)
			out = append(out, textRow{parts[0], parts[1], time.Unix(n, 0).UTC().Format("2006-01-02 15:04:05")})
		case m != nil:
			clock := m[2]
			if clock == "" {
				clock = "00:00:00"
			}
			out = append(out, textRow{parts[0], parts[1], m[1] + " " + clock})
		}
	}
	return out
}

// retention is what the hourly clean-up keeps of each table, in days, in
// the order it deletes them.
var retention = []struct {
	table string
	days  int
}{{"peer_metrics", 7}, {"audit_connections", 90}, {"audit_files", 90}, {"audit_alarms", 90}}

// textRow is a row whose time reads as a time (timeRows): the first two
// columns of the query that found it (table and id, or id and online
// flag) and the time.
type textRow struct {
	first, second, text string
}

// sqliteNow is the database's clock on SQLite, the zero time elsewhere.
func sqliteNow(t *testing.T, db *store.DB) time.Time {
	t.Helper()
	if db.Dialect() != store.SQLite {
		return time.Time{}
	}
	var seconds int64
	if err := db.QueryRow(context.Background(), `SELECT CAST(strftime('%s', 'now') AS INTEGER)`).Scan(&seconds); err != nil {
		t.Fatal(err)
	}
	return time.Unix(seconds, 0).UTC()
}

// leftAtThisHour is the recorded list of rows left ("table|id", ordered by
// table and id), with each row of bound in a table the clean-up reached
// left or gone as the comparison of its time with that table's cut-off
// decides now.
func leftAtThisHour(want []string, bound []textRow, cutoffs map[string]string) []string {
	left := map[string]bool{}
	for _, key := range want {
		left[key] = true
	}
	for _, r := range bound {
		cutoff, reached := cutoffs[r.first]
		if !reached {
			continue
		}
		key := r.first + "|" + r.second
		if r.text < cutoff {
			delete(left, key)
		} else {
			left[key] = true
		}
	}
	out := make([]string, 0, len(left))
	for key := range left {
		out = append(out, key)
	}
	sort.Slice(out, func(a, b int) bool {
		ta, ia, _ := strings.Cut(out[a], "|")
		tb, ib, _ := strings.Cut(out[b], "|")
		na, _ := strconv.Atoi(ia)
		nb, _ := strconv.Atoi(ib)
		return ta < tb || ta == tb && na < nb
	})
	return out
}

// cutoffsUntil are the cut-offs of the tables the clean-up deletes from
// before it meets the missing table ("" for none), as datetime('now',
// '-<days> days') writes them.
func cutoffsUntil(now time.Time, missing string) map[string]string {
	out := map[string]string{}
	for _, r := range retention {
		if r.table == missing {
			break
		}
		out[r.table] = now.AddDate(0, 0, -r.days).Format("2006-01-02 15:04:05")
	}
	return out
}

// TestIntegrationAgainstNode replays the Node adapter's hourly clean-up
// on rows of every age and form: the same rows must be left, and when a
// table is missing the run must stop there, leaving the tables after it
// as they were. On SQLite the rows whose time reads as a time go by that
// time, where Node compared text (timeRows).
func TestIntegrationAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		rec := load(t, db.Dialect())
		if len(rec.Integration) != 2 {
			t.Fatalf("recording has %d integration runs", len(rec.Integration))
		}
		ctx := context.Background()
		const times = `SELECT 'peer_metrics', id, created_at FROM peer_metrics UNION ALL SELECT 'audit_connections', id, created_at FROM audit_connections
    UNION ALL SELECT 'audit_files', id, created_at FROM audit_files UNION ALL SELECT 'audit_alarms', id, created_at FROM audit_alarms`

		full := rec.Integration[0]
		run(t, db, full.Seed)
		bound := timeRows(t, db, times)
		now := sqliteNow(t, db)
		if err := housekeeping.Integration(ctx, db); err != nil {
			t.Fatalf("Integration: %v", err)
		}
		const left = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
    UNION ALL SELECT 'audit_files', id FROM audit_files UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`
		want := leftAtThisHour(recorded(t, full.Left), bound, cutoffsUntil(now, ""))
		if got := strings.Join(rows(t, db, left), "\n"); got != strings.Join(want, "\n") {
			t.Errorf("rows left after the clean-up at %s:\n got %s\nwant %s", now, got, strings.Join(want, "\n"))
		}

		broken := rec.Integration[1]
		run(t, db, broken.Seed)
		const timesWithout = `SELECT 'peer_metrics', id, created_at FROM peer_metrics UNION ALL SELECT 'audit_connections', id, created_at FROM audit_connections
    UNION ALL SELECT 'audit_alarms', id, created_at FROM audit_alarms`
		bound = timeRows(t, db, timesWithout)
		now = sqliteNow(t, db)
		if err := housekeeping.Integration(ctx, db); err == nil {
			t.Error("a run on a database without audit_files reports no error")
		}
		const leftWithout = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
        UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`
		want = leftAtThisHour(recorded(t, broken.Left), bound, cutoffsUntil(now, "audit_files"))
		if got := strings.Join(rows(t, db, leftWithout), "\n"); got != strings.Join(want, "\n") {
			t.Errorf("rows left after the clean-up stopped at the missing table, at %s:\n got %s\nwant %s", now, got, strings.Join(want, "\n"))
		}
	})
}

// TestIntegrationPrunesTheAuditLog: the hourly run deletes audit log rows
// older than 90 days, also those of the cut-off's own day that are older,
// and keeps the younger ones of that day.
func TestIntegrationPrunesTheAuditLog(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		ago := func(hours int) string {
			if db.Dialect() == store.SQLite {
				return fmt.Sprintf("datetime('now', '-%d hours')", hours)
			}
			return fmt.Sprintf("NOW() - INTERVAL '%d hours'", hours)
		}
		for _, row := range []struct {
			action string
			hours  int
		}{{"young", 89 * 24}, {"cut-off day, younger", 90*24 - 1}, {"cut-off day, older", 90*24 + 1}, {"old", 91 * 24}, {"older", 400 * 24}} {
			if _, err := db.Exec(ctx, "INSERT INTO audit_log (action, created_at) VALUES (?, "+ago(row.hours)+")", row.action); err != nil {
				t.Fatal(err)
			}
		}
		if err := housekeeping.Integration(ctx, db); err != nil {
			t.Fatalf("Integration: %v", err)
		}
		if got := strings.Join(rows(t, db, "SELECT action FROM audit_log ORDER BY id"), ", "); got != "young, cut-off day, younger" {
			t.Errorf("audit log after the clean-up: %s", got)
		}
	})
}

// TestIntegrationExpiresPendingRegistrations: the hourly run deletes
// pending registrations older than 30 days by the database's clock and
// keeps the younger ones and every approved or rejected row, however old
// (BUGS.md RV8).
func TestIntegrationExpiresPendingRegistrations(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		ago := func(hours int) string {
			if db.Dialect() == store.SQLite {
				return fmt.Sprintf("datetime('now', '-%d hours')", hours)
			}
			return fmt.Sprintf("NOW() - INTERVAL '%d hours'", hours)
		}
		for _, row := range []struct {
			device, status string
			hours          int
		}{
			{"young", "pending", 29 * 24}, {"just-young", "pending", 30*24 - 1}, {"just-old", "pending", 30*24 + 1},
			{"old", "pending", 400 * 24}, {"approved-old", "approved", 400 * 24}, {"rejected-old", "rejected", 400 * 24},
		} {
			if _, err := db.Exec(ctx, "INSERT INTO pending_registrations (device_id, status, created_at) VALUES (?, ?, "+ago(row.hours)+")", row.device, row.status); err != nil {
				t.Fatal(err)
			}
		}
		if err := housekeeping.Integration(ctx, db); err != nil {
			t.Fatalf("Integration: %v", err)
		}
		if got := strings.Join(rows(t, db, "SELECT device_id FROM pending_registrations ORDER BY id"), ", "); got != "young, just-young, approved-old, rejected-old" {
			t.Errorf("registrations after the clean-up: %s", got)
		}
	})
}

// TestHourBoundRowsFollowTheTextComparison pins the rule the replays above
// use for the rows whose outcome depends on the hour.
func TestHourBoundRowsFollowTheTextComparison(t *testing.T) {
	bound := []textRow{
		{"peer_metrics", "1", "2026-11-12"}, {"peer_metrics", "2", "2026-11-13"},
		{"peer_metrics", "3", "2026-11-12T23:59:59.000Z"}, {"peer_metrics", "4", "2026-11-11T23:59:59.000Z"},
		{"audit_files", "5", "2026-01-01"},
	}
	got := leftAtThisHour([]string{"peer_metrics|1", "peer_metrics|9"}, bound, map[string]string{"peer_metrics": "2026-11-12 13:00:00"})
	if want := "peer_metrics|2 peer_metrics|3 peer_metrics|9"; strings.Join(got, " ") != want {
		t.Errorf("left %q, want %q", got, want)
	}
	if got := cutoffsUntil(time.Date(2026, 11, 20, 0, 30, 0, 0, time.UTC), "audit_files"); fmt.Sprint(got) != "map[audit_connections:2026-08-22 00:30:00 peer_metrics:2026-11-13 00:30:00]" {
		t.Errorf("cut-offs %v", got)
	}
}

// TestStalePeersAgainstNode replays the stale device clean-up with every
// recorded HEARTBEAT_STALE_THRESHOLD: the threshold read from it, the
// number of rows changed, the error, and the online flags afterwards.
func TestStalePeersAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		rec := load(t, db.Dialect())
		groups := devicegroups.New(devicegroups.Options{DB: db})
		ninety := -1
		for i, c := range rec.Stale {
			if c.Value == "90" {
				ninety = i
			}
		}
		if ninety < 0 {
			t.Fatal("the recording has no sweep with the threshold 90")
		}
		for _, c := range rec.Stale {
			jobs := housekeeping.New(housekeeping.Options{HeartbeatStaleThreshold: c.Value, Logf: func(string, ...any) {}})
			want, err := strconv.ParseFloat(c.Threshold, 64)
			if err != nil {
				t.Fatalf("threshold %q: %v", c.Threshold, err)
			}
			// A threshold outside 1 second to 30 days is 90, and the sweep
			// goes as Node's with 90 went (BUGS JB9)
			outcome := c
			if !(want >= 1 && want <= 30*24*3600) {
				want, outcome = 90, rec.Stale[ninety]
			}
			if got := jobs.StaleThreshold(); got != want {
				t.Errorf("threshold of %q = %v, want %v", c.Value, got, want)
			}
			run(t, db, c.Seed)
			wantRows, wantChanges := staleAtThisHour(t, db, recorded(t, outcome.Rows), outcome.Changes, jobs.StaleThreshold())
			changes, err := groups.CleanupStaleOnlinePeers(context.Background(), jobs.StaleThreshold())
			switch {
			case outcome.Error != nil && err == nil:
				t.Errorf("threshold %q: no error, Node failed with %s", c.Value, *outcome.Error)
			case outcome.Error == nil && err != nil:
				t.Errorf("threshold %q: %v", c.Value, err)
			case wantChanges != nil && changes != *wantChanges:
				t.Errorf("threshold %q: %d rows changed, want %d (Node %d)", c.Value, changes, *wantChanges, *outcome.Changes)
			}
			if got, want := strings.Join(rows(t, db, "SELECT id, status_online FROM peer ORDER BY id"), " "), strings.Join(wantRows, " "); got != want {
				t.Errorf("threshold %q: devices afterwards\n got %s\nwant %s", c.Value, got, want)
			}
		}
	})
}

// staleAtThisHour is the recorded outcome of a stale device sweep ("id|
// status_online" per device, the number of rows changed), with each online
// device whose last contact reads as a time on SQLite (timeRows) set
// offline or left as the time comparison with the cut-off,
// datetime('now', '-<threshold> seconds'), decides now, where Node
// compared text (BUGS JB11). Call it after the seed and before the sweep.
func staleAtThisHour(t *testing.T, db *store.DB, want []string, changes *int64, threshold float64) ([]string, *int64) {
	t.Helper()
	bound := timeRows(t, db, "SELECT id, status_online, last_online FROM peer")
	if len(bound) == 0 {
		return want, changes
	}
	var cutoff sql.NullString
	if err := db.QueryRow(context.Background(), `SELECT datetime('now', '-' || ? || ' seconds')`, threshold).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	out := append([]string(nil), want...)
	var n int64
	if changes != nil {
		n = *changes
	}
	for _, r := range bound {
		id := r.first
		if r.second != "1" {
			continue
		}
		goes := cutoff.Valid && r.text < cutoff.String
		for i, line := range out {
			if line == id+"|0" && !goes {
				out[i], n = id+"|1", n-1
			} else if line == id+"|1" && goes {
				out[i], n = id+"|0", n+1
			}
		}
	}
	if changes == nil {
		return out, nil
	}
	return out, &n
}

// TestSettingsAgainstNode reads STATUS_SYNC_INTERVAL the way server.js
// did, with the delay Node's timer used (one millisecond for what does
// not fit a timer).
func TestSettingsAgainstNode(t *testing.T) {
	rec := load(t, store.SQLite)
	if len(rec.Settings) < 20 {
		t.Fatalf("recording has %d settings", len(rec.Settings))
	}
	for _, c := range rec.Settings {
		jobs := housekeeping.New(housekeeping.Options{StatusSyncInterval: c.Value, HeartbeatStaleThreshold: c.Value, Logf: func(string, ...any) {}})
		// Outside 1 second to a day the interval is 15 seconds, where Node's
		// timer ran every millisecond or once in up to 24 days (BUGS JB10)
		interval := time.Duration(c.DelayMs * float64(time.Millisecond))
		if !(interval >= time.Second && interval <= 24*time.Hour) {
			interval = 15 * time.Second
		}
		if got := jobs.StatusSyncInterval(); got != interval {
			t.Errorf("interval of %q = %v, want %v", c.Value, got, interval)
		}
		want, _ := strconv.ParseFloat(c.Threshold, 64)
		// Outside 1 second to 30 days the threshold is 90 (BUGS JB9)
		if !(want >= 1 && want <= 30*24*3600) {
			want = 90
		}
		if got := jobs.StaleThreshold(); got != want {
			t.Errorf("threshold of %q = %v, want %v", c.Value, got, want)
		}
	}
}

// TestStatusSyncIntervalOutOfRange: a STATUS_SYNC_INTERVAL outside 1 to
// 86400 seconds is 15 seconds, with a warning that names the value.
func TestStatusSyncIntervalOutOfRange(t *testing.T) {
	for value, want := range map[string]time.Duration{
		"-5": 15 * time.Second, "86401": 15 * time.Second, "2147484": 15 * time.Second, "99999999999": 15 * time.Second,
		"86400": 24 * time.Hour, "1": time.Second, "30": 30 * time.Second, "": 15 * time.Second, "abc": 15 * time.Second,
	} {
		var logged []string
		jobs := housekeeping.New(housekeeping.Options{StatusSyncInterval: value, Logf: func(format string, args ...any) {
			logged = append(logged, fmt.Sprintf(format, args...))
		}})
		if got := jobs.StatusSyncInterval(); got != want {
			t.Errorf("STATUS_SYNC_INTERVAL=%q: %v, want %v", value, got, want)
		}
		warned := len(logged) == 1 && strings.Contains(logged[0], "STATUS_SYNC_INTERVAL") && strings.Contains(logged[0], strconv.Quote(value))
		if outOfRange := value == "-5" || value == "86401" || value == "2147484" || value == "99999999999"; warned != outOfRange {
			t.Errorf("STATUS_SYNC_INTERVAL=%q: logged %q", value, logged)
		}
	}
}

// TestStaleThresholdOutOfRange: a HEARTBEAT_STALE_THRESHOLD outside 1
// second to 30 days is 90, with a warning that names the value.
func TestStaleThresholdOutOfRange(t *testing.T) {
	for value, want := range map[string]float64{
		"-5": 90, "99999999999": 90, "2592001": 90, "1e400": 1, "1": 1, "2592000": 2592000, "45": 45, "": 90, "abc": 90,
	} {
		var logged []string
		jobs := housekeeping.New(housekeeping.Options{HeartbeatStaleThreshold: value, Logf: func(format string, args ...any) {
			logged = append(logged, fmt.Sprintf(format, args...))
		}})
		if got := jobs.StaleThreshold(); got != want {
			t.Errorf("HEARTBEAT_STALE_THRESHOLD=%q: %v, want %v", value, got, want)
		}
		warned := len(logged) == 1 && strings.Contains(logged[0], "HEARTBEAT_STALE_THRESHOLD") && strings.Contains(logged[0], strconv.Quote(value))
		if outOfRange := value == "-5" || value == "99999999999" || value == "2592001"; warned != outOfRange {
			t.Errorf("HEARTBEAT_STALE_THRESHOLD=%q: logged %q", value, logged)
		}
	}
}

// fakeTicker hands out tick channels the test drives, and records the
// intervals asked for and whether each ticker was stopped.
type fakeTicker struct {
	mu        sync.Mutex
	intervals []time.Duration
	ticks     []chan time.Time
	stopped   int
	created   chan struct{}
}

func newFakeTicker() *fakeTicker { return &fakeTicker{created: make(chan struct{}, 16)} }

func (f *fakeTicker) New(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time)
	f.intervals = append(f.intervals, d)
	f.ticks = append(f.ticks, ch)
	f.created <- struct{}{}
	return ch, func() {
		f.mu.Lock()
		f.stopped++
		f.mu.Unlock()
	}
}

// tick sends a tick to the i-th ticker made, once it exists, and returns
// when the job has taken it.
func (f *fakeTicker) tick(i int) {
	for {
		f.mu.Lock()
		if len(f.ticks) > i {
			ch := f.ticks[i]
			f.mu.Unlock()
			ch <- time.Now()
			return
		}
		f.mu.Unlock()
		<-f.created
	}
}

// TestJobsRunAtTheirTicksAndEndWithTheContext drives both jobs with a
// test clock: nothing runs before the first tick, each tick runs the
// work once with the configured threshold, a failing run does not end
// the job, and both return when the context ends, with their tickers
// stopped.
func TestJobsRunAtTheirTicksAndEndWithTheContext(t *testing.T) {
	db := storetest.SQLite(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock := newFakeTicker()
	calls := make(chan float64, 10)
	jobs := housekeeping.New(housekeeping.Options{
		DB: db,
		StalePeers: func(ctx context.Context, threshold float64) (int64, error) {
			calls <- threshold
			return 0, fmt.Errorf("database is gone")
		},
		StatusSyncInterval:      "20",
		HeartbeatStaleThreshold: "45",
		NewTicker:               clock.New,
	})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); jobs.RunStatusSync(ctx) }()
	select {
	case got := <-calls:
		t.Fatalf("stale clean-up ran before the first tick (%v)", got)
	default:
	}
	clock.tick(0)
	go func() { defer wg.Done(); jobs.RunIntegration(ctx) }()

	if got := <-calls; got != 45 {
		t.Errorf("stale clean-up ran with threshold %v, want 45", got)
	}
	clock.tick(0)
	<-calls
	if _, err := db.Exec(context.Background(), `INSERT INTO peer_metrics (peer_id, created_at) VALUES ('old', datetime('now', '-8 days'))`); err != nil {
		t.Fatal(err)
	}
	clock.tick(1)
	clock.tick(1) // the second tick is taken only after the first run ended
	var n int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM peer_metrics`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d old metrics left after the hourly run", n)
	}

	cancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the jobs did not end with their context")
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if got := fmt.Sprint(clock.intervals); got != "[20s 1h0m0s]" {
		t.Errorf("intervals = %s, want [20s 1h0m0s]", got)
	}
	if clock.stopped != 2 {
		t.Errorf("%d tickers stopped, want 2", clock.stopped)
	}
	select {
	case got := <-calls:
		t.Errorf("an extra stale clean-up ran (%v)", got)
	default:
	}
}
