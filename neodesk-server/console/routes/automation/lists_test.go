package automation_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// The alert history and the command list answer at most 500 rows also
// when the request names no limit, as the web app's requests do, and the
// command list leaves each command's result out: the single command has
// it, and the list says with has_result whether there is one (BUGS RV39).
func TestTheListsAnswerAtMost500Rows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		big := strings.Repeat("x", 10000)
		w.Exec("INSERT INTO alert_rules (name, condition_type, condition_op, condition_value, created_by) VALUES ('Rule', 'cpu_usage', 'gt', 80, 'admin')", nil)
		for i := range 520 {
			w.Exec("INSERT INTO alert_history (rule_id, device_id, severity, message, triggered_at) VALUES (1, 'dev-1', 'warning', 'over', '2026-11-21T10:00:00.000Z')", nil)
			result := any(nil)
			switch i % 3 {
			case 1:
				result = big
			case 2:
				result = ""
			}
			w.Exec("INSERT INTO remote_commands (device_id, payload, created_by, status, result) VALUES ('dev-1', 'uptime', 'admin', 'completed', ?)", []any{result})
		}

		for _, path := range []string{"/api/automation/alerts", "/api/automation/alerts?limit=", "/api/automation/commands", "/api/automation/commands?limit="} {
			got := w.Do(replay.Request{Role: "admin", Path: path})
			var list struct {
				Alerts   []map[string]any `json:"alerts"`
				Commands []map[string]any `json:"commands"`
				Total    int              `json:"total"`
			}
			if got.Status != 200 || json.Unmarshal([]byte(got.Body), &list) != nil {
				t.Fatalf("GET %s: %d %.300s", path, got.Status, got.Body)
			}
			rows := len(list.Alerts) + len(list.Commands)
			if rows != 500 || list.Total != 500 {
				t.Errorf("GET %s: %d rows, total %d, want 500", path, rows, list.Total)
			}
			if strings.Contains(got.Body, big) || strings.Contains(got.Body, `"result"`) {
				t.Errorf("GET %s answers the results of the commands", path)
			}
			with := 0
			for _, c := range list.Commands {
				switch c["has_result"] {
				case true:
					with++
				case false:
				default:
					t.Fatalf("GET %s: has_result %v in %v", path, c["has_result"], c)
				}
			}
			if len(list.Commands) > 0 && (with < 160 || with > 170) {
				t.Errorf("GET %s: %d of 500 commands with a result, want every third", path, with)
			}
		}
		// A limit still counts, and the single command has its result
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/automation/commands?limit=3"}); !strings.Contains(got.Body, `"total":3`) {
			t.Errorf("a limit of 3: %.300s", got.Body)
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/automation/commands/2"}); got.Status != 200 || !strings.Contains(got.Body, `"result":"`+big+`"`) {
			t.Errorf("command 2: %d %.300s", got.Status, got.Body)
		}
	})
}
