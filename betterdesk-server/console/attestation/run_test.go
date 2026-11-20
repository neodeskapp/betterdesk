package attestation

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// fakeClock is time that moves only when the run waits or connects. Events
// at a time run when the clock passes it, in time order and then in the
// order they were added.
type fakeClock struct {
	now    time.Time
	seq    int
	events []clockEvent
}

type clockEvent struct {
	at  time.Time
	seq int
	fn  func()
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) at(t time.Time, fn func()) {
	c.seq++
	c.events = append(c.events, clockEvent{t, c.seq, fn})
	sort.SliceStable(c.events, func(i, j int) bool {
		if !c.events[i].at.Equal(c.events[j].at) {
			return c.events[i].at.Before(c.events[j].at)
		}
		return c.events[i].seq < c.events[j].seq
	})
}

func closed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (c *fakeClock) SleepUntil(ctx context.Context, t time.Time, wake <-chan struct{}) (bool, error) {
	if closed(wake) {
		return false, nil
	}
	for len(c.events) > 0 && !c.events[0].at.After(t) {
		e := c.events[0]
		c.events = c.events[1:]
		if e.at.After(c.now) {
			c.now = e.at
		}
		e.fn()
		if closed(wake) {
			return false, nil
		}
	}
	if t.After(c.now) {
		c.now = t
	}
	return true, ctx.Err()
}

// The records of testdata/nodeoracle/run.js.

type wsRule struct {
	Connect    string `json:"connect"`
	Delay      int64  `json:"delay"`
	Reply      string `json:"reply"`
	ReplyDelay int64  `json:"replyDelay"`
}

type snapRecord struct {
	CPU float64 `json:"cpu"`
	Mem struct {
		Total   float64 `json:"total"`
		Percent float64 `json:"percent"`
	} `json:"mem"`
	Disks    []Disk `json:"disks"`
	Hostname string `json:"hostname"`
	CPUCount int    `json:"cpuCount"`
}

type runRecord struct {
	Start            int64  `json:"start"`
	End              int64  `json:"end"`
	StatusAfterStart string `json:"statusAfterStart"`
	Emits            []struct {
		T      int64  `json:"t"`
		Status string `json:"status"`
	} `json:"emits"`
	Aborts []struct {
		T     int64 `json:"t"`
		Value bool  `json:"value"`
	} `json:"aborts"`
	Outcome struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Error  string `json:"error"`
	} `json:"outcome"`
	StatusAfter string  `json:"statusAfter"`
	AbortAfter  bool    `json:"abortAfter"`
	Row         *string `json:"row"`
	LastResult  string  `json:"lastResult"`
}

type scenarioRecord struct {
	Name   string            `json:"name"`
	Env    map[string]string `json:"env"`
	Config struct {
		BetterdeskAPIURL string `json:"betterdeskApiUrl"`
		HbbsAPIURL       string `json:"hbbsApiUrl"`
		WsProxy          *struct {
			HbbsHost string  `json:"hbbsHost"`
			HbbsPort float64 `json:"hbbsPort"`
			HbbrPort float64 `json:"hbbrPort"`
		} `json:"wsProxy"`
		AllowSelfSignedCerts bool `json:"allowSelfSignedCerts"`
	} `json:"config"`
	Stored    *string             `json:"stored"`
	FailSave  bool                `json:"failSave"`
	WS        map[string][]wsRule `json:"ws"`
	Snapshots []snapRecord        `json:"snapshots"`
	Stats     []json.RawMessage   `json:"stats"`
	Plans     []struct {
		Gap    int64   `json:"gap"`
		Aborts []int64 `json:"aborts"`
	} `json:"plans"`
	Runs []runRecord      `json:"runs"`
	Log  []map[string]any `json:"log"`
}

type runFile struct {
	Dialect   string           `json:"dialect"`
	Start     int64            `json:"start"`
	Scenarios []scenarioRecord `json:"scenarios"`
}

// world is a scenario played in Go: the fakes and what they saw.
type world struct {
	sc       *scenarioRecord
	clock    *fakeClock
	start    time.Time
	log      []map[string]any
	sockets  int
	connects map[string]int
	snaps    int
	stats    int
	random   int
}

