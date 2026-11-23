package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/config"
	"github.com/neodeskapp/neodesk/neodesk-server/peer"
)

// TestADeviceOnlyTheSignalServerSawIsARustDesk: an enrollment request
// that only the signal server queued (a stock RustDesk client sends no
// enrollment of its own, so no device type) is approved as a RustDesk
// device, not as NeoDesk (L341).
func TestADeviceOnlyTheSignalServerSawIsARustDesk(t *testing.T) {
	database := testSetupDB(t)
	defer database.Close()
	cfg := config.DefaultConfig()
	cfg.EnrollmentMode = config.EnrollmentModeManaged
	srv := New(cfg, database, peer.NewMap(), nil, "test")
	for id, pending := range map[string]string{
		"289485454": `{"device_id":"289485454","hostname":"","platform":"","version":"","ip":"10.10.10.75","created_at":"2026-11-23T08:40:00Z"}`,
		"289228142": `{"device_id":"289228142","hostname":"ndlab-linux","device_type":"neodesk-desktop","ip":"10.10.10.74"}`,
	} {
		if err := database.SetConfig(pendingDevicePrefix+id, pending); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/enrollment/approve/"+id, bytes.NewBufferString(`{"sync_mode":"standard"}`))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		srv.handleApproveDevice(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("approve %s: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	for id, want := range map[string]string{"289485454": "rustdesk", "289228142": "neodesk-desktop"} {
		if p, err := database.GetPeer(id); err != nil || p == nil || p.DeviceType != want {
			t.Errorf("%s: device type %+v, want %s (%v)", id, p, want, err)
		}
	}
}
