package devices_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/bdrelay/fakeagent"
	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// The bugs of BUGS.md that the device routes fixed, each with the
// behaviour the fix changed.

// TestHardDeleteClearsDeviceRows: a hard delete that the server confirmed
// clears what the console keeps of the device: its system information,
// metrics, folder, manual device groups, its row in peer, its inventory
// and telemetry, its app usage and the accounts' direct grants to it. A
// soft delete keeps all of it, since the device can be restored, and so
// does a hard delete the server refused (BUGS DE1, RV65, DECISIONS A59).
// Other devices keep theirs.
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
			exec(`INSERT INTO device_inventory (device_id, hardware) VALUES (?, '{}')`, id)
			exec(`INSERT INTO device_telemetry (device_id, cpu_usage_percent) VALUES (?, 1)`, id)
			exec(`INSERT INTO activity_sessions (device_id, app_name, started_at, ended_at) VALUES (?, 'x', '2026-01-01 10:00:00', '2026-01-01 10:01:00')`, id)
			exec(`INSERT INTO activity_summaries (device_id, reported_at) VALUES (?, '2026-01-01 10:00:00')`, id)
			exec(`INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, ?)`, id)
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
				`SELECT COUNT(*) FROM device_inventory WHERE device_id = ?`,
				`SELECT COUNT(*) FROM device_telemetry WHERE device_id = ?`,
				`SELECT COUNT(*) FROM activity_sessions WHERE device_id = ?`,
				`SELECT COUNT(*) FROM activity_summaries WHERE device_id = ?`,
				`SELECT COUNT(*) FROM user_peer_grants WHERE peer_id = ?`,
			} {
				var n int
				if err := db.QueryRow(ctx, q, id).Scan(&n); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				counts = append(counts, fmt.Sprint(n))
			}
			return strings.Join(counts, " ")
		}
		const all, none = "1 1 1 1 1 1 1 1 1 1", "0 0 0 0 0 0 0 0 0 0"
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

// TestHardCascadeClearsTheLinkedDevicesRows: a hard delete with cascade
// clears what the console keeps of every device the server names as
// deleted with it, not only of the named one; a soft delete with cascade
// keeps theirs (BUGS DE10).
func TestHardCascadeClearsTheLinkedDevicesRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		folder, err := db.Insert(ctx, `INSERT INTO folders (name) VALUES ('Office')`)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"A", "A2", "A3"} {
			for _, q := range []string{
				`INSERT INTO peer_sysinfo (peer_id, hostname) VALUES (?, 'h')`,
				`INSERT INTO peer_metrics (peer_id, cpu_usage) VALUES (?, 1)`,
				`INSERT INTO peer (id) VALUES (?)`,
				`INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, ` + fmt.Sprint(folder) + `)`,
				`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, ? FROM device_groups WHERE guid = 'g-viewer'`,
			} {
				if _, err := db.Exec(ctx, q, id); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
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
		api.On("DELETE /api/peers/A?cascade=true", 200, `{"status":"deleted","id":"A","revoked":false,"cascaded":["A2"]}`)
		if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK {
			t.Fatalf("soft delete with cascade: %d %s", got.Status, got.Body)
		}
		if got := rows("A2"); got != all {
			t.Errorf("rows of A2 after a soft delete with cascade: %s, want %s", got, all)
		}
		api.On("DELETE /api/peers/A?cascade=true&hard=true", 200, `{"status":"deleted","id":"A","revoked":false,"cascaded":["A2"]}`)
		if got := admin.Do("DELETE", "/api/devices/A?cascade=true&hard=true", ""); got.Status != http.StatusOK {
			t.Fatalf("hard delete with cascade: %d %s", got.Status, got.Body)
		}
		for id, want := range map[string]string{"A": none, "A2": none, "A3": all} {
			if got := rows(id); got != want {
				t.Errorf("rows of %s after a hard delete of A with cascade: %s, want %s", id, got, want)
			}
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

var strategyGUIDField = regexp.MustCompile(`"guid":"([^"]+)"`)

// TestUpdateStrategyKeepsItsSwitch: a change to a rule that is switched
// off leaves it off unless the body switches it on, on both databases
// (BUGS DE2).
func TestUpdateStrategyKeepsItsSwitch(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night","enabled":false}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if created.Status != http.StatusOK || m == nil || !strings.Contains(created.Body, `"enabled":false`) {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		path := "/api/panel/strategies/" + m[1]
		for _, c := range []struct{ body, enabled string }{
			{`{}`, `"enabled":false`},
			{`{"name":"Late night"}`, `"enabled":false`},
			{`{"enabled":true}`, `"enabled":true`},
			{`{"name":"Night"}`, `"enabled":true`},
			{`{"enabled":false}`, `"enabled":false`},
		} {
			got := admin.Do("PATCH", path, c.body)
			if got.Status != http.StatusOK || !strings.Contains(got.Body, c.enabled) {
				t.Errorf("PATCH %s: %d %s, want %s", c.body, got.Status, got.Body, c.enabled)
			}
			if got := admin.Do("GET", path, ""); !strings.Contains(got.Body, c.enabled) {
				t.Errorf("GET after PATCH %s: %d %s, want %s", c.body, got.Status, got.Body, c.enabled)
			}
		}
	})
}

// TestDeleteNamesCascadedDevices: the answer of a delete with cascade
// names the devices the server deleted with it (BUGS DE3).
func TestDeleteNamesCascadedDevices(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	api.On("DELETE /api/peers/A?cascade=true", 200, `{"status":"deleted","id":"A","revoked":false,"cascaded":["X","Y"]}`)
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":false,"cascaded":["X","Y"]}` {
		t.Errorf("delete with cascade: %d %s", got.Status, got.Body)
	}
	// The server leaves cascaded out when nothing was linked
	api.On("DELETE /api/peers/A?cascade=true", 200, `{"status":"deleted","id":"A","revoked":false}`)
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":false,"cascaded":[]}` {
		t.Errorf("delete with cascade and nothing linked: %d %s", got.Status, got.Body)
	}
}

