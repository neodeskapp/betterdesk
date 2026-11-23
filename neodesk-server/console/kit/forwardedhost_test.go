package kit

import (
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// X-Forwarded-Host counts only from a proxy the console trusts
// (TRUST_PROXY). The client addresses, config string, QR code and install
// script were built from it whatever its source, so any request could
// name another server (BUGS L377).
func TestForwardedHostOnlyFromATrustedProxy(t *testing.T) {
	trusted, err := httpx.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		trust  httpx.TrustProxy
		remote string
		kept   bool
	}{
		{"no TRUST_PROXY", httpx.TrustProxy{}, "203.0.113.9:4000", false},
		{"an untrusted hop", trusted, "203.0.113.9:4000", false},
		{"a trusted proxy", trusted, "127.0.0.1:4000", true},
	} {
		r := httptest.NewRequest("GET", "http://console.example/api/settings/client-config", nil)
		r.RemoteAddr = c.remote
		r.Header.Set("X-Forwarded-Host", "evil.example")
		r = Begin(r, c.trust, nil)
		if got := r.Header.Get("X-Forwarded-Host"); (got == "evil.example") != c.kept {
			t.Errorf("%s: X-Forwarded-Host %q", c.name, got)
		}
	}
}
