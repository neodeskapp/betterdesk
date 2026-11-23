package panelaccess_test

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The rows of the launch (BUGS L rows) that the panel access routes fixed.

// TestSaveKeepsTheSaversWriteToUsers: a save that would leave the saving
// account without write access to Users, through its own rows or a user
// group it is in, is refused with the reason and stores nothing. Saves
// that keep it, saves for others and saves by a super admin, whose rows
// never apply to itself, go through (BUGS L110).
func TestSaveKeepsTheSaversWriteToUsers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		// The console's own texts, for the reason of the refusal
		w := routetest.New(t, routetest.Options{DB: db, Config: func(cfg *config.Config) { cfg.LangDir = "../../static/files/lang" }})
		exec(t, w.DB, "INSERT INTO user_groups (guid, name) VALUES ('mine', 'Mine'), ('others', 'Others')")
		exec(t, w.DB, "INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 4 FROM user_groups WHERE guid = 'mine'")
		exec(t, w.DB, "INSERT INTO user_group_members (user_group_id, user_id) SELECT id, 5 FROM user_groups WHERE guid = 'others'")
		refused := `{"success":false,"error":"You can't take away your own write access to Users: you could no longer change panel access."}`
		global := w.As("global_admin")
		for _, c := range []struct{ path, body string }{
			{"/api/panel/access/users/4", `{"sections":[{"resource_key":"users","access_level":"read"}]}`},
			{"/api/panel/access/users/4", `{"sections":[{"resource_key":"users","access_level":"none"},{"resource_key":"devices","access_level":"write"}]}`},
			{"/api/panel/access/users/4", `{"sections":[{"resource_key":"group:administration","access_level":"read"}]}`},
		} {
			if got := global.Do("PUT", c.path, c.body); got.Status != 400 || got.Body != refused {
				t.Errorf("global_admin saves %s %s: %d %s", c.path, c.body, got.Status, got.Body)
			}
		}
		// The group's rows count for the account only when its own rows
		// do not name the section
		if got := global.Do("PUT", "/api/panel/access/groups/mine", `{"sections":[{"resource_key":"users","access_level":"none"}]}`); got.Status != 400 || got.Body != refused {
			t.Errorf("global_admin saves its own group: %d %s", got.Status, got.Body)
		}
		var stored int
		if err := w.DB.QueryRow(context.Background(), "SELECT COUNT(*) FROM panel_access_policies").Scan(&stored); err != nil || stored != 0 {
			t.Errorf("rows stored by refused saves: %d %v", stored, err)
		}
		if rows := w.AuditRows(); len(rows) != 0 {
			t.Errorf("audit log after refused saves: %q", rows)
		}
		if got := global.Do("PUT", "/api/panel/access/users/4", `{}`); got.Status != 200 {
			t.Errorf("global_admin clears its own rows: %d %s", got.Status, got.Body)
		}
		for _, c := range []struct{ path, body string }{
			{"/api/panel/access/users/4", `{"sections":[{"resource_key":"users","access_level":"write"},{"resource_key":"devices","access_level":"none"}]}`},
			{"/api/panel/access/groups/mine", `{"sections":[{"resource_key":"users","access_level":"none"}]}`},
			{"/api/panel/access/users/5", `{"sections":[{"resource_key":"users","access_level":"none"}]}`},
			{"/api/panel/access/groups/others", `{"sections":[{"resource_key":"group:administration","access_level":"none"}]}`},
		} {
			if got := global.Do("PUT", c.path, c.body); got.Status != 200 {
				t.Errorf("global_admin saves %s %s: %d %s", c.path, c.body, got.Status, got.Body)
			}
		}
		for _, role := range []string{"admin", "super_admin"} {
			id := map[string]string{"admin": "1", "super_admin": "2"}[role]
			if got := w.As(role).Do("PUT", "/api/panel/access/users/"+id, `{"sections":[{"resource_key":"users","access_level":"none"}]}`); got.Status != 200 {
				t.Errorf("%s saves users none for itself: %d %s", role, got.Status, got.Body)
			}
		}
	})
}