func (w *world) t() float64 { return float64(w.clock.now.Sub(w.start).Milliseconds()) }

func (w *world) record(op string, fields ...any) {
	e := map[string]any{"t": w.t(), "op": op}
	for i := 0; i+1 < len(fields); i += 2 {
		e[fields[i].(string)] = fields[i+1]
	}
	w.log = append(w.log, e)
}

func (w *world) rule(url string) wsRule {
	list := w.sc.WS[url]
	if list == nil {
		list = w.sc.WS["*"]
	}
	if list == nil {
		list = []wsRule{{Connect: "error", Delay: 1}}
	}
	n := w.connects[url]
	w.connects[url] = n + 1
	if n >= len(list) {
		n = len(list) - 1
	}
	return list[n]
}

// Read gives the bytes run.js's randomBytes gave.
func (w *world) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte((w.random*167 + 13) & 0xff)
		w.random++
	}
	return len(p), nil
}

func (w *world) snapshot() Snapshot {
	list := w.sc.Snapshots
	s := list[min(w.snaps, len(list)-1)]
	w.snaps++
	w.record("snapshot")
	return Snapshot{CPU: s.CPU, MemTotal: s.Mem.Total, MemPercent: s.Mem.Percent, Disks: s.Disks, Hostname: s.Hostname, CPUCount: s.CPUCount}
}

func (w *world) serverStats(context.Context) any {
	raw := w.sc.Stats[min(w.stats, len(w.sc.Stats)-1)]
	w.stats++
	w.record("stats")
	if string(raw) == `"throw"` {
		return nil
	}
	v, err := branding.ParseJSON(raw)
	if err != nil {
		panic(err)
	}
	if !branding.Truthy(branding.Field(v, "success")) {
		return nil
	}
	return branding.Field(v, "data")
}

type fakeDialer struct{ w *world }

func (d fakeDialer) Dial(ctx context.Context, url string, timeout time.Duration) (Conn, error) {
	w := d.w
	rule := w.rule(url)
	w.sockets++
	id := float64(w.sockets)
	w.record("connect", "socket", id, "url", url, "timeout", float64(timeout.Milliseconds()), "verify", !w.sc.Config.AllowSelfSignedCerts)
	start := w.clock.now
	delay := time.Duration(rule.Delay) * time.Millisecond
	if (rule.Connect == "open" || rule.Connect == "error") && delay <= timeout {
		if _, err := w.clock.SleepUntil(ctx, start.Add(delay), nil); err != nil {
			return nil, err
		}
		w.record(rule.Connect, "socket", id)
		if rule.Connect == "error" {
			return nil, errors.New("connect ECONNREFUSED")
		}
		return &fakeConn{w: w, id: id, rule: rule}, nil
	}
	if _, err := w.clock.SleepUntil(ctx, start.Add(timeout), nil); err != nil {
		return nil, err
	}
	w.record("terminate", "socket", id)
	return nil, errors.New("connect timeout")
}

type fakeConn struct {
	w      *world
	id     float64
	rule   wsRule
	closed bool
	// reply is when the answer to a send arrives and whether it is a failure
	replyAt  time.Time
	replied  bool
	failed   bool
	expected bool
}

func (c *fakeConn) Send(payload []byte) error {
	c.w.record("send", "socket", c.id, "data", hex.EncodeToString(payload))
	if c.rule.Reply == "message" || c.rule.Reply == "error" {
		c.expected = true
		c.replyAt = c.w.clock.now.Add(time.Duration(c.rule.ReplyDelay) * time.Millisecond)
		c.w.clock.at(c.replyAt, func() {
			if c.closed {
				return
			}
			c.w.record(c.rule.Reply, "socket", c.id)
			c.replied, c.failed = true, c.rule.Reply == "error"
		})
	}
	return nil
}

func (c *fakeConn) AwaitMessage(ctx context.Context, timeout time.Duration) error {
	deadline := c.w.clock.now.Add(timeout)
	if c.expected && c.replyAt.Before(deadline) {
		if _, err := c.w.clock.SleepUntil(ctx, c.replyAt, nil); err != nil {
			return err
		}
		if c.failed {
			return errors.New("socket hang up")
		}
		return nil
	}
	if _, err := c.w.clock.SleepUntil(ctx, deadline, nil); err != nil {
		return err
	}
	return errors.New("probe timeout")
}

