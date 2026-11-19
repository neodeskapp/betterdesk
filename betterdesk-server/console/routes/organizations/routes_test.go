package organizations_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

func want(t *testing.T, what string, got routetest.Answer, status int, body string) {
	t.Helper()
	if got.Status != status || got.Body != body {
		t.Errorf("%s:\n got %d %s\nwant %d %s", what, got.Status, got.Body, status, body)
	}
}

func calls(t *testing.T, what string, api *routetest.API, wanted ...string) {
	t.Helper()
	if got := strings.Join(api.Calls(), "\n"); got != strings.Join(wanted, "\n") {
		t.Errorf("%s: calls\n%s\nwant\n%s", what, got, strings.Join(wanted, "\n"))
	}
}

func exec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestPassingOn(t *testing.T) {
	api := routetest.NewAPI()
	w := routetest.New(t, routetest.Options{API: api})
	admin, reader := w.As("admin"), w.As("operator")

	api.On("GET /api/org", 200, `{"organizations":[{"id":"o1","name":"One"}]}`)
	want(t, "list", reader.Do("GET", "/api/panel/org", ""), 200, `{"organizations":[{"id":"o1","name":"One"}]}`)
	api.On("GET /api/org/o1/settings", 200, `{"timezone":"UTC"}`)
	want(t, "settings, as an operator", reader.Do("GET", "/api/panel/org/o1/settings", ""), 200, `{"timezone":"UTC"}`)
	calls(t, "reads", api, "GET /api/org", "GET /api/org/o1/settings")

	// A write goes on with the body; the API server's refusal comes back
	api.On("PUT /api/org/o1", 409, `{"error":"slug taken"}`)
	want(t, "update", admin.Do("PUT", "/api/panel/org/o1", `{"name":"x","__proto__":{"a":1}}`), 409, `{"error":"slug taken"}`)
	calls(t, "update", api, `PUT /api/org/o1 {"name":"x"}`)
	// No body from the API server, or no answer at all
	api.On("DELETE /api/org/o1", 503, ``)
	want(t, "delete", admin.Do("DELETE", "/api/panel/org/o1", ""), 503, `{"error":"Go server unreachable"}`)
	api.Calls()

	// Ids are checked before anything is sent
	want(t, "an odd org id", reader.Do("GET", "/api/panel/org/bad$id/devices", ""), 400, `{"error":"Invalid orgId"}`)
	want(t, "an odd user id", admin.Do("PUT", "/api/panel/org/o1/users/a%20b", `{}`), 400, `{"error":"Invalid userId"}`)
	want(t, "an odd device id", admin.Do("DELETE", "/api/panel/org/o1/devices/x$", ""), 400, `{"error":"Invalid deviceId"}`)
	calls(t, "refused ids", api)

	// The peer id of the credential vault is only escaped
	api.On("PUT /api/org/o%3A1/peer-credentials/a%3Ab%40c%24", 400, `{"error":"invalid peer id"}`)
	want(t, "credential", admin.Do("PUT", "/api/panel/org/o:1/peer-credentials/a:b@c$", `{"password":"x"}`), 400, `{"error":"invalid peer id"}`)
	calls(t, "credential", api, `PUT /api/org/o%3A1/peer-credentials/a%3Ab%40c%24 {"password":"x"}`)
	// "." gets through to the API client's path check, which refuses the
	// call: that is an unanswered call here, not a 400 (BUGS OR1)
	want(t, "credential .", admin.Do("DELETE", "/api/panel/org/o1/peer-credentials/%2E", ""), 500, `{"error":"Go server unreachable"}`)
	calls(t, "credential .", api)

	// A failure of the API server goes back as it came; an id with a
	// slash in it is refused
	api.On("GET /api/org/org-42", 502, `{"error":"Upstream failure"}`)
	want(t, "detail", reader.Do("GET", "/api/panel/org/org-42", ""), 502, `{"error":"Upstream failure"}`)
	calls(t, "detail", api, "GET /api/org/org-42")
	want(t, "a slash in the id", reader.Do("GET", "/api/panel/org/foo%2Fbar", ""), 400, `{"error":"Invalid orgId"}`)
	calls(t, "a slash in the id", api)

	// Permissions
	if got := reader.Do("PUT", "/api/panel/org/o1/settings", `{}`); got.Status != 403 || !strings.Contains(got.Body, "org.edit") {
		t.Errorf("an operator writes settings: %d %s", got.Status, got.Body)
	}
	want(t, "an operator creates", w.As("operator").Do("POST", "/api/panel/org", `{"name":"Ops","slug":"ops"}`), 403, `{"success":false,"error":"Permission denied: org.create"}`)
	if got := w.SignedOut().Do("GET", "/api/panel/org", ""); got.Status != 401 {
		t.Errorf("signed out: %d %s", got.Status, got.Body)
	}
	calls(t, "refused", api)
}

