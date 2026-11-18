package devices

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// TestPanelRole is the API server's resolvePanelRole: the account's role
// from the users table when there is a row (none for a role the server
// does not know, also where the claim is good), else the claimed role
// unless it is unknown, the device role or the pro role.
func TestPanelRole(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO users (id, username, password_hash, role) VALUES (41, 'has_viewer', 'x', 'viewer')`,
			`INSERT INTO users (id, username, password_hash, role) VALUES (42, 'has_odd', 'x', 'made_up')`,
			`INSERT INTO users (id, username, password_hash, role) VALUES (43, 'has_device', 'x', 'device')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range []struct{ subject, claimed, want string }{
			{"has_viewer", "admin", "viewer"},
			{"has_viewer", "made_up", "viewer"},
			{"has_odd", "operator", ""},
			{"has_device", "operator", ""},
			{"nobody", "operator", "operator"},
			{"nobody", "super_admin", "super_admin"},
			{"nobody", "made_up", ""},
			{"nobody", "device", ""},
			{"nobody", "pro", ""},
		} {
			if got := panelRole(ctx, db, c.subject, c.claimed); got != c.want {
				t.Errorf("panelRole(%q, %q) = %q, want %q", c.subject, c.claimed, got, c.want)
			}
		}
	})
}

// TestWithPeer is the member list the route writes back: the list as a
// set, the device added at its end or taken out.
func TestWithPeer(t *testing.T) {
	for _, c := range []struct {
		peers string
		in    bool
		want  string
	}{
		{`["A","B","A"]`, true, `["A","B","D"]`},
		{`["A","B","A"]`, false, `["A","B"]`},
		{`["D","A","D"]`, true, `["D","A"]`},
		{`["D","A","D"]`, false, `["A"]`},
		{`["B"]`, true, `["B","D"]`},
		{`[]`, false, `[]`},
		{`[5,"D",5,null,{"a":1},{"a":1},[1],[1],null,"5",true,true]`, false, `[5,null,{"a":1},{"a":1},[1],[1],"5",true]`},
		{`["D","D"]`, true, `["D"]`},
	} {
		parsed, err := goapi.Parse([]byte(c.peers))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(withPeer(parsed.([]any), "D", c.in))
		if string(got) != c.want {
			t.Errorf("withPeer(%s, in %v) = %s, want %s", c.peers, c.in, got, c.want)
		}
	}
}
