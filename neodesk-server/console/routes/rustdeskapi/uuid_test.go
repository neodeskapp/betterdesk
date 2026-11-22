package rustdeskapi_test

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Heartbeats, system information and audit records are taken only with the
// uuid the device registered with at the signal server: the base64 of its
// bytes, as RustDesk sends it, or the text itself. Without it, or with
// another, the answer is the one of an unknown device, also for a banned
// one, and nothing is stored (BUGS RV2, RV7).
func TestClientReportsNeedTheRegisteredUUID(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		db = storetest.Prepare(t, db, storetest.GoServerFirst)
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		// The machine id of a Windows client (text with dashes, stored as the
		// hex of its characters) and of a Linux one (hex, stored as the hex of
		// the 16 bytes it spells)
		windows, linux := "4c4c4544-0042-3510-8048-b4c04f4d4c32", "0123456789abcdef0123456789abcdef"
		banned := db.Pick("1", "TRUE")
		mustExec(t, db, `INSERT INTO peers (id, uuid, hostname) VALUES ('900300001', ?, 'win'), ('900300002', ?, 'linux')`,
			hex.EncodeToString([]byte(windows)), linux)
		mustExec(t, db, `INSERT INTO peers (id, uuid, hostname, banned) VALUES ('900300003', ?, 'banned', `+banned+`)`, linux)
		b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		body := func(fields ...string) string {
			m := map[string]string{}
			for i := 0; i+1 < len(fields); i += 2 {
				m[fields[i]] = fields[i+1]
			}
			raw, _ := json.Marshal(m)
			return string(raw)
		}
		agent := w.SignedOut()
		count := func(query string) int {
			t.Helper()
			var n int
			if err := db.QueryRow(ctx, query).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}

		for _, c := range []struct {
			name, uuid string
			taken      bool
		}{
			{"no uuid", "", false},
			{"another uuid", b64("4c4c4544-0042-3510-8048-b4c04f4d4c33"), false},
			{"the id as the uuid", "900300001", false},
			{"the stored hex", hex.EncodeToString([]byte(windows)), true},
			{"the text", windows, true},
			{"base64 as RustDesk sends it", b64(windows), true},
		} {
			fields := []string{"id", "900300001", "hostname", "h-" + c.name}
			if c.uuid != "" {
				fields = append(fields, "uuid", c.uuid)
			}
			beat := agent.Do("POST", "/api/heartbeat", body(fields...))
			if asked := strings.Contains(beat.Body, `"sysinfo":true`); beat.Status != 200 || asked != c.taken {
				t.Errorf("heartbeat with %s: %d %s", c.name, beat.Status, beat.Body)
			}
			want := "ID_NOT_FOUND"
			if c.taken {
				want = "SYSINFO_UPDATED"
			}
			if got := agent.Do("POST", "/api/sysinfo", body(fields...)); got.Status != 200 || got.Body != want {
				t.Errorf("sysinfo with %s: %d %s, want %s", c.name, got.Status, got.Body, want)
			}
			mustExec(t, db, `DELETE FROM peer_sysinfo`)
		}
		if n := count(`SELECT COUNT(*) FROM peer_metrics WHERE peer_id = '900300001'`); n != 3 {
			t.Errorf("%d heartbeats stored load figures, want the 3 with the uuid", n)
		}
		for name, uuid := range map[string]string{"text": linux, "base64": b64(linux)} {
			if got := agent.Do("POST", "/api/sysinfo", body("id", "900300002", "uuid", uuid)); got.Body != "SYSINFO_UPDATED" {
				t.Errorf("sysinfo of the Linux client with the uuid as %s: %d %s", name, got.Status, got.Body)
			}
		}
		if got := agent.Do("POST", "/api/sysinfo_ver", body("id", "900300002")); len(got.Body) != 16 {
			t.Errorf("the version of the stored system information: %d %q", got.Status, got.Body)
		}

		// A banned device is told so only with its uuid
		if got := agent.Do("POST", "/api/heartbeat", body("id", "900300003")); got.Status != 200 || strings.Contains(got.Body, "BANNED") {
			t.Errorf("heartbeat of the banned device without its uuid: %d %s", got.Status, got.Body)
		}
		if got := agent.Do("POST", "/api/heartbeat", body("id", "900300003", "uuid", linux)); got.Status != 200 || !strings.Contains(got.Body, "BANNED") {
			t.Errorf("heartbeat of the banned device with its uuid: %d %s", got.Status, got.Body)
		}

		refused := `{"error":"Device not found"}`
		for _, c := range []struct {
			target, body string
			taken        bool
		}{
			{"/api/audit/conn", `{"host_id":"900300001","conn_type":1}`, false},
			{"/api/audit/conn", `{"host_id":"900300001","conn_type":1,"uuid":"` + b64("other") + `"}`, false},
			{"/api/audit/conn", `{"host_id":"900300009","conn_type":1,"uuid":"` + b64(windows) + `"}`, false},
			{"/api/audit/conn", `{"host_id":"900300001","conn_type":1,"uuid":"` + b64(windows) + `"}`, true},
			{"/api/audit/conn", `{"host_id":"900300001","conn_type":1,"host_uuid":"` + windows + `"}`, true},
			{"/api/audit/file", `{"host_id":"900300002","path":"/x"}`, false},
			{"/api/audit/file", `{"host_id":"900300002","path":"/x","host_uuid":"` + linux + `"}`, true},
			{"/api/audit/alarm", `{"alarm_type":1,"uuid":"` + b64(linux) + `"}`, false},
			{"/api/audit/alarm", `{"alarm_type":1,"host_id":"900300002","uuid":"` + b64(windows) + `"}`, false},
			{"/api/audit/alarm", `{"alarm_type":1,"host_id":"900300002","uuid":"` + b64(linux) + `"}`, true},
		} {
			got := agent.Do("POST", c.target, c.body)
			if c.taken && (got.Status != 200 || got.Body != "{}") || !c.taken && (got.Status != 404 || got.Body != refused) {
				t.Errorf("%s %s: %d %s", c.target, c.body, got.Status, got.Body)
			}
		}
		for table, want := range map[string]int{"audit_connections": 2, "audit_files": 1, "audit_alarms": 1} {
			if n := count(`SELECT COUNT(*) FROM ` + table); n != want {
				t.Errorf("%d rows in %s, want %d", n, table, want)
			}
		}
	})
}

// registerDevice gives the device a live row in the signal server's peers
// table with the uuid it registered with, creating a table of the columns
// the client API reads where the database has none.
func registerDevice(t *testing.T, db *store.DB, id, uuid string) {
	t.Helper()
	mustExec(t, db, `CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', soft_deleted BOOLEAN DEFAULT FALSE)`)
	mustExec(t, db, `INSERT INTO peers (id, uuid) VALUES (?, ?)`, id, uuid)
}
