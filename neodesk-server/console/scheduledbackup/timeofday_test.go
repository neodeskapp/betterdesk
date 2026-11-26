package scheduledbackup

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A schedule with a time of day runs at that time in the server's time
// zone, not counted from when it was saved: "every day at 02:00" saved at
// 10:00 runs at 02:00 the next night, and every night after.

func newClockedService(t *testing.T, loc *time.Location) (*Service, *fakeBackups, *clock) {
	t.Helper()
	s, fb, clk, _ := newTestService(t)
	s.o.Location = loc
	return s, fb, clk
}

func at(t *testing.T, loc *time.Location, text string) time.Time {
	t.Helper()
	v, err := time.ParseInLocation("2006-01-02 15:04", text, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func setClock(clk *clock, t time.Time) {
	clk.mu.Lock()
	clk.t = t
	clk.mu.Unlock()
}

func mustSave(t *testing.T, s *Service, body string) Config {
	t.Helper()
	c, err := s.SaveConfig(context.Background(), mustParse(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func wantNext(t *testing.T, s *Service, want time.Time) {
	t.Helper()
	got, on, err := s.NextRun(context.Background())
	if err != nil || !on || !got.Equal(want) {
		t.Fatalf("next run %v (on %v, %v), want %v", got, on, err, want)
	}
}

func wantSkip(t *testing.T, s *Service, why string) {
	t.Helper()
	skip, res, err := s.RunIfDue(context.Background())
	if err != nil || res != nil || skip == nil || skip.Reason != why {
		t.Fatalf("want skip %q, got %+v %+v %v", why, skip, res, err)
	}
}

func wantRun(t *testing.T, s *Service) {
	t.Helper()
	skip, res, err := s.RunIfDue(context.Background())
	if err != nil || skip != nil || res == nil {
		t.Fatalf("want a run, got %+v %v", skip, err)
	}
}

func TestDailyAtATimeOfDay(t *testing.T) {
	loc := time.UTC
	s, fb, clk := newClockedService(t, loc)
	setClock(clk, at(t, loc, "2026-11-10 10:00"))
	c := mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"02:00","retentionCount":14}`)
	if c.TimeOfDay != "02:00" || c.RetentionCount != 14 {
		t.Fatalf("saved %+v", c)
	}
	// Saved at 10:00: nothing runs now, the first backup is at 02:00 tonight
	wantSkip(t, s, "not_due")
	wantNext(t, s, at(t, loc, "2026-11-11 02:00"))
	setClock(clk, at(t, loc, "2026-11-11 01:59"))
	wantSkip(t, s, "not_due")
	setClock(clk, at(t, loc, "2026-11-11 02:00"))
	wantRun(t, s)
	clk.add(time.Minute)
	wantSkip(t, s, "not_due")
	wantNext(t, s, at(t, loc, "2026-11-12 02:00"))
	// The server was off at 02:00: the backup runs when it is back, and the
	// next one is at 02:00 again
	setClock(clk, at(t, loc, "2026-11-12 09:30"))
	wantRun(t, s)
	wantNext(t, s, at(t, loc, "2026-11-13 02:00"))
	if fb.made != 2 {
		t.Errorf("%d archives, want 2", fb.made)
	}
}

func TestTimeOfDayIsTheServersZone(t *testing.T) {
	berlin := time.FixedZone("CET", 3600)
	s, _, clk := newClockedService(t, berlin)
	setClock(clk, at(t, time.UTC, "2026-11-10 10:00"))
	mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"02:00"}`)
	wantNext(t, s, at(t, time.UTC, "2026-11-11 01:00"))
	if got := s.TimeZone(); got != "CET" {
		t.Errorf("time zone %q, want CET", got)
	}
}

func TestWeeklyAtATimeOfDay(t *testing.T) {
	loc := time.UTC
	s, _, clk := newClockedService(t, loc)
	setClock(clk, at(t, loc, "2026-11-10 10:00"))
	mustSave(t, s, `{"enabled":true,"intervalHours":168,"timeOfDay":"03:30"}`)
	wantNext(t, s, at(t, loc, "2026-11-11 03:30"))
	setClock(clk, at(t, loc, "2026-11-11 03:30"))
	wantRun(t, s)
	wantNext(t, s, at(t, loc, "2026-11-18 03:30"))
	setClock(clk, at(t, loc, "2026-11-17 03:30"))
	wantSkip(t, s, "not_due")
}

func TestSavingOtherSettingsKeepsTheTime(t *testing.T) {
	loc := time.UTC
	s, _, clk := newClockedService(t, loc)
	setClock(clk, at(t, loc, "2026-11-10 10:00"))
	mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"02:00"}`)
	// A new number to keep at 01:00 does not move tonight's backup
	setClock(clk, at(t, loc, "2026-11-11 01:00"))
	mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"02:00","retentionCount":14}`)
	wantNext(t, s, at(t, loc, "2026-11-11 02:00"))
	// A new time counts from the save
	mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"01:30"}`)
	wantNext(t, s, at(t, loc, "2026-11-11 01:30"))
	mustSave(t, s, `{"enabled":true,"intervalHours":24,"timeOfDay":"00:30"}`)
	wantNext(t, s, at(t, loc, "2026-11-12 00:30"))
}

func TestIntervalWithoutATimeCountsFromTheLastBackup(t *testing.T) {
	loc := time.UTC
	s, _, clk := newClockedService(t, loc)
	setClock(clk, at(t, loc, "2026-11-10 10:00"))
	mustSave(t, s, `{"enabled":true,"intervalHours":24}`)
	// As before: the first backup within a minute, then 24 hours after it
	wantNext(t, s, at(t, loc, "2026-11-10 10:00"))
	wantRun(t, s)
	wantNext(t, s, at(t, loc, "2026-11-11 10:00"))
	mustSave(t, s, `{"enabled":false}`)
	if _, on, err := s.NextRun(context.Background()); on || err != nil {
		t.Errorf("a schedule that is off has a next run (%v)", err)
	}
}

func TestTimeOfDayValues(t *testing.T) {
	for _, c := range []struct{ body, want string }{
		{`{"intervalHours":24,"timeOfDay":"02:00"}`, "02:00"},
		{`{"intervalHours":168,"timeOfDay":"23:59"}`, "23:59"},
		{`{"intervalHours":48,"timeOfDay":"00:00"}`, "00:00"},
		// only a whole number of days has a time of day
		{`{"intervalHours":6,"timeOfDay":"02:00"}`, ""},
		{`{"intervalHours":24,"timeOfDay":"2:00"}`, ""},
		{`{"intervalHours":24,"timeOfDay":"24:00"}`, ""},
		{`{"intervalHours":24,"timeOfDay":"02:60"}`, ""},
		{`{"intervalHours":24,"timeOfDay":2}`, ""},
		{`{"intervalHours":24,"timeOfDay":""}`, ""},
		{`{"intervalHours":24}`, ""},
	} {
		got, err := Normalize(mustParse(t, c.body))
		if err != nil || got.TimeOfDay != c.want {
			t.Errorf("%s: time of day %q (%v), want %q", c.body, got.TimeOfDay, err, c.want)
		}
	}
	// A schedule without a time of day is stored and answered as before
	if got := ConfigJSON(Config{IntervalHours: 24, RetentionCount: 7, DestinationType: "local"}); strings.Contains(got, "timeOfDay") || strings.Contains(got, "changedAt") {
		t.Errorf("no time of day, but %s", got)
	}
	if got := ConfigJSON(Config{IntervalHours: 24, RetentionCount: 7, DestinationType: "local", TimeOfDay: "02:00"}); !strings.Contains(got, `"timeOfDay":"02:00"`) {
		t.Errorf("time of day missing: %s", got)
	}
}
