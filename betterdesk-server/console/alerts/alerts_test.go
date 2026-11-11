package alerts

import (
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jobs"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// addRule writes an enabled rule and returns its id.
func addRule(t *testing.T, db *store.DB, conditionType, op string, limit float64, more map[string]any) int64 {
	t.Helper()
	column := store.TypeInteger
	if db.IsPostgres() {
		column = store.TypeBoolean
	}
	cols := map[string]any{"name": "Rule", "enabled": store.BoolArg(column, true), "condition_type": conditionType, "condition_op": op,
		"condition_value": limit, "cooldown_secs": 300, "notify_emails": ""}
	for k, v := range more {
		cols[k] = v
	}
	var names, marks []string
	var args []any
	for name, v := range cols {
		names, marks, args = append(names, name), append(marks, "?"), append(args, v)
	}
	id, err := db.Insert(context.Background(), "INSERT INTO alert_rules ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(marks, ", ")+")", args...)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type raised struct {
	ruleID   int64
	deviceID string
	message  string
}

func alertsIn(t *testing.T, db *store.DB) []raised {
	t.Helper()
	rows, err := db.Query(context.Background(), "SELECT rule_id, device_id, message FROM alert_history ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []raised
	for rows.Next() {
		var r raised
		if err := rows.Scan(&r.ruleID, &r.deviceID, &r.message); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func device(fields ...any) *goapi.Object { return goapi.NewObject(fields...) }

var sweepNow = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// sweepDevices is the device list of the Jest test.
func sweepDevices() []*goapi.Object {
	return []*goapi.Object{
		device("id", "off-2h", "online", false, "last_online", "2026-11-09T10:00:00Z"),
		device("id", "off-1m", "online", false, "last_online", "2026-11-09T11:59:00Z"),
		device("id", "on", "online", true, "last_online", "2026-11-09T09:00:00Z"),
		device("id", "banned", "online", false, "banned", true, "last_online", "2026-11-01T00:00:00Z"),
		device("id", "gone", "online", false, "soft_deleted", true, "last_online", "2026-11-01T00:00:00Z"),
		device("id", "never", "online", false, "last_online", ""),
	}
}

func listOf(devices []*goapi.Object) func(context.Context) ([]*goapi.Object, error) {
	return func(context.Context) ([]*goapi.Object, error) { return devices, nil }
}

// ---- tests/alertRulesEngine.test.js ----

func TestTelemetryRaisesACPUAlertOverTheLimit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "cpu_usage", "gt", 80, nil)
		engine := New(Options{DB: db, Logger: &logLines{}})
		err := engine.EvaluateTelemetry(context.Background(), jsbind.String("dev-1"), Telemetry{
			CPUUsagePercent: jsbind.Number(95), MemoryUsedBytes: jsbind.Number(1), MemoryTotalBytes: jsbind.Number(4),
		})
		if err != nil {
			t.Fatal(err)
		}
		got := alertsIn(t, db)
		if len(got) != 1 || got[0] != (raised{id, "dev-1", "Rule: cpu_usage = 95 (gt 80)"}) {
			t.Fatalf("alerts %+v", got)
		}
	})
}

func TestTelemetryRaisesNothingUnderTheLimit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addRule(t, db, "cpu_usage", "gt", 80, nil)
		engine := New(Options{DB: db, Logger: &logLines{}})
		if err := engine.EvaluateTelemetry(context.Background(), jsbind.String("dev-1"), Telemetry{CPUUsagePercent: jsbind.Number(20)}); err != nil {
			t.Fatal(err)
		}
		if got := alertsIn(t, db); len(got) != 0 {
			t.Fatalf("alerts %+v", got)
		}
	})
}

