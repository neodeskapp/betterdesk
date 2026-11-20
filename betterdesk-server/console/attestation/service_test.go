package attestation

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// hangingDialer never connects: Dial waits until its context ends.
type hangingDialer struct{ dialing chan struct{} }

func (d hangingDialer) Dial(ctx context.Context, _ string, _ time.Duration) (Conn, error) {
	select {
	case d.dialing <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// When the console stops, a test that runs ends with it: nothing is
// stored, the state is back at rest, and Serve returns.
func TestServeEndsARunWhenTheConsoleStops(t *testing.T) {
	settings := newMapSettings()
	clock := &fakeClock{now: time.UnixMilli(1_700_000_000_000)}
	dialer := hangingDialer{dialing: make(chan struct{}, 1)}
	var mu sync.Mutex
	var lines []string
	s := New(Options{
		Settings: settings, Clock: clock, Dialer: dialer,
		Snapshot: func() Snapshot {
			return Snapshot{CPU: 10, MemTotal: 1 << 30, MemPercent: 20, CPUCount: 2, Hostname: "h"}
		},
		Logf: func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, format)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Serve(ctx)
		close(done)
	}()
	if !s.Start() {
		t.Fatal("did not start")
	}
	if s.Start() {
		t.Error("a second test started")
	}
	<-dialer.dialing
	cancel()
	<-done
	st := s.Status(context.Background())
	if st.Running || st.Phase != "cool-down" {
		t.Errorf("after the stop: running %v, phase %s", st.Running, st.Phase)
	}
	if len(settings.writes) != 0 {
		t.Errorf("stored %q", settings.writes)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "run failed") {
		t.Errorf("log %q", lines)
	}
}

// A failed save keeps the texts read before; a read that fails gives the
// defaults for that request, and the next one reads the row again (BUGS
// SA5).
func TestBadgeConfigIsReadOnce(t *testing.T) {
	ctx := context.Background()
	settings := newMapSettings()
	stored := `{"badgeTitle":"First"}`
	settings.rows[BadgeConfigKey] = &stored
	s := New(Options{Settings: settings})
	if got := s.BadgeConfig(ctx).BadgeTitle; got != "First" {
		t.Fatalf("title %q", got)
	}
	changed := `{"badgeTitle":"Changed"}`
	settings.rows[BadgeConfigKey] = &changed
	if got := s.BadgeConfig(ctx).BadgeTitle; got != "First" {
		t.Errorf("title after a change behind its back %q", got)
	}
	settings.failWrite = errors.New("disk full")
	if _, err := s.SaveBadgeConfig(ctx, `{"badgeTitle":"Third"}`); err == nil {
		t.Error("a failed write saved")
	}
	if got := s.BadgeConfig(ctx).BadgeTitle; got != "First" {
		t.Errorf("title after a failed save %q", got)
	}
	settings.failWrite = nil
	if c, err := s.SaveBadgeConfig(ctx, `{"badgeTitle":"  Fourth "}`); err != nil || c.BadgeTitle != "Fourth" {
		t.Errorf("save: %+v %v", c, err)
	}
	if got := s.BadgeConfig(ctx).BadgeTitle; got != "Fourth" {
		t.Errorf("title after a save %q", got)
	}

	failing := newMapSettings()
	failing.rows[BadgeConfigKey] = &stored
	failing.failing[BadgeConfigKey] = true
	s = New(Options{Settings: failing})
	if got := s.BadgeConfig(ctx); got != (BadgeConfig{}) {
		t.Errorf("unreadable texts %+v", got)
	}
	failing.failing[BadgeConfigKey] = false
	if got := s.BadgeConfig(ctx).BadgeTitle; got != "First" {
		t.Errorf("title after the failed read %q", got)
	}
}

// The status shows the stored result the first time it is asked for, and
// a falsy stored value as none.
func TestStatusLoadsTheStoredResultOnce(t *testing.T) {
	ctx := context.Background()
	settings := newMapSettings()
	stored := `{"tier":"iron"}`
	settings.rows[ResultKey] = &stored
	s := New(Options{Settings: settings})
	if got := s.Status(ctx); got.Running || got.Phase != "idle" || got.Progress != nil || got.LastResult == nil {
		t.Fatalf("status %+v", got)
	}
	settings.rows[ResultKey] = nil
	if got := s.Status(ctx).LastResult; got == nil {
		t.Error("the loaded result was read again")
	}
	zero := "0"
	settings.rows[ResultKey] = &zero
	if got := New(Options{Settings: settings}).Status(ctx).LastResult; got != nil {
		t.Errorf("stored 0 shows as %v", got)
	}
}

// The real clock returns at once for a time that passed, on wake and on
// the end of the context.
func TestRealClock(t *testing.T) {
	c := RealClock{}
	if ok, err := c.SleepUntil(context.Background(), c.Now().Add(-time.Second), nil); !ok || err != nil {
		t.Errorf("past time: %v %v", ok, err)
	}
	wake := make(chan struct{})
	close(wake)
	if ok, err := c.SleepUntil(context.Background(), c.Now().Add(time.Hour), wake); ok || err != nil {
		t.Errorf("woken: %v %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := c.SleepUntil(ctx, c.Now().Add(time.Hour), nil); ok || err == nil {
		t.Errorf("ended: %v %v", ok, err)
	}
}

// An abort ends the wait in progress at once, and every wait after it
// before it starts (BUGS SA1).
func TestAnAbortEndsTheWaitAtOnce(t *testing.T) {
	start := time.UnixMilli(1_700_000_000_000)
	clock := &fakeClock{now: start}
	s := New(Options{Settings: newMapSettings(), Clock: clock, Snapshot: func() Snapshot { return Snapshot{} }})
	if !s.Start() {
		t.Fatal("did not start")
	}
	r := <-s.starts
	clock.at(start.Add(3037*time.Millisecond), func() { s.RequestAbort() })
	if err := r.pause(context.Background(), baselineWait); err != errAborted {
		t.Fatalf("the wait ended with %v", err)
	}
	if got := clock.now.Sub(start); got != 3037*time.Millisecond {
		t.Errorf("the wait ended %v after its start, the abort came after 3.037s", got)
	}
	if err := r.pause(context.Background(), rampInterval); err != errAborted || clock.now.Sub(start) != 3037*time.Millisecond {
		t.Errorf("a wait after the abort: %v at %v", err, clock.now.Sub(start))
	}
}

// scriptedDialer connects or fails in the order of its script and keeps
// the connections it made.
type scriptedDialer struct {
	script []bool
	conns  []*trackedConn
}

type trackedConn struct{ closed, drained bool }

func (c *trackedConn) Send([]byte) error                                 { return nil }
func (c *trackedConn) AwaitMessage(context.Context, time.Duration) error { return nil }
func (c *trackedConn) Drain()                                            { c.drained = true }
func (c *trackedConn) Close()                                            { c.closed = true }

func (d *scriptedDialer) Dial(context.Context, string, time.Duration) (Conn, error) {
	ok := d.script[len(d.conns)%len(d.script)]
	c := &trackedConn{}
	d.conns = append(d.conns, c)
	if !ok {
		return nil, errors.New("connect ECONNREFUSED")
	}
	return c, nil
}

// A relay pair whose second connection fails closes its first one: it
// stays neither open nor outside the pool (BUGS SA3).
func TestAHalfOpenRelayPairIsClosed(t *testing.T) {
	dialer := &scriptedDialer{script: []bool{true, false}}
	s := New(Options{Settings: newMapSettings(), Dialer: dialer, Clock: &fakeClock{now: time.UnixMilli(1_700_000_000_000)}})
	if !s.Start() {
		t.Fatal("did not start")
	}
	r := <-s.starts
	if err := r.addRelayPairs(context.Background(), "ws://relay"); err != nil {
		t.Fatal(err)
	}
	if len(dialer.conns) != 2*batchRelay {
		t.Fatalf("%d dials", len(dialer.conns))
	}
	for i := 0; i < len(dialer.conns); i += 2 {
		if first := dialer.conns[i]; !first.closed {
			t.Errorf("the first connection of pair %d is left open", i/2)
		}
	}
	if _, _, pairs, _, attempts := r.counts(); pairs != 0 || attempts != 2*batchRelay || s.pool.failures != batchRelay {
		t.Errorf("%d pairs, %d attempts, %d failures", pairs, attempts, s.pool.failures)
	}
}
