package reports

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/netmonitor"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The methods of services/dbAdapter.js the report engine called, for both
// adapters. Each sends the statement of its adapter; where the two
// adapters differ, both are kept. The rows come back as the old drivers
// gave them (SelectRows).
//
// getAllPeers and getPeerStats first copied the signal server's devices
// into the console's peer table (syncGoPeers). Options.SyncPeers is that
// copy.

func (e *Engine) rows(ctx context.Context, query string, args ...any) ([]*goapi.Object, error) {
	return SelectRows(ctx, e.query, e.clock.loc, query, args...)
}

// inScope is the condition that keeps the rows whose column names a
// device of the scope, and its parameter: the ids as one JSON list, which
// holds a scope of any size. ok is false for a scope without a limit,
// which keeps every row.
func (e *Engine) inScope(scope *access.DeviceScope, column string) (condition string, arg any, ok bool) {
	if scope.Unlimited() {
		return "", nil, false
	}
	ids, err := json.Marshal(scope.IDs())
	if err != nil {
		panic(err)
	}
	return column + e.db.Pick(
		" IN (SELECT value FROM json_each(?))",
		" IN (SELECT jsonb_array_elements_text(?::jsonb))",
	), string(ids), true
}

// andInScope is inScope as a condition to add to a WHERE clause, and the
// arguments with its parameter appended; nothing for a scope without a
// limit.
func (e *Engine) andInScope(scope *access.DeviceScope, column string, args []any) (string, []any) {
	condition, arg, ok := e.inScope(scope, column)
	if !ok {
		return "", args
	}
	return " AND " + condition, append(args, arg)
}

// peer is what the reports read of a device after parsePeer.
type peer struct {
	id, hostname, ip, platform, banReason any
	online, banned                        bool
}

// allPeers is getAllPeers() without filters: the devices of the scope
// not deleted, by id.
func (e *Engine) allPeers(ctx context.Context, scope *access.DeviceScope) ([]peer, error) {
	e.sync(ctx)
	condition, args := e.andInScope(scope, "id", nil)
	rows, err := e.rows(ctx, e.db.Pick(
		"SELECT * FROM peer WHERE is_deleted = 0"+condition+" ORDER BY id",
		"SELECT * FROM peer WHERE NOT is_deleted"+condition+" ORDER BY id",
	), args...)
	if err != nil {
		return nil, fmt.Errorf("read the devices: %w", err)
	}
	peers := make([]peer, 0, len(rows))
	for _, row := range rows {
		peers = append(peers, e.parsePeer(row))
	}
	return peers, nil
}

// parsePeer reads a row of peer as parsePeer of the adapter did: the info
// column is JSON (text on SQLite, jsonb on PostgreSQL). Info that is no
// object has no members, like {}; the tags are not read.
func (e *Engine) parsePeer(row *goapi.Object) peer {
	var info any = goapi.NewObject()
	raw := row.Get("info")
	if e.db.IsPostgres() {
		switch v := raw.(type) {
		case *goapi.Object, []any:
			info = v
		case string:
			if v != "" {
				if parsed, err := ParseJSON(v); err == nil {
					info = parsed
				}
			}
		}
	} else if Truthy(raw) {
		if parsed, err := ParseJSON(e.clock.String(raw)); err == nil {
			info = parsed
		}
	}
	p := peer{id: row.Get("id")}
	hostname, found := prop(info, "hostname")
	p.hostname = or(row.Get("note"), true, or(hostname, found, ""))
	osName, osFound := prop(info, "os")
	platform, platformFound := prop(info, "platform")
	p.platform = or(osName, osFound, or(platform, platformFound, ""))
	ip, ipFound := prop(info, "ip")
	p.ip = or(ip, ipFound, or(row.Get("ip"), true, ""))
	p.banReason = or(row.Get("banned_reason"), true, "")
	if e.db.IsPostgres() {
		p.online, p.banned = Truthy(row.Get("status_online")), Truthy(row.Get("is_banned"))
	} else {
		p.online, p.banned = row.Get("status_online") == float64(1), row.Get("is_banned") == float64(1)
	}
	return p
}

