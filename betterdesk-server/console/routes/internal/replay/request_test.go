package replay

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
)

// TestRequestAsAConnectionMakesIt: the request of a step has the path,
// escaped path and query net/http makes of the same request line, so dot
// segments and a "#" stay in the path the router reads (BUGS.md RV105).
func TestRequestAsAConnectionMakesIt(t *testing.T) {
	for _, target := range []string{
		"/api/x/../y", "/downloads/a/.", "/downloads/./top.txt", "/api/x#frag", "/api/x?a=1#b=2",
		"/api/a%2Fb?q=%20", "/api/activity//D1?x=1", "/api/devices/%61bc", "/api/x?",
	} {
		r, err := newRequest("GET", target, nil)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		wire, err := http.ReadRequest(bufio.NewReader(strings.NewReader("GET " + target + " HTTP/1.1\r\nHost: console.test\r\n\r\n")))
		if err != nil {
			t.Fatalf("%s: net/http: %v", target, err)
		}
		if r.URL.Path != wire.URL.Path || r.URL.EscapedPath() != wire.URL.EscapedPath() || r.URL.RawQuery != wire.URL.RawQuery || r.RequestURI != wire.RequestURI {
			t.Errorf("%s: path %q, escaped %q, query %q; net/http %q, %q, %q", target, r.URL.Path, r.URL.EscapedPath(), r.URL.RawQuery,
				wire.URL.Path, wire.URL.EscapedPath(), wire.URL.RawQuery)
		}
	}
}
