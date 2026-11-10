package auth

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// FuzzBadge feeds stored settings of any text to the badge. Whatever the
// settings hold, the badge is null or has a tier, a brand and a title, and
// the brand and title are clean text within their limits.
func FuzzBadge(f *testing.F) {
	f.Add(`{"tier":"iron","valid":true}`, `{"badgeTitle":"x","tierLabels":{"iron":"y"}}`, "BetterDesk")
	f.Add(`{"tier":["iron"]}`, `[]`, "")
	f.Add(`{"tier":{"toString":1}}`, `{"badgeTitle":{"toString":1}}`, "  \x01  ")
	f.Add(`{"tier":"__proto__"}`, `null`, "Brand")
	f.Fuzz(func(t *testing.T, result, config, brand string) {
		same := func(key string) string { return key }
		badge := badgeFor(&result, &config, true, brand, same)
		if badge == nil {
			return
		}
		if got := strings.Join(badge.Keys(), ","); got != "tier,brand,badgeTitle,tierLabel" && got != "tier,brand,badgeTitle" {
			t.Fatalf("keys %s", got)
		}
		for _, key := range []string{"brand", "badgeTitle"} {
			text := badge.Text(key)
			if text == "" || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\x1f\x7f") {
				t.Fatalf("%s = %q", key, text)
			}
		}
		if n := len(utf16Units(badge.Text("brand"))); n > badgeTitleMax {
			t.Fatalf("brand of %d units", n)
		}
		_ = branding.Stringify(badge)
	})
}

func utf16Units(s string) []rune {
	var out []rune
	for _, r := range s {
		out = append(out, r)
		if r >= 0x10000 {
			out = append(out, 0)
		}
	}
	return out
}