// TestCascadeChecksTheLinkedDevices: a delete with cascade, which the
// server carries out on the devices linked to the named one, is refused
// with 403 when one of them is outside the account's scope, before
// anything is deleted; with every linked device in scope it goes through.
// A delete without cascade and the roles that see every device do not
// look the linked devices up (BUGS DE11).
func TestCascadeChecksTheLinkedDevices(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), routetest.Overrides{"operator": {"device.delete": true}}, nil)
	operator, admin := w.As("operator"), w.As("admin")
	forbidden := `{"success":false,"error":"You don't have access to this device."}`
	deletes := func(calls []string) []string {
		var out []string
		for _, call := range calls {
			if !strings.HasPrefix(call, "GET ") {
				out = append(out, call)
			}
		}
		return out
	}
	api.On("GET /api/peers/A/linked", 200, `{"peer_id":"A","linked":[{"id":"A2"},{"id":"C"}],"total":2}`)
	for _, path := range []string{"/api/devices/A?cascade=true", "/api/devices/A?cascade=true&hard=true", "/api/devices/A?revoke=true&cascade=true"} {
		api.Calls()
		if got := operator.Do("DELETE", path, ""); got.Status != http.StatusForbidden || got.Body != forbidden {
			t.Errorf("%s with C linked, as operator: %d %s, want 403 %s", path, got.Status, got.Body, forbidden)
		}
		if calls := deletes(api.Calls()); len(calls) > 0 {
			t.Errorf("%s with C linked, as operator, reached the server: %q", path, calls)
		}
	}
	api.On("GET /api/peers/A/linked", 500, `{"error":"database is locked"}`)
	api.Calls()
	if got := operator.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusInternalServerError {
		t.Errorf("a cascade whose linked devices cannot be read, as operator: %d %s, want 500", got.Status, got.Body)
	}
	if calls := deletes(api.Calls()); len(calls) > 0 {
		t.Errorf("a cascade whose linked devices cannot be read reached the server: %q", calls)
	}

	api.On("GET /api/peers/A/linked", 200, `{"peer_id":"A","linked":[{"id":"A2"}],"total":1}`)
	if got := operator.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK {
		t.Errorf("a cascade with every linked device in scope, as operator: %d %s", got.Status, got.Body)
	}
	api.On("GET /api/peers/A/linked", 200, `{"peer_id":"A","linked":[{"id":"C"}],"total":1}`)
	api.Calls()
	if got := operator.Do("DELETE", "/api/devices/A", ""); got.Status != http.StatusOK {
		t.Errorf("a delete without cascade, as operator: %d %s", got.Status, got.Body)
	}
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK {
		t.Errorf("a cascade with C linked, as admin: %d %s", got.Status, got.Body)
	}
	for _, call := range api.Calls() {
		if strings.HasSuffix(call, "/linked") {
			t.Errorf("a delete without cascade or by admin looked the linked devices up: %s", call)
		}
	}
}

