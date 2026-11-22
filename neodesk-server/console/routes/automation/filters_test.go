package automation_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The filters of the alert history and the command list are one text
// each, acknowledged is true or false, and the limit a whole number from
// 1 (held at 500): anything else is 400 on both databases, where the old
// routes failed, found nothing or ignored the filter (BUGS AU6).
func TestFiltersAreOneText(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		seedOne(t, db)
		w := replay.NewWorld(t, db, nil)
		w.Exec("UPDATE alert_history SET acknowledged = "+db.Pick("1", "TRUE")+" WHERE id = 1", nil)
		refused := func(list, field string, queries ...string) {
			t.Helper()
			want := `{"error":"Invalid ` + field + `"}`
			for _, query := range queries {
				if got := w.Do(replay.Request{Role: "admin", Path: "/api/automation/" + list + "?" + query}); got.Status != 400 || got.Body != want {
					t.Errorf("%s?%s: %d %s, want 400 %s", list, query, got.Status, got.Body, want)
				}
			}
		}
		badLimits := []string{"limit=-1", "limit=0", "limit=00", "limit=abc", "limit=%203", "limit=1.5", "limit=1e3", "limit=+3", "limit=1&limit=2", "limit[rows]=1"}
		refused("alerts", "device_id", "device_id=a&device_id=b", "device_id[]=D1", "device_id[25]=D1", "device_id[id]=x&severity[]=D1", "[device_id]=a&device_id=b")
		refused("alerts", "severity", "severity[toString]=1", "severity=a&severity=")
		refused("alerts", "acknowledged", "acknowledged=", "acknowledged=1", "acknowledged=yes", "acknowledged=TRUE", "acknowledged=true&acknowledged=true", "acknowledged[]=true")
		refused("alerts", "limit", badLimits...)
		refused("commands", "device_id", "device_id=a&device_id=b", "device_id[]=D1", "device_id[25]=D1", "device_id[id]=x&status[]=D1")
		refused("commands", "status", "status=pending&status=running", "status[is]=pending")
		refused("commands", "limit", badLimits...)

		found := func(list string, want map[string]string) {
			t.Helper()
			for query, total := range want {
				got := w.Do(replay.Request{Role: "admin", Path: "/api/automation/" + list + "?" + query})
				if got.Status != 200 || !strings.HasSuffix(got.Body, `"total":`+total+"}") {
					t.Errorf("%s?%s: %d %s, want 200 with %s rows", list, query, got.Status, got.Body, total)
				}
			}
		}
		found("alerts", map[string]string{
			"": "1", "device_id=D1": "1", "device_id=D2": "0", "device_id=&severity=": "1", "[device_id]=D2": "0",
			"acknowledged=true": "1", "acknowledged=false": "0", "limit=1": "1", "limit=007": "1",
			"limit=99999999999999999999": "1", "limit=": "1", "device_id=D1#device_id=x": "1", "other[]=1&other=2&device_id=D1": "1",
		})
		found("commands", map[string]string{"device_id=D1&status=pending&limit=1": "1", "status=done": "0", "limit=600": "1"})
	})
}