// peerStats is getPeerStats(): {total, online, banned, offline} of the
// devices of the scope.
func (e *Engine) peerStats(ctx context.Context, scope *access.DeviceScope) (*goapi.Object, error) {
	e.sync(ctx)
	var total, online, banned float64
	condition, args := e.andInScope(scope, "id", nil)
	if e.db.IsPostgres() {
		rows, err := e.rows(ctx, `
                SELECT
                    COUNT(*) FILTER (WHERE NOT is_deleted) as total,
                    COUNT(*) FILTER (WHERE NOT is_deleted AND status_online) as online,
                    COUNT(*) FILTER (WHERE NOT is_deleted AND is_banned) as banned
                FROM peer
            WHERE TRUE`+condition, args...)
		if err != nil {
			return nil, fmt.Errorf("count the devices: %w", err)
		}
		r := rows[0]
		total, online, banned = e.clock.Number(r.Get("total")), e.clock.Number(r.Get("online")), e.clock.Number(r.Get("banned"))
	} else {
		var err error
		if total, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0"+condition, args...); err != nil {
			return nil, err
		}
		if online, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0 AND status_online = 1"+condition, args...); err != nil {
			return nil, err
		}
		if banned, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0 AND is_banned = 1"+condition, args...); err != nil {
			return nil, err
		}
	}
	return goapi.NewObject("total", total, "online", online, "banned", banned, "offline", total-online), nil
}