func (c *fakeConn) Drain() {}

func (c *fakeConn) Close() {
	c.w.record("close", "socket", c.id)
	c.closed = true
}

// network is the scenario's configuration as the console reads it.
func (sc *scenarioRecord) network() Network {
	n := Network{
		SignalHostEnv:   sc.Env["WS_HBBS_HOST"],
		RelayHostEnv:    sc.Env["WS_HBBR_HOST"],
		APIURL:          sc.Config.BetterdeskAPIURL,
		TLS:             strings.ToLower(sc.Env["WS_USE_TLS"]) == "true",
		AllowSelfSigned: sc.Config.AllowSelfSignedCerts,
	}
	if n.APIURL == "" {
		n.APIURL = sc.Config.HbbsAPIURL
	}
	port := func(env string, proxy float64, fallback int) int {
		if v, err := strconv.Atoi(strings.TrimSpace(env)); err == nil && v != 0 {
			return v
		}
		if proxy != 0 {
			return int(proxy)
		}
		return fallback
	}
	var signalProxy, relayProxy float64
	if p := sc.Config.WsProxy; p != nil {
		n.ProxySignalHost = p.HbbsHost
		signalProxy, relayProxy = p.HbbsPort, p.HbbrPort
	}
	n.SignalPort = port(sc.Env["WS_HBBS_PORT"], signalProxy, 21116)
	n.RelayPort = port(sc.Env["WS_HBBR_PORT"], relayProxy, 21117)
	return n
}

// failingSettings fails every write.
type failingSettings struct{ Settings }

func (failingSettings) Set(context.Context, string, string) error {
	return errors.New("database is locked")
}

func statusString(st Status) string {
	o := branding.NewObject()
	o.Set("running", st.Running)
	o.Set("phase", st.Phase)
	o.Set("progress", st.Progress)
	o.Set("lastResult", st.LastResult)
	return branding.Stringify(o)
}

