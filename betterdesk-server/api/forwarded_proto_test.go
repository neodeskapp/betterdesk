package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unitronix/betterdesk-server/config"
)

// TestForceHTTPSHonoursForwardedProtoOnlyFromATrustedProxy: with
// ForceHTTPS a plain request passes on X-Forwarded-Proto https only when
// it comes from a trusted proxy; the pairing answer reads the forwarded
// scheme and host under the same rule (RV180).
func TestForceHTTPSHonoursForwardedProtoOnlyFromATrustedProxy(t *testing.T) {
	srv, _ := newInternalServer(t, func(c *config.Config) { c.ForceHTTPS = true })
	header := map[string]string{"X-API-Key": testViewerKey, "X-Forwarded-Proto": "https"}
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/peers", "", header); status != http.StatusForbidden {
		t.Errorf("forwarded https from any address: %d %s, want 403", status, body)
	}

	proxies, err := config.ParseTrustedProxies("203.0.113.0/24")
	if err != nil {
		t.Fatal(err)
	}
	srv.cfg.TrustProxy = true
	srv.cfg.TrustedProxies = proxies
	if status, body := listenerCall(t, srv, http.MethodGet, "/api/peers", "", header); status != http.StatusOK {
		t.Errorf("forwarded https from a trusted proxy: %d %s, want 200", status, body)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/pairing/config", nil)
	req.Host = "api.example.org"
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.RemoteAddr = "198.51.100.9:1234"
	endpoints := srv.connectionEndpoints(req)
	if got, want := endpoints["api"], "https://api.example.org:"+itoa(uint64(srv.cfg.APIPort)); got != want {
		t.Errorf("api endpoint from an untrusted address: %v, want %s", got, want)
	}
}
