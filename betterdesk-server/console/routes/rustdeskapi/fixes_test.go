package rustdeskapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// clientToken stores a live access token of a test account and returns
// the Authorization header that carries it.
func clientToken(t *testing.T, w *routetest.World, role string) []string {
	t.Helper()
	seed := sha256.Sum256([]byte("token of " + role))
	token := string(hexOf(seed[:]))
	sum := sha256.Sum256([]byte(token))
	revoked := any(0)
	if w.DB.IsPostgres() {
		revoked = false
	}
	if _, err := w.DB.Exec(context.Background(), `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked)
		VALUES (?, ?, ?, 'c', 'u', '2999-01-01 00:00:00', ?)`, token, string(hexOf(sum[:])), routetest.RoleIDs[role], revoked); err != nil {
		t.Fatal(err)
	}
	return []string{"Authorization", "Bearer " + token}
}

func mustExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// The four audit reads ask the token's account for audit.view, with the
// overrides of its role: guest_issuer has no audit.view, viewer has it
// until an override takes it away (BUGS RV4).
func TestAuditReadsAskForAuditView(t *testing.T) {
	paths := []string{"/api/audit", "/api/audit/conn", "/api/audit/file", "/api/audit/alarm"}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		guest, viewer := clientToken(t, w, "guest_issuer"), clientToken(t, w, "viewer")
		for _, path := range paths {
			if got := w.SignedOut().Do("GET", path, "", guest...); got.Status != 403 || got.Body != `{"error":"Permission denied: audit.view"}` {
				t.Errorf("guest_issuer %s: %d %s", path, got.Status, got.Body)
			}
			if got := w.SignedOut().Do("GET", path, "", viewer...); got.Status != 200 {
				t.Errorf("viewer %s: %d %s", path, got.Status, got.Body)
			}
		}
	})
	w := routetest.New(t, routetest.Options{Roles: routetest.Overrides{"viewer": {"audit.view": false}}})
	viewer := clientToken(t, w, "viewer")
	for _, path := range paths {
		if got := w.SignedOut().Do("GET", path, "", viewer...); got.Status != 403 {
			t.Errorf("viewer without audit.view %s: %d %s", path, got.Status, got.Body)
		}
	}
}

// scopedWorld is a console whose devices nobody was given are hidden, with
// the devices D1 and D2 in folder 1 and in the device group dg-both, and
// the operator granted D1 alone. The
// signal server set up the database, so it has its table peers.
func scopedWorld(t *testing.T, db *store.DB) *routetest.World {
	t.Helper()
	db = storetest.Prepare(t, db, storetest.GoServerFirst)
	api := routetest.NewAPI().On("GET /api/peers", http.StatusOK, `{"peers":[{"id":"D1"},{"id":"D2"}]}`)
	w := routetest.New(t, routetest.Options{DB: db, API: api, Config: func(cfg *config.Config) { cfg.DeviceScopeDefault = "restricted" }})
	mustExec(t, db, `INSERT INTO peer (id, uuid, info) VALUES ('D1', 'u-1', '{}'), ('D2', 'u-2', '{}')`)
	mustExec(t, db, `INSERT INTO peers (id, uuid) VALUES ('D1', 'u-1'), ('D2', 'u-2')`)
	mustExec(t, db, `INSERT INTO folders (id, name) VALUES (1, 'Lab')`)
	mustExec(t, db, `INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('D1', 1), ('D2', 1)`)
	mustExec(t, db, `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('dg-both', 'Both', '', '', 'manual', '')`)
	mustExec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'D1' FROM device_groups WHERE guid = 'dg-both'`)
	mustExec(t, db, `INSERT INTO device_group_members (device_group_id, peer_id) SELECT id, 'D2' FROM device_groups WHERE guid = 'dg-both'`)
	mustExec(t, db, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (?, 'D1')`, routetest.RoleIDs["operator"])
	return w
}

// ids lists the "id" members of a JSON list at a path of members.
func ids(t *testing.T, body string, path ...string) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	for _, name := range path {
		m, _ := v.(map[string]any)
		v = m[name]
	}
	var out []string
	list, _ := v.([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		id, _ := m["id"].(string)
		out = append(out, id)
	}
	return out
}

