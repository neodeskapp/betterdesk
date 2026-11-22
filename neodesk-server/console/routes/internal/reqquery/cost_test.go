package reqquery

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// deepIndexQuery is a query of about 32 KB whose 1,000 keys each ask for
// five nested lists with one element at index 999.
func deepIndexQuery() string {
	parts := make([]string, 1000)
	for i := range parts {
		parts[i] = fmt.Sprintf("k%d%s=x", i, strings.Repeat("[999]", 5))
	}
	return strings.Join(parts, "&")
}

// allocated is how many bytes f allocates.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestIndexCostsOneEntry: a list's element at index 999 costs one entry,
// not 1,000 places, so a 32 KB query costs memory in proportion to its
// length (BUGS.md RV99).
func TestIndexCostsOneEntry(t *testing.T) {
	query := deepIndexQuery()
	var q *Query
	if n := allocated(func() { q = of(query) }); n > 16<<20 {
		t.Errorf("a %d byte query allocated %d MB", len(query), n>>20)
	}
	if got, _ := json.Marshal(q.Get("k7").JSON()); string(got) != `[[[[["x"]]]]]` {
		t.Errorf("k7 = %s", got)
	}
}

// TestQueryMatchesExpress: the parser gives the values Express gave for
// the queries of package jsreq's vectors.
func TestQueryMatchesExpress(t *testing.T) {
	raw, err := os.ReadFile("../jsreq/testdata/query.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Queries []struct {
			Query string          `json:"query"`
			Value json.RawMessage `json:"value"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Queries) < 1000 {
		t.Fatalf("only %d queries", len(vectors.Queries))
	}
	for _, vec := range vectors.Queries {
		got, _ := json.Marshal(of(vec.Query).Get("a").JSON())
		want := []byte("null")
		if vec.Value != nil {
			parsed, err := goapi.Parse(vec.Value)
			if err != nil {
				t.Fatal(err)
			}
			want, _ = json.Marshal(parsed)
		}
		if string(got) != string(want) {
			t.Errorf("%.80q: %s, Express gave %s", vec.Query, got, want)
		}
	}
}
