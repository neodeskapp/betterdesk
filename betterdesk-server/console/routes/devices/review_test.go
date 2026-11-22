package devices_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
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
			// operator sees every device and makes tag groups; access lists
			// need user.edit, which it has not (BUGS RV59, A91)
			for _, q := range []string{`DELETE FROM device_group_user_access`, `DELETE FROM device_group_user_group_access`} {
				if _, err := db.Exec(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			if got := operator.Do("POST", "/api/device-groups", `{"name":"Lab","source_type":"tag","tag_filter":"lab"}`); got.Status != http.StatusOK {
				t.Errorf("a tag group as an operator who sees every device (console writer %v): %d %s", node, got.Status, got.Body)
			}
			if got := operator.Do("POST", "/api/device-groups", `{"name":"Lab 2","allowed_users":"e2e_operator"}`); got.Status != http.StatusForbidden || got.Body != forbidden {
				t.Errorf("a group with a list as an operator who sees every device (console writer %v): %d %s", node, got.Status, got.Body)
			}
		})
	}
}

// TestDeletingAGroupWithAnAccessListNeedsUserEdit: deleting a device group
// that has an access list opens its devices to everyone, so an operator
// (device.edit, no user.edit) deletes only a group without a list, also
// when the list is open to it; the refusal is 403, writes nothing and
// calls no write on the API server. A global admin deletes the group with
// a list (L47).
func TestDeletingAGroupWithAnAccessListNeedsUserEdit(t *testing.T) {
	for _, node := range []bool{false, true} {
		storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
			w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
			operator, globalAdmin := w.As("operator"), w.As("global_admin")
			for _, q := range []string{
				`DELETE FROM device_group_user_access`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-lab', 'Lab', '', '', 'manual', ''), ('g-team', 'Team', '', '', 'manual', '')`,
				// g-viewer's list names e2e_operator, g-team's a user group
				// it is in: both are open to it
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'g-viewer'`,
				`INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-1', 'Support', '', '')`,
				`INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 5 FROM user_groups WHERE guid = 'ug-1'`,
				`INSERT INTO device_group_user_group_access (device_group_id, user_group_id) SELECT d.id, u.id FROM device_groups d, user_groups u WHERE d.guid = 'g-team' AND u.guid = 'ug-1'`,
			} {
				if _, err := db.Exec(context.Background(), q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			forbidden := `{"success":false,"error":"You don't have access to this device."}`
			for _, guid := range []string{"g-viewer", "g-team"} {
				api.Calls()
				if got := operator.Do("DELETE", "/api/device-groups/"+guid, ""); got.Status != http.StatusForbidden || got.Body != forbidden {
					t.Errorf("delete %s as operator (console writer %v): %d %s, want 403 %s", guid, node, got.Status, got.Body, forbidden)
				}
				for _, call := range api.Calls() {
					if !strings.HasPrefix(call, "GET ") {
						t.Errorf("delete %s as operator (console writer %v) reached the server: %s", guid, node, call)
					}
				}
			}
			if got := groupGUIDs(t, db); got != "g-lab,g-team,g-viewer" {
				t.Errorf("groups after the refusals (console writer %v): %s", node, got)
			}
			if got := operator.Do("DELETE", "/api/device-groups/g-lab", ""); got.Status != http.StatusOK {
				t.Errorf("delete the group without a list as operator (console writer %v): %d %s", node, got.Status, got.Body)
			}
			if got := globalAdmin.Do("DELETE", "/api/device-groups/g-viewer", ""); got.Status != http.StatusOK {
				t.Errorf("delete the group with a list as global admin (console writer %v): %d %s", node, got.Status, got.Body)
			}
		})
	}
}

// groupGUIDs lists the guids of the test's device groups (g-...), in
// order.
func groupGUIDs(t *testing.T, db *store.DB) string {
	t.Helper()
	res, err := db.Query(context.Background(), `SELECT guid FROM device_groups WHERE guid LIKE 'g-%' ORDER BY guid`)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	var out []string
	for res.Next() {
		var guid string
		if err := res.Scan(&guid); err != nil {
			t.Fatal(err)
		}
		out = append(out, guid)
	}
	return strings.Join(out, ",")
}

