package devicegroups_test

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestCascadeNeedsTwoIDs carries a changed id into the console's tables,
// and does nothing when either id is empty or both are the same.
func TestCascadeNeedsTwoIDs(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		s := newService(t, db)
		ctx := context.Background()
		exec(t, db, `INSERT INTO peer (id, uuid) VALUES ('OLD', 'u')`)
		exec(t, db, `INSERT INTO peer_sysinfo (peer_id, hostname) VALUES ('OLD', 'h')`)
		ids := func() string {
			var peer, sysinfo string
			if err := db.QueryRow(ctx, `SELECT (SELECT id FROM peer), (SELECT peer_id FROM peer_sysinfo)`).Scan(&peer, &sysinfo); err != nil {
				t.Fatal(err)
			}
			return peer + "," + sysinfo
		}
		s.CascadePeerIDChange(ctx, "OLD", "")
		s.CascadePeerIDChange(ctx, "", "NEW")
		s.CascadePeerIDChange(ctx, "OLD", "OLD")
		if got := ids(); got != "OLD,OLD" {
			t.Errorf("after the changes that are none: %s", got)
		}
		s.CascadePeerIDChange(ctx, "OLD", "NEW")
		if got := ids(); got != "NEW,NEW" {
			t.Errorf("after the change: %s", got)
		}
	})
}
