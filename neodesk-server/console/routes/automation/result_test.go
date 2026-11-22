package automation_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// An agent reports on its command only while the command waits or runs
// (BUGS AU13): a command that completed or failed stays as it is, and a
// report on it is 409. A result that is no text is stored as its JSON,
// and null as no result. The old route took any state after any other,
// wrote over the result each time and stored a result as String() made
// it: an object as [object Object], null as the text null.
func TestAFinishedCommandStaysFinished(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := deviceWorld(t, db)
		w.Exec("INSERT INTO remote_commands (device_id, payload, created_by) VALUES ('device-a', 'uptime', 'e2e_admin')", nil)
		report := func(id, body string) replay.Answer {
			t.Helper()
			return w.Do(replay.Request{Method: "POST", Path: "/api/bd/commands/" + id + "/result", Body: replay.Text(body),
				Headers: map[string]string{"Authorization": "Bearer tok-device-a"}})
		}
		const row = "SELECT id, status, result, CASE WHEN executed_at IS NULL THEN 'no' ELSE 'yes' END AS ran," +
			" CASE WHEN completed_at IS NULL THEN 'no' WHEN completed_at < '2020-01-01' THEN 'before 2020' ELSE 'yes' END AS ended FROM remote_commands ORDER BY id"
		for _, step := range []struct{ id, body, want string }{
			{"1", `{"status":"running","result":{"step":1.0,"of":[2,"x\"y"],"b":null}}`, `{"id":1,"status":"running","result":"{\"step\":1,\"of\":[2,\"x\\\"y\"],\"b\":null}","ran":"yes","ended":"no"}`},
			{"1", `{"status":"running","result":5}`, `{"id":1,"status":"running","result":"5","ran":"yes","ended":"no"}`},
			{"1", `{"status":"running","result":[true,1e21]}`, `{"id":1,"status":"running","result":"[true,1e+21]","ran":"yes","ended":"no"}`},
			{"1", `{"status":"completed","result":null}`, `{"id":1,"status":"completed","result":null,"ran":"yes","ended":"yes"}`},
			{"2", `{"status":"failed","result":"exit 1"}`, `{"id":2,"status":"failed","result":"exit 1","ran":"no","ended":"yes"}`},
		} {
			if got := report(step.id, step.body); got.Status != 200 || got.Body != `{"success":true}` {
				t.Fatalf("command %s with %s: %d %s", step.id, step.body, got.Status, got.Body)
			}
			if got := w.Rows(row, nil); !strings.Contains(got, step.want) {
				t.Errorf("command %s after %s: %s, want %s", step.id, step.body, got, step.want)
			}
		}
		w.Exec("UPDATE remote_commands SET completed_at = '2019-05-06 07:08:09'", nil)
		before := w.Rows(row, nil)
		for _, id := range []string{"1", "2"} {
			for _, body := range []string{`{"status":"running"}`, `{"status":"completed","result":"again"}`, `{"status":"failed","result":{"a":1}}`} {
				if got := report(id, body); got.Status != 409 || got.Body != `{"error":"Command already finished"}` {
					t.Errorf("command %s finished, then %s: %d %s, want 409", id, body, got.Status, got.Body)
				}
			}
		}
		if after := w.Rows(row, nil); after != before {
			t.Errorf("the reports on finished commands changed them:\nbefore %s\n after %s", before, after)
		}

		// A command the old console let run again after it completed keeps
		// the time it completed first
		w.Exec("INSERT INTO remote_commands (device_id, payload, created_by, status, executed_at, completed_at) VALUES ('device-a', 'again', 'e2e_admin', 'running', '2019-05-06 07:08:09', '2019-05-06 07:08:09')", nil)
		if got := report("3", `{"status":"completed","result":"done"}`); got.Status != 200 {
			t.Fatalf("command 3 completed: %d %s", got.Status, got.Body)
		}
		if got, want := w.Rows(row, nil), `{"id":3,"status":"completed","result":"done","ran":"yes","ended":"before 2020"}`; !strings.Contains(got, want) {
			t.Errorf("command 3 after it completed again: %s, want %s", got, want)
		}
	})
}
