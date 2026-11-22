package serverinfo

import (
	"net/http/httptest"
	"testing"
)

func TestQueryString(t *testing.T) {
	for _, c := range []struct{ query, want string }{
		{"host=a.example", "a.example"},
		{"x=1&host=a.example&y=2", "a.example"},
		{"host=a+b%20c", "a b c"},
		{"%68ost=a.example", "a.example"},
		{"host=", ""},
		{"host", ""},
		{"", ""},
		{"other=a.example", ""},
		{"Host=a.example", ""},
		{"host=a.example&host=b.example", ""},
		{"host=a.example&host=", ""},
		{"host[]=a.example", ""},
		{"host[0]=a.example", ""},
		{"host[x]=a.example", ""},
		{"host%5Bx%5D=a.example", ""},
		{"host=a.example&host[x]=1", ""},
		{"hostname=a.example", ""},
		{"host=%zz", "%zz"},
		{"&&host=a.example&&", "a.example"},
	} {
		r := httptest.NewRequest("GET", "/api/keys/server-info", nil)
		r.URL.RawQuery = c.query
		if got := QueryString(r, "host"); got != c.want {
			t.Errorf("QueryString(%q) = %q, want %q", c.query, got, c.want)
		}
	}
}
