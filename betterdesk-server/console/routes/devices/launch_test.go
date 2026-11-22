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