// /api/devices lists the devices of the account's scope, none to a pro
// account; the folders and device groups of /api/group list their devices
// of the scope (BUGS RV4).
func TestDeviceListsFollowTheScope(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := scopedWorld(t, db)
		admin, operator, pro := clientToken(t, w, "admin"), clientToken(t, w, "operator"), clientToken(t, w, "pro")

		for _, c := range []struct {
			name string
			auth []string
			want string
		}{{"admin", admin, "D1,D2"}, {"operator", operator, "D1"}, {"pro", pro, ""}} {
			got := w.SignedOut().Do("GET", "/api/devices", "", c.auth...)
			if found := strings.Join(ids(t, got.Body, "data"), ","); got.Status != 200 || found != c.want {
				t.Errorf("%s /api/devices: %d %s, want the devices %q", c.name, got.Status, got.Body, c.want)
			}
		}

		for _, c := range []struct {
			name string
			auth []string
			want string
		}{{"admin", admin, "D1,D2"}, {"operator", operator, "D1"}} {
			got := w.SignedOut().Do("GET", "/api/group", "", c.auth...)
			var answer struct {
				Data []struct {
					GUID string `json:"guid"`
					Team struct {
						Peers []struct {
							ID string `json:"id"`
						} `json:"peers"`
					} `json:"team"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.Body), &answer); err != nil {
				t.Fatalf("%s /api/group: %v %s", c.name, err, got.Body)
			}
			found := map[string]string{"folder_1": "missing", "dg-both": "missing"}
			for _, g := range answer.Data {
				if _, wanted := found[g.GUID]; wanted {
					var peers []string
					for _, p := range g.Team.Peers {
						peers = append(peers, p.ID)
					}
					found[g.GUID] = strings.Join(peers, ",")
				}
			}
			for guid, peers := range found {
				if peers != c.want {
					t.Errorf("%s /api/group %s: %q, want %q (%s)", c.name, guid, peers, c.want, got.Body)
				}
			}
		}
	})
}

// The strategies are read with device.view, so never by a pro account,
// and a strategy's devices are those of the account's scope (BUGS RV4).
func TestStrategiesFollowTheScopeAndTheProRule(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		w := scopedWorld(t, db)
		admin, operator, pro := clientToken(t, w, "admin"), clientToken(t, w, "operator"), clientToken(t, w, "pro")
		created := w.SignedOut().Do("POST", "/api/strategies", `{"name":"contract"}`, admin...)
		var strategy struct {
			GUID string `json:"guid"`
		}
		if err := json.Unmarshal([]byte(created.Body), &strategy); err != nil || strategy.GUID == "" {
			t.Fatalf("create: %d %s", created.Status, created.Body)
		}
		if got := w.SignedOut().Do("POST", "/api/strategies/assign", `{"strategy":"`+strategy.GUID+`","peers":["D1","D2"]}`, admin...); got.Status != 200 {
			t.Fatalf("assign: %d %s", got.Status, got.Body)
		}

		peers := func(auth []string) (int, []string, float64) {
			got := w.SignedOut().Do("GET", "/api/strategies/"+strategy.GUID, "", auth...)
			var detail struct {
				Peers     []string `json:"peers"`
				PeerCount float64  `json:"peer_count"`
			}
			_ = json.Unmarshal([]byte(got.Body), &detail)
			return got.Status, detail.Peers, detail.PeerCount
		}
		status, all, count := peers(admin)
		if status != 200 || len(all) != 2 || count != 2 {
			t.Fatalf("admin's strategy: %d %v %v", status, all, count)
		}
		var d1 string
		if err := w.DB.QueryRow(context.Background(), `SELECT guid FROM peers WHERE id = 'D1'`).Scan(&d1); err != nil {
			t.Fatal(err)
		}
		if status, mine, count := peers(operator); status != 200 || len(mine) != 1 || mine[0] != d1 || count != 1 {
			t.Errorf("operator's strategy: %d %v %v, want only %s", status, mine, count, d1)
		}

		if got := w.SignedOut().Do("GET", "/api/strategies", "", pro...); got.Status != 200 || got.Body != `{"data":[],"total":0}` {
			t.Errorf("pro list: %d %s", got.Status, got.Body)
		}
		if got := w.SignedOut().Do("GET", "/api/strategies/"+strategy.GUID, "", pro...); got.Status != 404 {
			t.Errorf("pro strategy: %d %s", got.Status, got.Body)
		}
		if got := w.SignedOut().Do("GET", "/api/strategies", "", operator...); got.Status != 200 || !strings.Contains(got.Body, strategy.GUID) {
			t.Errorf("operator list: %d %s", got.Status, got.Body)
		}
	})
}

// What a client reports without a credential is bounded: an alarm's
// details longer than 4,096 UTF-16 units are stored as {}, and so are the
// system information's platform_additions, as its features are; and the heartbeats of one device store at
// most six rows of load figures a minute while each still marks the
// device online (BUGS RV3).
func TestClientReportsAreBounded(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		long := strings.Repeat("y", 5000)
		for _, body := range []string{
			`{"alarm_type":1,"host_id":"big-object","details":{"k":"` + long + `"}}`,
			`{"alarm_type":1,"host_id":"big-list","details":["` + long + `"]}`,
			`{"alarm_type":1,"host_id":"small-object","details":{"k":"v"}}`,
		} {
			if got := w.SignedOut().Do("POST", "/api/audit/alarm", body); got.Status != 200 {
				t.Fatalf("alarm: %d %s", got.Status, got.Body)
			}
		}
		for host, want := range map[string]string{"big-object": "{}", "big-list": "{}", "small-object": `{"k":"v"}`} {
			var details string
			if err := db.QueryRow(ctx, `SELECT CAST(details AS TEXT) FROM audit_alarms WHERE host_id = ?`, host).Scan(&details); err != nil || strings.ReplaceAll(details, " ", "") != want {
				t.Errorf("%s: details of %d characters (%v), want %d", host, len(details), err, len(want))
			}
		}

		mustExec(t, db, `INSERT INTO peer (id, uuid, info) VALUES ('900300001', 'u-1', '{}')`)
		sysinfo := `{"id":"900300001","hostname":"h","platform_additions":{"k":"` + long + `"}}`
		if got := w.SignedOut().Do("POST", "/api/sysinfo", sysinfo); got.Status != 200 || got.Body != "SYSINFO_UPDATED" {
			t.Fatalf("sysinfo: %d %s", got.Status, got.Body)
		}
		var additions string
		if err := db.QueryRow(ctx, `SELECT CAST(platform_additions AS TEXT) FROM peer_sysinfo WHERE peer_id = '900300001'`).Scan(&additions); err != nil || additions != "{}" {
			t.Errorf("platform_additions of %d characters (%v), want {}", len(additions), err)
		}

		for i := 0; i < 8; i++ {
			if got := w.SignedOut().Do("POST", "/api/heartbeat", `{"id":"900300001","cpu":10}`); got.Status != 200 {
				t.Fatalf("heartbeat %d: %d %s", i, got.Status, got.Body)
			}
		}
		var rows int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM peer_metrics WHERE peer_id = '900300001'`).Scan(&rows); err != nil || rows != 6 {
			t.Errorf("%d rows of load figures after eight heartbeats (%v), want 6", rows, err)
		}
	})
}