// The inventory route hands in the fullest disk in percent, the activity
// route the seconds without input.
func TestDiskAndIdleAlerts(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		disk := addRule(t, db, "disk_usage", "gt", 90, nil)
		idle := addRule(t, db, "idle_duration", "gt", 600, nil)
		engine := New(Options{DB: db, Logger: &logLines{}})
		ctx := context.Background()
		if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "disk_usage", jsbind.Number(97), EvalOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "idle_duration", jsbind.Number(3600), EvalOptions{}); err != nil {
			t.Fatal(err)
		}
		got := alertsIn(t, db)
		if len(got) != 2 || got[0] != (raised{disk, "dev-1", "Rule: disk_usage = 97 (gt 90)"}) || got[1] != (raised{idle, "dev-1", "Rule: idle_duration = 3600 (gt 600)"}) {
			t.Fatalf("alerts %+v", got)
		}
	})
}

func TestSweepDoesNotReadTheDeviceListWithoutAnOfflineRule(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addRule(t, db, "cpu_usage", "gt", 90, nil)
		reads := 0
		engine := New(Options{DB: db, Logger: &logLines{}, Devices: func(context.Context) ([]*goapi.Object, error) {
			reads++
			return sweepDevices(), nil
		}})
		if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil || reads != 0 {
			t.Fatalf("%v, device list read %d times", err, reads)
		}
	})
}

func TestSweepRaisesOneAlertPerDeviceOfflineLongerThanTheLimit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "offline_duration", "gt", 3600, nil)
		engine := New(Options{DB: db, Logger: &logLines{}, Devices: listOf(sweepDevices())})
		if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		got := alertsIn(t, db)
		if len(got) != 1 || got[0] != (raised{id, "off-2h", "Rule: offline_duration = 7200 (gt 3600)"}) {
			t.Fatalf("alerts %+v", got)
		}
		// It is not raised again while the device stays offline: the
		// cooldown covers the whole time offline (2 h), not just 5 minutes.
		for range 3 {
			if err := engine.PeriodicCheckAt(context.Background(), sweepNow.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		}
		if got := alertsIn(t, db); len(got) != 1 {
			t.Fatalf("alerts after more sweeps %+v", got)
		}
	})
}

func TestSweepAsksForACooldownAsLongAsTheTimeOffline(t *testing.T) {
	mem := &memStore{t: t, c: &memCase{Rules: []map[string]string{{"id": "6", "name": `"Rule"`, "condition_type": `"offline_duration"`, "condition_op": `"gt"`,
		"condition_value": "3600", "cooldown_secs": "300"}}}}
	engine := New(Options{Logger: &logLines{}, Devices: listOf(sweepDevices())})
	engine.store = mem
	if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil {
		t.Fatal(err)
	}
	if len(mem.recent) != 1 || mem.recent[0] != [3]string{"6", `"off-2h"`, "7200"} {
		t.Fatalf("cooldown checks %q", mem.recent)
	}
}

func TestSweepKeepsARuleForOneDeviceToThatDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addRule(t, db, "offline_duration", "gt", 30, map[string]any{"scope_device_id": "off-1m"})
		engine := New(Options{DB: db, Logger: &logLines{}, Devices: listOf(sweepDevices())})
		if err := engine.PeriodicCheckAt(context.Background(), sweepNow); err != nil {
			t.Fatal(err)
		}
		got := alertsIn(t, db)
		if len(got) != 1 || got[0].deviceID != "off-1m" || got[0].message != "Rule: offline_duration = 60 (gt 30)" {
			t.Fatalf("alerts %+v", got)
		}
	})
}

// countingStore answers one offline rule and counts how often the rules
// are asked for, which is once per sweep.
type countingStore struct {
	memStore
	loads atomic.Int32
}

func (s *countingStore) enabledRules(context.Context, string) ([]Rule, error) {
	s.loads.Add(1)
	return []Rule{{ID: jsbind.Number(1), Name: jsbind.String("Rule"), ConditionOp: jsbind.String("gt"), ConditionValue: jsbind.Number(1)}}, nil
}

func newCountingStore(t *testing.T) *countingStore {
	return &countingStore{memStore: memStore{t: t, c: &memCase{}}}
}

