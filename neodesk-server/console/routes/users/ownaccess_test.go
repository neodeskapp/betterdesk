package users_test

import (
	"context"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
)

// TestNoAccountWidensItsOwnAccess: Edit users on a role that may give no
// role (a viewer given user.view and user.edit, as the audit did) changes
// the account's own password and email, but not what it may reach: its
// Direct devices, Folder access, user groups, strategy and panel access
// stay as they are. An account that may give the role still sets them
// (L390).
func TestNoAccountWidensItsOwnAccess(t *testing.T) {
	roles := routetest.Overrides{"viewer": {"user.view": true, "user.edit": true}}
	w := world(t, nil, routetest.Options{Roles: roles})
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `INSERT INTO user_groups (guid, name, note, team_id) VALUES ('ug-wide', 'Wide', '', '')`); err != nil {
		t.Fatal(err)
	}
	viewer := w.As("viewer")
	const own = "/api/users/6"
	for _, body := range []string{
		`{"peerIds":["A","B"]}`,
		`{"folderIds":[1]}`,
		`{"groupGuids":["ug-wide"]}`,
		`{"strategyGuid":""}`,
		`{"email":"v@example.org","peerIds":["A"]}`,
	} {
		if got := viewer.Do("PATCH", own, body); got.Status != 403 {
			t.Errorf("viewer widens its own account with %s: %d %s, want 403", body, got.Status, got.Body)
		}
	}
	// Panel access: limited to none in Devices by an admin, the viewer
	// cannot lift the limit, but may lower its own access further
	if got := w.As("admin").Do("PUT", "/api/panel/access/users/6", `{"sections":[{"resource_key":"devices","access_level":"none"}],"features":[{"feature_key":"terminal","enabled":false}]}`); got.Status != 200 {
		t.Fatalf("admin limits the viewer: %d %s", got.Status, got.Body)
	}
	for _, body := range []string{
		`{"sections":[{"resource_key":"devices","access_level":"read"}],"features":[{"feature_key":"terminal","enabled":false}]}`,
		`{"sections":[{"resource_key":"devices","access_level":"none"}],"features":[]}`,
		`{}`,
	} {
		if got := viewer.Do("PUT", "/api/panel/access/users/6", body); got.Status != 403 {
			t.Errorf("viewer lifts its own panel access limit with %s: %d %s, want 403", body, got.Status, got.Body)
		}
	}
	if got := viewer.Do("PUT", "/api/panel/access/users/6", `{"sections":[{"resource_key":"devices","access_level":"none"},{"resource_key":"activity","access_level":"none"}],"features":[{"feature_key":"terminal","enabled":false}]}`); got.Status != 200 {
		t.Errorf("viewer lowers its own panel access: %d %s, want 200", got.Status, got.Body)
	}
	grants, err := w.Server.Deps().Access.UserPeerGrantIDs(ctx, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 0 {
		t.Errorf("the viewer granted itself devices: %v", grants)
	}
	var groups int
	if err := w.DB.QueryRow(ctx, `SELECT COUNT(*) FROM user_group_members WHERE user_id = 6`).Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if groups != 0 {
		t.Errorf("the viewer joined %d user groups", groups)
	}

	// Its own email and password it still changes: a new address is not
	// refused for its role, only because this console has no mail settings
	// to confirm it with (A272)
	if got := viewer.Do("PATCH", own, `{"email":"v@example.org"}`); got.Status != 409 || !strings.Contains(got.Body, `"code":"email_change_needs_mail"`) {
		t.Errorf("viewer sets its own email: %d %s, want 409 email_change_needs_mail", got.Status, got.Body)
	}
	// An account that may give viewer sets them
	global := w.As("global_admin")
	if got := global.Do("PATCH", own, `{"peerIds":["A"],"groupGuids":["ug-wide"]}`); got.Status != 200 || !strings.Contains(got.Body, `"peer_grants":["A"]`) {
		t.Errorf("global_admin sets a viewer's devices: %d %s, want 200", got.Status, got.Body)
	}
}
