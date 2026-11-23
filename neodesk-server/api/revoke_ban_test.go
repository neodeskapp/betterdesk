package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/security"
)

// TestRevokeBanSurvivesARestart: Revoke device bans the device ID in the
// database, with and without Permanently delete, so the blocklist of the
// next start still refuses it (L127).
func TestRevokeBanSurvivesARestart(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	blocklist := security.NewBlocklist()
	if err := blocklist.UseStore(database); err != nil {
		t.Fatal(err)
	}
	srv.SetBlocklist(blocklist)
	handler := srv.InternalHandler()
	for _, path := range []string{"/api/peers/PEERAAA1?revoke=true&hard=true", "/api/peers/PEERBBB2?revoke=true"} {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}

	restarted := security.NewBlocklist()
	if err := restarted.UseStore(database); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"PEERAAA1", "PEERBBB2"} {
		if !restarted.IsIDBlocked(id) {
			t.Errorf("%s is not blocked after a restart", id)
		}
	}
}