// waitFor waits until cond holds, for as long as a busy machine may need.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// 'runs the sweep on a timer and stops it again', with the console's job
// registry driving it as the server does.
func TestRunSweepsOnATimerAndStops(t *testing.T) {
	counting := newCountingStore(t)
	engine := New(Options{Logger: &logLines{}, SweepEvery: 25 * time.Millisecond, Devices: listOf(nil)})
	engine.store = counting
	registry := jobs.New()
	registry.Add("alert sweep", engine.Run)
	time.Sleep(100 * time.Millisecond)
	if n := counting.loads.Load(); n != 0 {
		t.Fatalf("%d sweeps before the registry started", n)
	}
	started := time.Now()
	registry.Start(context.Background())
	waitFor(t, "the first sweep", func() bool { return counting.loads.Load() >= 1 })
	if waited := time.Since(started); waited < 25*time.Millisecond {
		t.Fatalf("the first sweep came after %v: it is due after one interval", waited)
	}
	waitFor(t, "three sweeps", func() bool { return counting.loads.Load() >= 3 })
	if !registry.Stop() {
		t.Fatal("the job did not stop")
	}
	ran := counting.loads.Load()
	time.Sleep(150 * time.Millisecond)
	if n := counting.loads.Load(); n != ran {
		t.Fatalf("%d sweeps after the stop", n-ran)
	}
}

// One sweep at a time: a second call while one runs does nothing, and so
// do the runs of the job that come due meanwhile.
func TestOneSweepAtATime(t *testing.T) {
	counting := newCountingStore(t)
	entered, release := make(chan struct{}, 8), make(chan struct{})
	engine := New(Options{Logger: &logLines{}, SweepEvery: 10 * time.Millisecond, Devices: func(context.Context) ([]*goapi.Object, error) {
		entered <- struct{}{}
		<-release
		return nil, nil
	}})
	engine.store = counting

	done := make(chan struct{})
	go func() {
		engine.Sweep(context.Background())
		close(done)
	}()
	<-entered
	engine.Sweep(context.Background()) // returns at once
	if n := counting.loads.Load(); n != 1 {
		t.Fatalf("%d sweeps at the same time", n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		engine.Run(ctx)
		close(stopped)
	}()
	time.Sleep(150 * time.Millisecond)
	if n := counting.loads.Load(); n != 1 {
		t.Fatalf("%d sweeps while the first still runs", n)
	}
	cancel()
	<-stopped
	close(release)
	<-done
	// Afterwards a sweep runs again
	engine.Sweep(context.Background())
	if n := counting.loads.Load(); n != 2 {
		t.Fatalf("%d sweeps after the first finished, want 2", n)
	}
}

// A sweep longer than the interval: the run that comes due during it is
// dropped, it does not start right behind it. The ticks are handed in by
// hand, so nothing here depends on how fast the machine is.
func TestRunDropsTheRunACoveredByASweep(t *testing.T) {
	counting := newCountingStore(t)
	entered, release := make(chan struct{}, 8), make(chan struct{}, 8)
	engine := New(Options{Logger: &logLines{}, Devices: func(context.Context) ([]*goapi.Object, error) {
		entered <- struct{}{}
		<-release
		return nil, nil
	}})
	engine.store = counting
	// One tick waits in the channel, as with a time.Ticker
	ticks := make(chan time.Time, 1)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		engine.sweepOn(ctx, ticks)
		close(stopped)
	}()

	ticks <- time.Now()
	<-entered             // the first sweep runs
	ticks <- time.Now()   // the next comes due while it runs
	release <- struct{}{} // the first sweep ends
	waitFor(t, "the waiting tick to be taken", func() bool { return len(ticks) == 0 })
	time.Sleep(100 * time.Millisecond)
	if n := counting.loads.Load(); n != 1 {
		t.Fatalf("%d sweeps: the run that came due during the first was not dropped", n)
	}
	// The tick after that starts the next sweep
	ticks <- time.Now()
	<-entered
	release <- struct{}{}
	waitFor(t, "the end of the second sweep", func() bool { return !engine.sweeping.Load() })
	if n := counting.loads.Load(); n != 2 {
		t.Fatalf("%d sweeps after two free ticks", n)
	}
	cancel()
	<-stopped
}

