package devices_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/bdrelay/fakeagent"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// The bugs of BUGS.md that the device routes fixed, each with the
// behaviour the fix changed.

// TestHardDeleteClearsDeviceRows: a hard delete that the server confirmed
// clears what the console keeps of the device: its system information,
// metrics, folder, manual device groups and its row in peer. A soft
// delete keeps all of it, since the device can be restored, and so does a
// hard delete the server refused (BUGS DE1, DECISIONS A59). Other devices
// keep theirs.
func TestHardDeleteClearsDeviceRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, nil)
		ctx := context.Background()
		exec := func(query string, args ...any) {
			t.Helper()
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
		}
		folder, err := db.Insert(ctx, `INSERT INTO folders (name) VALUES ('Office')`)
		if err != nil {
			t.Fatal(err)
		}
		// C is in the group g-viewer of the world already
		exec(`INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'A' FROM device_groups WHERE guid = 'g-viewer'`)
		for _, id := range []string{"A", "C"} {
			exec(`INSERT INTO peer_sysinfo (peer_id, hostname) VALUES (?, 'h')`, id)
			exec(`INSERT INTO peer_metrics (peer_id, cpu_usage) VALUES (?, 1)`, id)
			exec(`INSERT INTO peer (id) VALUES (?)`, id)
			exec(`INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, ?)`, id, folder)
		}
		rows := func(id string) string {
			t.Helper()
			var counts []string
			for _, q := range []string{
				`SELECT COUNT(*) FROM peer_sysinfo WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM peer_metrics WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM device_folder_assignments WHERE device_id = ?`,
				`SELECT COUNT(*) FROM device_group_members WHERE peer_id = ?`,
				`SELECT COUNT(*) FROM peer WHERE id = ?`,
			} {
				var n int
				if err := db.QueryRow(ctx, q, id).Scan(&n); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
				counts = append(counts, fmt.Sprint(n))
			}
			return strings.Join(counts, " ")
		}
		const all, none = "1 1 1 1 1", "0 0 0 0 0"
		admin := w.As("admin")

		api.On("DELETE /api/peers/A?hard=true", 500, `{"error":"delete failed"}`)
		if got := admin.Do("DELETE", "/api/devices/A?hard=true", ""); got.Status != http.StatusInternalServerError {
			t.Errorf("a hard delete the server refused: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != all {
			t.Errorf("rows of A after a refused hard delete: %s, want %s", got, all)
		}
		api.On("DELETE /api/peers/A?hard=true", 200, `{"status":"deleted","id":"A","revoked":false}`)

		if got := admin.Do("DELETE", "/api/devices/A", ""); got.Status != http.StatusOK {
			t.Fatalf("soft delete: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != all {
			t.Errorf("rows of A after a soft delete: %s, want %s", got, all)
		}
		if got := admin.Do("DELETE", "/api/devices/A?hard=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":true,"cascaded":[]}` {
			t.Fatalf("hard delete: %d %s", got.Status, got.Body)
		}
		if got := rows("A"); got != none {
			t.Errorf("rows of A after a hard delete: %s, want %s", got, none)
		}
		if got := rows("C"); got != all {
			t.Errorf("rows of C after A's hard delete: %s, want %s", got, all)
		}
	})
}

// TestSysinfoNullColumnsAnswerTheFallback: a system information row whose
// list and object columns are NULL answers [] and {} for them, where the
// web app reads lists and objects; stored values are parsed as before
// (BUGS D19).
func TestSysinfoNullColumnsAnswerTheFallback(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO peer_sysinfo (peer_id, hostname, displays, encoding, features, platform_additions) VALUES ('A', 'h', NULL, NULL, NULL, NULL)`,
			`INSERT INTO peer_sysinfo (peer_id, hostname, displays, encoding, features, platform_additions) VALUES ('C', 'h', '[{"name":"x"}]', '["h264"]', '{"a":1}', '{"b":2}')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		admin := w.As("admin")
		for id, wants := range map[string][]string{
			"A": {`"displays":[]`, `"encoding":[]`, `"features":{}`, `"platform_additions":{}`},
			"C": {`"displays":[{"name":"x"}]`, `"encoding":["h264"]`, `"features":{"a":1}`, `"platform_additions":{"b":2}`},
		} {
			got := admin.Do("GET", "/api/devices/"+id, "")
			for _, want := range wants {
				if got.Status != http.StatusOK || !strings.Contains(got.Body, want) {
					t.Errorf("device %s: %d %s, want %s", id, got.Status, got.Body, want)
				}
			}
		}
	})
}

var strategyGUIDField = regexp.MustCompile(`"guid":"([^"]+)"`)

