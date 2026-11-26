package devices_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestReachSaysWhyAndWhoMayUnblock: the viewer asks why a device can't be
// reached; the answer carries the server's reason and whether this account
// may unblock it (L550, L556).
func TestReachSaysWhyAndWhoMayUnblock(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	api.On("GET /api/peers/A/reach", 200, `{"reason":"device_revoked","last_online":"2026-11-23T10:00:00Z"}`)
	got := w.As("admin").Do("GET", "/api/devices/A/reach", "")
	if got.Status != http.StatusOK || got.Body != `{"success":true,"reason":"device_revoked","last_online":"2026-11-23T10:00:00Z","can_unblock":true}` {
		t.Errorf("admin: %d %s", got.Status, got.Body)
	}
	api.On("GET /api/peers/C/reach", 200, `{"reason":"device_offline","last_online":""}`)
	got = w.As("viewer").Do("GET", "/api/devices/C/reach", "")
	if got.Status != http.StatusOK || !strings.Contains(got.Body, `"can_unblock":false`) {
		t.Errorf("viewer: %d %s", got.Status, got.Body)
	}
}

// TestUnblockLiftsTheBlockAndWritesTheAudit: Unblock this computer takes
// the ID off the server's blocklist and leaves an audit entry; an ID that is
// not blocked is 404 with a text, not the server's code.
func TestUnblockLiftsTheBlockAndWritesTheAudit(t *testing.T) {
	db := storetest.Migrated(t)
	w, api := world(t, db, nil, nil)
	admin := w.As("admin")
	api.On("POST /api/peers/A/unblock", 200, `{"status":"unblocked","id":"A"}`)
	if got := admin.Do("POST", "/api/devices/A/unblock", "{}"); got.Status != http.StatusOK || got.Body != `{"success":true}` {
		t.Fatalf("unblock: %d %s", got.Status, got.Body)
	}
	var n int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action = 'device_unblocked'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit entries %d %v", n, err)
	}
	api.On("POST /api/peers/A/unblock", 404, `{"error":"not_blocked"}`)
	got := admin.Do("POST", "/api/devices/A/unblock", "{}")
	if got.Status != http.StatusNotFound || strings.Contains(got.Body, "not_blocked") || !strings.Contains(got.Body, "is not blocked") {
		t.Fatalf("not blocked: %d %s", got.Status, got.Body)
	}
	if got := w.As("viewer").Do("POST", "/api/devices/C/unblock", "{}"); got.Status != http.StatusForbidden {
		t.Fatalf("a viewer unblocked: %d %s", got.Status, got.Body)
	}
}
