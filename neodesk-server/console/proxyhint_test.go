package console

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// HTTPS-8: requests that come through a proxy TRUST_PROXY does not trust
// make the console log one hint, naming the proxy and the setting; a
// trusted proxy and a request without forwarded headers make none.
func TestUntrustedProxyIsLoggedOnce(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	note := func(h *untrustedProxyHint, trust httpx.TrustProxy, headers ...string) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "172.18.0.1:40000"
		for i := 0; i+1 < len(headers); i += 2 {
			r.Header.Set(headers[i], headers[i+1])
		}
		h.note(r, trust)
	}
	trusted, err := httpx.ParseTrustProxy("1")
	if err != nil {
		t.Fatal(err)
	}

	var quiet untrustedProxyHint
	note(&quiet, httpx.TrustProxy{})
	note(&quiet, trusted, "X-Forwarded-Proto", "https")
	if buf.Len() != 0 {
		t.Fatalf("logged without an untrusted proxy: %s", buf.String())
	}

	var hint untrustedProxyHint
	for i := 0; i < 3; i++ {
		note(&hint, httpx.TrustProxy{}, "X-Forwarded-Proto", "https", "X-Forwarded-For", "203.0.113.5")
	}
	out := buf.String()
	if strings.Count(out, "TRUST_PROXY") < 1 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "172.18.0.1") {
		t.Fatalf("want one line naming the proxy and TRUST_PROXY, got:\n%s", out)
	}
}
