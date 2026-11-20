package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/timesync"
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
