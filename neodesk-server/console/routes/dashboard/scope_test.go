package dashboard_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestFeedAndCountsKeepToTheScope: an account limited to some devices
// gets the device counts of those, and no entry of the activity feed
// about a device outside its scope; entries without a device, or about
// something that is no device (a setting), stay. The
// viewer, whose scope has every device, gets all of them (BUGS RV64).
func TestFeedAndCountsKeepToTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		api := routetest.NewAPI()
		api.Fallback = func(c routetest.Call) routetest.Reply {
			switch {
			case c.Method == "GET" && (c.Target == "/api/peers" || c.Target == "/api/peers?include_deleted=true"):
				return routetest.Reply{Status: 200, Body: `{"peers":[{"id":"A","hostname":"a","live_online":true},{"id":"C","hostname":"c","live_online":true,"banned":true,"note":"x"}]}`}
			case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/remote-targets"):
				return routetest.Reply{Status: 200, Body: `{"targets":[]}`}
			case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/audit/events"):
				return routetest.Reply{Status: 200, Body: `{"events":[` +
					`{"action":"peer_banned","target":"C","details":{"reason":"secret reason for C"}},` +
					`{"action":"peer_banned","target":"A","details":{"reason":"reason for A"}},` +
					`{"action":"config_changed","details":{"message":"relay changed"}},` +
					`{"action":"config_changed","target":"availability_history_enabled","details":{"message":"setting changed"}}]}`}
			}
			return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
		}
		w := routetest.New(t, routetest.Options{DB: db, API: api})
		for _, q := range []string{
			`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('g-viewer', 'Viewer only', '', '', 'manual', '')`,
			`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 6 FROM device_groups WHERE guid = 'g-viewer'`,
			`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'C' FROM device_groups WHERE guid = 'g-viewer'`,
		} {
			if _, err := db.Exec(context.Background(), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		operator, viewer := w.As("operator"), w.As("viewer")
		feed := operator.Do("GET", "/api/dashboard/activity", "")
		if feed.Status != http.StatusOK || strings.Contains(feed.Body, "secret reason") || !strings.Contains(feed.Body, "reason for A") || !strings.Contains(feed.Body, "relay changed") || !strings.Contains(feed.Body, "setting changed") {
			t.Errorf("the operator's feed: %d %s", feed.Status, feed.Body)
		}
		if got := viewer.Do("GET", "/api/dashboard/activity", ""); !strings.Contains(got.Body, "secret reason") || !strings.Contains(got.Body, "reason for A") {
			t.Errorf("the viewer's feed: %d %s", got.Status, got.Body)
		}
		stats := operator.Do("GET", "/api/stats", "")
		if stats.Status != http.StatusOK || !strings.Contains(stats.Body, `"devices":{"total":1,"online":1,"offline":0,"banned":0,"withNotes":0}`) {
			t.Errorf("the operator's counts: %d %s", stats.Status, stats.Body)
		}
		if got := viewer.Do("GET", "/api/stats", ""); !strings.Contains(got.Body, `"devices":{"total":2,"online":2,"offline":0,"banned":1,"withNotes":1}`) {
			t.Errorf("the viewer's counts: %d %s", got.Status, got.Body)
		}
	})
}
