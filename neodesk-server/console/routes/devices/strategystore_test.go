package devices

import (
	"context"
	"regexp"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// TestAssignmentKeys resolves the references of an assignment: a device
// by id, guid or uuid, getting the lower case of a uuid-shaped uuid as
// its guid and a random one otherwise, and keeping it; an account by
// guid, username or id. A check without write finds the same references
// and gives nothing a guid.
func TestAssignmentKeys(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('U', 'AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE', '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('N', 'not-a-uuid-at-all-but-36-chars-long', '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('G', 'u-g', 'g1')`,
			`INSERT INTO users (id, username, password_hash, role, guid) VALUES (21, 'ann', 'x', 'viewer', ''), (22, 'bob', 'x', 'viewer', 'guid-bob')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		for _, ref := range []string{"U", "N", "G"} {
			if got, err := resolvePeerAssignmentKey(ctx, db, ref, false); err != nil || (got != "" && got != "g1") {
				t.Errorf("checking device %s: %q, %v", ref, got, err)
			}
		}
		if got, err := resolveUserAssignmentKey(ctx, db, "ann", false); err != nil || got != "" {
			t.Errorf("checking account ann: %q, %v", got, err)
		}
		var written int
		if err := db.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM peers WHERE guid <> '') + (SELECT COUNT(*) FROM users WHERE username = 'ann' AND guid <> '')`).Scan(&written); err != nil || written != 1 {
			t.Errorf("guids after the checks: %d, %v; want only G's", written, err)
		}
		key := func(ref string) string {
			got, err := resolvePeerAssignmentKey(ctx, db, ref, true)
			if err != nil {
				return "error: " + err.Error()
			}
			return got
		}
		if got := key("U"); got != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" {
			t.Errorf("device with a uuid: %s", got)
		}
		if got := key("AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"); got != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" {
			t.Errorf("by uuid, after the guid was stored: %s", got)
		}
		random := key("N")
		if !uuidV4.MatchString(random) || key("N") != random {
			t.Errorf("device without a uuid: %s, then %s", random, key("N"))
		}
		if got := key("g1"); got != "g1" {
			t.Errorf("by guid: %s", got)
		}
		for ref, want := range map[string]string{"nobody": "error: peer not found", "": "error: empty peer reference"} {
			if got := key(ref); got != want {
				t.Errorf("%q: %s, want %s", ref, got, want)
			}
		}
		user := func(ref string) string {
			got, err := resolveUserAssignmentKey(ctx, db, ref, true)
			if err != nil {
				return "error: " + err.Error()
			}
			return got
		}
		if got := user("22"); got != "guid-bob" {
			t.Errorf("account by id: %s", got)
		}
		ann := user("ann")
		if !uuidV4.MatchString(ann) || user("21") != ann {
			t.Errorf("account without a guid: %s, then by id %s", ann, user("21"))
		}
		if got := user("nobody"); got != "error: user not found" {
			t.Errorf("unknown account: %s", got)
		}
	})
}
