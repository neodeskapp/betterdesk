package devices_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The bugs of BUGS.md that the device routes fixed, each with the
// behaviour the fix changed.

// TestHardDeleteClearsDeviceRows: a hard delete that the server confirmed
// clears what the console keeps of the device: its system information,
// metrics, folder, manual device groups and its row in peer. A soft
// delete keeps all of it, since the device can be restored, and so does a
// hard delete the server refused (BUGS DE1, DECISIONS A59). Other devices
// keep theirs.
func TestHardDeleteClearsDeviceRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
		folder, err := db.Insert(ctx, `INSERT INTO folders (name) VALUES ('Office')`)
		if err != nil {
			t.Fatal(err)
		}
		// C is in the group g-viewer of the world already
		exec(`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'A' FROM device_groups WHERE guid = 'g-viewer'`)
		for _, id := range []string{"A", "C"} {
			exec(`INSERT INTO peer_sysinfo (peer_id, hostname) VALUES (?, 'h')`, id)
			exec(`INSERT INTO peer_metrics (peer_id, cpu_usage) VALUES (?, 1)`, id)
			exec(`INSERT INTO peer (id) VALUES (?)`, id)
			exec(`INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, ?)`, id, folder)
		}
		rows := func(id string) string {
			t.Helper()
			var counts []string
			for _, q := range []string{
				`SELECT COUNT(*) FROM peer_sysinfo WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM peer_metrics WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM device_folder_assignments WHERE device_id = ?`,
				`SELECT COUNT(*) FROM device_group_members WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM peer WHERE id = ?`,
			} {
				var n int
				if err := db.QueryRow(ctx, q, id).Scan(&n); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				counts = append(counts, fmt.Sprint(n))
			}
			return strings.Join(counts, " ")
		}
		const all, none = "1 1 1 1 1", "0 0 0 0 0"
		admin := w.As("admin")

		api.On("DELETE /api/peers/A?hard=true", 500, `{"error":"delete failed"}`)
		if got := admin.Do("DELETE", "/api/devices/A?hard=true", ""); got.Status != http.StatusInternalServerError {
			t.Errorf("a hard delete the server refused: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != all {
			t.Errorf("rows of A after a refused hard delete: %s, want %s", got, all)
		}
		api.On("DELETE /api/peers/A?hard=true", 200, `{"status":"deleted","id":"A","revoked":false}`)

		if got := admin.Do("DELETE", "/api/devices/A", ""); got.Status != http.StatusOK {
			t.Fatalf("soft delete: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != all {
			t.Errorf("rows of A after a soft delete: %s, want %s", got, all)
		}
		if got := admin.Do("DELETE", "/api/devices/A?hard=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":true,"cascaded":[]}` {
			t.Fatalf("hard delete: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != none {
			t.Errorf("rows of A after a hard delete: %s, want %s", got, none)
		}
		if got := rows("C"); got != all {
			t.Errorf("rows of C after A's hard delete: %s, want %s", got, all)
		}
	})
}

// TestSysinfoNullColumnsAnswerTheFallback: a system information row whose
// list and object columns are NULL answers [] and {} for them, where the
// web app reads lists and objects; stored values are parsed as before
// (BUGS D19).
func TestSysinfoNullColumnsAnswerTheFallback(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO peer_sysinfo (peer_id, hostname, displays, encoding, features, platform_additions) VALUES ('A', 'h', NULL, NULL, NULL, NULL)`,
			`INSERT INTO peer_sysinfo (peer_id, hostname, displays, encoding, features, platform_additions) VALUES ('C', 'h', '[{"name":"x"}]', '["h264"]', '{"a":1}', '{"b":2}')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		admin := w.As("admin")
		for id, wants := range map[string][]string{
			"A": {`"displays":[]`, `"encoding":[]`, `"features":{}`, `"platform_additions":{}`},
			"C": {`"displays":[{"name":"x"}]`, `"encoding":["h264"]`, `"features":{"a":1}`, `"platform_additions":{"b":2}`},
		} {
			got := admin.Do("GET", "/api/devices/"+id, "")
			for _, want := range wants {
				if got.Status != http.StatusOK || !strings.Contains(got.Body, want) {
					t.Errorf("device %s: %d %s, want %s", id, got.Status, got.Body, want)
				}
			}
		}
	})
}
