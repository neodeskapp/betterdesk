package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPairingAnswersGiveTheAPIWithTheRequestsScheme: the API address of
// the pairing answers is http:// for a plain request and https:// for one
// over TLS (L147).
func TestPairingAnswersGiveTheAPIWithTheRequestsScheme(t *testing.T) {
	srv := newPairingTestServer(t)
	plain := httptest.NewRequest(http.MethodPost, "/api/pairing/config", nil)
	plain.Host = "bd.example.org"
	if got := srv.connectionEndpoints(plain)["api"].(string); !strings.HasPrefix(got, "http://bd.example.org:") {
		t.Errorf("plain request: api %s, want http://bd.example.org:...", got)
	}
	secure := httptest.NewRequest(http.MethodPost, "/api/pairing/config", nil)
	secure.Host = "bd.example.org"
	secure.TLS = &tls.ConnectionState{}
	if got := srv.connectionEndpoints(secure)["api"].(string); !strings.HasPrefix(got, "https://bd.example.org:") {
		t.Errorf("request over TLS: api %s, want https://bd.example.org:...", got)
	}
}