// The sign-in tells whether a device is banned only after the password:
// a wrong password with a banned device's id is the plain refusal, counts
// as a failed attempt and writes no entry about the device; the right
// password is refused for the banned device (BUGS RV9).
func TestSignInChecksTheBanAfterThePassword(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "Rv9-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE users SET password_hash = ? WHERE id = 6`, hash)
		mustExec(t, db, `INSERT INTO peer (id, uuid, info, is_banned) VALUES ('900400001', 'u', '{}', `+db.Pick("1", "TRUE")+`)`)

		got := w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"wrong","id":"900400001"}`)
		if got.Status != 401 || got.Body != `{"error":"Invalid credentials"}` {
			t.Errorf("a wrong password: %d %s", got.Status, got.Body)
		}
		for _, row := range w.AuditRows() {
			if strings.HasPrefix(row, "api_login_banned_device|") {
				t.Errorf("audit entry before the password was checked: %s", row)
			}
		}
		var failed int
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM login_attempts WHERE username = 'e2e_viewer' AND success = `+db.Pick("0", "FALSE")).Scan(&failed); err != nil || failed != 1 {
			t.Errorf("%d failed attempts (%v), want 1", failed, err)
		}

		got = w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"Rv9-Test-1!x","id":"900400001"}`)
		if got.Status != 403 || got.Body != `{"error":"Device is banned"}` {
			t.Errorf("the right password: %d %s", got.Status, got.Body)
		}
	})
}

// The id of a sign-in is stored as the token's client id only when it has
// the form of a device id: 3 to 64 letters, digits, "_" and "-". Any other
// text, also a longer one, counts as missing (BUGS RV1).
func TestSignInTakesOnlyADeviceIDAsClientID(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "Rv1-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE users SET password_hash = ? WHERE id = 6`, hash)
		for id, want := range map[string]string{
			"900100001":             "900100001",
			"dev_1-a":               "dev_1-a",
			strings.Repeat("9", 64): strings.Repeat("9", 64),
			strings.Repeat("9", 65): "",
			"ab":                    "",
			"a b":                   "",
			"a.b":                   "",
			"<script>":              "",
		} {
			body, _ := json.Marshal(map[string]string{"username": "e2e_viewer", "password": "Rv1-Test-1!x", "id": id, "uuid": "u-" + id})
			got := w.SignedOut().Do("POST", "/api/login", string(body))
			if got.Status != 200 {
				t.Fatalf("sign-in with id %q: %d %s", id, got.Status, got.Body)
			}
			var stored string
			if err := db.QueryRow(ctx, `SELECT client_id FROM access_tokens WHERE user_id = 6 ORDER BY id DESC LIMIT 1`).Scan(&stored); err != nil || stored != want {
				t.Errorf("sign-in with id %q stored client id %q (%v), want %q", id, stored, err, want)
			}
		}
	})
}

