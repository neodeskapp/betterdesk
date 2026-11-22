package automation_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Rules and commands made in the same second keep an order in their lists
// (BUGS AU10): the rule and the command made last come first, by id, and
// a device's pending commands come the one made first first. The old
// queries ordered by the time alone, which SQLite keeps to the second.
func TestListsOfTheSameSecondKeepTheirOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db)
		const second = "2019-05-06 07:08:09"
		for _, id := range []string{"3", "1", "2"} {
			w.Exec("INSERT INTO alert_rules (id, name, condition_type, created_at) VALUES ("+id+", 'rule "+id+"', 'custom', '"+second+"')", nil)
			w.Exec("INSERT INTO remote_commands (id, device_id, payload, created_by, created_at) VALUES ("+id+", 'device-a', 'whoami', 'e2e_admin', '"+second+"')", nil)
		}
		w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, token_kind) VALUES (?, ?, 5, 'device-a', '2999-01-01 00:00:00', 'device')",
			[]any{"tok-device-a", "a70dc32ded6e642ec955672a88edb7cc9d6d9e2abb6bf78146a63491adb24155"})
		ids := regexp.MustCompile(`\{"id":(\d+)`)
		order := func(body string) string {
			var out []string
			for _, m := range ids.FindAllStringSubmatch(body, -1) {
				out = append(out, m[1])
			}
			return strings.Join(out, ",")
		}
		for _, list := range []struct {
			req  replay.Request
			want string
		}{
			{replay.Request{Role: "admin", Path: "/api/automation/rules"}, "3,2,1"},
			{replay.Request{Role: "admin", Path: "/api/automation/commands"}, "3,2,1"},
			{replay.Request{Path: "/api/bd/commands", Headers: map[string]string{"Authorization": "Bearer tok-device-a"}}, "1,2,3"},
		} {
			got := w.Do(list.req)
			if got.Status != 200 || order(got.Body) != list.want {
				t.Errorf("%s: %d, ids %s, want %s", list.req.Path, got.Status, order(got.Body), list.want)
			}
		}
	})
}
