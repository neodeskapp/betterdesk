package registrations

import (
	"math"
	"testing"
	"time"
)

// FuzzParseDate reads any text the API server or a database could give as
// a time: it never panics, a time it reads lies within the range of a
// JavaScript date and is whole milliseconds, and its ISO text reads back
// as the same time.
func FuzzParseDate(f *testing.F) {
	for _, s := range []string{
		"", "2026", "2026-11", "2026-11-18", "2026-11-18Z", "2026-11-18 10:00:00", "2026-11-18T10:00",
		"2026-11-18T10:00:00.123456789Z", "2026-11-18t10:00:00+02:00", "2026-11-18T10:00:00-0130",
		"+275760-09-13T00:00:00.000Z", "+010000-01-01T00:00:00.000Z", "-000001-01-01T00:00:00.000Z", "-000000-01-01", "2026-02-31",
		"2026-11-18T24:00:00Z", "2026-11-18T23:59:60Z", "nonsense", "9999-12-31T23:59:59.999+23:59",
	} {
		f.Add(s)
	}
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, loc := range []*time.Location{time.UTC, berlin} {
			ms, ok := parseDate(s, loc)
			if !ok {
				continue
			}
			if math.IsNaN(ms) || math.Abs(ms) > maxTime || ms != math.Trunc(ms) {
				t.Fatalf("parseDate(%q) = %v", s, ms)
			}
			iso := isoString(ms)
			back, ok := parseDate(iso, loc)
			if !ok || back != ms {
				t.Fatalf("parseDate(%q) = %v, its text %q reads as %v %v", s, ms, iso, back, ok)
			}
		}
	})
}
