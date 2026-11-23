package users_test

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// TestUsersRefuseBadValues: Add user and Edit refuse what they used to
// store or rewrite without a word, each with a reason, and write nothing:
// an unknown role (was Viewer), a device ID that is no ID (was stored and
// counted), a user group or folder that doesn't exist (was dropped) and an
// unknown RustDesk Pro strategy (was 500) (BUGS L397).
func TestUsersRefuseBadValues(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO folders (name) VALUES ('Office')`,
		`INSERT INTO user_groups (guid, name) VALUES ('g1', 'Group one')`,
	} {
		if _, err := w.DB.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	count := func() string {
		t.Helper()
		var s string
		if err := w.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM users) || ' ' || (SELECT COUNT(*) FROM user_peer_grants) || ' ' ||
			(SELECT COUNT(*) FROM user_group_members) || ' ' || (SELECT COUNT(*) FROM strategy_assignments)`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	admin := w.As("admin")
	for _, c := range []struct {
		method, path, body, reason string
	}{
		{"POST", "/api/users", `{"username":"bad_role","password":"Strong-Pass-1!","role":"root"}`, "Invalid role"},
		{"POST", "/api/users", `{"username":"bad_peer","password":"Strong-Pass-1!","peerIds":["<script>"]}`, "device ID"},
		{"PATCH", "/api/users/6", `{"peerIds":["x y"]}`, "device ID"},
		{"POST", "/api/users", `{"username":"bad_group","password":"Strong-Pass-1!","groupGuids":["no-such-group"]}`, "user group"},
		{"PATCH", "/api/users/6", `{"groupGuids":["g1","no-such-group"]}`, "user group"},
		{"PATCH", "/api/users/6", `{"folderIds":[1,999]}`, "folder"},
		{"POST", "/api/users", `{"username":"bad_strategy","password":"Strong-Pass-1!","strategyGuid":"missing"}`, "strategy"},
		{"PATCH", "/api/users/6", `{"strategyGuid":"missing"}`, "strategy"},
	} {
		before := count()
		got := admin.Do(c.method, c.path, c.body)
		if got.Status != 400 || !strings.Contains(got.Body, c.reason) {
			t.Errorf("%s %s %s: %d %s, want 400 naming %q", c.method, c.path, c.body, got.Status, got.Body, c.reason)
		}
		if after := count(); after != before {
			t.Errorf("%s %s: the refused request wrote (%s before, %s after)", c.method, c.body, before, after)
		}
	}
	// Right values still go through
	if got := admin.Do("PATCH", "/api/users/6", `{"peerIds":["900100001","dev-2"],"groupGuids":["g1"],"folderIds":[1]}`); got.Status != 200 {
		t.Errorf("good values: %d %s", got.Status, got.Body)
	}
}

// TestUserGroupEditKeepsWhatTheBodyLeavesOut: a user group edit that sends
// only a name keeps the note, and the answer counts the group's members
// (BUGS L397).
func TestUserGroupEditKeepsWhatTheBodyLeavesOut(t *testing.T) {
	w := world(t, nil, routetest.Options{})
	admin := w.As("admin")
	created := admin.Do("POST", "/api/panel/user-groups", `{"name":"Helpdesk","note":"First line"}`)
	if created.Status != 200 {
		t.Fatalf("create: %d %s", created.Status, created.Body)
	}
	guid := between(created.Body, `"guid":"`, `"`)
	if got := admin.Do("PATCH", "/api/users/6", `{"groupGuids":["`+guid+`"]}`); got.Status != 200 {
		t.Fatalf("member: %d %s", got.Status, got.Body)
	}
	got := admin.Do("PATCH", "/api/panel/user-groups/"+guid, `{"name":"Helpdesk team"}`)
	if got.Status != 200 || !strings.Contains(got.Body, `"note":"First line"`) || !strings.Contains(got.Body, `"member_count":1`) {
		t.Errorf("rename only: %d %s, want the note kept and member_count 1", got.Status, got.Body)
	}
	got = admin.Do("GET", "/api/panel/user-groups", "")
	if !strings.Contains(got.Body, `"name":"Helpdesk team","note":"First line"`) {
		t.Errorf("after a rename: %s, want the note kept", got.Body)
	}
}

func between(s, start, end string) string {
	_, rest, _ := strings.Cut(s, start)
	value, _, _ := strings.Cut(rest, end)
	return value
}