// count runs a SQLite statement whose one row has the count in column c.
func (e *Engine) count(ctx context.Context, query string, args ...any) (float64, error) {
	rows, err := e.rows(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	n, _ := rows[0].Get("c").(float64)
	return n, nil
}

// rangeArgs binds the bounds of a time range (see bound) that are set the
// way the old drivers bound them; it returns the conditions to add.
func (e *Engine) rangeArgs(from, to string, fromCondition, toCondition string) (string, []any, error) {
	conditions := ""
	var values []jsbind.Value
	if from != "" {
		conditions += fromCondition
		values = append(values, jsbind.String(from))
	}
	if to != "" {
		conditions += toCondition
		values = append(values, jsbind.String(to))
	}
	args, err := jsbind.Args(e.db.Dialect(), len(values), values...)
	if err != nil {
		return "", nil, err
	}
	return conditions, args, nil
}

// inPeriod is the condition that keeps the rows whose column of times lies
// in a report's period (see bound), the ends included, and its arguments;
// nothing without a period. SQLite reads both sides as times, so a stored
// "2026-11-01 10:00:00" and a bound "2026-11-01T00:00:00.000Z" compare as
// times.
func (e *Engine) inPeriod(column, from, to string) (string, []any, error) {
	compare := func(op string) string {
		if e.db.IsPostgres() {
			return " AND " + column + " " + op + " CAST(? AS TIMESTAMPTZ)"
		}
		return " AND datetime(" + column + ") " + op + " datetime(?)"
	}
	return e.rangeArgs(from, to, compare(">="), compare("<="))
}

// summaryConditions are the conditions on the activity summaries s of a
// range (see bound) and a scope, and their arguments.
func (e *Engine) summaryConditions(from, to string, scope *access.DeviceScope) (string, []any, error) {
	conditions, args, err := e.rangeArgs(from, to, " AND s.reported_at >= ?", " AND s.reported_at <= ?")
	if err != nil {
		return "", nil, err
	}
	inScope, args := e.andInScope(scope, "s.device_id", args)
	return conditions + inScope, args, nil
}

// activityPerDevice counts and sums the activity summaries of the range
// and the scope per device: {device_id, reports, active, idle}, the device
// whose newest summary was received last first, as the old console met
// them in getAllActivitySummaries({from, to}).
//
// SQLite stores what an agent sent, also a text in a column of numbers.
// The sums there take the numbers alone, and odd holds the summaries with
// another value in total_active_secs or idle_seconds as {active, idle},
// each the odd value or null, for the caller to add as JavaScript reads
// them (Number('0x10') is 16).
func (e *Engine) activityPerDevice(ctx context.Context, from, to string, scope *access.DeviceScope) (perDevice, odd []*goapi.Object, err error) {
	conditions, args, err := e.summaryConditions(from, to, scope)
	if err != nil {
		return nil, nil, err
	}
	sum := func(column string) string { return "SUM(s." + column + ")" }
	if !e.db.IsPostgres() {
		sum = func(column string) string {
			return "SUM(CASE WHEN typeof(s." + column + ") IN ('integer', 'real') THEN s." + column + " END)"
		}
	}
	perDevice, err = e.rows(ctx, `SELECT s.device_id, COUNT(*) AS reports, `+
		sum("total_active_secs")+` AS active, `+sum("idle_seconds")+` AS idle
            FROM activity_summaries s WHERE 1=1`+conditions+`
            GROUP BY s.device_id ORDER BY MAX(s.received_at) DESC, MIN(s.id)`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("count the activity summaries: %w", err)
	}
	if e.db.IsPostgres() {
		return perDevice, nil, nil
	}
	odd, err = e.rows(ctx, `SELECT
                CASE WHEN typeof(s.total_active_secs) IN ('text', 'blob') THEN s.total_active_secs END AS active,
                CASE WHEN typeof(s.idle_seconds) IN ('text', 'blob') THEN s.idle_seconds END AS idle
            FROM activity_summaries s WHERE 1=1`+conditions+`
              AND (typeof(s.total_active_secs) IN ('text', 'blob') OR typeof(s.idle_seconds) IN ('text', 'blob'))`, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("read the odd activity summaries: %w", err)
	}
	return perDevice, odd, nil
}

// appsPerDevice is getTopApps(deviceId, {from, to}) of every device with
// a summary in the range and the scope, in one statement: the time per app
// of each device, by the device's id as text, most time first. SQLite
// groups by app, PostgreSQL by app and category.
func (e *Engine) appsPerDevice(ctx context.Context, from, to string, scope *access.DeviceScope) (map[string][]*goapi.Object, error) {
	devices, args, err := e.summaryConditions(from, to, scope)
	if err != nil {
		return nil, err
	}
	conditions, rangeArgs, err := e.rangeArgs(from, to, " AND a.started_at >= ?", " AND a.ended_at <= ?")
	if err != nil {
		return nil, err
	}
	rows, err := e.rows(ctx, `SELECT a.device_id, a.app_name, a.category,
                SUM(a.duration_secs) as total_secs,
                COUNT(*) as session_count
            FROM activity_sessions a
            WHERE a.device_id IN (SELECT s.device_id FROM activity_summaries s WHERE 1=1`+devices+`)`+conditions+
		e.db.Pick(" GROUP BY a.device_id, a.app_name", " GROUP BY a.device_id, a.app_name, a.category")+
		" ORDER BY a.device_id, total_secs DESC", append(args, rangeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("read the apps of the devices: %w", err)
	}
	out := map[string][]*goapi.Object{}
	for _, row := range rows {
		id := e.clock.String(row.Get("device_id"))
		out[id] = append(out[id], row)
	}
	return out, nil
}

// dlpEventStats is getDlpEventStats() over the events of the scope and
// the report's period. On SQLite the sums of no events are null, on
// PostgreSQL every count is a number.
func (e *Engine) dlpEventStats(ctx context.Context, f Filters) (*goapi.Object, error) {
	period, args, err := e.inPeriod("created_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	condition, args := e.andInScope(f.Scope, "device_id", args)
	condition = period + condition
	rows, err := e.rows(ctx, e.db.Pick(`
                SELECT
                    COUNT(*) AS total,
                    SUM(CASE WHEN action = 'block' THEN 1 ELSE 0 END) AS blocked,
                    SUM(CASE WHEN action = 'log' THEN 1 ELSE 0 END) AS logged,
                    SUM(CASE WHEN event_source = 'usb' THEN 1 ELSE 0 END) AS usb_events,
                    SUM(CASE WHEN event_source = 'file' THEN 1 ELSE 0 END) AS file_events
                FROM dlp_events
            WHERE 1=1`+condition, `
                SELECT
                    COUNT(*)::INTEGER AS total,
                    COUNT(*) FILTER (WHERE action = 'block')::INTEGER AS blocked,
                    COUNT(*) FILTER (WHERE action = 'log')::INTEGER AS logged,
                    COUNT(*) FILTER (WHERE event_source = 'usb')::INTEGER AS usb_events,
                    COUNT(*) FILTER (WHERE event_source = 'file')::INTEGER AS file_events
                FROM dlp_events
            WHERE 1=1`+condition), args...)
	if err != nil {
		return nil, fmt.Errorf("count the data guard events: %w", err)
	}
	return rows[0], nil
}

// dlpEvents is getDlpEvents({limit}): the newest events of the scope and
// the report's period. On SQLite created_at is declared DATETIME, which
// the Go driver would turn into a time; the second created_at
// (+created_at has no declared type) puts the stored text in its place.
func (e *Engine) dlpEvents(ctx context.Context, limit int, f Filters) ([]*goapi.Object, error) {
	period, args, err := e.inPeriod("dlp_events.created_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	condition, args := e.andInScope(f.Scope, "dlp_events.device_id", args)
	condition = period + condition
	rows, err := e.rows(ctx, e.db.Pick(
		"SELECT *, +created_at AS created_at FROM dlp_events WHERE 1=1"+condition+" ORDER BY dlp_events.created_at DESC LIMIT ?",
		"SELECT * FROM dlp_events WHERE 1=1"+condition+" ORDER BY created_at DESC LIMIT ?",
	), append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("read the data guard events: %w", err)
	}
	return rows, nil
}

// auditLogs is getAuditLogs(limit, offset): the newest entries of the
// report's period with the account's name.
func (e *Engine) auditLogs(ctx context.Context, limit, offset int, f Filters) ([]*goapi.Object, error) {
	period, args, err := e.inPeriod("a.created_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	rows, err := e.rows(ctx, `
                SELECT a.*, u.username FROM audit_log a
                LEFT JOIN users u ON a.user_id = u.id WHERE 1=1`+period+`
                ORDER BY a.created_at DESC LIMIT ? OFFSET ?
            `, append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("read the audit log: %w", err)
	}
	return rows, nil
}

// ticketStats is getTicketStats() over the tickets made in the report's
// period: {total, open, in_progress, resolved, closed}.
func (e *Engine) ticketStats(ctx context.Context, f Filters) (*goapi.Object, error) {
	period, args, err := e.inPeriod("created_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	if e.db.IsPostgres() {
		rows, err := e.rows(ctx, `
                SELECT COUNT(*) as total,
                    COUNT(*) FILTER (WHERE status = 'open') as open,
                    COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress,
                    COUNT(*) FILTER (WHERE status = 'resolved') as resolved,
                    COUNT(*) FILTER (WHERE status = 'closed') as closed
                FROM tickets WHERE 1=1`+period, args...)
		if err != nil {
			return nil, fmt.Errorf("count the tickets: %w", err)
		}
		r := rows[0]
		stats := goapi.NewObject()
		for _, key := range []string{"total", "open", "in_progress", "resolved", "closed"} {
			stats.Set(key, e.clock.Number(r.Get(key)))
		}
		return stats, nil
	}
	stats := goapi.NewObject()
	for _, c := range []struct{ key, query string }{
		{"total", "SELECT COUNT(*) as c FROM tickets WHERE 1=1"},
		{"open", "SELECT COUNT(*) as c FROM tickets WHERE status = 'open'"},
		{"in_progress", "SELECT COUNT(*) as c FROM tickets WHERE status = 'in_progress'"},
		{"resolved", "SELECT COUNT(*) as c FROM tickets WHERE status = 'resolved'"},
		{"closed", "SELECT COUNT(*) as c FROM tickets WHERE status = 'closed'"},
	} {
		n, err := e.count(ctx, c.query+period, args...)
		if err != nil {
			return nil, err
		}
		stats.Set(c.key, n)
	}
	return stats, nil
}

// ticketsBy counts the tickets made in the report's period by the values
// of a column: {name, n}, the value of the newest ticket first, as the old
// console met them in getAllTickets().
func (e *Engine) ticketsBy(ctx context.Context, column string, f Filters) ([]*goapi.Object, error) {
	period, args, err := e.inPeriod("created_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	rows, err := e.rows(ctx, "SELECT "+column+" AS name, COUNT(*) AS n FROM tickets WHERE 1=1"+period+" GROUP BY "+column+
		" ORDER BY MAX(created_at) DESC, MIN(id)", args...)
	if err != nil {
		return nil, fmt.Errorf("count the tickets by %s: %w", column, err)
	}
	return rows, nil
}

// inventory is a row of getAllInventories() as the report reads it.
type inventory struct {
	hardware any
}

// allInventories is getAllInventories(): every report of the scope, newest
// received first, with its hardware parsed. A report whose hardware does
// not parse is left out; the software is not read.
func (e *Engine) allInventories(ctx context.Context, scope *access.DeviceScope) ([]inventory, error) {
	condition, args := e.andInScope(scope, "device_id", nil)
	rows, err := e.rows(ctx, "SELECT hardware FROM device_inventory WHERE 1=1"+condition+" ORDER BY received_at DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("read the inventory: %w", err)
	}
	out := make([]inventory, 0, len(rows))
	for _, row := range rows {
		if hardware, err := e.inventoryDocument(row.Get("hardware")); err == nil {
			out = append(out, inventory{hardware: hardware})
		}
	}
	return out, nil
}

// inventoryDocument is JSON.parse(row.x || '{}') on SQLite and, on
// PostgreSQL, the value itself when it is an object or null, else the
// same parse.
func (e *Engine) inventoryDocument(v any) (any, error) {
	if e.db.IsPostgres() && (v == nil || isObject(v)) {
		return v, nil
	}
	if !Truthy(v) {
		return goapi.NewObject(), nil
	}
	return ParseJSON(e.clock.String(v))
}

// alertRules is getAlertRules() without filters, newest first.
func (e *Engine) alertRules(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := e.rows(ctx, "SELECT * FROM alert_rules WHERE 1=1 ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("read the alert rules: %w", err)
	}
	return rows, nil
}

// alertHistory is getAlertHistory() without filters: the 200 newest
// alerts of the report's period with their rule's name. A scope keeps the
// alerts of its devices and those that name no device.
func (e *Engine) alertHistory(ctx context.Context, f Filters) ([]*goapi.Object, error) {
	period, args, err := e.inPeriod("h.triggered_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	condition, arg, scoped := e.inScope(f.Scope, "h.device_id")
	if scoped {
		condition = " AND (h.device_id IS NULL OR " + condition + ")"
		args = append(args, arg)
	}
	rows, err := e.rows(ctx, "SELECT h.*, r.name as rule_name FROM alert_history h LEFT JOIN alert_rules r ON h.rule_id = r.id WHERE 1=1"+period+condition+" ORDER BY h.triggered_at DESC LIMIT 200", args...)
	if err != nil {
		return nil, fmt.Errorf("read the alert history: %w", err)
	}
	return rows, nil
}

// networkTargets is getNetworkTargets() without filters, by name, with
// the members the network report reads as JavaScript values.
func (e *Engine) networkTargets(ctx context.Context) ([]*goapi.Object, error) {
	targets, err := netmonitor.ListTargets(ctx, e.db, netmonitor.TargetFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]*goapi.Object, 0, len(targets))
	for _, t := range targets {
		row := goapi.NewObject()
		for _, f := range []struct {
			key   string
			value interface{ MarshalJSON() ([]byte, error) }
		}{
			{"id", t.ID}, {"name", t.Name}, {"host", t.Host}, {"check_type", t.CheckType},
			{"last_status", t.LastStatus}, {"last_rtt_ms", t.LastRTT},
		} {
			raw, err := f.value.MarshalJSON()
			if err != nil {
				return nil, err
			}
			v, err := goapi.Parse(raw)
			if err != nil {
				return nil, err
			}
			row.Set(f.key, v)
		}
		row.Set("last_check_at", e.timeValue(t.LastCheckAt))
		out = append(out, row)
	}
	return out, nil
}

// lastChecks is, for each network target, its newest check in the
// report's period: status, rtt_ms and checked_at as the old drivers gave
// them.
func (e *Engine) lastChecks(ctx context.Context, f Filters) (map[string]*goapi.Object, error) {
	period, args, err := e.inPeriod("checked_at", f.from, f.to)
	if err != nil {
		return nil, err
	}
	rows, err := e.rows(ctx, "SELECT target_id, status, rtt_ms, checked_at FROM network_checks WHERE 1=1"+period+" ORDER BY checked_at DESC, id DESC", args...)
	if err != nil {
		return nil, fmt.Errorf("read the network checks: %w", err)
	}
	out := map[string]*goapi.Object{}
	for _, row := range rows {
		key := e.clock.String(row.Get("target_id"))
		if _, seen := out[key]; !seen {
			out[key] = row
		}
	}
	return out, nil
}

// timeValue is a time column as the old driver gave it: the stored text
// on SQLite, a Date on PostgreSQL.
func (e *Engine) timeValue(t store.Time) any {
	if t.IsNull() {
		return nil
	}
	if e.db.IsPostgres() {
		if instant, ok := t.Instant(); ok {
			return Date{T: instant}
		}
	}
	return t.String()
}
