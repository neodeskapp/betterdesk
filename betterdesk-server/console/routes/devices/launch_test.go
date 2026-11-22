package devices_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "github.com/unitronix/betterdesk-server/api"
	serverconfig "github.com/unitronix/betterdesk-server/config"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/db"
	"github.com/unitronix/betterdesk-server/peer"
)

// The rows of the launch (BUGS L rows) that the device routes fixed, each
// with the behaviour the fix changed.

// TestDeleteTagClearsTheGroupsThatFilterOnIt: deleting a tag takes it off
// the devices and out of the filter of every tag group that selects by it,
// in any letter case, with either writer of the groups. Other tag groups
// and manual groups keep what they have, and the answer counts the groups
// it changed (BUGS L99).
func TestDeleteTagClearsTheGroupsThatFilterOnIt(t *testing.T) {
	for _, node := range []bool{false, true} {
		storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
			w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = node })
			api.On("GET /api/peers", http.StatusOK, `{"peers":[{"id":"A","hostname":"a","tags":["Lab","x"]},{"id":"C","hostname":"c","tags":["other"]}]}`)
			ctx := context.Background()
			for _, q := range []string{
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-lab', 'Lab', '', '', 'tag', 'LAB')`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-other', 'Other', '', '', 'tag', 'other')`,
				`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-plain', 'Plain', '', '', 'manual', '')`,
			} {
				if _, err := db.Exec(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			got := w.As("admin").Do("POST", "/api/tags/delete", `{"tag":"lab"}`)
			if got.Status != http.StatusOK || !strings.Contains(got.Body, `"affectedGroups":1`) || !strings.Contains(got.Body, `"updatedDevices":1`) {
				t.Errorf("delete of the tag lab (console writer %v): %d %s", node, got.Status, got.Body)
			}
			rows, err := db.Query(ctx, `SELECT guid || ':' || source_type || ':' || tag_filter FROM device_groups WHERE guid IN ('g-lab', 'g-other', 'g-plain') ORDER BY guid`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var groups []string
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				groups = append(groups, s)
			}
			if want := "g-lab:tag:, g-other:tag:other, g-plain:manual:"; strings.Join(groups, ", ") != want {
				t.Errorf("groups after the delete (console writer %v): %s, want %s", node, strings.Join(groups, ", "), want)
			}
		})
	}
}

// TestConnectPasswordNeedsTheDevicesOrganization: the stored connect
// password of a device in an organization goes only to an account that is
// a member of that organization, or to a role that sees every
// organization. The console asks the API server as the signed-in account,
// so the server's membership check decides (BUGS L216).
func TestConnectPasswordNeedsTheDevicesOrganization(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		gdb := storetest.GoServer(t, sdb)
		srv := apiserver.New(serverconfig.DefaultConfig(), gdb, peer.NewMap(), nil, "test")
		if err := srv.InitPeerCredentialVault("l216-vault-secret-0123456789abcdef"); err != nil {
			t.Fatal(err)
		}
		w := routetest.New(t, routetest.Options{DB: sdb, API: srv.InternalHandler(), Env: raisedLimits, Config: nodeLanguages})

		if err := gdb.UpsertPeer(&db.Peer{ID: "ORGPEER1", UUID: "u-orgpeer1"}); err != nil {
			t.Fatal(err)
		}
		if err := gdb.CreateOrganization(&db.Organization{ID: "org-a", Name: "Org A", Slug: "org-a", Settings: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := gdb.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-a", DeviceID: "ORGPEER1"}); err != nil {
			t.Fatal(err)
		}
		put := httptest.NewRequest(http.MethodPut, "/api/org/org-a/peer-credentials/ORGPEER1", strings.NewReader(`{"password":"l216-stored-pw"}`))
		put = put.WithContext(apiserver.WithIdentity(put.Context(), apiserver.Identity{Username: "setup", Role: "super_admin"}))
		rec := httptest.NewRecorder()
		srv.InternalHandler().ServeHTTP(rec, put)
		if rec.Code != http.StatusOK {
			t.Fatalf("store the password: %d %s", rec.Code, rec.Body.String())
		}

		get := func(role string) routetest.Answer {
			return w.As(role).Do(http.MethodGet, "/api/devices/ORGPEER1/connect-password", "")
		}
		if got := get("operator"); got.Status != http.StatusForbidden || strings.Contains(got.Body, "l216-stored-pw") {
			t.Errorf("operator outside the organization: %d %s, want 403 without the password", got.Status, got.Body)
		}
		for _, role := range []string{"super_admin", "admin", "global_admin"} {
			if got := get(role); got.Status != http.StatusOK || !strings.Contains(got.Body, `"password":"l216-stored-pw"`) {
				t.Errorf("%s outside the organization: %d %s, want 200 with the password", role, got.Status, got.Body)
			}
		}

		if err := gdb.CreateOrgUser(&db.OrgUser{ID: "ou-operator", OrgID: "org-a", ServerUserID: routetest.RoleIDs["operator"], Username: "e2e_operator", Role: "user"}); err != nil {
			t.Fatal(err)
		}
		if got := get("operator"); got.Status != http.StatusOK || !strings.Contains(got.Body, `"password":"l216-stored-pw"`) {
			t.Errorf("operator in the organization: %d %s, want 200 with the password", got.Status, got.Body)
		}
	})
}
