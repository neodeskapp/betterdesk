package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// TestEnrollmentDecisionsNameTheDevice: approving, rejecting and allowing a
// request again log the device as the target, not the account.
func TestEnrollmentDecisionsNameTheDevice(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	log := audit.NewLogger("")
	log.UseStore(database)
	t.Cleanup(func() { log.Close() })
	srv.SetAuditLogger(log)
	handler := srv.InternalHandler()
	call := func(path, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:0"
		req = req.WithContext(WithIdentity(req.Context(), Identity{Username: "apikey:console-bridge", Role: auth.RoleAdmin, Actor: "alice"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, id := range []string{"AUDAPP1", "AUDREJ1"} {
		if err := database.SetConfig(pendingDevicePrefix+id, `{"device_id":"`+id+`","ip":"198.51.100.9"}`); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
	}
	call("/api/enrollment/approve/AUDAPP1", `{}`)
	call("/api/enrollment/reject/AUDREJ1", `{}`)
	call("/api/enrollment/clear-rejection/AUDREJ1", `{}`)

	events := log.Recent(3)
	want := map[string]string{"device_approved": "AUDAPP1", "device_rejected": "AUDREJ1", "enrollment_rejection_cleared": "AUDREJ1"}
	if len(events) != len(want) {
		t.Fatalf("events %+v", events)
	}
	for _, e := range events {
		if target, ok := want[string(e.Action)]; !ok || e.Target != target || e.Actor != "alice" {
			t.Errorf("%s by %q on %q, want by alice on %q", e.Action, e.Actor, e.Target, target)
		}
	}
}
