package meshcentral

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// vector is one line of testdata/query.json.gz: what the Node console made
// of a query string (testdata/nodeoracle).
type vector struct {
	Q        string          `json:"q"`
	Search   json.RawMessage `json:"search"`
	Share    json.RawMessage `json:"share"`
	Guest    json.RawMessage `json:"guest"`
	ViewOnly bool            `json:"viewOnly"`
	Watch    json.RawMessage `json:"watch"`
}

// notText is the message of the error JavaScript raises when a value
// cannot be turned into text.
const notText = "Cannot convert object to primitive value"

// expected reads a recorded value: its text, or that JavaScript raised an
// error there.
func expected(t *testing.T, raw json.RawMessage) (text string, ok bool) {
	t.Helper()
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, true
	}
	var thrown struct {
		Throws string `json:"throws"`
	}
	if err := json.Unmarshal(raw, &thrown); err != nil || thrown.Throws != notText {
		t.Fatalf("vector value %s is neither a text nor the error %q", raw, notText)
	}
	return "", false
}

func TestQueryVectors(t *testing.T) {
	file, err := os.Open("testdata/query.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	lines := bufio.NewScanner(unzipped)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	count, failures := 0, 0
	for lines.Scan() {
		var v vector
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		count++
		check := func(what string, want json.RawMessage, got string, gotOK bool) {
			wantText, wantOK := expected(t, want)
			if got != wantText || gotOK != wantOK {
				failures++
				if failures <= 20 {
					t.Errorf("%q: %s = %q (text %v), Node has %s", v.Q, what, got, gotOK, want)
				}
			}
		}
		q := parseQuery(v.Q)
		// The routes write out only parameters that are one text each
		// (BUGS.md MC2); Node's text of a query is the rule for those
		if search, bad := q.only(q.values.keys()...); bad == "" {
			check("search", v.Search, search, true)
		}
		share, ok := trimmedText(q.get("mesh_share"))
		check("share", v.Share, share, ok)
		guest, ok := trimmedText(orValue(q.get("guest"), q.get("t")))
		check("guest", v.Guest, guest, ok)
		if got := truthy(q.get("view_only")); got != v.ViewOnly {
			failures++
			t.Errorf("%q: view_only is %v, Node has %v", v.Q, got, v.ViewOnly)
		}
		watching := parseQuery(v.Q)
		if !truthy(watching.get("view_only")) {
			watching.set("view_only", "1")
		}
		if watch, bad := watching.only(watching.values.keys()...); bad == "" {
			check("search with view_only", v.Watch, watch, true)
		}
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 6000 {
		t.Fatalf("read %d vectors, the file has more than 6000", count)
	}
	if failures > 20 {
		t.Errorf("%d differences in all", failures)
	}
}

func TestRequestQueryReadsTheRequestLine(t *testing.T) {
	cases := []struct{ target, want string }{
		{"/x", ""},
		{"/x?", ""},
		{"/x?a=1&b=2", "a=1&b=2"},
		{"/x?a=1#b=2", "a=1"},
		{"/x?a=1&b=%23", "a=1&b=%23"},
		// A byte above 0x7F is one Latin-1 letter, which goes out as UTF-8
		{"/x?a=\xe9", "a=%C3%A9"},
		{"/x?a=%E9", "a=%25E9"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x", nil)
		path, raw, _ := strings.Cut(c.target, "?")
		r.URL.Path, r.URL.RawQuery = path, raw
		q := requestQuery(r)
		got, bad := q.only(q.values.keys()...)
		if bad != "" || got != c.want {
			t.Errorf("%q: search = %q (bad %q), want %q", c.target, got, bad, c.want)
		}
	}
}

// FuzzParseQuery feeds the parser arbitrary query strings: it must not
// fail, and what it writes back must be a query string of plain
// characters that reads back as texts only, one per name.
func FuzzParseQuery(f *testing.F) {
	for _, seed := range []string{
		"", "a=1&a=2", "a[]=1&a[x]=2", "a[toString]=x", "mesh_share[]=abc&guest=%20", "a[1001]=x&a=y", "[0]=a&0=b",
		"a[a][b][c][d][e][f][g]=1", "a%5B%5D=1&%zz=%E9", "__proto__[x]=1&constructor=2", "a[=1&a]=2&=3&&",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		q := parseQuery(raw)
		search, bad := q.only(q.values.keys()...)
		if bad != "" {
			if _, isText := q.get(bad).(string); isText {
				t.Fatalf("%q: the text of %q was refused", raw, bad)
			}
			return
		}
		for i := 0; i < len(search); i++ {
			c := search[i]
			plain := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
			if !plain && !strings.ContainsRune("*-._+%&=", rune(c)) {
				t.Fatalf("%q was written as %q, with the character %q", raw, search, c)
			}
		}
		if n := strings.Count(search, "="); n != len(q.values.keys()) {
			t.Fatalf("%q was written as %q: %d pairs for %d parameters", raw, search, n, len(q.values.keys()))
		}
		for _, key := range q.values.keys() {
			if key == "__proto__" {
				t.Fatalf("%q gave a parameter named __proto__", raw)
			}
		}
	})
}

// TestListIndexCostsOneEntry: an index in a key costs one entry, not a
// place for every index below it. 1000 keys of five [999] groups, 32 KB,
// took about 170 MB when lists grew to their index (BUGS RV99).
func TestListIndexCostsOneEntry(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 1000; i++ {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString("k" + strconv.Itoa(i) + "[999][999][999][999][999]=x")
	}
	raw := b.String()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	q := parseQuery(raw)
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 20<<20 {
		t.Errorf("a query of %d bytes allocated %d MB", len(raw), allocated>>20)
	}
	if got := q.values.keys(); len(got) != 1000 {
		t.Fatalf("%d keys, want 1000", len(got))
	}
	v, _ := q.values.get("k7")
	for depth := 0; depth < 5; depth++ {
		list, ok := v.([]any)
		if !ok || len(list) != 1 {
			t.Fatalf("k7 at depth %d is %#v, want a list of one", depth, v)
		}
		v = list[0]
	}
	if v != "x" {
		t.Fatalf("k7's value is %#v, want x", v)
	}
}
