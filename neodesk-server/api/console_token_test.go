package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// The console's API tokens (ndt_..., package console/admintoken) work on
// the console's port only. The API server takes none: a request with one
// is a request without credentials.
func TestAPIServerRefusesConsoleAPITokens(t *testing.T) {
	server := New(config.DefaultConfig(), nil, peer.NewMap(), nil, "test")
	handler := server.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	token := "ndt_abcdefghijkm_" + strings.Repeat("A1", 20)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/peers"}, {http.MethodPost, "/api/users"}, {http.MethodGet, "/api/users"},
	} {
		request := httptest.NewRequest(c.method, c.path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with the token: status %d, want 401", c.method, c.path, recorder.Code)
		}
	}
}