// TestGroupAccessListsNeedUserEdit: an operator who sees every device
// (device.edit, no user.edit) renames a device group and saves it with
// its lists as they are, but sets, changes and clears no access list; a
// global admin, who has user.edit, does (BUGS RV59, A91).
func TestGroupAccessListsNeedUserEdit(t *testing.T) {
	for _, node := range []bool{false, true} {
		storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
			w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
			operator, globalAdmin := w.As("operator"), w.As("global_admin")
			// No group has a list (the world's g-viewer loses its own), so
			// the operator's scope has no limit
			for _, q := range []string{
				`DELETE FROM device_group_user_access`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-lab', 'Lab', '', '', 'manual', '')`,
			} {
				if _, err := db.Exec(context.Background(), q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			forbidden := `{"success":false,"error":"You don't have access to this device."}`
			for _, body := range []string{
				`{"guid":"g-lab","name":"Lab","allowed_users":"e2e_operator"}`,
				`{"guid":"g-lab","name":"Lab","allowed_users":"","allowed_groups":["ug-1"]}`,
				`{"name":"New","allowed_users":"e2e_viewer"}`,
			} {
				api.Calls()
				if got := operator.Do("POST", "/api/device-groups", body); got.Status != http.StatusForbidden || got.Body != forbidden {
					t.Errorf("%s as operator (console writer %v): %d %s, want 403 %s", body, node, got.Status, got.Body, forbidden)
				}
				for _, call := range api.Calls() {
					if !strings.HasPrefix(call, "GET ") {
						t.Errorf("%s as operator (console writer %v) reached the server: %s", body, node, call)
					}
				}
			}
			for _, body := range []string{
				`{"guid":"g-lab","name":"Laboratory","allowed_users":"","allowed_groups":[]}`,
				`{"name":"Plain","allowed_users":"","allowed_groups":[]}`,
			} {
				if got := operator.Do("POST", "/api/device-groups", body); got.Status != http.StatusOK {
					t.Errorf("%s as operator (console writer %v): %d %s, want 200", body, node, got.Status, got.Body)
				}
			}
			if got := globalAdmin.Do("POST", "/api/device-groups", `{"guid":"g-lab","name":"Laboratory","allowed_users":"e2e_viewer"}`); got.Status != http.StatusOK {
				t.Errorf("the global admin's list (console writer %v): %d %s", node, got.Status, got.Body)
			}
		})
	}
}

// TestStrategyRoutesKeepTheScope: an account whose device scope is
// limited reads a rule's assignments without the devices outside its
// scope, other accounts and device groups closed to it; it may not
// change or delete a rule that has such an assignment, nor bind a rule
// to a closed device group. Each refusal is 403 and writes nothing, here
// or on the API server. The admin sees and changes every rule (BUGS
// RV63).
func TestStrategyRoutesKeepTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('A', '', 'guid-a'), ('C', '', 'guid-c')`,
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-open', 'Open', '', '', 'manual', '')`,
			`UPDATE users SET guid = 'guid-operator' WHERE username = 'e2e_operator'`,
			`UPDATE users SET guid = 'guid-viewer' WHERE username = 'e2e_viewer'`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		admin, operator := w.As("admin"), w.As("operator")
		create := func(a *routetest.Agent, body string) string {
			t.Helper()
			created := a.Do("POST", "/api/panel/strategies", body)
			m := strategyGUIDField.FindStringSubmatch(created.Body)
			if created.Status != http.StatusOK || m == nil {
				t.Fatalf("create %s: %d %s", body, created.Status, created.Body)
			}
			return "/api/panel/strategies/" + m[1]
		}
		mixed := create(admin, `{"name":"Mixed"}`)
		if got := admin.Do("POST", mixed+"/assign", `{"peers":["A","C"],"users":["e2e_operator","e2e_viewer"],"groups":["g-open","g-viewer"]}`); got.Status != http.StatusOK {
			t.Fatalf("assign as admin: %d %s", got.Status, got.Body)
		}
		mine := create(admin, `{"name":"Mine"}`)
		bound := create(admin, `{"name":"Bound","device_group_guid":"g-viewer"}`)
		state := func() string {
			t.Helper()
			var rules, assignments int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM strategies`).Scan(&rules); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM strategy_assignments`).Scan(&assignments); err != nil {
				t.Fatal(err)
			}
			var groups []string
			rows, err := db.Query(ctx, `SELECT name || '=' || COALESCE(device_group_guid, '') FROM strategies ORDER BY name`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				groups = append(groups, s)
			}
			return fmt.Sprintf("%d rules, %d assignments, %s", rules, assignments, strings.Join(groups, " "))
		}

		got := operator.Do("GET", mixed, "")
		for _, want := range []string{`"peer_count":1`, `"user_count":1`, `"device_group_count":1`, `"peers":["guid-a"]`, `"users":["guid-operator"]`, `"groups":["g-open"]`, `"peer_ids":["A"]`, `"user_names":["e2e_operator"]`, `"group_guids":["g-open"]`} {
			if got.Status != http.StatusOK || !strings.Contains(got.Body, want) {
				t.Errorf("the mixed rule as operator: %d %s, want %s", got.Status, got.Body, want)
			}
		}
		if got := admin.Do("GET", mixed, ""); !strings.Contains(got.Body, `"peer_ids":["A","C"]`) || !strings.Contains(got.Body, `"group_guids":["g-open","g-viewer"]`) || !strings.Contains(got.Body, `"user_names":["e2e_operator","e2e_viewer"]`) {
			t.Errorf("the mixed rule as admin: %d %s", got.Status, got.Body)
		}

		before := state()
		forbidden := `{"success":false,"error":"You don't have access to this device."}`
		for _, c := range []struct{ method, path, body string }{
			{"PATCH", mixed, `{"name":"Mixed","enabled":false}`},
			{"DELETE", mixed, ``},
			{"POST", "/api/panel/strategies", `{"name":"Closed","device_group_guid":"g-viewer"}`},
			{"PATCH", mine, `{"device_group_guid":"g-viewer"}`},
			{"PATCH", bound, `{"device_group_guid":"g-open"}`},
			{"DELETE", bound, ``},
		} {
			api.Calls()
			if got := operator.Do(c.method, c.path, c.body); got.Status != http.StatusForbidden || got.Body != forbidden {
				t.Errorf("%s %s %s as operator: %d %s, want 403 %s", c.method, c.path, c.body, got.Status, got.Body, forbidden)
			}
			for _, call := range api.Calls() {
				if !strings.HasPrefix(call, "GET ") {
					t.Errorf("%s %s %s as operator reached the server: %s", c.method, c.path, c.body, call)
				}
			}
			if got := state(); got != before {
				t.Fatalf("%s %s %s as operator wrote: %s, want %s", c.method, c.path, c.body, got, before)
			}
		}

		// What stays in the operator's scope goes through
		if got := operator.Do("GET", mine, ""); got.Status != http.StatusOK || !strings.Contains(got.Body, `"name":"Mine"`) || !strings.Contains(got.Body, `"peer_count":0`) {
			t.Errorf("the operator's own rule: %d %s", got.Status, got.Body)
		}
		create(operator, `{"name":"Open","device_group_guid":"g-open"}`)
		if got := operator.Do("PATCH", mine, `{"name":"Mine too","device_group_guid":"g-open"}`); got.Status != http.StatusOK {
			t.Errorf("a change of a rule in scope as operator: %d %s", got.Status, got.Body)
		}
		if got := operator.Do("DELETE", mine, ""); got.Status != http.StatusOK {
			t.Errorf("delete of a rule in scope as operator: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("PATCH", mixed, `{"device_group_guid":"g-viewer"}`); got.Status != http.StatusOK {
			t.Errorf("a change of the mixed rule as admin: %d %s", got.Status, got.Body)
		}
		if got := admin.Do("DELETE", mixed, ""); got.Status != http.StatusOK {
			t.Errorf("delete of the mixed rule as admin: %d %s", got.Status, got.Body)
		}
	})
}

