package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/timesync"
)

// TestBillingExportNamesTheBillingDay: an export's file name carries the
// day in the billing time zone, where the billing months begin and end: at
// 23:30 UTC it is already the next day in Berlin (BUGS CM4).
func TestBillingExportNamesTheBillingDay(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	srv.SetTimeSyncService(timesync.NewService(database, timesync.Config{BillingTimezone: "Europe/Berlin"}))
	srv.now = func() time.Time { return time.Date(2026, 3, 14, 23, 30, 0, 0, time.UTC) }
	for path, want := range map[string]string{
		"/api/billing/sessions/export?format=csv": `attachment; filename="billing-sessions-20260315.csv"`,
		"/api/billing/reports/export?format=csv":  `attachment; filename="billing-reports-20260315.csv"`,
		"/api/billing/reports/export?format=pdf":  `attachment; filename="billing-reports-20260315.pdf"`,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		req = req.WithContext(WithIdentity(req.Context(), consoleBridge))
		rec := httptest.NewRecorder()
		srv.InternalHandler().ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Disposition"); rec.Code != http.StatusOK || got != want {
			t.Errorf("%s: %d %q, want %q", path, rec.Code, got, want)
		}
	}
}

// TestBillingExportsKeepToTheNamedDevices: device_ids names the devices
// whose sessions and work reports an export holds, so the console can cut
// an export to a limited account's scope as it cuts the lists; an empty
// list holds none, and without the parameter an export holds every device
// (L48).
func TestBillingExportsKeepToTheNamedDevices(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	started := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)
	for _, s := range []struct{ id, device string }{{"sess-one", "DEVONE01"}, {"sess-two", "DEVTWO02"}} {
		if err := database.CreateBillingSession(&db.BillingSession{ID: s.id, DeviceID: s.device, OperatorID: "op", Transport: "rustdesk", Status: "closed", BillingPhase: "included", StartedAt: started, Currency: "EUR"}); err != nil {
			t.Fatal(err)
		}
		if err := database.CreateBillingWorkReport(&db.BillingWorkReport{SessionID: s.id, OperatorID: "op", Summary: "work on " + s.device}); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		req = req.WithContext(WithIdentity(req.Context(), consoleBridge))
		rec := httptest.NewRecorder()
		srv.InternalHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, path := range []string{"/api/billing/sessions/export?format=csv", "/api/billing/reports/export?format=csv", "/api/billing/reports/export?format=pdf"} {
		all := get(path)
		if !strings.Contains(all, "sess-one") || !strings.Contains(all, "sess-two") {
			t.Errorf("%s without device_ids: %q, want both sessions", path, all)
		}
		two := get(path + "&device_ids=DEVTWO02,OTHER")
		if strings.Contains(two, "sess-one") || !strings.Contains(two, "sess-two") {
			t.Errorf("%s for DEVTWO02: %q, want sess-two alone", path, two)
		}
		none := get(path + "&device_ids=")
		if strings.Contains(none, "sess-one") || strings.Contains(none, "sess-two") {
			t.Errorf("%s for no device: %q, want no session", path, none)
		}
	}
}
