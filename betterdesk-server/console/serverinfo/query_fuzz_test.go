package serverinfo

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// FuzzQueryString reads any query: a value sent once under the name comes
// back as it was, and a name that is not in the query gives nothing.
func FuzzQueryString(f *testing.F) {
	for _, seed := range []string{"host=a.example", "host=a&host=b", "host[]=a", "host%5Bx%5D=a", "x=1&host=a+b%20c&y", "host=%zz", "%68ost=a", "&&=&host", "host=a;b", ""} {
		f.Add(seed, "a.example org")
	}
	f.Fuzz(func(t *testing.T, query, value string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.URL.RawQuery = query
		got := QueryString(r, "host")
		if got != "" && !strings.Contains(strings.ToLower(query), "h") && !strings.Contains(query, "%68") {
			t.Fatalf("query %q gives %q without naming host", query, got)
		}
		if again := QueryString(r, "host"); again != got {
			t.Fatalf("query %q gives %q and then %q", query, got, again)
		}

		// One host parameter with any value, among parameters of other names
		r.URL.RawQuery = "a=1&host=" + url.QueryEscape(value) + "&b=2"
		if got := QueryString(r, "host"); got != value {
			t.Fatalf("value %q comes back as %q", value, got)
		}
		r.URL.RawQuery += "&host=" + url.QueryEscape(value)
		if got := QueryString(r, "host"); got != "" {
			t.Fatalf("value %q sent twice comes back as %q", value, got)
		}
	})
}