// TestCascadeNamesTheLinkedDevicesItChecked: the server's cascade deletes
// only the linked devices whose scope the console checked, so a device
// linked between that check and the delete stays. A role that sees every
// device names none, and the cascade takes every linked device (BUGS DE13).
func TestCascadeNamesTheLinkedDevicesItChecked(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), routetest.Overrides{"operator": {"device.delete": true}}, nil)
	operator, admin := w.As("operator"), w.As("admin")
	api.On("GET /api/peers/A/linked", 200, `{"peer_id":"A","linked":[{"id":"A2"},{"id":"A3"}],"total":2}`)
	deletes := func() []string {
		var out []string
		for _, call := range api.Calls() {
			if strings.HasPrefix(call, "DELETE ") {
				out = append(out, call)
			}
		}
		return out
	}
	api.Calls()
	if got := operator.Do("DELETE", "/api/devices/A?revoke=true&cascade=true", ""); got.Status != http.StatusOK {
		t.Fatalf("a cascade with every linked device in scope, as operator: %d %s", got.Status, got.Body)
	}
	if got, want := deletes(), []string{"DELETE /api/peers/A?revoke=true&cascade=true&cascade_only=A2%2CA3"}; !slices.Equal(got, want) {
		t.Errorf("the operator's cascade asked the server for %q, want %q", got, want)
	}
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK {
		t.Fatalf("a cascade as admin: %d %s", got.Status, got.Body)
	}
	if got, want := deletes(), []string{"DELETE /api/peers/A?cascade=true"}; !slices.Equal(got, want) {
		t.Errorf("the admin's cascade asked the server for %q, want %q", got, want)
	}
}

// TestBulkDeleteIsNoDeviceID: the scope check in front of the addresses
// below /api/devices/{id} does not take bulk-delete for a device id on the
// bulk delete, which checks each device of its list itself. A device named
// bulk-delete outside the account's scope does not refuse the account's
// bulk deletes, and none is looked up; the routes of such a device keep
// the check (BUGS UM35).
func TestBulkDeleteIsNoDeviceID(t *testing.T) {
	db := storetest.Migrated(t)
	w, api := world(t, db, routetest.Overrides{"operator": {"device.delete": true}}, nil)
	if _, err := db.Exec(context.Background(), `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'bulk-delete' FROM device_groups WHERE guid = 'g-viewer'`); err != nil {
		t.Fatal(err)
	}
	api.On("GET /api/peers/bulk-delete", 200, `{"id":"bulk-delete","hostname":"h"}`)
	operator := w.As("operator")
	api.Calls()
	for _, path := range []string{"/api/devices/bulk-delete", "/API/Devices/Bulk-Delete/"} {
		if got := operator.Do("POST", path, `{"ids":["A"]}`); got.Status != http.StatusOK || got.Body != `{"success":true,"data":{"deleted":1}}` {
			t.Errorf("POST %s of a device in scope, as operator: %d %s", path, got.Status, got.Body)
		}
		if calls := strings.Join(api.Calls(), "\n"); calls != "GET /api/peers/A\nDELETE /api/peers/A" {
			t.Errorf("POST %s of a device in scope, as operator, called %q", path, calls)
		}
	}
	forbidden := `{"success":false,"error":"You don't have access to this device."}`
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/devices/bulk-delete"},
		{"POST", "/api/devices/bulk-delete/restore"},
		{"DELETE", "/api/devices/bulk-delete"},
	} {
		if got := operator.Do(c.method, c.path, `{}`); got.Status != http.StatusForbidden || got.Body != forbidden {
			t.Errorf("%s %s, a device outside the operator's scope: %d %s, want 403", c.method, c.path, got.Status, got.Body)
		}
	}
}

// TestSetTagsKeepsWholeCharacters: a tag cut to 50 UTF-16 units stops
// before a character outside the Basic Multilingual Plane that does not
// fit whole, so the server stores no half of it (BUGS DGR2).
func TestSetTagsKeepsWholeCharacters(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	a49 := strings.Repeat("a", 49)
	api.Calls()
	got := w.As("admin").Do("PUT", "/api/devices/A/tags", `{"tags":["`+a49+`😀","b😀"]}`)
	if want := `{"success":true,"data":{"tags":["` + a49 + `","b😀"]}}`; got.Status != http.StatusOK || got.Body != want {
		t.Errorf("tags cut in an emoji: %d %s, want %s", got.Status, got.Body, want)
	}
	if calls := strings.Join(api.Calls(), "\n"); !strings.Contains(calls, `PUT /api/peers/A/tags {"tags":["`+a49+`","b😀"]}`) {
		t.Errorf("the tags the server got: %s", calls)
	}
}

// TestSetTagsTrimsAfterTheCut: a tag whose 50th character is white space
// is stored without it (BUGS DGR6).
func TestSetTagsTrimsAfterTheCut(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	a49 := strings.Repeat("a", 49)
	api.Calls()
	got := w.As("admin").Do("PUT", "/api/devices/A/tags", `{"tags":["`+a49+` b","x"]}`)
	if want := `{"success":true,"data":{"tags":["` + a49 + `","x"]}}`; got.Status != http.StatusOK || got.Body != want {
		t.Errorf("a tag with a space as its 50th character: %d %s, want %s", got.Status, got.Body, want)
	}
	if calls := strings.Join(api.Calls(), "\n"); !strings.Contains(calls, `PUT /api/peers/A/tags {"tags":["`+a49+`","x"]}`) {
		t.Errorf("the tags the server got: %s", calls)
	}
}