// The engine does nothing until it is called: the job is the server's to
// register, and only when the Node console does not sweep as well.
func TestNewStartsNothing(t *testing.T) {
	db := storetest.Migrated(t)
	addRule(t, db, "offline_duration", "gt", 1, nil)
	before := runtime.NumGoroutine()
	var reads atomic.Int32
	mailer := &fakeMailer{}
	engine := New(Options{DB: db, Mail: mailer, SweepEvery: time.Millisecond, Logger: &logLines{}, Devices: func(context.Context) ([]*goapi.Object, error) {
		reads.Add(1)
		return sweepDevices(), nil
	}})
	time.Sleep(80 * time.Millisecond)
	if reads.Load() != 0 || len(alertsIn(t, db)) != 0 || len(mailer.keys()) != 0 || engine.sweeping.Load() {
		t.Fatalf("the engine worked on its own: %d device list reads, %d alerts", reads.Load(), len(alertsIn(t, db)))
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("New started %d goroutines", after-before)
	}
}

func TestSweepLogsAFailure(t *testing.T) {
	logs := &logLines{}
	mem := &memStore{t: t, c: &memCase{Rules: []map[string]string{{"id": "1", "condition_op": `"valueOf"`, "condition_value": "1"}}}}
	engine := New(Options{Logger: logs, Now: func() time.Time { return sweepNow }, Devices: listOf(sweepDevices())})
	engine.store = mem
	engine.Sweep(context.Background())
	engine.Sweep(context.Background())
	want := [2]string{"error", "[AlertEngine] Sweep failed: Cannot convert undefined or null to object"}
	if len(logs.lines) != 2 || logs.lines[0] != want || logs.lines[1] != want {
		t.Fatalf("logs %q", logs.lines)
	}
}

// ---- tests/dbAdapter.alertCooldown.test.js, on both databases ----

func alertAgo(t *testing.T, db *store.DB, ruleID int64, deviceID string, ago time.Duration) {
	t.Helper()
	err := CreateAlert(context.Background(), db, NewAlert{
		RuleID: jsbind.Number(float64(ruleID)), DeviceID: jsbind.String(deviceID), Severity: jsbind.String("warning"), Message: jsbind.String("x"),
		TriggeredAt: jsbind.String(store.ISOTime(time.Now().Add(-ago))),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

func recent(t *testing.T, db *store.DB, ruleID int64, deviceID string, cooldown float64) bool {
	t.Helper()
	found, err := GetRecentAlert(context.Background(), db, jsbind.Number(float64(ruleID)), jsbind.String(deviceID), jsbind.Number(cooldown))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestCooldownCountsAnAlertInsideItAsRecent(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "cpu_usage", "gt", 90, nil)
		alertAgo(t, db, id, "dev-1", time.Minute)
		if !recent(t, db, id, "dev-1", 300) {
			t.Fatal("an alert of a minute ago is not recent")
		}
	})
}

// Alerts are stored with an ISO time ("...T...Z"). Compared as text with
// the database's "now" every alert of the same day would count as recent.
func TestCooldownDoesNotCountAnOlderAlert(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "cpu_usage", "gt", 90, nil)
		alertAgo(t, db, id, "dev-1", 20*time.Minute)
		if recent(t, db, id, "dev-1", 300) {
			t.Fatal("an alert of 20 minutes ago counts as inside a cooldown of 5")
		}
		if !recent(t, db, id, "dev-1", 1800) {
			t.Fatal("an alert of 20 minutes ago does not count as inside a cooldown of 30")
		}
	})
}

func TestCooldownKeepsRulesAndDevicesApart(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "cpu_usage", "gt", 90, nil)
		other := addRule(t, db, "cpu_usage", "gt", 90, nil)
		alertAgo(t, db, id, "dev-1", time.Minute)
		if recent(t, db, id, "dev-2", 300) || recent(t, db, other, "dev-1", 300) {
			t.Fatal("an alert counts for another device or another rule")
		}
	})
}

