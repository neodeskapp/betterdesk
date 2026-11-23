package netmonitor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// pollWorld is a monitor on a test database whose only target is a ping
// target that is always due. Every poll is one call of the ping command.
type pollWorld struct {
	t     *testing.T
	db    *store.DB
	m     *Monitor
	pings atomic.Int32
	// hold makes the ping command wait until it is closed.
	mu   sync.Mutex
	hold chan struct{}
}

func newPollWorld(t *testing.T) *pollWorld {
	t.Helper()
	w := &pollWorld{t: t, db: storetest.Migrated(t)}
	no := false
	w.m = New(Options{
		Store:    w.db,
		Resolver: &tableResolver{names: map[string][]string{}, errors: map[string]string{}},
		Windows:  &no,
		Ping: func(ctx context.Context, _ string, _ ...string) ([]byte, int, error) {
			w.pings.Add(1)
			w.mu.Lock()
			hold := w.hold
			w.mu.Unlock()
			if hold != nil {
				select {
				case <-hold:
				case <-ctx.Done():
				}
			}
			return []byte("time=1.0 ms"), 0, nil
		},
	})
	if _, err := w.db.Exec(context.Background(), "INSERT INTO network_targets (name, host, check_type, interval_ms) VALUES ('t', '10.1.2.3', 'ping', 1)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.m.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return when its context ended")
		}
	})
	return w
}

// waitFor waits until n polls have started.
func (w *pollWorld) waitFor(n int32) {
	w.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if w.pings.Load() >= n {
			return
		}
	}
	w.t.Fatalf("%d polls after 5 s, want %d", w.pings.Load(), n)
}

// idle waits until the poll that is under way has finished.
func (w *pollWorld) idle() {
	w.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if !w.m.PollInFlight() {
			return
		}
	}
	w.t.Fatal("the poll does not end")
}

// never checks that no further poll starts for a while.
func (w *pollWorld) never(what string, n int32, wait time.Duration) {
	w.t.Helper()
	time.Sleep(wait)
	if got := w.pings.Load(); got != n {
		w.t.Errorf("%s: %d polls, want %d", what, got, n)
	}
}

func TestMonitorPollsOnlyAfterStart(t *testing.T) {
	w := newPollWorld(t)
	w.never("before Start", 0, 150*time.Millisecond)
	if w.m.Running() || w.m.PollInterval() != 60000 {
		t.Errorf("a new monitor: running %v, interval %s", w.m.Running(), w.m.PollInterval())
	}
	w.m.Start(3600000)
	w.waitFor(1)
	if !w.m.Running() || w.m.PollInterval() != 3600000 {
		t.Errorf("after Start: running %v, interval %s", w.m.Running(), w.m.PollInterval())
	}
	w.never("one interval has not passed", 1, 150*time.Millisecond)
}

func TestMonitorPollsAgainAfterEachInterval(t *testing.T) {
	w := newPollWorld(t)
	w.m.Start(40)
	w.waitFor(4)
	w.m.Stop()
	w.idle()
	n := w.pings.Load()
	w.never("after Stop", n, 250*time.Millisecond)
	if w.m.Running() || w.m.PollInterval() != 40 {
		t.Errorf("after Stop: running %v, interval %s", w.m.Running(), w.m.PollInterval())
	}
}

// The wait between two polls starts when a poll has finished.
func TestMonitorWaitsAfterAPollHasFinished(t *testing.T) {
	w := newPollWorld(t)
	w.hold = make(chan struct{})
	w.m.Start(200)
	w.waitFor(1)
	w.never("while the first poll runs", 1, 400*time.Millisecond)
	close(w.hold)
	w.never("right after the first poll", 1, 100*time.Millisecond)
	w.waitFor(2)
}

func TestStartKeepsTheIntervalOfARunningMonitor(t *testing.T) {
	w := newPollWorld(t)
	w.m.Start(3600000)
	w.waitFor(1)
	w.m.Start(5)
	if w.m.PollInterval() != 3600000 {
		t.Errorf("interval %s after a second Start, want 3600000", w.m.PollInterval())
	}
	w.never("a second Start", 1, 150*time.Millisecond)
}

func TestStopAndStartPollAtOnce(t *testing.T) {
	w := newPollWorld(t)
	w.m.Start(3600000)
	w.waitFor(1)
	w.idle()
	w.never("waiting", 1, 50*time.Millisecond)
	w.m.Stop()
	w.m.Start(1800000)
	w.waitFor(2)
	if w.m.PollInterval() != 1800000 {
		t.Errorf("interval %s, want the one of the new Start", w.m.PollInterval())
	}
	w.never("after the poll of the new Start", 2, 150*time.Millisecond)
}