// TestSaveGroupTakesTextOnly: a device group whose name, guid, note, team
// id or tag filter is no text is refused with 400 before anything is
// written, where it was saved with the value read as text (BUGS DGR5).
func TestSaveGroupTakesTextOnly(t *testing.T) {
	for _, node := range []bool{false, true} {
		w, api := world(t, storetest.Migrated(t), nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
		admin := w.As("admin")
		count := func() int {
			var n int
			if err := w.DB.QueryRow(context.Background(), `SELECT COUNT(*) FROM device_groups`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		before := count()
		for _, c := range []struct{ body, member string }{
			{`{"name":["a","b"]}`, "name"},
			{`{"name":{"a":1}}`, "name"},
			{`{"name":"Lab","guid":1e21}`, "guid"},
			{`{"name":"Lab","note":true}`, "note"},
			{`{"name":"Lab","team_id":5}`, "team_id"},
			{`{"name":"Lab","source_type":"tag","tag_filter":["lab"]}`, "tag_filter"},
		} {
			api.Calls()
			want := `{"success":false,"error":"` + c.member + ` must be text"}`
			if got := admin.Do("POST", "/api/device-groups", c.body); got.Status != http.StatusBadRequest || got.Body != want {
				t.Errorf("%s (console writer %v): %d %s, want 400 %s", c.body, node, got.Status, got.Body, want)
			}
			if calls := api.Calls(); len(calls) > 0 || count() != before {
				t.Errorf("%s (console writer %v) wrote: calls %q, %d groups", c.body, node, calls, count())
			}
		}
		if got := admin.Do("POST", "/api/device-groups", `{"name":"Lab","note":null,"team_id":null}`); got.Status != http.StatusOK {
			t.Errorf("a group with null note and team id (console writer %v): %d %s", node, got.Status, got.Body)
		}
	}
}

// TestSaveGroupRefusesLongAccessLists: a device group with more than 100
// different usernames or user groups on its access list is refused with
// 400 before anything is written, where the first 100 were kept and the
// rest left out without a word; 100 are saved (BUGS PA10).
func TestSaveGroupRefusesLongAccessLists(t *testing.T) {
	list := func(n int, prefix string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `"` + prefix + strconv.Itoa(i) + `"`
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	for _, node := range []bool{false, true} {
		w, api := world(t, storetest.Migrated(t), nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
		admin := w.As("admin")
		for _, c := range []struct{ body, list string }{
			{`{"name":"Lab","allowed_users":` + list(101, "user") + `}`, "allowed_users"},
			{`{"name":"Lab","allowed_groups":` + list(101, "ug-") + `}`, "allowed_groups"},
			{`{"name":"Lab","guid":"g-viewer","allowed_user_groups":` + list(150, "ug-") + `}`, "allowed_groups"},
		} {
			api.Calls()
			want := `{"success":false,"error":"` + c.list + ` can hold at most 100 entries"}`
			if got := admin.Do("POST", "/api/device-groups", c.body); got.Status != http.StatusBadRequest || got.Body != want {
				t.Errorf("%.60s... (console writer %v): %d %s, want 400 %s", c.body, node, got.Status, got.Body, want)
			}
			if calls := api.Calls(); len(calls) > 0 {
				t.Errorf("%.60s... (console writer %v) reached the server: %q", c.body, node, calls)
			}
		}
		var names string
		if err := w.DB.QueryRow(context.Background(), `SELECT name FROM device_groups WHERE guid = 'g-viewer'`).Scan(&names); err != nil || names != "Viewer only" {
			t.Errorf("g-viewer after the refusals: %q, %v", names, err)
		}
		if got := admin.Do("POST", "/api/device-groups", `{"name":"Lab","allowed_users":`+list(100, "user")+`}`); got.Status != http.StatusOK {
			t.Errorf("100 usernames (console writer %v): %d %s", node, got.Status, got.Body)
		}
	}
}

// TestMembershipIsOneWrite: the device's manual groups go to the API
// server in one call, which names the chosen group and the manual groups
// the account may see; the server writes them in one transaction. A
// refusal is answered with its status and changes nothing, also not
// through the console's own writer; an API server that does not have the
// call (404) leaves the write to the console's own writer (BUGS UM33).
func TestMembershipIsOneWrite(t *testing.T) {
	db := storetest.Migrated(t)
	w, api := world(t, db, nil, nil)
	ctx := context.Background()
	for _, q := range []string{
		`DELETE FROM device_groups WHERE guid <> 'g-viewer'`,
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-a', 'A', '', '', 'manual', '')`,
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-b', 'B', '', '', 'manual', '')`,
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-tag', 'Tag', '', '', 'tag', 'lab')`,
		`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'A' FROM device_groups WHERE guid = 'g-a'`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	memberOf := func() string {
		return rows(t, db, `SELECT g.guid FROM device_group_members m JOIN device_groups g ON g.id = m.device_group_id WHERE m.peer_id = 'A' ORDER BY g.guid`)
	}
	panelCalls := func() []string {
		var out []string
		for _, call := range api.Calls() {
			if strings.Contains(call, "/api/panel/") {
				out = append(out, call)
			}
		}
		return out
	}
	const write = "PUT /api/panel/devices/A/device-group"
	operator, admin := w.As("operator"), w.As("admin")

	api.Calls()
	api.On(write, 200, `{"device_id":"A","guid":"g-b"}`)
	if got := operator.Do("PUT", "/api/devices/A/groups", `{"groupGuids":["g-b"]}`); got.Status != http.StatusOK {
		t.Fatalf("move into g-b: %d %s", got.Status, got.Body)
	}
	if got, want := panelCalls(), []string{write + ` {"guid":"g-b","groups":["g-a","g-b"]}`}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls of the operator, who may not see g-viewer: %q, want %q", got, want)
	}
	admin.Do("PUT", "/api/devices/A/groups", `{"groupGuids":[]}`)
	if got, want := panelCalls(), []string{write + ` {"guid":"","groups":["g-a","g-b","g-viewer"]}`}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls of admin: %q, want %q", got, want)
	}
	if got := memberOf(); got != `[{"guid":"g-a"}]` {
		t.Errorf("the console's own rows after writes the server took: %s", got)
	}

	api.On(write, 400, `{"error":"invalid JSON"}`)
	got := admin.Do("PUT", "/api/devices/A/groups", `{"groupGuids":["g-b"]}`)
	if got.Status != http.StatusBadRequest || got.Body != `{"success":false,"error":"Request failed with status code 400"}` {
		t.Errorf("a refused write: %d %s", got.Status, got.Body)
	}
	if calls := panelCalls(); len(calls) != 1 {
		t.Errorf("a refused write made the calls %q", calls)
	}
	if got := memberOf(); got != `[{"guid":"g-a"}]` {
		t.Errorf("rows after a refused write: %s", got)
	}

	api.On(write, 404, `{"error":"not found"}`)
	if got := admin.Do("PUT", "/api/devices/A/groups", `{"groupGuids":["g-b"]}`); got.Status != http.StatusOK {
		t.Errorf("a write the API server does not have: %d %s", got.Status, got.Body)
	}
	if got := memberOf(); got != `[{"guid":"g-b"}]` {
		t.Errorf("rows after the console's own writer: %s", got)
	}
}

// TestCutsKeepWholeCharacters: a note, a user name and a display name
// cut to their length stop before a character outside the Basic
// Multilingual Plane that does not fit whole, so the server gets no
// U+FFFD in place of half of one, and so does a connection mode reason
// (BUGS DE12).
func TestCutsKeepWholeCharacters(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	a511, b127 := strings.Repeat("a", 511), strings.Repeat("b", 127)
	api.Calls()
	got := admin.Do("PATCH", "/api/devices/A", `{"note":"`+a511+`😀","display_name":"`+b127+`😀","user":"😀"}`)
	if got.Status != http.StatusOK {
		t.Fatalf("update: %d %s", got.Status, got.Body)
	}
	r499 := strings.Repeat("r", 499)
	if got := admin.Do("POST", "/api/devices/A/connection-mode", `{"mode":"normal","reason":"`+r499+`😀"}`); got.Status != http.StatusOK {
		t.Fatalf("connection mode: %d %s", got.Status, got.Body)
	}
	calls := strings.Join(api.Calls(), "\n")
	if strings.Contains(calls, `�`) || strings.Contains(calls, "�") {
		t.Errorf("the server got U+FFFD: %s", calls)
	}
	for _, want := range []string{`"note":"` + a511 + `"`, `"display_name":"` + b127 + `"`, `"user":"😀"`, `"` + r499 + `"`} {
		if !strings.Contains(calls, want) {
			t.Errorf("the server did not get %.40s...: %s", want, calls)
		}
	}
}

// TestListIgnoresQueryLists: search and sortOrder given as a list or an
// object are ignored like the other parameters, and the list answers
// (BUGS DE4).
func TestListIgnoresQueryLists(t *testing.T) {
	w, _ := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	want := admin.Do("GET", "/api/devices", "")
	if want.Status != http.StatusOK || !strings.Contains(want.Body, `"id":"A"`) {
		t.Fatalf("the list: %d %s", want.Status, want.Body)
	}
	for _, query := range []string{"search[]=x", "search[a]=1", "search[]=a&search[]=b", "sortOrder[]=asc", "sortOrder[x]=1", "search[]=x&sortOrder[]=asc"} {
		if got := admin.Do("GET", "/api/devices?"+query, ""); got.Status != http.StatusOK || got.Body != want.Body {
			t.Errorf("?%s: %d %s, want the list without it", query, got.Status, got.Body)
		}
	}
}

// TestChangeIDTakesTextOnly: a new id that is no text is refused like one
// of the wrong length, before anything reaches the server (BUGS DE5).
func TestChangeIDTakesTextOnly(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	refused := admin.Do("POST", "/api/devices/A/change-id", `{"newId":"abc"}`)
	if refused.Status != http.StatusBadRequest {
		t.Fatalf("a short id: %d %s", refused.Status, refused.Body)
	}
	for _, body := range []string{`{"newId":1234567}`, `{"newId":true}`, `{"newId":{"length":8}}`, `{"newId":["ABCDEFG"]}`, `{"newId":null}`} {
		api.Calls()
		if got := admin.Do("POST", "/api/devices/A/change-id", body); got.Status != refused.Status || got.Body != refused.Body {
			t.Errorf("%s: %d %s, want %d %s", body, got.Status, got.Body, refused.Status, refused.Body)
		}
		for _, call := range api.Calls() {
			if !strings.HasPrefix(call, "GET /api/peers/A") {
				t.Errorf("%s reached the server: %s", body, call)
			}
		}
	}
	if got := admin.Do("POST", "/api/devices/A/change-id", `{"newId":"NEWID1"}`); got.Status != http.StatusOK {
		t.Errorf("a new id as text: %d %s", got.Status, got.Body)
	}
}

// TestSetGroupsNeedsAList: groupGuids that is missing or no list is
// refused and leaves the device in its group; an empty list still takes
// it out of every manual group (BUGS DE6).
func TestSetGroupsNeedsAList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = true })
		admin := w.As("admin")
		ctx := context.Background()
		member := func() int {
			var n int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM device_group_members WHERE peer_id = 'C'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		refused := admin.Do("PUT", "/api/devices/C/groups", `{"groupGuids":["not a guid!"]}`)
		if refused.Status != http.StatusBadRequest {
			t.Fatalf("an invalid guid: %d %s", refused.Status, refused.Body)
		}
		for _, body := range []string{`{}`, `{"groupGuids":null}`, `{"groupGuids":"g-viewer"}`, `{"groupGuids":{"0":"g-viewer"}}`, `{"groupGuids":5}`} {
			api.Calls()
			if got := admin.Do("PUT", "/api/devices/C/groups", body); got.Status != refused.Status || got.Body != refused.Body {
				t.Errorf("%s: %d %s, want %d %s", body, got.Status, got.Body, refused.Status, refused.Body)
			}
			if n := member(); n != 1 {
				t.Fatalf("%s took C out of its group", body)
			}
		}
		if got := admin.Do("PUT", "/api/devices/C/groups", `{"groupGuids":[]}`); got.Status != http.StatusOK || member() != 0 {
			t.Errorf("an empty list: %d %s, %d memberships left", got.Status, got.Body, member())
		}
	})
}

