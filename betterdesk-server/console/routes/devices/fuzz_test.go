package devices

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// FuzzBearerToken checks the test that hands a request to the RustDesk
// client API against its definition: the header starts with "Bearer "
// and the rest, read as Latin-1 and trimmed as JavaScript trims, is not
// empty.
func FuzzBearerToken(f *testing.F) {
	for _, seed := range []string{"", "Bearer", "Bearer ", "Bearer x", "Bearer \t\n x \xa0", "bearer x", "Bearer \xa0\xa0", "Basic x", "Bearerx", "Bearer a b", "Bearer 　"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		rest, ok := strings.CutPrefix(header, "Bearer ")
		latin1 := make([]rune, len(rest))
		for i := 0; i < len(rest); i++ {
			latin1[i] = rune(rest[i])
		}
		want := ok && jsTrim(string(latin1)) != ""
		if got := hasBearerToken(header); got != want {
			t.Fatalf("%q: %v, want %v", header, got, want)
		}
	})
}

// FuzzBodyValues reads values of request bodies the way the routes do:
// String(), the length check of a new id and stored times never panic,
// and String() fails only where JavaScript throws, for an object with a
// member named toString.
func FuzzBodyValues(f *testing.F) {
	for _, seed := range []string{`"x"`, `5`, `true`, `null`, `[1,[2,null],"a"]`, `{"toString":1}`, `[{"toString":1}]`, `{"length":"7"}`, `"2020-01-01 10:00:00"`, `1e400`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		v, err := goapi.Parse([]byte(raw))
		if err != nil {
			return
		}
		s, err := text(v)
		if err != nil && !strings.Contains(raw, "toString") {
			t.Fatalf("String(%s) failed: %v", raw, err)
		}
		if err == nil && strings.Contains(s, "\x00") && !strings.Contains(raw, `\u0000`) {
			t.Fatalf("String(%s) = %q", raw, s)
		}
		_ = newIDLength(v)
		_ = parseStoredTime(v)
		_ = spread(v)
	})
}