// TestUpdateStrategyKeepsItsSwitch: a change to a rule that is switched
// off leaves it off unless the body switches it on, on both databases
// (BUGS DE2).
func TestUpdateStrategyKeepsItsSwitch(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night","enabled":false}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if created.Status != http.StatusOK || m == nil || !strings.Contains(created.Body, `"enabled":false`) {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		path := "/api/panel/strategies/" + m[1]
		for _, c := range []struct{ body, enabled string }{
			{`{}`, `"enabled":false`},
			{`{"name":"Late night"}`, `"enabled":false`},
			{`{"enabled":true}`, `"enabled":true`},
			{`{"name":"Night"}`, `"enabled":true`},
			{`{"enabled":false}`, `"enabled":false`},
		} {
			got := admin.Do("PATCH", path, c.body)
			if got.Status != http.StatusOK || !strings.Contains(got.Body, c.enabled) {
				t.Errorf("PATCH %s: %d %s, want %s", c.body, got.Status, got.Body, c.enabled)
			}
			if got := admin.Do("GET", path, ""); !strings.Contains(got.Body, c.enabled) {
				t.Errorf("GET after PATCH %s: %d %s, want %s", c.body, got.Status, got.Body, c.enabled)
			}
		}
	})
}

// TestDeleteNamesCascadedDevices: the answer of a delete with cascade
// names the devices the server deleted with it (BUGS DE3).
func TestDeleteNamesCascadedDevices(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	api.On("DELETE /api/peers/A?cascade=true", 200, `{"status":"deleted","id":"A","revoked":false,"cascaded":["X","Y"]}`)
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":false,"cascaded":["X","Y"]}` {
		t.Errorf("delete with cascade: %d %s", got.Status, got.Body)
	}
	// The server leaves cascaded out when nothing was linked
	api.On("DELETE /api/peers/A?cascade=true", 200, `{"status":"deleted","id":"A","revoked":false}`)
	if got := admin.Do("DELETE", "/api/devices/A?cascade=true", ""); got.Status != http.StatusOK || got.Body != `{"success":true,"revoked":false,"hard":false,"cascaded":[]}` {
		t.Errorf("delete with cascade and nothing linked: %d %s", got.Status, got.Body)
	}
}

// TestListIgnoresQueryLists: search and sortOrder given as a list or an
// object are ignored like the other parameters, and the list answers
// (BUGS DE4).
func TestListIgnoresQueryLists(t *testing.T) {
	w, _ := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	want := admin.Do("GET", "/api/devices", "")
	if want.Status != http.StatusOK || !strings.Contains(want.Body, `"id":"A"`) {
		t.Fatalf("the list: %d %s", want.Status, want.Body)
	}
	for _, query := range []string{"search[]=x", "search[a]=1", "search[]=a&search[]=b", "sortOrder[]=asc", "sortOrder[x]=1", "search[]=x&sortOrder[]=asc"} {
		if got := admin.Do("GET", "/api/devices?"+query, ""); got.Status != http.StatusOK || got.Body != want.Body {
			t.Errorf("?%s: %d %s, want the list without it", query, got.Status, got.Body)
		}
	}
}

// TestChangeIDTakesTextOnly: a new id that is no text is refused like one
// of the wrong length, before anything reaches the server (BUGS DE5).
func TestChangeIDTakesTextOnly(t *testing.T) {
	w, api := world(t, storetest.Migrated(t), nil, nil)
	admin := w.As("admin")
	refused := admin.Do("POST", "/api/devices/A/change-id", `{"newId":"abc"}`)
	if refused.Status != http.StatusBadRequest {
		t.Fatalf("a short id: %d %s", refused.Status, refused.Body)
	}
	for _, body := range []string{`{"newId":1234567}`, `{"newId":true}`, `{"newId":{"length":8}}`, `{"newId":["ABCDEFG"]}`, `{"newId":null}`} {
		api.Calls()
		if got := admin.Do("POST", "/api/devices/A/change-id", body); got.Status != refused.Status || got.Body != refused.Body {
			t.Errorf("%s: %d %s, want %d %s", body, got.Status, got.Body, refused.Status, refused.Body)
		}
		for _, call := range api.Calls() {
			if !strings.HasPrefix(call, "GET /api/peers/A") {
				t.Errorf("%s reached the server: %s", body, call)
			}
		}
	}
	if got := admin.Do("POST", "/api/devices/A/change-id", `{"newId":"NEWID1"}`); got.Status != http.StatusOK {
		t.Errorf("a new id as text: %d %s", got.Status, got.Body)
	}
}

