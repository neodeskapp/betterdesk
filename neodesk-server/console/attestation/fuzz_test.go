package attestation

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
)

// unitsOf counts UTF-16 units the way JavaScript does, a lone surrogate
// in WTF-8 as one.
func unitsOf(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if isSurrogateAt(s, i) {
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		i += size
	}
	return n
}

// FuzzSignInBadge feeds stored settings of any text to the sign-in badge.
// Whatever the settings hold, the badge is null or has a tier, a brand
// and a title, and the brand and title are clean text within their
// limits.
func FuzzSignInBadge(f *testing.F) {
	f.Add(`{"tier":"iron","valid":true}`, `{"badgeTitle":"x","tierLabels":{"iron":"y"}}`, "NeoDesk")
	f.Add(`{"tier":["iron"]}`, `[]`, "")
	f.Add(`{"tier":{"toString":1}}`, `{"badgeTitle":{"toString":1}}`, "  \x01  ")
	f.Add(`{"tier":"__proto__"}`, `null`, "Brand")
	f.Add(`{"tier":"constructor"}`, `{"badgeTitle":"\ud83d"}`, "\xed\xa0\xbd")
	f.Fuzz(func(t *testing.T, result, config, brand string) {
		settings := newMapSettings()
		settings.rows[ResultKey] = &result
		settings.rows[BadgeConfigKey] = &config
		badge := SignInBadge(context.Background(), settings, brand, func(key string) string { return key })
		if badge == nil {
			return
		}
		if got := strings.Join(badge.Keys(), ","); got != "tier,brand,badgeTitle,tierLabel" {
			t.Fatalf("keys %s", got)
		}
		for _, key := range []string{"brand", "badgeTitle"} {
			text := badge.Text(key)
			if text == "" || strings.ContainsAny(text, "\x00\x1f\x7f") {
				t.Fatalf("%s = %q", key, text)
			}
		}
		if n := unitsOf(badge.Text("brand")); n > BadgeTitleMax {
			t.Fatalf("brand of %d units", n)
		}
		_ = branding.Stringify(badge)
	})
}

// FuzzNormalizeBadgeConfig feeds any request body to the badge texts.
// They either fail with JavaScript's TypeError or come out clean: no
// control characters, no white space at the start, within the limits.
// White space at the end is left only by a cut at the limit, and reading
// the stored texts again removes just that.
func FuzzNormalizeBadgeConfig(f *testing.F) {
	f.Add(`{"badgeTitle":"  Custom\u0000 title  ","tierLabels":{"bronze":"Starter","iron":7}}`)
	f.Add(`{"badgeTitle":{"toString":1}}`)
	f.Add(`{"tierLabels":["a"],"badgeTitle":[1,[2,null]]}`)
	f.Add(`{"badgeTitle":"😀\ud83d"}`)
	f.Add(`[]`)
	f.Add(`not json`)
	f.Fuzz(func(t *testing.T, body string) {
		c, err := NormalizeBadgeConfig(body)
		if err != nil {
			if branding.Message(err) != "Cannot convert object to primitive value" {
				t.Fatalf("error %v", err)
			}
			return
		}
		again, err := NormalizeBadgeConfig(branding.Stringify(c.Object()))
		if err != nil {
			t.Fatalf("stored texts fail: %v", err)
		}
		texts := append([]string{c.BadgeTitle}, c.TierLabels[:]...)
		reread := append([]string{again.BadgeTitle}, again.TierLabels[:]...)
		for i, text := range texts {
			limit := BadgeLabelMax
			if i == 0 {
				limit = BadgeTitleMax
			}
			n := unitsOf(text)
			trimmed := branding.Trim(text)
			if n > limit || strings.ContainsAny(text, "\x00\x01\x1f\x7f") || !strings.HasPrefix(text, trimmed) ||
				(trimmed != text && n != limit) {
				t.Fatalf("text %d = %q", i, text)
			}
			if reread[i] != trimmed {
				t.Fatalf("text %d %q reads back as %q", i, text, reread[i])
			}
		}
	})
}