// play runs a recorded scenario against the Go service on db and lists
// every way it differs from what Node did.
func play(t *testing.T, db *store.DB, sc *scenarioRecord, start time.Time) []string {
	ctx := context.Background()
	if _, err := db.Exec(ctx, "DELETE FROM settings WHERE key = ?", ResultKey); err != nil {
		t.Fatal(err)
	}
	var settings Settings = StoreSettings(db)
	if sc.Stored != nil {
		if err := settings.Set(ctx, ResultKey, *sc.Stored); err != nil {
			t.Fatal(err)
		}
	}
	if sc.FailSave {
		settings = failingSettings{settings}
	}
	w := &world{sc: sc, clock: &fakeClock{now: start}, start: start, connects: map[string]int{}}
	s := New(Options{
		Settings: settings, Network: sc.network(), Snapshot: w.snapshot, ServerStats: w.serverStats,
		Dialer: fakeDialer{w}, Clock: w.clock, Random: w,
	})
	var diffs []string
	differ := func(format string, args ...any) { diffs = append(diffs, fmt.Sprintf(format, args...)) }
	for i, plan := range sc.Plans {
		node := sc.Runs[i]
		w.clock.now = w.clock.now.Add(time.Duration(plan.Gap) * time.Millisecond)
		runStart := w.t()
		if runStart != float64(node.Start) {
			differ("run %d starts at %v, node %d", i, runStart, node.Start)
		}
		type abortSeen struct {
			T     int64 `json:"t"`
			Value bool  `json:"value"`
		}
		var aborts []abortSeen
		for _, at := range plan.Aborts {
			w.clock.at(w.clock.now.Add(time.Duration(at)*time.Millisecond), func() {
				aborts = append(aborts, abortSeen{int64(w.t()), s.RequestAbort()})
			})
		}
		type emitSeen struct {
			T      int64
			Status string
		}
		var emits []emitSeen
		s.onProgress = func() { emits = append(emits, emitSeen{int64(w.t()), statusString(s.Status(ctx))}) }
		if !s.Start() {
			t.Fatalf("run %d did not start", i)
		}
		if got := statusString(s.Status(ctx)); got != node.StatusAfterStart {
			differ("run %d status after start:\n  node %s\n    go %s", i, node.StatusAfterStart, got)
		}
		r := <-s.starts
		result, err := s.execute(ctx, r)
		w.clock.events = nil
		if got := int64(w.t()); got != node.End {
			differ("run %d ends at %d, node %d", i, got, node.End)
		}
		switch {
		case node.Outcome.OK && err != nil:
			differ("run %d failed with %v; node: %s", i, err, node.Outcome.Result)
		case !node.Outcome.OK && err == nil:
			differ("run %d succeeded with %s; node failed with %s", i, branding.Stringify(result), node.Outcome.Error)
		case !node.Outcome.OK && branding.Message(err) != node.Outcome.Error:
			differ("run %d failed with %q, node %q", i, branding.Message(err), node.Outcome.Error)
		case node.Outcome.OK && branding.Stringify(result) != node.Outcome.Result:
			differ("run %d result:\n  node %s\n    go %s", i, node.Outcome.Result, branding.Stringify(result))
		}
		if len(emits) != len(node.Emits) {
			differ("run %d: %d steps, node %d", i, len(emits), len(node.Emits))
		}
		for j := 0; j < min(len(emits), len(node.Emits)); j++ {
			if emits[j].T != node.Emits[j].T || emits[j].Status != node.Emits[j].Status {
				differ("run %d step %d:\n  node %d %s\n    go %d %s", i, j, node.Emits[j].T, node.Emits[j].Status, emits[j].T, emits[j].Status)
				break
			}
		}
		gotAborts, _ := json.Marshal(aborts)
		wantAborts, _ := json.Marshal(node.Aborts)
		if string(gotAborts) != string(wantAborts) && !(len(aborts) == 0 && len(node.Aborts) == 0) {
			differ("run %d aborts %s, node %s", i, gotAborts, wantAborts)
		}
		if got := statusString(s.Status(ctx)); got != node.StatusAfter {
			differ("run %d status after:\n  node %s\n    go %s", i, node.StatusAfter, got)
		}
		if got := s.RequestAbort(); got != node.AbortAfter {
			differ("run %d abort after the run = %v", i, got)
		}
		row, found, err := StoreSettings(db).Get(ctx, ResultKey)
		if err != nil {
			t.Fatal(err)
		}
		if found != (node.Row != nil) || (found && row != *node.Row) {
			differ("run %d stored row %q (%v), node %v", i, row, found, show(node.Row))
		}
		if got := branding.Stringify(s.LastResult(ctx)); got != node.LastResult {
			differ("run %d last result %s, node %s", i, got, node.LastResult)
		}
	}
	got, _ := json.Marshal(w.log)
	var gotLog []map[string]any
	_ = json.Unmarshal(got, &gotLog)
	if !reflect.DeepEqual(gotLog, sc.Log) {
		n := min(len(gotLog), len(sc.Log))
		for j := 0; j < n; j++ {
			if !reflect.DeepEqual(gotLog[j], sc.Log[j]) {
				differ("event %d of %d (node %d): node %v, go %v", j, len(gotLog), len(sc.Log), sc.Log[j], gotLog[j])
				break
			}
		}
		if len(gotLog) != len(sc.Log) {
			differ("%d events, node %d", len(gotLog), len(sc.Log))
		}
	}
	return diffs
}