// TestAssignChecksEveryReferenceFirst: an assignment checks every
// reference (text, not empty, found) before it writes a guid, and answers
// 400 with a fixed text for each bad one, on assign and unassign; a
// failure of the database is 500 on both (BUGS DE7).
func TestAssignChecksEveryReferenceFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('A', 'not-a-uuid', '')`,
			`UPDATE users SET guid = '' WHERE username = 'e2e_viewer'`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		guids := func() string {
			var peer, user string
			if err := db.QueryRow(ctx, `SELECT COALESCE(guid, '') FROM peers WHERE id = 'A'`).Scan(&peer); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT COALESCE(guid, '') FROM users WHERE username = 'e2e_viewer'`).Scan(&user); err != nil {
				t.Fatal(err)
			}
			return peer + "|" + user
		}
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night"}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if m == nil {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		assign := "/api/panel/strategies/" + m[1] + "/assign"
		for _, c := range []struct{ body, error string }{
			{`{"peers":[""]}`, "empty peer reference"},
			{`{"peers":["  "]}`, "empty peer reference"},
			{`{"peers":{"length":2}}`, "peers must be a list"},
			{`{"peers":5,"users":["e2e_viewer"]}`, "peers must be a list"},
			{`{"peers":[5]}`, "invalid peer reference"},
			{`{"peers":["A","nope"]}`, "peer not found"},
			{`{"peers":["A"],"users":["nobody"]}`, "user not found"},
			{`{"peers":["A"],"users":[""]}`, "empty user reference"},
			{`{"users":["e2e_viewer"],"groups":["no such group"]}`, "device group not found"},
			{`{"users":["e2e_viewer"],"groups":[{"x":1}]}`, "invalid device group reference"},
		} {
			want := `{"success":false,"error":"` + c.error + `"}`
			for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
				if got := admin.Do("POST", path, c.body); got.Status != http.StatusBadRequest || got.Body != want {
					t.Errorf("%s %s: %d %s, want 400 %s", path, c.body, got.Status, got.Body, want)
				}
				if got := guids(); got != "|" {
					t.Fatalf("%s %s wrote the guids %s", path, c.body, got)
				}
			}
		}
		if got := admin.Do("POST", assign, `{"peers":["A"],"users":["e2e_viewer"]}`); got.Status != http.StatusOK {
			t.Errorf("good references: %d %s", got.Status, got.Body)
		}
		if peer, user, _ := strings.Cut(guids(), "|"); peer == "" || user == "" {
			t.Errorf("guids after a good assignment: %q and %q, want both", peer, user)
		}
		if _, err := db.Exec(ctx, `DROP TABLE strategy_assignments`); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
			if got := admin.Do("POST", path, `{"peers":["A"]}`); got.Status != http.StatusInternalServerError || strings.Contains(got.Body, "strategy_assignments") {
				t.Errorf("%s without the table: %d %s, want 500 without the database's text", path, got.Status, got.Body)
			}
		}
	})
}

