package onboard_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// auditRow is a row of GET /api/settings/audit, the list of Security and
// audit.
type auditRow struct {
	Action    string `json:"action"`
	Details   string `json:"details"`
	IPAddress string `json:"ip_address"`
}

func unprovedRows(t *testing.T, admin *browser) []auditRow {
	t.Helper()
	res := admin.do(http.MethodGet, "/api/settings/audit?limit=500", "")
	if res.status != http.StatusOK {
		t.Fatalf("audit list: %d %s", res.status, res.body)
	}
	var out struct {
		Data []auditRow `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.body), &out); err != nil {
		t.Fatalf("audit list: %v: %s", err, res.body)
	}
	var rows []auditRow
	for _, row := range out.Data {
		if row.Action == "onboard_join_unproved" {
			rows = append(rows, row)
		}
	}
	return rows
}

// TestARefusedJoinShowsInTheConsolesAuditList: a join under an enrolled ID
// without the proof of its key lands in the console's audit list (Security
// and audit, GET /api/settings/audit) with the device, the address and the
// reason, the first time and again after a loop, once a minute per device
// (L522: it was only in the API server's own event log).
func TestARefusedJoinShowsInTheConsolesAuditList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		admin := w.browser("admin")
		invite := func() string {
			return str(admin.do(http.MethodPost, "/api/onboard/invites", `{"name":"Lab","lifetime_hours":24,"uses":5}`).json(t), "code")
		}
		key := newLabKey(t)
		body := func(code, publicKey string) string {
			return `{"code":"` + code + `","device_id":"289485454","uuid":"machine-1","hostname":"ndlab-win11","platform":"windows","public_key":"` + publicKey + `"}`
		}
		if res := w.app().do(http.MethodPost, "/api/onboard/join", body(invite(), key.b64)); res.status != http.StatusOK {
			t.Fatalf("first join: %d %s", res.status, res.body)
		}
		if rows := unprovedRows(t, admin); len(rows) != 0 {
			t.Fatalf("a proved join is in the list: %+v", rows)
		}

		code := invite()
		// An older client rejoining its own enrolled ID: its key, no proof
		if res := w.app().do(http.MethodPost, "/api/onboard/join", body(code, key.b64)); res.status != http.StatusForbidden {
			t.Fatalf("join without proof: %d %s", res.status, res.body)
		}
		rows := unprovedRows(t, admin)
		if len(rows) != 1 {
			t.Fatalf("%d entries after the refused join, want 1", len(rows))
		}
		if !strings.Contains(rows[0].Details, "289485454") || !strings.Contains(rows[0].Details, "no_proof") || rows[0].IPAddress == "" {
			t.Fatalf("entry %+v", rows[0])
		}
		// A stranger in a loop within the minute: no new entry
		for i := 0; i < 5; i++ {
			w.app().do(http.MethodPost, "/api/onboard/join", `{"code":"`+code+`","device_id":"289485454"}`)
		}
		if rows := unprovedRows(t, admin); len(rows) != 1 {
			t.Fatalf("a loop made %d entries, want 1", len(rows))
		}
	})
}
