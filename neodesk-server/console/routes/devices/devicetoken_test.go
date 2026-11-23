package devices_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestIssueANewDeviceToken: "Issue a new token" ends the device's device
// token at once and makes the token of its approved registration due
// again, so that the device's next registration status carries a new one.
// It needs device.edit; an unknown device is 404 (L45).
func TestIssueANewDeviceToken(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		storetest.GoServerMigrate(t, db)
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		authn := w.Server.Deps().Authn
		api.On("GET /api/peers/dev-reissue", 200, `{"id":"dev-reissue","hostname":"h"}`)
		token, err := authn.IssueDeviceToken(ctx, "dev-reissue", "test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO pending_registrations (device_id, status, approved_by, access_token)
			VALUES ('dev-reissue', 'approved', 'e2e_admin', 'sha256 delivered')`); err != nil {
			t.Fatal(err)
		}
		stored := func() string {
			t.Helper()
			var value string
			if err := db.QueryRow(ctx, `SELECT access_token FROM pending_registrations WHERE device_id = 'dev-reissue'`).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}

		if got := w.As("viewer").Do("POST", "/api/devices/dev-reissue/device-token", `{}`); got.Status != http.StatusForbidden {
			t.Errorf("viewer: %d %s, want 403", got.Status, got.Body)
		}
		if _, err := authn.RequireDeviceToken(ctx, "Bearer "+token); err != nil || stored() == "" {
			t.Fatalf("after the viewer's refusal the token or the registration changed: %v, %q", err, stored())
		}
		if got := w.As("admin").Do("POST", "/api/devices/dev-reissue/device-token", `{}`); got.Status != http.StatusOK || got.Body != `{"success":true}` {
			t.Errorf("admin: %d %s, want 200 {\"success\":true}", got.Status, got.Body)
		}
		if _, err := authn.RequireDeviceToken(ctx, "Bearer "+token); err == nil {
			t.Error("the device's token still works after a new one was issued")
		}
		if got := stored(); got != "" {
			t.Errorf("the registration holds %q, want its token due (\"\")", got)
		}
		if got := w.As("admin").Do("POST", "/api/devices/no-such-device/device-token", `{}`); got.Status != http.StatusNotFound {
			t.Errorf("unknown device: %d %s, want 404", got.Status, got.Body)
		}
	})
}