// TestAssignChecksTheDeviceScope: an account with a limited device scope
// gives a rule to, or takes it from, only the devices and device groups it
// may see. A device or a device group outside its scope, named by id,
// guid or name, is refused with 403 on assign and unassign, and nothing is
// written, here or on the server. The roles that see every device are not
// checked (BUGS UM30).
func TestAssignChecksTheDeviceScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('A', '', ''), ('C', '', 'guid-c')`,
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-open', 'Open', '', '', 'manual', '')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		written := func() string {
			var n int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM strategy_assignments`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			var guid string
			if err := db.QueryRow(ctx, `SELECT COALESCE(guid, '') FROM peers WHERE id = 'A'`).Scan(&guid); err != nil {
				t.Fatal(err)
			}
			return fmt.Sprintf("%d assignments, guid of A %q", n, guid)
		}
		admin, operator := w.As("admin"), w.As("operator")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night"}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if m == nil {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		assign := "/api/panel/strategies/" + m[1] + "/assign"
		const nothing = `0 assignments, guid of A ""`
		forbidden := `{"success":false,"error":"You don't have access to this device."}`
		for _, body := range []string{
			`{"peers":["C"]}`,
			`{"peers":["guid-c"]}`,
			`{"peers":["A","C"]}`,
			`{"groups":["g-viewer"]}`,
			`{"groups":["Viewer only"]}`,
			`{"peers":["A"],"users":["e2e_operator"],"groups":["g-open","g-viewer"]}`,
		} {
			for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
				api.Calls()
				if got := operator.Do("POST", path, body); got.Status != http.StatusForbidden || got.Body != forbidden {
					t.Errorf("%s %s as operator: %d %s, want 403 %s", path, body, got.Status, got.Body, forbidden)
				}
				if got := written(); got != nothing {
					t.Fatalf("%s %s as operator wrote: %s", path, body, got)
				}
				for _, call := range api.Calls() {
					if !strings.HasPrefix(call, "GET ") {
						t.Errorf("%s %s as operator reached the server: %s", path, body, call)
					}
				}
			}
		}
		// A reference that names nothing is still the 400 it was
		if got := operator.Do("POST", assign, `{"peers":["C","nope"]}`); got.Status != http.StatusBadRequest {
			t.Errorf("an unknown device as operator: %d %s, want 400", got.Status, got.Body)
		}
		if got := operator.Do("POST", assign, `{"peers":["A"],"groups":["g-open"]}`); got.Status != http.StatusOK {
			t.Errorf("a device and a group in the operator's scope: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("POST", assign, `{"peers":["C"],"groups":["g-viewer"]}`); got.Status != http.StatusOK {
			t.Errorf("a device and a group outside the operator's scope as admin: %d %s", got.Status, got.Body)
		}
		if got := operator.Do("POST", "/api/panel/strategies/unassign", `{"peers":["A"]}`); got.Status != http.StatusOK {
			t.Errorf("unassign of a device in the operator's scope: %d %s", got.Status, got.Body)
		}
	})
}

