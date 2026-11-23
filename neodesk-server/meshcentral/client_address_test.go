package meshcentral

import (
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// TestClientIPTakesTheRightmostUntrustedForwardedAddress: behind a trusted
// proxy the gateway counts a client by the API's rule (L139): the rightmost
// X-Forwarded-For entry that is not a trusted proxy, before X-Real-IP, so
// an address the client wrote into a header itself is never taken.
func TestClientIPTakesTheRightmostUntrustedForwardedAddress(t *testing.T) {
	proxies, err := config.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{TrustProxy: true, TrustedProxies: proxies}
	cases := []struct{ name, remote, xff, realIP, want string }{
		{"client wrote its own entry", "10.0.0.2:5000", "6.6.6.6, 203.0.113.7", "", "203.0.113.7"},
		{"two trusted hops", "10.0.0.2:5000", "6.6.6.6, 203.0.113.7, 10.0.0.9", "", "203.0.113.7"},
		{"X-Real-IP without X-Forwarded-For", "10.0.0.2:5000", "", "203.0.113.8", "203.0.113.8"},
		{"socket not a trusted proxy", "198.51.100.9:5000", "203.0.113.7", "", "198.51.100.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/agent.ashx", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		if got := clientIP(req, cfg); got != c.want {
			t.Errorf("%s: clientIP = %q, want %q", c.name, got, c.want)
		}
	}
}
