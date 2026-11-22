package branding

import (
	"strings"
	"testing"
	"time"
)

// TestCleanersStayFastOnNestedWords: a value built so that every pass of
// the cleaners frees one more match costs a bounded number of passes
// over a bounded part of the value, and what comes out holds none of the
// words (BUGS.md RV191).
func TestCleanersStayFastOnNestedWords(t *testing.T) {
	const n = 16000
	for _, c := range []struct {
		name, in, gone string
		clean          func(string) string
	}{
		{"custom CSS expression(", strings.Repeat("ex", n) + strings.Repeat("pression(", n), "expression(", func(s string) string { return sanitizeCustomCSS(s) }},
		{"custom CSS @import", strings.Repeat("@im", n) + strings.Repeat("port;", n), "@import", func(s string) string { return sanitizeCustomCSS(s) }},
		{"colour url(", strings.Repeat("ur", n) + strings.Repeat("l(", n), "url(", func(s string) string { return sanitizeCSSColorValue(s) }},
		// The 2 MB a request body may hold
		{"2 MB of custom CSS", strings.Repeat("ex", 10*n) + strings.Repeat("pression(", 10*n), "expression(", func(s string) string { return sanitizeCustomCSS(s) }},
	} {
		start := time.Now()
		out := c.clean(c.in)
		// The time says nothing under the race detector
		if took := time.Since(start); took > 4*time.Second && !raceDetector {
			t.Errorf("%s: %v for %d characters", c.name, took, len(c.in))
		}
		if strings.Contains(strings.ToLower(out), c.gone) {
			t.Errorf("%s: %q left in %.60q", c.name, c.gone, out)
		}
	}
}