// TestAssignNamesOnlyItselfWithALimitedScope: an account whose device
// scope is limited may name only its own account in the users list of an
// assignment, by name, guid or id; any other account is refused with 403
// on assign and unassign, and nothing is written. An account that sees
// every device, by its role or because no device group has an access
// list, names any account (BUGS UM30, DECISIONS A72).
func TestAssignNamesOnlyItselfWithALimitedScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('A', '', '')`,
			`UPDATE users SET guid = 'guid-operator' WHERE username = 'e2e_operator'`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		assignments := func() int {
			var n int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM strategy_assignments`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		admin, operator := w.As("admin"), w.As("operator")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night"}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if m == nil {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		assign := "/api/panel/strategies/" + m[1] + "/assign"
		forbidden := `{"success":false,"error":"You don't have access to this device."}`
		for _, body := range []string{
			`{"users":["e2e_viewer"]}`,
			`{"users":["6"]}`,
			`{"users":["e2e_operator","e2e_viewer"]}`,
			`{"peers":["A"],"users":["e2e_admin"]}`,
		} {
			for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
				api.Calls()
				if got := operator.Do("POST", path, body); got.Status != http.StatusForbidden || got.Body != forbidden {
					t.Errorf("%s %s as operator: %d %s, want 403 %s", path, body, got.Status, got.Body, forbidden)
				}
				if n := assignments(); n != 0 {
					t.Fatalf("%s %s as operator wrote %d assignments", path, body, n)
				}
				for _, call := range api.Calls() {
					if !strings.HasPrefix(call, "GET ") {
						t.Errorf("%s %s as operator reached the server: %s", path, body, call)
					}
				}
			}
		}
		for _, body := range []string{`{"users":["e2e_operator"]}`, `{"users":["guid-operator"]}`, `{"users":["5"]}`} {
			if got := operator.Do("POST", assign, body); got.Status != http.StatusOK {
				t.Errorf("%s as operator: %d %s, want 200", body, got.Status, got.Body)
			}
		}
		if got := admin.Do("POST", assign, `{"users":["e2e_viewer"]}`); got.Status != http.StatusOK {
			t.Errorf("another account as admin: %d %s", got.Status, got.Body)
		}
		// Without an access list on any device group the operator sees
		// every device, and names any account
		if _, err := db.Exec(ctx, `DELETE FROM device_group_user_access`); err != nil {
			t.Fatal(err)
		}
		if got := operator.Do("POST", "/api/panel/strategies/unassign", `{"users":["e2e_viewer"]}`); got.Status != http.StatusOK {
			t.Errorf("another account as an operator who sees every device: %d %s", got.Status, got.Body)
		}
	})
}

