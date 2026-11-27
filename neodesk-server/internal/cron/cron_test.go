package cron

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	from := time.Date(2026, 11, 27, 10, 7, 30, 0, time.UTC) // a Friday
	for _, c := range []struct{ expr, want string }{
		{"* * * * *", "2026-11-27 10:08"},
		{"*/5 * * * *", "2026-11-27 10:10"},
		{"0 * * * *", "2026-11-27 11:00"},
		{"30 2 * * *", "2026-11-28 02:30"},
		{"0 9 * * mon-fri", "2026-11-30 09:00"},
		{"0 9 * * 1-5", "2026-11-30 09:00"},
		{"0 0 * * 0", "2026-11-29 00:00"},
		{"0 0 * * 7", "2026-11-29 00:00"},
		{"0 0 1 * *", "2026-12-01 00:00"},
		{"15 10 27 11 *", "2026-11-27 10:15"},
		{"5 10 27 11 *", "2027-11-27 10:05"},
		{"0 0 29 2 *", "2028-02-29 00:00"},
		{"10/20 * * * *", "2026-11-27 10:10"},
		{"0 8-18/4 * * *", "2026-11-27 12:00"},
		{"0 0 1,15 jan,jul *", "2027-01-01 00:00"},
		// Day of month or day of week when both are set
		{"0 0 13 * fri", "2026-12-04 00:00"},
	} {
		s, err := Parse(c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got := s.Next(from).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("%s: next %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8",
		"*/0 * * * *", "5-1 * * * *", "a * * * *", "1,,2 * * * *", "0 0 30 2 *", "0 0 31 4 *",
	} {
		if _, err := Parse(expr); err == nil {
			t.Errorf("%q was accepted", expr)
		}
	}
}

func TestMatches(t *testing.T) {
	s, err := Parse("30 14 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Matches(time.Date(2026, 1, 2, 14, 30, 59, 0, time.UTC)) || s.Matches(time.Date(2026, 1, 2, 14, 31, 0, 0, time.UTC)) {
		t.Error("Matches is off")
	}
}

func TestNextKeepsTheLocation(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	s, _ := Parse("0 3 * * *")
	// 2026-03-29: the clocks go from 02:00 to 03:00 in Berlin
	got := s.Next(time.Date(2026, 3, 28, 12, 0, 0, 0, berlin))
	if got.Format("2006-01-02 15:04 MST") != "2026-03-29 03:00 CEST" {
		t.Errorf("next %s", got.Format("2006-01-02 15:04 MST"))
	}
}
