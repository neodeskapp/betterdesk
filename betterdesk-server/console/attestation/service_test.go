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
// defaults, and they are kept until a save.
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
	if got := s.BadgeConfig(ctx); got != (BadgeConfig{}) {
		t.Errorf("texts after the failed read %+v", got)
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

// The first look of a wait after an abort.
func TestNextLook(t *testing.T) {
	start := time.UnixMilli(10_000)
	cases := map[int64]int64{9_000: 10_200, 10_000: 10_200, 10_001: 10_200, 10_200: 10_200, 10_201: 10_400, 13_037: 13_200}
	for at, want := range cases {
		if got := nextLook(start, time.UnixMilli(at)).UnixMilli(); got != want {
			t.Errorf("nextLook(10000, %d) = %d, want %d", at, got, want)
		}
	}
}