// A stop and a start while a poll runs do not start a second poll: the
// running one ends, and the next comes one interval later.
func TestStopAndStartDuringAPoll(t *testing.T) {
	w := newPollWorld(t)
	hold := make(chan struct{})
	w.mu.Lock()
	w.hold = hold
	w.mu.Unlock()
	w.m.Start(3600000)
	w.waitFor(1)
	w.m.Stop()
	w.m.Start(300)
	w.never("while the poll runs", 1, 100*time.Millisecond)
	w.mu.Lock()
	w.hold = nil
	w.mu.Unlock()
	close(hold)
	w.never("right after the poll", 1, 120*time.Millisecond)
	w.waitFor(2)
}

// A poll that was under way when the monitor was stopped is the last.
func TestStopDuringAPoll(t *testing.T) {
	w := newPollWorld(t)
	hold := make(chan struct{})
	w.mu.Lock()
	w.hold = hold
	w.mu.Unlock()
	w.m.Start(20)
	w.waitFor(1)
	w.m.Stop()
	w.mu.Lock()
	w.hold = nil
	w.mu.Unlock()
	close(hold)
	w.never("after the poll", 1, 200*time.Millisecond)
}

func TestStartDefaults(t *testing.T) {
	for _, interval := range []Number{0, NaN()} {
		m := New(Options{})
		m.Start(interval)
		if m.PollInterval() != DefaultPollIntervalMs || !m.Running() {
			t.Errorf("Start(%s): interval %s, running %v", interval, m.PollInterval(), m.Running())
		}
	}
	m := New(Options{})
	m.Start(-5)
	if m.PollInterval() != -5 {
		t.Errorf("Start(-5): interval %s", m.PollInterval())
	}
}

func TestTimerDelay(t *testing.T) {
	cases := map[Number]time.Duration{
		60000: time.Minute, 1: time.Millisecond, 1.9: time.Millisecond, 2.5: 2 * time.Millisecond,
		0: time.Millisecond, 0.5: time.Millisecond, -5: time.Millisecond,
		2147483647: 2147483647 * time.Millisecond, 2147483648: time.Millisecond, 1e21: time.Millisecond,
		Number(math.Inf(1)): time.Millisecond,
	}
	for ms, want := range cases {
		if got := timerDelay(ms); got != want {
			t.Errorf("timerDelay(%s) = %s, want %s", ms, got, want)
		}
	}
	if got := timerDelay(NaN()); got != time.Millisecond {
		t.Errorf("timerDelay(NaN) = %s", got)
	}
}