func TestCreate(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		admin := w.As("admin")
		exec(t, db, `DELETE FROM device_groups`)

		api.On("POST /api/org", 201, `{"id":"org-1","name":"  Acme  ","slug":"acme"}`)
		want(t, "create", admin.Do("POST", "/api/panel/org", `{"name":"  Acme  ","slug":"acme"}`), 201, `{"id":"org-1","name":"  Acme  ","slug":"acme"}`)
		api.On("POST /api/org", 201, `{"id":"org-2","name":"   "}`)
		admin.Do("POST", "/api/panel/org", `{"name":"x"}`)
		api.On("POST /api/org", 201, `{"name":"no id"}`)
		admin.Do("POST", "/api/panel/org", `{"name":"x"}`)
		api.On("POST /api/org", 400, `{"error":"name is required"}`)
		want(t, "refused", admin.Do("POST", "/api/panel/org", `{}`), 400, `{"error":"name is required"}`)
		api.On("POST /api/org", 500, `null`)
		want(t, "no answer", admin.Do("POST", "/api/panel/org", `{}`), 500, `{"error":"Go server unreachable"}`)

		rows, err := db.Query(context.Background(), `SELECT name, note, team_id, source_type, tag_filter, guid FROM device_groups ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		guid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
		for rows.Next() {
			var name, note, team, kind, filter, id string
			if err := rows.Scan(&name, &note, &team, &kind, &filter, &id); err != nil {
				t.Fatal(err)
			}
			if !guid.MatchString(id) {
				t.Errorf("guid %q", id)
			}
			got = append(got, strings.Join([]string{name, note, team, kind, filter}, "|"))
		}
		if strings.Join(got, "\n") != "Acme|Default group created with organization (commercialization)|org-1|manual|\n"+
			"Organization|Default group created with organization (commercialization)|org-2|manual|" {
			t.Errorf("device groups made with organizations:\n%s", strings.Join(got, "\n"))
		}
	})
}

func TestRemoveMember(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(routetest.Call) routetest.Reply { return routetest.Reply{Status: 204} }
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		admin := w.As("admin")
		// An account's id goes on as that id, everything else as it came
		for _, id := range []string{"0x1", "1e0", "%201", "+5", "0005", "99", "someone", "-1", "1.5", "0"} {
			admin.Do("DELETE", "/api/panel/org/o1/members/"+id, "")
		}
		calls(t, "unlink", api,
			"DELETE /api/org/o1/members/1", "DELETE /api/org/o1/members/1", "DELETE /api/org/o1/members/1",
			"DELETE /api/org/o1/members/5", "DELETE /api/org/o1/members/5", "DELETE /api/org/o1/members/99",
			"DELETE /api/org/o1/members/someone", "DELETE /api/org/o1/members/-1", "DELETE /api/org/o1/members/1.5",
			"DELETE /api/org/o1/members/0")
		want(t, "an odd user id", admin.Do("DELETE", "/api/panel/org/o1/members/a$", ""), 400, `{"error":"Invalid userId"}`)
		want(t, "an odd org id", admin.Do("DELETE", "/api/panel/org/o$/members/1", ""), 400, `{"error":"Invalid orgId"}`)
		calls(t, "refused", api)
	})
}

func TestLinkedGroups(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.On("GET /api/peers", 200, `{"peers":[{"id":"A","tags":"lab"},{"id":"B","tags":"office"}]}`)
		api.Fallback = func(routetest.Call) routetest.Reply { return routetest.Reply{Status: 200, Body: `{"targets":[]}`} }
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		exec(t, db, `DELETE FROM device_groups`)
		exec(t, db, `DELETE FROM user_groups`)
		exec(t, db, `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES
			('g-tag', 'Lab', 'n', ' o1 ', 'tag', 'LAB'), ('folder_3', 'Folder', '', 'o1', 'manual', ''),
			('g-other', 'Other', '', 'o2', 'manual', ''), ('g-manual', 'Manual', NULL, 'o1', 'manual', '')`)
		exec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'X' FROM device_groups WHERE guid = 'g-manual'`)
		exec(t, db, `INSERT INTO user_groups (guid, name, note, team_id) VALUES ('u1', 'Team', NULL, 'o1'), ('u2', 'Else', 'x', 'o3')`)
		exec(t, db, `INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 1 FROM user_groups WHERE guid = 'u1'`)

		got := w.As("operator").Do("GET", "/api/panel/org/%20o1%20/device-groups", "")
		body := regexp.MustCompile(`"created_at":"[^"]+"`).ReplaceAllString(got.Body, `"created_at":"T"`)
		body = regexp.MustCompile(`"id":\d+`).ReplaceAllString(body, `"id":0`)
		want(t, "linked groups", routetest.Answer{Status: got.Status, Body: body}, 200, `{"org_id":"o1","device_groups":[`+
			`{"id":0,"guid":"g-tag","name":"Lab","note":"n","team_id":" o1 ","source_type":"tag","tag_filter":"LAB","created_at":"T","member_count":1,"allowed_users":[],"allowed_groups":[],"allowed_user_groups":[]},`+
			`{"id":0,"guid":"g-manual","name":"Manual","note":null,"team_id":"o1","source_type":"manual","tag_filter":"","created_at":"T","member_count":1,"allowed_users":[],"allowed_groups":[],"allowed_user_groups":[]}],`+
			`"user_groups":[{"guid":"u1","name":"Team","note":"","member_count":1}]}`)
		want(t, "an odd org id", w.As("operator").Do("GET", "/api/panel/org/o$/device-groups", ""), 400, `{"error":"Invalid orgId"}`)

		exec(t, db, `DROP TABLE device_group_members`)
		want(t, "a table that fails", w.As("operator").Do("GET", "/api/panel/org/o1/device-groups", ""), 500, `{"error":"Failed to load organization groups"}`)
	})
}

