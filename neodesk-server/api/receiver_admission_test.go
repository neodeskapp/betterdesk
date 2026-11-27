package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
)

// admission asks for an admission of a quick support session as the
// console does.
func admission(t *testing.T, srv *Server) string {
	t.Helper()
	status, raw := asConsole(t, srv, http.MethodPost, "/api/onboard/admissions", `{"session":"0123456789abcdef"}`)
	var out struct {
		Success   bool   `json:"success"`
		Admission string `json:"admission"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || status != http.StatusOK || !out.Success || len(out.Admission) != receiverAdmissionLen {
		t.Fatalf("admission: %d %s", status, raw)
	}
	end, err := time.Parse(time.RFC3339, out.ExpiresAt)
	if err != nil || end.Sub(srv.clock()) > ReceiverAdmissionLifetime || end.Sub(srv.clock()) < ReceiverAdmissionLifetime-time.Minute {
		t.Fatalf("admission ends at %q, want 24 hours from now", out.ExpiresAt)
	}
	return out.Admission
}

// An admission admits one device in managed and locked mode without a join
// request, and no second one.
func TestQuickSupportAdmissionAdmitsOnce(t *testing.T) {
	for _, mode := range []string{config.EnrollmentModeManaged, config.EnrollmentModeLocked} {
		t.Run(mode, func(t *testing.T) {
			placementStores(t, mode, func(t *testing.T, f *placementFixture) {
				secret := admission(t, f.srv)
				first := f.register(t, map[string]any{"device_id": "ADMIT0001", "admission": secret})
				if first.Status != "approved" || first.DeviceToken == "" {
					t.Fatalf("first device: %+v, want approved with a device token", first)
				}
				if p, _ := f.srv.db.GetPeer("ADMIT0001"); p == nil {
					t.Fatal("the admitted device has no peer row")
				}
				if got := f.folderOf(t, "ADMIT0001"); got != 0 {
					t.Errorf("admitted device is in folder %d, want none", got)
				}
				second := f.register(t, map[string]any{"device_id": "ADMIT0002", "admission": secret})
				if second.Status != "rejected" || second.Error != "admission_rejected" {
					t.Fatalf("second device: %+v, want admission_rejected", second)
				}
				if p, _ := f.srv.db.GetPeer("ADMIT0002"); p != nil {
					t.Fatal("a used admission admitted a second device")
				}
			})
		})
	}
}

// An admission whose 24 hours are up admits nothing, and an unknown one
// neither; a device without one still waits for approval.
func TestQuickSupportAdmissionExpires(t *testing.T) {
	placementStores(t, config.EnrollmentModeManaged, func(t *testing.T, f *placementFixture) {
		now := time.Now()
		f.srv.admissionNow = func() time.Time { return now }
		secret := admission(t, f.srv)
		now = now.Add(ReceiverAdmissionLifetime + time.Second)
		late := f.register(t, map[string]any{"device_id": "ADMIT0003", "admission": secret})
		if late.Status != "rejected" || late.Error != "admission_rejected" {
			t.Fatalf("expired admission: %+v, want admission_rejected", late)
		}
		unknown := f.register(t, map[string]any{"device_id": "ADMIT0004", "admission": "0123456789abcdef0123456789abcdef0123456789abcdef"})
		if unknown.Status != "rejected" {
			t.Fatalf("unknown admission: %+v, want rejected", unknown)
		}
		if waiting := f.register(t, map[string]any{"device_id": "ADMIT0005"}); waiting.Status != "pending" {
			t.Fatalf("no admission: %+v, want pending", waiting)
		}
		// Making a new admission drops the expired ones
		admission(t, f.srv)
		rows, err := f.srv.db.ListConfigByPrefix(receiverAdmissionPrefix)
		if err != nil || len(rows) != 1 {
			t.Fatalf("stored admissions after the prune: %d %v, want only the new one", len(rows), err)
		}
	})
}

// Admissions come only from the console in the same process.
func TestQuickSupportAdmissionIsInProcessOnly(t *testing.T) {
	database := testSetupDB(t)
	t.Cleanup(func() { database.Close() })
	srv := onboardServer(database)
	req := httptest.NewRequest(http.MethodPost, "/api/onboard/admissions", strings.NewReader(`{"session":"0123456789abcdef"}`))
	req.RemoteAddr = "127.0.0.1:5000"
	rec := httptest.NewRecorder()
	srv.mux.get(srv).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("from the listener: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if rows, _ := srv.db.ListConfigByPrefix(receiverAdmissionPrefix); len(rows) != 0 {
		t.Fatal("a listener request made an admission")
	}
}