// TestSetGroupsNeedsAList: groupGuids that is missing or no list is
// refused and leaves the device in its group; an empty list still takes
// it out of every manual group (BUGS DE6).
func TestSetGroupsNeedsAList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, api := world(t, db, nil, func(cfg *config.Config) { cfg.PanelGroupingNode = true })
		admin := w.As("admin")
		ctx := context.Background()
		member := func() int {
			var n int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM device_group_members WHERE peer_id = 'C'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		refused := admin.Do("PUT", "/api/devices/C/groups", `{"groupGuids":["not a guid!"]}`)
		if refused.Status != http.StatusBadRequest {
			t.Fatalf("an invalid guid: %d %s", refused.Status, refused.Body)
		}
		for _, body := range []string{`{}`, `{"groupGuids":null}`, `{"groupGuids":"g-viewer"}`, `{"groupGuids":{"0":"g-viewer"}}`, `{"groupGuids":5}`} {
			api.Calls()
			if got := admin.Do("PUT", "/api/devices/C/groups", body); got.Status != refused.Status || got.Body != refused.Body {
				t.Errorf("%s: %d %s, want %d %s", body, got.Status, got.Body, refused.Status, refused.Body)
			}
			if n := member(); n != 1 {
				t.Fatalf("%s took C out of its group", body)
			}
		}
		if got := admin.Do("PUT", "/api/devices/C/groups", `{"groupGuids":[]}`); got.Status != http.StatusOK || member() != 0 {
			t.Errorf("an empty list: %d %s, %d memberships left", got.Status, got.Body, member())
		}
	})
}

// TestAssignChecksEveryReferenceFirst: an assignment checks every
// reference (text, not empty, found) before it writes a guid, and answers
// 400 with a fixed text for each bad one, on assign and unassign; a
// failure of the database is 500 on both (BUGS DE7).
func TestAssignChecksEveryReferenceFirst(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w, _ := world(t, db, nil, nil)
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', guid TEXT DEFAULT '')`,
			`INSERT INTO peers (id, uuid, guid) VALUES ('A', 'not-a-uuid', '')`,
			`UPDATE users SET guid = '' WHERE username = 'e2e_viewer'`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		guids := func() string {
			var peer, user string
			if err := db.QueryRow(ctx, `SELECT COALESCE(guid, '') FROM peers WHERE id = 'A'`).Scan(&peer); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(ctx, `SELECT COALESCE(guid, '') FROM users WHERE username = 'e2e_viewer'`).Scan(&user); err != nil {
				t.Fatal(err)
			}
			return peer + "|" + user
		}
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night"}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if m == nil {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		assign := "/api/panel/strategies/" + m[1] + "/assign"
		for _, c := range []struct{ body, error string }{
			{`{"peers":[""]}`, "empty peer reference"},
			{`{"peers":["  "]}`, "empty peer reference"},
			{`{"peers":{"length":2}}`, "peers must be a list"},
			{`{"peers":5,"users":["e2e_viewer"]}`, "peers must be a list"},
			{`{"peers":[5]}`, "invalid peer reference"},
			{`{"peers":["A","nope"]}`, "peer not found"},
			{`{"peers":["A"],"users":["nobody"]}`, "user not found"},
			{`{"peers":["A"],"users":[""]}`, "empty user reference"},
			{`{"users":["e2e_viewer"],"groups":["no such group"]}`, "device group not found"},
			{`{"users":["e2e_viewer"],"groups":[{"x":1}]}`, "invalid device group reference"},
		} {
			want := `{"success":false,"error":"` + c.error + `"}`
			for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
				if got := admin.Do("POST", path, c.body); got.Status != http.StatusBadRequest || got.Body != want {
					t.Errorf("%s %s: %d %s, want 400 %s", path, c.body, got.Status, got.Body, want)
				}
				if got := guids(); got != "|" {
					t.Fatalf("%s %s wrote the guids %s", path, c.body, got)
				}
			}
		}
		if got := admin.Do("POST", assign, `{"peers":["A"],"users":["e2e_viewer"]}`); got.Status != http.StatusOK {
			t.Errorf("good references: %d %s", got.Status, got.Body)
		}
		if peer, user, _ := strings.Cut(guids(), "|"); peer == "" || user == "" {
			t.Errorf("guids after a good assignment: %q and %q, want both", peer, user)
		}
		if _, err := db.Exec(ctx, `DROP TABLE strategy_assignments`); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{assign, "/api/panel/strategies/unassign"} {
			if got := admin.Do("POST", path, `{"peers":["A"]}`); got.Status != http.StatusInternalServerError || strings.Contains(got.Body, "strategy_assignments") {
				t.Errorf("%s without the table: %d %s, want 500 without the database's text", path, got.Status, got.Body)
			}
		}
	})
}