// The roster of an organization's users needs user.view on top of
// org.manage_users.
func TestRosterNeedsUserView(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/org/org-1/users", 200, `[]`)
	w := routetest.New(t, routetest.Options{API: api, Roles: routetest.Overrides{"operator": {"org.manage_users": true, "user.view": false}}})
	if got := w.As("operator").Do("GET", "/api/panel/org/org-1/users", ""); got.Status != 403 || !strings.Contains(got.Body, "user.view") {
		t.Errorf("roster without user.view: %d %s", got.Status, got.Body)
	}
	calls(t, "roster without user.view", api)
	want(t, "roster as admin", w.As("admin").Do("GET", "/api/panel/org/org-1/users", ""), 200, `[]`)
}

// TestDeleteKeepsTheDeviceGroupWithoutItsTeam: deleting an organization
// keeps the device group made with it, members included, and clears its
// team id; a delete the API server refuses changes nothing.
func TestDeleteKeepsTheDeviceGroupWithoutItsTeam(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		admin := w.As("admin")
		exec(t, db, `DELETE FROM device_groups`)
		api.On("POST /api/org", 201, `{"id":"org-1","name":"Acme"}`)
		admin.Do("POST", "/api/panel/org", `{"name":"Acme"}`)
		exec(t, db, `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('other', 'Other', '', 'org-2', 'manual', '')`)
		exec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, '900100001' FROM device_groups WHERE team_id = 'org-1'`)
		groups := func() string {
			res, err := db.Query(context.Background(), `SELECT g.name || '|' || g.team_id || '|' || COUNT(m.peer_id) FROM device_groups g
				LEFT JOIN device_group_members m ON m.device_group_id = g.id GROUP BY g.id, g.name, g.team_id ORDER BY g.name`)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Close()
			var out []string
			for res.Next() {
				var row string
				if err := res.Scan(&row); err != nil {
					t.Fatal(err)
				}
				out = append(out, row)
			}
			return strings.Join(out, ",")
		}

		api.On("DELETE /api/org/org-1", 409, `{"error":"organization has devices"}`)
		want(t, "a refused delete", admin.Do("DELETE", "/api/panel/org/org-1", ""), 409, `{"error":"organization has devices"}`)
		if got := groups(); got != "Acme|org-1|1,Other|org-2|0" {
			t.Errorf("groups after a refused delete: %s", got)
		}
		api.On("DELETE /api/org/org-1", 200, `{"success":true}`)
		want(t, "delete", admin.Do("DELETE", "/api/panel/org/org-1", ""), 200, `{"success":true}`)
		if got := groups(); got != "Acme||1,Other|org-2|0" {
			t.Errorf("groups after the delete: %s", got)
		}
		api.On("GET /api/peers", 200, `[]`)
		want(t, "groups of the deleted organization", admin.Do("GET", "/api/panel/org/org-1/device-groups", ""), 200,
			`{"org_id":"org-1","device_groups":[],"user_groups":[]}`)
	})
}

// G16 (DECISIONS A39): reading organizations needs user.view, so viewer and
// guest_issuer get 403 (the organizations section of the panel access guard
// refuses them first); the devices with a stored password need
// org.manage_devices, which server_admin lacks. Nothing reaches the API
// server for a refused read.
func TestReadingNeedsUserView(t *testing.T) {
	api := routetest.NewAPI()
	api.On("GET /api/org", 200, `{"organizations":[]}`)
	api.On("GET /api/org/o1/peer-credentials", 200, `{"credentials":[]}`)
	w := routetest.New(t, routetest.Options{API: api})
	refused := `{"success":false,"error":"Panel access denied: organizations","code":"panel_access_denied"}`
	for _, role := range []string{"viewer", "guest_issuer"} {
		for _, path := range []string{"/api/panel/org", "/api/panel/org/o1", "/api/panel/org/o1/devices", "/api/panel/org/o1/settings",
			"/api/panel/org/o1/address-book", "/api/panel/org/o1/device-groups"} {
			want(t, role+" reads "+path, w.As(role).Do("GET", path, ""), 403, refused)
		}
		want(t, role+" reads the stored passwords", w.As(role).Do("GET", "/api/panel/org/o1/peer-credentials", ""), 403, refused)
	}
	want(t, "server_admin reads the stored passwords", w.As("server_admin").Do("GET", "/api/panel/org/o1/peer-credentials", ""), 403,
		`{"success":false,"error":"Permission denied: org.manage_devices"}`)
	calls(t, "refused reads", api)
	want(t, "server_admin lists", w.As("server_admin").Do("GET", "/api/panel/org", ""), 200, `{"organizations":[]}`)
	want(t, "an operator reads the stored passwords", w.As("operator").Do("GET", "/api/panel/org/o1/peer-credentials", ""), 200, `{"credentials":[]}`)
	calls(t, "allowed reads", api, "GET /api/org", "GET /api/org/o1/peer-credentials")
}
