package api

import (
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

// TestRemoteIPTakesTheRightmostUntrustedForwardedAddress: behind trusted
// proxies the client is the rightmost X-Forwarded-For entry that is not a
// trusted proxy, so an address the client wrote into the header itself
// (left of what the proxy appended) is never taken (L88).
func TestRemoteIPTakesTheRightmostUntrustedForwardedAddress(t *testing.T) {
	proxies, err := config.ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: &config.Config{TrustProxy: true, TrustedProxies: proxies}}

	cases := []struct {
		name     string
		remote   string
		xff      []string
		realIP   string
		expected string
	}{
		{"client wrote its own entry", "10.0.0.2:5000", []string{"6.6.6.6, 203.0.113.7"}, "", "203.0.113.7"},
		{"two trusted hops", "10.0.0.2:5000", []string{"6.6.6.6, 203.0.113.7, 10.0.0.9"}, "", "203.0.113.7"},
		{"entries over several header lines", "10.0.0.2:5000", []string{"6.6.6.6", "203.0.113.7"}, "", "203.0.113.7"},
		{"every hop trusted", "10.0.0.2:5000", []string{"10.0.0.8, 10.0.0.9"}, "", "10.0.0.8"},
		{"entry with a port", "10.0.0.2:5000", []string{"6.6.6.6, 203.0.113.7:4444"}, "", "203.0.113.7"},
		{"IPv6 entry with a port", "10.0.0.2:5000", []string{"[2001:db8::7]:4444"}, "", "2001:db8::7"},
		{"entry that is no address", "10.0.0.2:5000", []string{"203.0.113.7, unknown"}, "", "10.0.0.2"},
		{"X-Real-IP without X-Forwarded-For", "10.0.0.2:5000", nil, "203.0.113.8", "203.0.113.8"},
		{"X-Forwarded-For wins over X-Real-IP", "10.0.0.2:5000", []string{"6.6.6.6, 203.0.113.7"}, "6.6.6.6", "203.0.113.7"},
		{"socket not a trusted proxy", "198.51.100.9:5000", []string{"203.0.113.7"}, "203.0.113.8", "198.51.100.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.RemoteAddr = c.remote
		for _, v := range c.xff {
			req.Header.Add("X-Forwarded-For", v)
		}
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		if got := srv.remoteIP(req); got != c.expected {
			t.Errorf("%s: remoteIP = %q, want %q", c.name, got, c.expected)
		}
	}

	off := &Server{cfg: &config.Config{TrustedProxies: proxies}}
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.RemoteAddr = "10.0.0.2:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	if got := off.remoteIP(req); got != "10.0.0.2" {
		t.Errorf("TRUST_PROXY off: remoteIP = %q, want the socket", got)
	}
}
