// Package cron reads five-field cron expressions (minute, hour, day of
// month, month, day of week) and finds the next time one matches.
//
// A field is *, a number, a range a-b, a list of those separated by
// commas, and each may have a step: */15, 1-30/5, 10/20 (from 10 to the
// end of the field). Months take jan-dec, days of week sun-sat; 0 and 7
// are both Sunday. As in classic cron, when both the day of month and the
// day of week are restricted, a day matches when either does.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed expression.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domAny and dowAny say the field was "*" (or "*/1").
	domAny, dowAny bool
}

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	months = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	weekdays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
	fields   = []field{
		{"minute", 0, 59, nil},
		{"hour", 0, 23, nil},
		{"day of month", 1, 31, nil},
		{"month", 1, 12, months},
		{"day of week", 0, 7, weekdays},
	}
)

// Parse reads expr. An expression that never matches (30 February) is
// refused too.
func Parse(expr string) (*Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron: want 5 fields (minute hour day month weekday), got %d", len(parts))
	}
	var bits [5]uint64
	var wild [5]bool
	for i, part := range parts {
		b, all, err := parseField(part, fields[i])
		if err != nil {
			return nil, err
		}
		bits[i], wild[i] = b, all
	}
	// Sunday is 0 and 7
	if bits[4]&(1<<7) != 0 {
		bits[4] |= 1
		bits[4] &^= 1 << 7
	}
	s := &Schedule{minute: bits[0], hour: bits[1], dom: bits[2], month: bits[3], dow: bits[4], domAny: wild[2], dowAny: wild[4]}
	if s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)).IsZero() {
		return nil, fmt.Errorf("cron: %q never matches", expr)
	}
	return s, nil
}

func parseField(text string, f field) (uint64, bool, error) {
	var bits uint64
	all := false
	for _, item := range strings.Split(text, ",") {
		if item == "" {
			return 0, false, fmt.Errorf("cron: empty item in the %s field", f.name)
		}
		rangePart, step := item, 1
		if i := strings.IndexByte(item, '/'); i >= 0 {
			n, err := strconv.Atoi(item[i+1:])
			if err != nil || n <= 0 {
				return 0, false, fmt.Errorf("cron: bad step %q in the %s field", item[i+1:], f.name)
			}
			rangePart, step = item[:i], n
		}
		lo, hi := f.min, f.max
		switch {
		case rangePart == "*":
			if step == 1 {
				all = true
			}
		case strings.Contains(rangePart, "-"):
			a, b, _ := strings.Cut(rangePart, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, false, err
			}
			if hi, err = value(b, f); err != nil {
				return 0, false, err
			}
			if lo > hi {
				return 0, false, fmt.Errorf("cron: range %q in the %s field runs backwards", rangePart, f.name)
			}
		default:
			v, err := value(rangePart, f)
			if err != nil {
				return 0, false, err
			}
			lo = v
			if !strings.Contains(item, "/") {
				hi = v
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, all, nil
}

func value(text string, f field) (int, error) {
	if v, ok := f.names[strings.ToLower(text)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("cron: %q is no %s (%d-%d)", text, f.name, f.min, f.max)
	}
	return v, nil
}

func has(bits uint64, v int) bool { return bits&(1<<uint(v)) != 0 }

func (s *Schedule) dayMatches(t time.Time) bool {
	dom, dow := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dow
	case s.dowAny:
		return dom
	default:
		return dom || dow
	}
}

// Matches reports whether the minute of t matches.
func (s *Schedule) Matches(t time.Time) bool {
	return has(s.month, int(t.Month())) && s.dayMatches(t) && has(s.hour, t.Hour()) && has(s.minute, t.Minute())
}

// Next is the first matching minute after t, in t's location; zero when
// none comes within five years.
func (s *Schedule) Next(after time.Time) time.Time {
	loc := after.Location()
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !has(s.month, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if !has(s.hour, t.Hour()) {
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			if !next.After(t) { // a clock change repeats the hour
				next = t.Add(time.Hour).Truncate(time.Hour)
			}
			t = next
			continue
		}
		if !has(s.minute, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}
