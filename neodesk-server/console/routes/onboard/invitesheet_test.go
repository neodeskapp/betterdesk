package onboard_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestTheInviteSheetFollowsTheJoin: GET /api/onboard/invites/{id} answers
// the invite and the computers that joined with it, with what the sheet's
// steps start from (name, folder, online), also once the join used the
// invite up.
func TestTheInviteSheetFollowsTheJoin(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		if _, err := w.db.Exec(context.Background(), `INSERT INTO folders (id, name) VALUES (7, 'Sales')`); err != nil {
			t.Fatal(err)
		}
		admin := w.browser("admin")
		made := admin.do(http.MethodPost, "/api/onboard/invites", `{"name":"Anna's laptop","folder_id":7}`).json(t)
		id, code := str(made, "invite", "id"), str(made, "code")

		res := admin.do(http.MethodGet, "/api/onboard/invites/"+id, "")
		if res.status != http.StatusOK {
			t.Fatalf("before the join: %d %s", res.status, res.body)
		}
		before := res.json(t)
		if str(before, "invite", "name") != "Anna's laptop" || len(before["devices"].([]any)) != 0 || before["refused"] != nil {
			t.Fatalf("before the join: %s", res.body)
		}

		if joined := w.app().do(http.MethodPost, "/api/onboard/join", `{"code":"`+code+`","device_id":"523456789","uuid":"u-1","hostname":"anna-pc","platform":"Windows"}`); joined.status != http.StatusOK {
			t.Fatalf("join: %d %s", joined.status, joined.body)
		}
		res = admin.do(http.MethodGet, "/api/onboard/invites/"+id, "")
		if res.status != http.StatusOK {
			t.Fatalf("after the join: %d %s", res.status, res.body)
		}
		after := res.json(t)
		devices, _ := after["devices"].([]any)
		if len(devices) != 1 {
			t.Fatalf("devices %s", res.body)
		}
		d := devices[0].(map[string]any)
		if str(d, "id") != "523456789" || str(d, "name") != "Anna's laptop" || str(d, "hostname") != "anna-pc" ||
			str(d, "folder_name") != "Sales" || str(d, "joined_at") == "" {
			t.Fatalf("device %v", d)
		}
		if left, _ := after["invite"].(map[string]any)["uses_left"].(float64); left != 0 {
			t.Fatalf("uses left %v", left)
		}

		if res := w.browser("operator").do(http.MethodGet, "/api/onboard/invites/"+id, ""); res.status != http.StatusForbidden {
			t.Errorf("an operator read the invite: %d %s", res.status, res.body)
		}
		for _, other := range []string{"0123456789abcdef", "not-an-id"} {
			if res := admin.do(http.MethodGet, "/api/onboard/invites/"+other, ""); res.status != http.StatusNotFound {
				t.Errorf("%s: %d %s", other, res.status, res.body)
			}
		}
	})
}