// A client's sign-in gives an account's token, whatever id the client
// names: it signs the client in, and the device routes refuse it for the
// device it names. A token the console issued for the device passes them
// and signs no account in (BUGS RV1).
func TestASignInTokenProvesNoDevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		authn := w.Server.Deps().Authn
		hash, err := authn.HashPassword(ctx, "Rv1-Test-2!x")
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, db, `UPDATE users SET password_hash = ? WHERE id = 6`, hash)
		got := w.SignedOut().Do("POST", "/api/login", `{"username":"e2e_viewer","password":"Rv1-Test-2!x","id":"900100001","uuid":"u-rv1"}`)
		var signedIn struct {
			Token string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(got.Body), &signedIn); err != nil || got.Status != 200 || signedIn.Token == "" {
			t.Fatalf("sign-in: %d %s", got.Status, got.Body)
		}
		account := []string{"Authorization", "Bearer " + signedIn.Token}
		device, err := authn.IssueDeviceToken(ctx, 6, "900100001", "")
		if err != nil {
			t.Fatal(err)
		}
		bound := []string{"Authorization", "Bearer " + device}
		agent := w.SignedOut()

		if got := agent.Do("GET", "/api/currentUser", "", account...); got.Status != 200 {
			t.Errorf("current user with the sign-in's token: %d %s", got.Status, got.Body)
		}
		for _, target := range []string{"/api/bd/heartbeat", "/api/bd/remote-agent-token", "/api/bd/help-request"} {
			got := agent.Do("POST", target, `{"device_id":"900100001"}`, account...)
			if got.Status != 401 || got.Body != `{"error":"Invalid or unbound access token"}` {
				t.Errorf("%s with the sign-in's token: %d %s", target, got.Status, got.Body)
			}
		}

		if got := agent.Do("GET", "/api/currentUser", "", bound...); got.Status != 401 || got.Body != `{"error":"Invalid or expired token"}` {
			t.Errorf("current user with the device token: %d %s", got.Status, got.Body)
		}
		if got := agent.Do("POST", "/api/bd/heartbeat", `{"device_id":"900100001"}`, bound...); got.Status == 401 || got.Status == 403 {
			t.Errorf("heartbeat with the device token: %d %s", got.Status, got.Body)
		}
	})
}
