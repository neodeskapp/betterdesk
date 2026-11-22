package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestQueryIndexCostsOneEntry: the lang parameter's list element at index
// 999 costs one entry, not 1,000 places, so a 32 KB query costs memory in
// proportion to its length. Every request's language is read from the
// query (BUGS.md RV99).
func TestQueryIndexCostsOneEntry(t *testing.T) {
	parts := make([]string, 1000)
	for i := range parts {
		parts[i] = fmt.Sprintf("lang[%d]%s=x", i, strings.Repeat("[999]", 4))
	}
	query := strings.Join(parts, "&")
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := qsParam(query, "lang")
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 16<<20 {
		t.Errorf("a %d byte query allocated %d MB", len(query), n>>20)
	}
	if s := stringify(qsParam("lang[999]=x", "lang"), ""); s != `["x"]` {
		t.Errorf("lang[999]=x gives %s", s)
	}
	if list, ok := got.([]any); !ok || len(list) != 1000 {
		t.Errorf("the long query gives %.80s", stringify(got, ""))
	}
}

// TestQueryParamMatchesExpress: the parser gives the values Express gave
// for the queries of package jsreq's vectors.
func TestQueryParamMatchesExpress(t *testing.T) {
	raw, err := os.ReadFile("../routes/internal/jsreq/testdata/query.json")
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
		got := qsParam(vec.Query, "a")
		if vec.Value == nil {
			if got != undefined {
				t.Errorf("%.80q: %s, Express left the parameter out", vec.Query, stringify(got, ""))
			}
			continue
		}
		want, err := parseJSON(string(vec.Value))
		if err != nil {
			t.Fatal(err)
		}
		if stringify(got, "") != stringify(want, "") {
			t.Errorf("%.80q: %s, Express gave %s", vec.Query, stringify(got, ""), stringify(want, ""))
		}
	}
}