// TestStrategyWriteAnswersPermissions: creating and changing a rule
// answers the stored permissions, also on PostgreSQL where the signal
// server created the table with a TEXT column for them (BUGS DE8).
func TestStrategyWriteAnswersPermissions(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		w, _ := world(t, db, nil, nil)
		admin := w.As("admin")
		created := admin.Do("POST", "/api/panel/strategies", `{"name":"Night","permissions":{"enable-audio":true}}`)
		m := strategyGUIDField.FindStringSubmatch(created.Body)
		if created.Status != http.StatusOK || m == nil || !strings.Contains(created.Body, `"permissions":{"enable-audio":"Y"}`) {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		got := admin.Do("PATCH", "/api/panel/strategies/"+m[1], `{"permissions":{"enable-audio":false,"enable-tunnel":"Y"}}`)
		if got.Status != http.StatusOK || !strings.Contains(got.Body, `"permissions":{"enable-audio":"N","enable-tunnel":"Y"}`) {
			t.Errorf("update: %d %s", got.Status, got.Body)
		}
	})
}

// TestAgentErrorsAreTheAgents: an agent that answers a request with the
// error agent_offline or agent_timeout gets 502 with its text, like any
// other error of an agent, and the server's snapshot is not asked for;
// the snapshot, the queued collection, 503 and 504 are for an agent that
// is not connected or does not answer in time (BUGS WS8; the replay
// covers those).
func TestAgentErrorsAreTheAgents(t *testing.T) {
	db := storetest.Migrated(t)
	t.Cleanup(wstest.Goroutines(t))
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		switch {
		case c.Method == "GET" && c.Target == "/api/peers/A1001":
			return routetest.Reply{Status: 200, Body: `{"id":"A1001","hostname":"a"}`}
		case c.Method == "GET" && strings.HasPrefix(c.Target, "/api/peers/A1001/telemetry"):
			return routetest.Reply{Status: 200, Body: `{"snapshots":{"services":{"data":[{"name":"cached"}],"collected_at":"2020-01-01T00:00:00Z"}}}`}
		case c.Method != "GET":
			return routetest.Reply{Status: 200, Body: `{"status":"queued"}`}
		}
		return routetest.Reply{Status: 404, Body: `{"error":"not found"}`}
	}
	w := routetest.New(t, routetest.Options{DB: db, API: api, Env: raisedLimits, Config: nodeLanguages})
	ctx := context.Background()
	if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS device_tokens (token_hash TEXT, peer_id TEXT, status TEXT,
		max_uses INTEGER, use_count INTEGER, expires_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(agentToken))
	if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked)
		VALUES (?, ?, 1, 'A1001', '', '2999-01-01 00:00:00', ?)`, agentToken, hex.EncodeToString(sum[:]), false); err != nil {
		t.Fatal(err)
	}
	agent, err := fakeagent.Connect(w.Addr(), "A1001", agentToken)
	if err != nil {
		t.Fatal(err)
	}
	defer agent.Close()
	admin := w.As("admin")
	for _, reason := range []string{"agent_offline", "agent_timeout"} {
		agent.Answer(func(fakeagent.Request) any { return fakeagent.JSON(`{"ok":false,"error":"` + reason + `"}`) })
		api.Calls()
		got := admin.Do("GET", "/api/devices/A1001/services", "")
		if want := `{"success":false,"error":"` + reason + `"}`; got.Status != http.StatusBadGateway || got.Body != want {
			t.Errorf("an agent that answers %s: %d %s, want 502 %s", reason, got.Status, got.Body, want)
		}
		if calls := api.Calls(); len(calls) != 1 {
			t.Errorf("an agent that answers %s: calls %q, want only the device lookup", reason, calls)
		}
	}
}
