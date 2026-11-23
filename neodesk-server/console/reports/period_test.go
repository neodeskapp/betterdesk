package reports

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestPeriodAppliesToEveryReportWithDatedRows: tickets, network checks,
// data guard events, audit entries and alerts count only within the
// chosen period, the day of "to" included; without a period they count
// as before. A network target counts by its newest check in the period,
// as unknown when it has none there (BUGS L105).
func TestPeriodAppliesToEveryReportWithDatedRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO tickets (title, created_by, status, category, priority, created_at) VALUES
				('before', 'x', 'open', 'network', 'high', '2026-10-31 23:00:00'),
				('first day', 'x', 'closed', 'printer', 'low', '2026-11-01 00:30:00'),
				('last day', 'x', 'open', 'printer', 'low', '2026-11-05 23:30:00'),
				('after', 'x', 'open', 'network', 'high', '2026-11-06 00:10:00')`,
			`INSERT INTO network_targets (id, name, host, check_type, last_status, last_check_at) VALUES
				(1, 'gw', 'gw.example.invalid', 'ping', 'up', '2026-11-08 10:00:00'),
				(2, 'nas', 'nas.example.invalid', 'ping', 'up', '2026-11-08 10:00:00')`,
			`INSERT INTO network_checks (target_id, status, checked_at) VALUES
				(1, 'up', '2026-11-02 10:00:00'), (1, 'down', '2026-11-04 10:00:00'), (1, 'up', '2026-11-08 10:00:00'),
				(2, 'up', '2026-11-08 10:00:00')`,
			`INSERT INTO dlp_events (device_id, action, event_source, created_at) VALUES
				('D1', 'block', 'usb', '2026-11-03 10:00:00'), ('D1', 'log', 'file', '2026-11-07 10:00:00')`,
			`INSERT INTO audit_log (action, details, created_at) VALUES
				('a', 'in', '2026-11-02 10:00:00'), ('b', 'out', '2026-10-01 10:00:00')`,
			"INSERT INTO alert_rules (id, name, condition_type) VALUES (1, 'Offline', 'offline')",
			`INSERT INTO alert_history (rule_id, device_id, message, triggered_at) VALUES
				(1, 'D1', 'in', '2026-11-03 10:00:00'), (1, 'D1', 'out', '2026-11-09 10:00:00')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		e := New(Options{DB: db, Now: func() time.Time { return now }, Location: time.UTC})
		from, to := jsbind.String("2026-11-01"), jsbind.String("2026-11-05")
		for _, c := range []struct {
			typ      string
			from, to jsbind.Value
			want     []string
		}{
			{"tickets", from, to, []string{`"total":2,`, `"open":1,`, `"closed":1`, `"total_tickets":2`, `"category_distribution":{"printer":2}`}},
			{"tickets", jsbind.Undefined, jsbind.Undefined, []string{`"total_tickets":4`, `"category_distribution":{"network":2,"printer":2}`}},
			{"network", from, to, []string{`"total_targets":2,"up":0,"down":1,"unknown":1`}},
			{"network", jsbind.Undefined, jsbind.Undefined, []string{`"total_targets":2,"up":2,"down":0,"unknown":0`}},
			{"security", from, to, []string{`"total":1,`, `"audit_log_count":1`}},
			{"security", jsbind.Undefined, jsbind.Undefined, []string{`"total":2,`, `"audit_log_count":2`}},
			{"alerts", from, to, []string{`"total_alerts":1,`}},
			{"alerts", jsbind.Undefined, jsbind.Undefined, []string{`"total_alerts":2,`}},
		} {
			report, err := e.Generate(ctx, jsbind.String(c.typ), Filters{From: c.from, To: c.to})
			if err != nil {
				t.Fatalf("%s: %v", c.typ, err)
			}
			raw, err := json.Marshal(report.Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range c.want {
				if !strings.Contains(string(raw), part) {
					t.Errorf("%s from %v to %v: %s\n has no %s", c.typ, c.from, c.to, raw, part)
				}
			}
		}
	})
}
