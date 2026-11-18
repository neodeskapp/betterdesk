package servermanagement

import (
	"math"
	"testing"

	"github.com/unitronix/betterdesk-server/console/servermgmt"
)

// FuzzGoDuration reads any text as the API server's uptime: no part is
// nil, and the seconds are whole, or NaN where a part had no digits.
func FuzzGoDuration(f *testing.F) {
	for _, seed := range []string{"26h3m4.5s", "1.5.5s", "..s", "3µs", "10ms5s", "1d", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		got := goDurationSeconds(text)
		if !durationPart.MatchString(text) {
			if got != nil {
				t.Fatalf("%q: %v without a part", text, got)
			}
			return
		}
		n, ok := got.(servermgmt.Num)
		if !ok {
			t.Fatalf("%q: %v", text, got)
		}
		if f := float64(n); !math.IsNaN(f) && !math.IsInf(f, 0) && f != math.Floor(f) {
			t.Fatalf("%q: %v is not whole", text, f)
		}
	})
}
