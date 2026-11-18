package housekeeping_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// TestIntegrationAgainstNode replays the Node adapter's hourly clean-up
// on rows of every age and form: the same rows must be left, and when a
// table is missing the run must stop there, leaving the tables after it
// as they were.
func TestIntegrationAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		rec := load(t, db.Dialect())
		if len(rec.Integration) != 2 {
			t.Fatalf("recording has %d integration runs", len(rec.Integration))
		}
		ctx := context.Background()

		full := rec.Integration[0]
		run(t, db, full.Seed)
		if err := housekeeping.Integration(ctx, db); err != nil {
			t.Fatalf("Integration: %v", err)
		}
		const left = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
    UNION ALL SELECT 'audit_files', id FROM audit_files UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`
		if got, want := strings.Join(rows(t, db, left), "\n"), strings.Join(recorded(t, full.Left), "\n"); got != want {
			t.Errorf("rows left after the clean-up:\n got %s\nwant %s", got, want)
		}

		broken := rec.Integration[1]
		run(t, db, broken.Seed)
		if err := housekeeping.Integration(ctx, db); err == nil {
			t.Error("a run on a database without audit_files reports no error")
		}
		const leftWithout = `SELECT 'peer_metrics' AS t, id FROM peer_metrics UNION ALL SELECT 'audit_connections', id FROM audit_connections
        UNION ALL SELECT 'audit_alarms', id FROM audit_alarms ORDER BY t, id`
		if got, want := strings.Join(rows(t, db, leftWithout), "\n"), strings.Join(recorded(t, broken.Left), "\n"); got != want {
			t.Errorf("rows left after the clean-up stopped at the missing table:\n got %s\nwant %s", got, want)
		}
	})
}

// TestStalePeersAgainstNode replays the stale device clean-up with every
// recorded HEARTBEAT_STALE_THRESHOLD: the threshold read from it, the
// number of rows changed, the error, and the online flags afterwards.
func TestStalePeersAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		rec := load(t, db.Dialect())
		groups := devicegroups.New(devicegroups.Options{DB: db})
		for _, c := range rec.Stale {
			jobs := housekeeping.New(housekeeping.Options{HeartbeatStaleThreshold: c.Value})
			want, err := strconv.ParseFloat(c.Threshold, 64)
			if err != nil {
				t.Fatalf("threshold %q: %v", c.Threshold, err)
			}
			if got := jobs.StaleThreshold(); got != want {
				t.Errorf("threshold of %q = %v, want %v", c.Value, got, want)
			}
			run(t, db, c.Seed)
			changes, err := groups.CleanupStaleOnlinePeers(context.Background(), jobs.StaleThreshold())
			switch {
			case c.Error != nil && err == nil:
				t.Errorf("threshold %q: no error, Node failed with %s", c.Value, *c.Error)
			case c.Error == nil && err != nil:
				t.Errorf("threshold %q: %v", c.Value, err)
			case c.Changes != nil && changes != *c.Changes:
				t.Errorf("threshold %q: %d rows changed, Node %d", c.Value, changes, *c.Changes)
			}
			if got, want := strings.Join(rows(t, db, "SELECT id, status_online FROM peer ORDER BY id"), " "), strings.Join(recorded(t, c.Rows), " "); got != want {
				t.Errorf("threshold %q: devices afterwards\n got %s\nwant %s", c.Value, got, want)
			}
		}
	})
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
		jobs := housekeeping.New(housekeeping.Options{StatusSyncInterval: c.Value, HeartbeatStaleThreshold: c.Value})
		if got, want := jobs.StatusSyncInterval(), time.Duration(c.DelayMs*float64(time.Millisecond)); got != want {
			t.Errorf("interval of %q = %v, want %v", c.Value, got, want)
		}
		want, _ := strconv.ParseFloat(c.Threshold, 64)
		if got := jobs.StaleThreshold(); got != want {
			t.Errorf("threshold of %q = %v, want %v", c.Value, got, want)
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