// The cooldown in the engine: a second report over the limit raises no
// second alert, and after the cooldown it does.
func TestARuleStaysQuietForItsCooldown(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		id := addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": 600})
		engine := New(Options{DB: db, Logger: &logLines{}})
		ctx := context.Background()
		report := func(deviceID string) {
			t.Helper()
			if err := engine.EvaluateRules(ctx, jsbind.String(deviceID), "cpu_usage", jsbind.Number(95), EvalOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		report("dev-1")
		report("dev-1")
		report("dev-1")
		if got := alertsIn(t, db); len(got) != 1 {
			t.Fatalf("%d alerts for three reports inside the cooldown", len(got))
		}
		// Another device has a cooldown of its own
		report("dev-2")
		if got := alertsIn(t, db); len(got) != 2 {
			t.Fatalf("%d alerts with a second device", len(got))
		}
		// With the alerts moved back past the cooldown the rule speaks again
		if _, err := db.Exec(ctx, "UPDATE alert_history SET triggered_at = ? WHERE rule_id = ?", store.ISOTime(time.Now().Add(-11*time.Minute)), id); err != nil {
			t.Fatal(err)
		}
		report("dev-1")
		if got := alertsIn(t, db); len(got) != 3 {
			t.Fatalf("%d alerts after the cooldown", len(got))
		}
	})
}

type blockingMailer struct {
	release chan struct{}
	calls   atomic.Int32
}

func (m *blockingMailer) SendAlertEmail(context.Context, mail.Alert, mail.AlertRule) (bool, error) {
	m.calls.Add(1)
	<-m.release
	return false, errors.New("smtp down")
}

// The alert mail goes out beside the evaluation and its failure is logged.
func TestAlertMailInTheBackground(t *testing.T) {
	db := storetest.Migrated(t)
	addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"notify_emails": "ops@example.com", "name": "CPU"})
	mailer := &blockingMailer{release: make(chan struct{})}
	logs := &logLines{}
	engine := New(Options{DB: db, Mail: mailer, Logger: logs})
	done := make(chan error, 1)
	go func() {
		done <- engine.EvaluateRules(context.Background(), jsbind.String("dev-1"), "cpu_usage", jsbind.Number(95), EvalOptions{})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the evaluation waits for the mail")
	}
	close(mailer.release)
	engine.Wait()
	if mailer.calls.Load() != 1 {
		t.Fatalf("%d mails", mailer.calls.Load())
	}
	want := [][2]string{{"info", "[AlertEngine] ALERT fired: CPU: cpu_usage = 95 (gt 80) for dev-1"}, {"error", "[AlertEngine] Email failed: smtp down"}}
	if !sameLines(logs.lines, want) {
		t.Fatalf("logs %q", logs.lines)
	}
}

// Background evaluations end with the console, and one that breaks takes
// nothing with it.
func TestBackgroundWorkEndsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logs := &logLines{}
	engine := New(Options{Logger: logs, Context: ctx})
	var wg sync.WaitGroup
	wg.Add(1)
	engine.EvaluateInBackground(func(ctx context.Context) error {
		defer wg.Done()
		<-ctx.Done()
		return ctx.Err()
	})
	engine.EvaluateInBackground(func(context.Context) error { panic("broken") })
	cancel()
	wg.Wait()
	engine.Wait()
	got := map[string]bool{}
	for _, line := range logs.lines {
		got[line[1]] = true
	}
	if len(logs.lines) != 2 || !got["[AlertEngine] Evaluation failed: context canceled"] || !got["[AlertEngine] Evaluation failed: broken"] {
		t.Fatalf("logs %q", logs.lines)
	}
}

type recordingTransport struct {
	mu    sync.Mutex
	mails []mail.Outgoing
	with  []mail.Settings
}

func (r *recordingTransport) Send(_ context.Context, settings mail.Settings, m mail.Outgoing) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails, r.with = append(r.mails, m), append(r.with, settings)
	return nil
}

func (r *recordingTransport) Verify(context.Context, mail.Settings) error { return nil }

