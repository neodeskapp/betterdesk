package devices_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The rows of the full review of 21 Nov (BUGS RV rows) that the device
// routes fixed, each with the behaviour the fix changed.

// TestGroupWritesKeepTheScope: an account whose device scope is limited
// changes and deletes only the device groups open to it, sets or changes
// no access list and makes no tag group, with either writer; each refusal
// is 403 before anything is written here or on the API server, and the
// account still may not see device C afterwards. The admin and an
// operator who sees every device do all of it (BUGS RV59).
func TestGroupWritesKeepTheScope(t *testing.T) {
	for _, node := range []bool{false, true} {
		storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
			w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
			ctx := context.Background()
			for _, q := range []string{
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-ops', 'Ops', '', '', 'manual', '')`,
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'g-ops'`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-vip', 'VIP', '', '', 'tag', 'vip')`,
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'g-vip'`,
			} {
				if _, err := db.Exec(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			groups := func() string {
				t.Helper()
				rows, err := db.Query(ctx, `SELECT g.guid || ':' || g.name || ':' || g.source_type || ':' || g.tag_filter || ':' || COALESCE(u.username, '')
					FROM device_groups g LEFT JOIN device_group_user_access a ON a.device_group_id = g.id LEFT JOIN users u ON u.id = a.user_id
					ORDER BY g.guid, u.username`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var out []string
				for rows.Next() {
					var s string
					if err := rows.Scan(&s); err != nil {
						t.Fatal(err)
					}
					out = append(out, s)
				}
				return strings.Join(out, ", ")
			}
			before := groups()
			operator := w.As("operator")
			forbidden := `{"success":false,"error":"You don't have access to this device."}`
			for _, c := range []struct{ method, path, body string }{
				{"POST", "/api/device-groups", `{"guid":"g-viewer","name":"Viewer only","source_type":"manual","tag_filter":"","allowed_users":"e2e_viewer, e2e_operator","allowed_groups":[]}`},
				{"POST", "/api/device-groups", `{"guid":"g-viewer","name":"Renamed","source_type":"manual","tag_filter":"","allowed_users":"e2e_viewer","allowed_groups":[]}`},
				{"DELETE", "/api/device-groups/g-viewer", ``},
				{"POST", "/api/device-groups", `{"name":"Mine","source_type":"tag","tag_filter":"vip","allowed_users":["e2e_operator"]}`},
				{"POST", "/api/device-groups", `{"name":"Mine","source_type":"tag","tag_filter":"vip"}`},
				{"POST", "/api/device-groups", `{"name":"Mine","allowed_users":"e2e_operator"}`},
				{"POST", "/api/device-groups", `{"name":"Mine","allowed_groups":["ug-1"]}`},
				{"POST", "/api/device-groups", `{"guid":"g-ops","name":"Ops","source_type":"manual","tag_filter":"","allowed_users":"e2e_operator, e2e_viewer","allowed_groups":[]}`},
				{"POST", "/api/device-groups", `{"guid":"g-ops","name":"Ops","source_type":"manual","tag_filter":"","allowed_users":"","allowed_groups":[]}`},
				{"POST", "/api/device-groups", `{"guid":"g-ops","name":"Ops","source_type":"tag","tag_filter":"lab","allowed_users":"e2e_operator","allowed_groups":[]}`},
				{"POST", "/api/device-groups", `{"guid":"g-vip","name":"VIP","source_type":"tag","tag_filter":"secret","allowed_users":"e2e_operator","allowed_groups":[]}`},
			} {
				api.Calls()
				if got := operator.Do(c.method, c.path, c.body); got.Status != http.StatusForbidden || got.Body != forbidden {
					t.Errorf("%s %s %s (console writer %v): %d %s, want 403 %s", c.method, c.path, c.body, node, got.Status, got.Body, forbidden)
				}
				for _, call := range api.Calls() {
					if !strings.HasPrefix(call, "GET ") {
						t.Errorf("%s %s %s (console writer %v) reached the server: %s", c.method, c.path, c.body, node, call)
					}
				}
				if got := groups(); got != before {
					t.Fatalf("%s %s %s (console writer %v) wrote: %s, want %s", c.method, c.path, c.body, node, got, before)
				}
			}
			if got := operator.Do("GET", "/api/devices/C", ""); got.Status != http.StatusForbidden {
				t.Errorf("device C after the refused writes (console writer %v): %d %s", node, got.Status, got.Body)
			}
			// What keeps the scope goes through: a group open to the
			// account renamed with its lists as they are, a tag group
			// renamed with its tag, a new manual group without a list
			for _, body := range []string{
				`{"guid":"g-ops","name":"Operations","source_type":"manual","tag_filter":"","allowed_users":"e2e_operator","allowed_groups":[]}`,
				`{"guid":"g-vip","name":"Very important","source_type":"tag","tag_filter":"vip","allowed_users":"e2e_operator","allowed_groups":[]}`,
				`{"name":"Plain","source_type":"manual","tag_filter":"","allowed_users":"","allowed_groups":[]}`,
			} {
				if got := operator.Do("POST", "/api/device-groups", body); got.Status != http.StatusOK {
					t.Errorf("%s as operator (console writer %v): %d %s, want 200", body, node, got.Status, got.Body)
				}
			}
			if got := operator.Do("DELETE", "/api/device-groups/g-ops", ""); got.Status != http.StatusOK {
				t.Errorf("delete of a group open to the operator (console writer %v): %d %s", node, got.Status, got.Body)
			}
			admin := w.As("admin")
			if got := admin.Do("POST", "/api/device-groups", `{"guid":"g-viewer","name":"Viewer only","allowed_users":"e2e_viewer, e2e_operator"}`); got.Status != http.StatusOK {
				t.Errorf("the admin's change of g-viewer (console writer %v): %d %s", node, got.Status, got.Body)
			}
			if got := admin.Do("POST", "/api/device-groups", `{"name":"Admins","source_type":"tag","tag_filter":"vip","allowed_users":"e2e_viewer"}`); got.Status != http.StatusOK {
				t.Errorf("the admin's tag group (console writer %v): %d %s", node, got.Status, got.Body)
			}
			// Without an access list on any group and without grants the
			// operator sees every device, and sets lists and tag groups
			for _, q := range []string{`DELETE FROM device_group_user_access`, `DELETE FROM device_group_user_group_access`} {
				if _, err := db.Exec(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			if got := operator.Do("POST", "/api/device-groups", `{"name":"Lab","source_type":"tag","tag_filter":"lab","allowed_users":"e2e_operator"}`); got.Status != http.StatusOK {
				t.Errorf("a tag group with a list as an operator who sees every device (console writer %v): %d %s", node, got.Status, got.Body)
			}
		})
	}
}
