package jsreq

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// TestQueryIndexCostsOneEntry: a list's element at index 999 costs one
// entry, not 1,000 places, so a 32 KB query costs memory in proportion to
// its length (BUGS.md RV99).
func TestQueryIndexCostsOneEntry(t *testing.T) {
	parts := make([]string, 1000)
	for i := range parts {
		parts[i] = fmt.Sprintf("k[%d]%s=x", i, strings.Repeat("[999]", 4))
	}
	query := strings.Join(parts, "&")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := QueryValue(query, "k")
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 16<<20 {
		t.Errorf("a %d byte query allocated %d MB", len(query), n>>20)
	}
	if list, ok := got.([]any); !ok || len(list) != 1000 || encode(t, list[7]) != `[[[["x"]]]]` {
		t.Errorf("k = %.80s", encode(t, got))
	}
	if s := encode(t, QueryValue("k[999]=x", "k")); s != `["x"]` {
		t.Errorf("k[999]=x gives %s", s)
	}
}
