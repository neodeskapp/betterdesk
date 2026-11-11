package devices

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

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