// Every recorded capacity test, played in Go with the same connections,
// readings and aborts, gives what the Node service gave: the steps of the
// progress, the result, the stored row, the state after it, and the
// connections opened, the messages sent and the connections closed, at
// the same times. Each database replays the recording made on it.
//
// The scenarios a bug fix changed on purpose (runFixes) differ from Node's
// runs in the ways testdata/runs_fixed.json holds, and in no other.
// ATTESTATION_RECORD_FIXED=1 writes that file from this run and fails for
// a listed scenario that runs as Node did.
func TestRunsAsNodeRanThem(t *testing.T) {
	files := map[string]runFile{}
	for _, dialect := range []string{"sqlite", "postgres"} {
		var f runFile
		readGzipJSON(t, "runs_"+dialect+".json.gz", &f)
		files[dialect] = f
	}
	recording := os.Getenv("ATTESTATION_RECORD_FIXED") == "1"
	fixed := map[string]map[string]fixedRun{}
	if !recording {
		fixed = loadFixedRuns(t)
	}
	written := map[string]map[string]fixedRun{}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		f := files["sqlite"]
		if db.IsPostgres() {
			f = files["postgres"]
		}
		if len(f.Scenarios) < 30 {
			t.Fatalf("only %d scenarios", len(f.Scenarios))
		}
		written[f.Dialect] = map[string]fixedRun{}
		runs, listed := 0, 0
		for i := range f.Scenarios {
			sc := &f.Scenarios[i]
			runs += len(sc.Runs)
			diffs := play(t, db, sc, time.UnixMilli(f.Start))
			bug, isListed := runFixes[sc.Name]
			switch {
			case isListed && recording:
				listed++
				if len(diffs) == 0 {
					t.Errorf("%s is listed for %s but runs as Node ran it", sc.Name, bug)
				}
				written[f.Dialect][sc.Name] = fixedRun{Bug: bug, Differences: diffs}
			case isListed:
				listed++
				want := fixed[f.Dialect][sc.Name]
				if want.Bug != bug {
					t.Errorf("%s is listed for %s, %s holds it for %q", sc.Name, bug, fixedRunsFile, want.Bug)
				}
				if strings.Join(diffs, "\n") != strings.Join(want.Differences, "\n") {
					t.Errorf("%s (%s) differs from Node's run otherwise than %s holds:\n%s\nwant:\n%s",
						sc.Name, bug, fixedRunsFile, strings.Join(diffs, "\n"), strings.Join(want.Differences, "\n"))
				}
			default:
				for _, d := range diffs {
					t.Errorf("%s: %s", sc.Name, d)
				}
			}
		}
		if !recording && len(fixed[f.Dialect]) != listed {
			t.Errorf("%s holds %d scenarios of %s, runFixes lists %d", fixedRunsFile, len(fixed[f.Dialect]), f.Dialect, listed)
		}
		t.Logf("%s: %d scenarios, %d runs", f.Dialect, len(f.Scenarios), runs)
	})
	if recording {
		writeFixedRuns(t, written)
	}
}

// runFixes are the scenarios a bug fix of BUGS.md changed on purpose, with
// the bug. A scenario that several fixes change stays with the first.
var runFixes = map[string]string{
	// A wait ends the moment an abort comes, not at its next look
	"abort-in-baseline":          "SA1",
	"abort-twice":                "SA1",
	"abort-in-first-pause":       "SA1",
	"abort-in-ramp-pause":        "SA1",
	"abort-at-end-of-ramp-pause": "SA1",
	"abort-while-connecting":     "SA1",
	"abort-in-stabilize":         "SA1",
	"abort-estimate-stabilize":   "SA1",
	"two-runs":                   "SA1",
	"stored-result-falsy":        "SA1",
	// The first connection of a relay pair whose second one fails is closed
	"relay-pair-half-open": "SA3",
}

// fixedRun is how a listed scenario differs from Node's run.
type fixedRun struct {
	Bug         string   `json:"bug"`
	Differences []string `json:"differences"`
}

const fixedRunsFile = "runs_fixed.json"

// loadFixedRuns reads testdata/runs_fixed.json: per database, per
// scenario, the differences. No file means none.
func loadFixedRuns(t *testing.T) map[string]map[string]fixedRun {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixedRunsFile))
	if os.IsNotExist(err) && len(runFixes) == 0 {
		return map[string]map[string]fixedRun{}
	}
	if err != nil {
		t.Fatalf("read %s: %v", fixedRunsFile, err)
	}
	var fixed map[string]map[string]fixedRun
	if err := json.Unmarshal(raw, &fixed); err != nil {
		t.Fatalf("parse %s: %v", fixedRunsFile, err)
	}
	return fixed
}

// writeFixedRuns writes testdata/runs_fixed.json, or removes it when no
// scenario is listed.
func writeFixedRuns(t *testing.T, fixed map[string]map[string]fixedRun) {
	t.Helper()
	path := filepath.Join("testdata", fixedRunsFile)
	if len(runFixes) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(fixed); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