// TestStrategyWriteAnswersPermissions: creating and changing a rule
// answers the stored permissions, also on PostgreSQL where the signal
// server created the table with a TEXT column for them (BUGS DE8).
func TestStrategyWriteAnswersPermissions(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		w, _ := world(t, db, nil, nil)
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night","permissions":{"enable-audio":true}}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if created.Status != http.StatusOK || m == nil || !strings.Contains(created.Body, `"permissions":{"enable-audio":"Y"}`) {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		got := admin.Do("PATCH", "/api/panel/strategies/"+m[1], `{"permissions":{"enable-audio":false,"enable-tunnel":"Y"}}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"permissions":{"enable-audio":"N","enable-tunnel":"Y"}`) {
			t.Errorf("update: %d %s", got.Status, got.Body)
		}
	})
}

// TestAgentErrorsAreTheAgents: an agent that answers a request with the
// error agent_offline or agent_timeout gets 502 with its text, like any
// other error of an agent, and the server's snapshot is not asked for;
// the snapshot, the queued collection, 503 and 504 are for an agent that
// is not connected or does not answer in time (BUGS WS8; the replay
// covers those).
func TestAgentErrorsAreTheAgents(t *testing.T) {
	db := storetest.Migrated(t)
	t.Cleanup(wstest.Goroutines(t))
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch {
		case c.Method == "GET" && c.Target == "/api/peers/A1001":
			return routetest.Reply{Status: 200, Body: `{"id":"A1001","hostname":"a"}`}
		case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/peers/A1001/telemetry"):
			return routetest.Reply{Status: 200, Body: `{"snapshots":{"services":{"data":[{"name":"cached"}],"collected_at":"2020-01-01T00:00:00Z"}}}`}
		case c.Method != "GET":
			return routetest.Reply{Status: 200, Body: `{"status":"queued"}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	w := routetest.New(t, routetest.Options{DB: db, API: api, Env: raisedLimits, Config: nodeLanguages})
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS device_tokens (token_hash TEXT, peer_id TEXT, status TEXT,
		max_uses INTEGER, use_count INTEGER, expires_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(agentToken))
	if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked, token_kind)
		VALUES (?, ?, 1, 'A1001', '', '2999-01-01 00:00:00', ?, 'device')`, agentToken, hex.EncodeToString(sum[:]), false); err != nil {
		t.Fatal(err)
	}
	agent, err := fakeagent.Connect(w.Addr(), "A1001", agentToken)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	admin := w.As("admin")
	for _, reason := range []string{"agent_offline", "agent_timeout"} {
		agent.Answer(func(fakeagent.Request) any { return fakeagent.JSON(`{"ok":false,"error":"` + reason + `"}`) })
		api.Calls()
		got := admin.Do("GET", "/api/devices/A1001/services", "")
		if want := `{"success":false,"error":"` + reason + `"}`; got.Status != http.StatusBadGateway || got.Body != want {
			t.Errorf("an agent that answers %s: %d %s, want 502 %s", reason, got.Status, got.Body, want)
		}
		if calls := api.Calls(); len(calls) != 1 {
			t.Errorf("an agent that answers %s: calls %q, want only the device lookup", reason, calls)
		}
	}
}