// TestDeleteAndBanRevokeTheDeviceTokens: every delete of the device page
// (soft, hard, with cascade, the bulk delete) and its ban revoke the device
// tokens of each device the server deleted or banned, once the server did
// it; an unban gives no token back, and a delete or ban the server refused
// leaves the token live (BUGS RV227).
func TestDeleteAndBanRevokeTheDeviceTokens(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		authn := w.Server.Deps().Authn
		devices := []string{"dev-ban", "dev-soft", "dev-hard", "dev-linked", "dev-bulk1", "dev-bulk2", "dev-keep"}
		tokens := map[string]string{}
		for _, id := range devices {
			api.On("GET /api/peers/"+id, 200, `{"id":"`+id+`","hostname":"h"}`)
			token, err := authn.IssueDeviceToken(ctx, 1, id, "")
			if err != nil {
				t.Fatal(err)
			}
			tokens[id] = token
		}
		live := func(id string) bool {
			_, err := authn.RequireDeviceToken(ctx, "Bearer "+tokens[id])
			return err == nil
		}
		admin := w.As("admin")

		api.On("POST /api/peers/dev-keep/ban", 500, `{"error":"ban failed"}`)
		api.On("DELETE /api/peers/dev-keep", 500, `{"error":"delete failed"}`)
		api.On("DELETE /api/peers/dev-hard?cascade=true&hard=true", 200, `{"status":"deleted","id":"dev-hard","revoked":false,"cascaded":["dev-linked"]}`)
		for _, c := range []struct {
			method, path, body string
			status             int
		}{
			{"POST", "/api/devices/dev-keep/ban", `{}`, http.StatusBadGateway},
			{"DELETE", "/api/devices/dev-keep", ``, http.StatusInternalServerError},
			{"POST", "/api/devices/dev-ban/ban", `{"reason":"stolen"}`, http.StatusOK},
			{"POST", "/api/devices/dev-ban/unban", `{}`, http.StatusOK},
			{"DELETE", "/api/devices/dev-soft", ``, http.StatusOK},
			{"DELETE", "/api/devices/dev-hard?cascade=true&hard=true", ``, http.StatusOK},
			{"POST", "/api/devices/bulk-delete", `{"ids":["dev-bulk1","dev-bulk2"]}`, http.StatusOK},
		} {
			if got := admin.Do(c.method, c.path, c.body); got.Status != c.status {
				t.Errorf("%s %s: %d %s, want %d", c.method, c.path, got.Status, got.Body, c.status)
			}
		}
		for _, id := range devices {
			if want := id == "dev-keep"; live(id) != want {
				t.Errorf("device token of %s live: %v, want %v", id, !want, want)
			}
		}
	})
}
