package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

// TestWSEffectiveRemoteAddrTakesTheRightmostUntrustedForwardedAddress:
// behind a trusted proxy the websocket relay names a client by the API's
// rule (L139): the rightmost X-Forwarded-For entry that is not a trusted
// proxy, before X-Real-IP, with the socket's port when the entry has none.
func TestWSEffectiveRemoteAddrTakesTheRightmostUntrustedForwardedAddress(t *testing.T) {
	proxies, err := config.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{TrustProxy: true, TrustedProxies: proxies}
	cases := []struct{ name, remote, xff, realIP, want string }{
		{"client wrote its own entry", "10.0.0.2:50123", "6.6.6.6, 203.0.113.7", "", "203.0.113.7:50123"},
		{"X-Forwarded-For wins over X-Real-IP", "10.0.0.2:50123", "203.0.113.7", "6.6.6.6", "203.0.113.7:50123"},
		{"entry with a port", "10.0.0.2:50123", "6.6.6.6, 203.0.113.7:4444", "", "203.0.113.7:4444"},
		{"X-Real-IP without X-Forwarded-For", "10.0.0.2:50123", "", "203.0.113.8", "203.0.113.8:50123"},
		{"socket not a trusted proxy", "198.51.100.9:50123", "203.0.113.7", "", "198.51.100.9:50123"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/ws/relay", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		if got := wsEffectiveRemoteAddr(req, cfg); got != c.want {
			t.Errorf("%s: wsEffectiveRemoteAddr = %q, want %q", c.name, got, c.want)
		}
	}
}