// The engine and the mail service together: a rule with recipients mails
// its alert with the stored SMTP settings.
func TestAlertMailThroughTheMailService(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		_, err := db.Exec(ctx, "INSERT INTO settings (key, value) VALUES (?, ?)", "smtp_config",
			`{"host":"smtp.example.com","port":2525,"secure":true,"user":"u","pass":"p","from":"panel@example.com"}`)
		if err != nil {
			t.Fatal(err)
		}
		addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"name": "CPU <high>", "notify_emails": "ops@example.com, oncall@example.com", "severity": "critical"})
		transport := &recordingTransport{}
		engine := New(Options{DB: db, Logger: &logLines{}, Mail: mail.New(mail.Options{DB: db, Transport: transport, Logger: quietMailLog{}})})
		if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "cpu_usage", jsbind.Number(95.5), EvalOptions{}); err != nil {
			t.Fatal(err)
		}
		engine.Wait()
		if len(transport.mails) != 1 {
			t.Fatalf("%d mails", len(transport.mails))
		}
		m, with := transport.mails[0], transport.with[0]
		if with != (mail.Settings{Host: "smtp.example.com", Port: 2525, Secure: true, User: "u", Pass: "p"}) {
			t.Fatalf("settings %+v", with)
		}
		if m.From != "panel@example.com" || len(m.To) != 1 || m.To[0] != "ops@example.com, oncall@example.com" || m.ToList ||
			m.Subject != "[BetterDesk Alert] CPU <high> — CRITICAL" ||
			!strings.Contains(*m.Text, "Message: CPU <high>: cpu_usage = 95.5 (gt 80)\n") || !strings.Contains(*m.Text, "\nDevice: dev-1\n") ||
			!strings.Contains(*m.HTML, "BetterDesk Alert: CPU &lt;high&gt;</h2>") {
			t.Fatalf("mail from %q to %q subject %q\n%s\n%s", m.From, m.To, m.Subject, *m.Text, *m.HTML)
		}
	})
}

type quietMailLog struct{}

func (quietMailLog) Info(...any)  {}
func (quietMailLog) Warn(...any)  {}
func (quietMailLog) Error(...any) {}

// What createAlert fills in for an alert that leaves something out: no
// device is NULL, no severity is "warning", no message is the empty text,
// no time is the time of the call.
func TestCreateAlertFillsInWhatIsMissing(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		id := addRule(t, db, "cpu_usage", "gt", 90, nil)
		at := time.Date(2026, 11, 10, 8, 30, 15, 987654321, time.UTC)
		for _, alert := range []NewAlert{
			{RuleID: jsbind.Number(float64(id))},
			{RuleID: jsbind.Number(float64(id)), DeviceID: jsbind.String(""), Severity: jsbind.String(""), Message: jsbind.String(""), TriggeredAt: jsbind.String("")},
			{RuleID: jsbind.Number(float64(id)), DeviceID: jsbind.Null, Severity: jsbind.Null, Message: jsbind.Null, TriggeredAt: jsbind.Null},
		} {
			if err := CreateAlert(ctx, db, alert, at); err != nil {
				t.Fatal(err)
			}
		}
		if err := CreateAlert(ctx, db, NewAlert{RuleID: jsbind.Number(float64(id)), DeviceID: jsbind.String("dev-1"), Severity: jsbind.String("critical"),
			Message: jsbind.String("text"), TriggeredAt: jsbind.String("2026-01-02T03:04:05.006Z")}, at); err != nil {
			t.Fatal(err)
		}
		rows, err := store.Select(ctx, db, "SELECT device_id, severity, message, triggered_at FROM alert_history ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var device, severity, message store.NullString
			var triggered store.Time
			if err := rows.Scan(&device, &severity, &message, &triggered); err != nil {
				t.Fatal(err)
			}
			instant, _ := triggered.Instant()
			got = append(got, strings.Join([]string{strconv.FormatBool(device.Valid), device.String, severity.String, message.String, store.ISOTime(instant)}, "|"))
		}
		missing := "false||warning||2026-11-10T08:30:15.987Z"
		want := []string{missing, missing, missing, "true|dev-1|critical|text|2026-01-02T03:04:05.006Z"}
		if !sameStrings(got, want) {
			t.Fatalf("rows %q", got)
		}
	})
}
