package guest

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// scopeDevices is the device table of TestScopeReadsTheDevicesFolderAndTags:
// dev-f in folder 1, dev-t with the tag secret, dev-1 in neither.
var scopeDevices = map[string]*goapi.Object{
	"dev-f": goapi.NewObject("id", "dev-f", "folder_id", float64(1)),
	"dev-t": goapi.NewObject("id", "dev-t", "tags", "secret"),
	"dev-1": goapi.NewObject("id", "dev-1"),
}

// TestScopeReadsTheDevicesFolderAndTags runs the create route on the real
// device scope with the devices as the device layer has them: a device in
// a folder, or with a tag, whose group is open to another account only is
// outside the operator's scope, and so is an id that is no device; under
// a restricted default a device the folder's group opens to the account
// is inside (RV109).
func TestScopeReadsTheDevicesFolderAndTags(t *testing.T) {
	for _, def := range []string{"", "restricted"} {
		t.Run("default "+def, func(t *testing.T) {
			db := storetest.Migrated(t)
			ctx := context.Background()
			for _, query := range []string{
				`INSERT INTO users (id, username, password_hash, role) VALUES (5, 'e2e_operator', 'x', 'operator')`,
				`INSERT INTO users (id, username, password_hash, role) VALUES (6, 'e2e_other', 'x', 'operator')`,
				`INSERT INTO folders (id, name, color, icon, sort_order) VALUES (1, 'Closed', '#ffffff', 'folder', 0)`,
				`INSERT INTO folders (id, name, color, icon, sort_order) VALUES (2, 'Open', '#ffffff', 'folder', 0)`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_1', 'Closed', '', '', 'manual', '')`,
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'folder_1'`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('tagged', 'Secret', '', '', 'tag', 'secret')`,
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'tagged'`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_2', 'Open', '', '', 'manual', '')`,
				`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 5 FROM device_groups WHERE guid = 'folder_2'`,
			} {
				if _, err := db.Exec(ctx, query); err != nil {
					t.Fatalf("%s: %v", query, err)
				}
			}
			devices := map[string]*goapi.Object{"dev-o": goapi.NewObject("id", "dev-o", "folder_id", "2")}
			for id, device := range scopeDevices {
				devices[id] = device
			}
			scopes := access.New(db, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: def})
			w := &world{}
			w.World = proxytest.NewWorld(t, func(d *kit.Deps) {
				d.Access = scopes
				d.GuestAccess = guestaccess.New(guestaccess.Options{GoAPI: d.GoAPI})
				register(d, func(ctx context.Context, user *access.ScopeUser, list []access.Device) (deviceScope, error) {
					return scopes.DeviceScopeForUser(ctx, user, list)
				}, func(_ context.Context, id string) *goapi.Object { return devices[id] })
			})
			created := &proxytest.Answer{Status: 201, Type: "application/json", Body: `{"id":"g1"}`}
			post := func(username, role string, id int64, peers string) int {
				account := &proxytest.Account{UserID: id}
				account.User.Username, account.User.Role = username, role
				body := `{"peer_ids":` + peers + `}`
				rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/guest/access-links", Session: account, Type: "application/json", Body: &body}, created)
				return rec.Code
			}
			want := map[string]int{`["dev-f"]`: 403, `["dev-t"]`: 403, `["nope"]`: 403, `["dev-o"]`: 201}
			if def == "" {
				want[`["dev-1"]`] = 201
			} else {
				want[`["dev-1"]`] = 403
			}
			for peers, status := range want {
				if got := post("e2e_operator", "operator", 5, peers); got != status {
					t.Errorf("operator, %s: %d, want %d", peers, got, status)
				}
			}
			if got := post("e2e_other", "operator", 6, `["dev-f","dev-t"]`); got != 201 {
				t.Errorf("the operator the groups are open to: %d", got)
			}
			if got := post("e2e_admin", "admin", 1, `["dev-f","nope"]`); got != 201 {
				t.Errorf("admin: %d", got)
			}
		})
	}
}