// CheckAll checks the enabled targets whose own interval has passed, by
// name, and at most 500 of them.
func TestCheckAll(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		now := time.Now()
		m := New(Options{Store: db, Now: func() time.Time { return now }})
		m.interval = 30000
		ago := func(d time.Duration) string { return store.ISOTime(now.Add(-d)) }
		rows := []struct {
			name     string
			interval int
			last     any
			enabled  bool
		}{
			{"a due", 60000, ago(61 * time.Second), true},
			{"b not yet", 600000, ago(120 * time.Second), true},
			{"c never checked", 300000, nil, true},
			{"d disabled", 60000, nil, false},
			{"e within the allowance", 60000, ago(56 * time.Second), true},
			{"f just before the allowance", 60000, ago(54 * time.Second), true},
		}
		for _, r := range rows {
			if _, err := db.Exec(ctx, "INSERT INTO network_targets (name, host, check_type, interval_ms, last_check_at, enabled) VALUES (?, 'x', 'icmp', ?, ?, ?)",
				r.name, r.interval, r.last, store.BoolArg(enabledColumn(db), r.enabled)); err != nil {
				t.Fatal(err)
			}
		}
		results := m.CheckAll(ctx)
		var checked []string
		for _, r := range results {
			target, err := GetTarget(ctx, db, Num(Number(r.TargetID.V)))
			if err != nil || target == nil {
				t.Fatal(err)
			}
			checked = append(checked, target.Name.String)
			if r.Status != "down" || r.Error == nil || *r.Error != "Unknown check type: icmp" {
				t.Errorf("%s: %+v", target.Name.String, r)
			}
		}
		if got, want := fmt.Sprint(checked), "[a due c never checked e within the allowance]"; got != want {
			t.Errorf("checked %s, want %s", got, want)
		}
		// Every check is stored and its target marked, so nothing is due now
		if again := m.CheckAll(ctx); len(again) != 0 {
			t.Errorf("a second call checked %d targets", len(again))
		}

		if _, err := db.Exec(ctx, "DELETE FROM network_targets"); err != nil {
			t.Fatal(err)
		}
		err := db.InTx(ctx, func(tx *store.Tx) error {
			for i := 0; i < 505; i++ {
				if _, err := tx.Exec(ctx, "INSERT INTO network_targets (name, host, check_type) VALUES (?, 'x', 'icmp')", fmt.Sprintf("many %03d", i)); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if results := m.CheckAll(ctx); len(results) != 500 {
			t.Errorf("%d targets checked in one call, want 500", len(results))
		}
		if results := m.CheckAll(ctx); len(results) != 5 {
			t.Errorf("%d targets checked in the next call, want the 5 that were left", len(results))
		}
	})
}

func enabledColumn(db *store.DB) store.ColumnType {
	if db.IsPostgres() {
		return store.TypeBoolean
	}
	return store.TypeInteger
}

// A list that cannot be read is logged and checks nothing.
func TestCheckAllWithoutDatabase(t *testing.T) {
	db := storetest.SQLite(t)
	var logged []string
	m := New(Options{Store: db, Log: logFunc(func(line string) { logged = append(logged, line) })})
	if results := m.CheckAll(context.Background()); results == nil || len(results) != 0 {
		t.Errorf("results %v, want an empty list", results)
	}
	if len(logged) != 1 || logged[0] != "[NetworkMonitor] checkAll error:no such table: network_targets" {
		t.Errorf("log %q", logged)
	}
}

func TestSQLiteArgs(t *testing.T) {
	cases := []struct {
		name   string
		count  int
		params []Param
		want   string
	}{
		{"plain values", 4, []Param{Text("a"), Num(1), Null(), Num(Number(math.NaN()))}, "[a 1 <nil> NaN]"},
		{"a list stands for its elements", 3, []Param{Text("a"), List(Text("b"), Num(2))}, "[a b 2]"},
		{"an empty list is nothing", 1, []Param{Text("a"), List()}, "[a]"},
		{"an object is nothing", 1, []Param{Text("a"), Object(`{"x":1}`)}, "[a]"},
		{"too few", 2, []Param{Text("a")}, bindTooFew},
		{"an object in place of a value", 1, []Param{Object(`{}`)}, bindTooFew},
		{"an empty list in place of a value", 2, []Param{Text("a"), List()}, bindTooFew},
		{"too many", 1, []Param{Text("a"), Text("b")}, bindTooMany},
		{"a list of two in place of a value", 2, []Param{List(Text("a"), Text("b")), Text("c")}, bindTooMany},
		{"a boolean", 1, []Param{Bool(true)}, bindWrongType},
		{"a boolean beyond the placeholders", 1, []Param{Text("a"), Bool(false)}, bindWrongType},
		{"a list in a list", 1, []Param{List(List(Text("a")))}, bindWrongType},
		{"an object in a list", 1, []Param{List(Object(`{}`))}, bindWrongType},
		{"a boolean in a list", 2, []Param{List(Text("a"), Bool(true))}, bindWrongType},
		{"two objects", 1, []Param{Object(`{}`), Object(`{}`), Text("a")}, bindTwoNamed},
		{"the first problem counts", 1, []Param{Text("a"), Text("b"), Bool(true)}, bindTooMany},
	}
	for _, c := range cases {
		args, err := sqliteArgs(c.count, c.params)
		got := fmt.Sprint(args)
		if err != nil {
			var bind *BindError
			if !errors.As(err, &bind) {
				t.Errorf("%s: error of type %T", c.name, err)
			}
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestPostgresArgs(t *testing.T) {
	args := postgresArgs([]Param{
		Text("a"), Num(42), Num(1.5), Num(1e21), Num(Number(math.NaN())), Num(Number(math.Inf(-1))), Null(), Bool(true), Bool(false),
		List(), List(Text("a"), Num(2), Null(), Bool(true)), List(Text(`say "hi"`), Text(`back\slash`), List(Text("in"), Null()), Object(`{"k":"v"}`)),
		Object(`{"2":[1.5],"b":1}`),
	})
	want := []any{
		"a", "42", "1.5", "1e+21", "NaN", "-Infinity", nil, "true", "false",
		"{}", `{"a","2",NULL,"true"}`, `{"say \"hi\"","back\\slash",{"in",NULL},"{\"k\":\"v\"}"}`,
		`{"2":[1.5],"b":1}`,
	}
	if fmt.Sprintf("%q", args) != fmt.Sprintf("%q", want) {
		t.Errorf("got  %q\nwant %q", args, want)
	}
}

func TestDriverMessage(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&BindError{Message: bindTooFew}, bindTooFew},
		{fmt.Errorf("create network target: %w", &BindError{Message: bindWrongType}), bindWrongType},
		{fmt.Errorf("update network target: %w", &pgconn.PgError{Message: `invalid input syntax for type integer: "x"`, Code: "22P02"}), `invalid input syntax for type integer: "x"`},
		{fmt.Errorf("update network target: %w", errors.New("constraint failed: NOT NULL constraint failed: network_targets.name (1299)")), "NOT NULL constraint failed: network_targets.name"},
		{errors.New("datatype mismatch (20)"), "datatype mismatch"},
		{errors.New("SQL logic error: no such table: network_targets (1)"), "no such table: network_targets"},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), "database is locked"},
		{errors.New("something: else"), "something: else"},
		{fmt.Errorf("list network targets: %w", context.DeadlineExceeded), "context deadline exceeded"},
	}
	for _, c := range cases {
		if got := DriverMessage(c.err); got != c.want {
			t.Errorf("DriverMessage(%q) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestNumbers(t *testing.T) {
	texts := []struct {
		n                       Number
		text, inspect, received string
		json                    string
	}{
		{0, "0", "0", "0", "0"},
		{Number(math.Copysign(0, -1)), "0", "-0", "-0", "0"},
		{-1, "-1", "-1", "-1", "-1"},
		{1.5, "1.5", "1.5", "1.5", "1.5"},
		{4294967296, "4294967296", "4294967296", "4294967296", "4294967296"},
		{4294967297, "4294967297", "4294967297", "4_294_967_297", "4294967297"},
		{-5000000000, "-5000000000", "-5000000000", "-5_000_000_000", "-5000000000"},
		{123456789012, "123456789012", "123456789012", "123_456_789_012", "123456789012"},
		{1e21, "1e+21", "1e+21", "1e_+21", "1e+21"},
		{-1e23, "-1e+23", "-1e+23", "-1e_+23", "-1e+23"},
		{1.5e25, "1.5e+25", "1.5e+25", "1_.5e_+25", "1.5e+25"},
		{1e-7, "1e-7", "1e-7", "1e-7", "1e-7"},
		{0.000001, "0.000001", "0.000001", "0.000001", "0.000001"},
		{Number(math.Inf(1)), "Infinity", "Infinity", "Infinity", "null"},
		{Number(math.Inf(-1)), "-Infinity", "-Infinity", "-Infinity", "null"},
		{NaN(), "NaN", "NaN", "NaN", "null"},
	}
	for _, c := range texts {
		encoded, _ := c.n.MarshalJSON()
		if c.n.String() != c.text || c.n.inspect() != c.inspect || c.n.received() != c.received || string(encoded) != c.json {
			t.Errorf("%v: String %q, inspect %q, received %q, JSON %s; want %q, %q, %q, %s",
				float64(c.n), c.n.String(), c.n.inspect(), c.n.received(), encoded, c.text, c.inspect, c.received, c.json)
		}
	}
	parsed := map[string]string{
		"42": "42", "  42  ": "42", "\t\n 7": "7", "+5": "5", "-5": "-5", "-0": "0", "12abc": "12", "1.9": "1", "1e5": "1", "0x50": "0",
		"": "NaN", "abc": "NaN", "-": "NaN", ".5": "NaN", "null": "NaN", "true": "NaN", "[object Object]": "NaN", "5,6": "5",
		"99999999999999999999999": "1e+23", "9007199254740993": "9007199254740992",
	}
	for text, want := range parsed {
		if got := ParseInt(text).String(); got != want {
			t.Errorf("ParseInt(%q) = %s, want %s", text, got, want)
		}
	}
	if !Number(5).Truthy() || Number(0).Truthy() || NaN().Truthy() || NaN().Or(7) != 7 || Number(0).Or(7) != 7 || Number(-1).Or(7) != -1 {
		t.Error("Truthy and Or do not follow JavaScript")
	}
}
