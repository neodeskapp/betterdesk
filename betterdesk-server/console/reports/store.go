package reports

import (
	"context"
	"errors"
	"fmt"

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
// into the console's peer table (syncGoPeers). That copy is not done
// here: the engine reads peer as it is.

// errNullInfo is the TypeError parsePeer threw for a device whose info is
// the JSON text null.
var errNullInfo = errors.New("Cannot read properties of null (reading 'hostname')")

// errTagsNotText is the TypeError parsePeer threw on SQLite for tags that
// are neither empty nor text.
var errTagsNotText = errors.New("row.tags.split is not a function")

func (e *Engine) rows(ctx context.Context, query string, args ...any) ([]*goapi.Object, error) {
	return SelectRows(ctx, e.db, e.clock.loc, query, args...)
}

// peer is what the reports read of a device after parsePeer.
type peer struct {
	id, hostname, ip, platform, banReason any
	online, banned                        bool
}

// allPeers is getAllPeers() without filters: the devices not deleted, by id.
func (e *Engine) allPeers(ctx context.Context) ([]peer, error) {
	rows, err := e.rows(ctx, e.db.Pick(
		"SELECT * FROM peer WHERE is_deleted = 0 ORDER BY id",
		"SELECT * FROM peer WHERE NOT is_deleted ORDER BY id",
	))
	if err != nil {
		return nil, fmt.Errorf("read the devices: %w", err)
	}
	peers := make([]peer, 0, len(rows))
	for _, row := range rows {
		p, err := e.parsePeer(row)
		if err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, nil
}

// parsePeer reads a row of peer as parsePeer of the adapter did: the info
// column is JSON (text on SQLite, jsonb on PostgreSQL); info that is the
// JSON null makes it throw.
func (e *Engine) parsePeer(row *goapi.Object) (peer, error) {
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
	if info == nil {
		return peer{}, errNullInfo
	}
	if !e.db.IsPostgres() {
		if tags := row.Get("tags"); Truthy(tags) {
			if _, isText := tags.(string); !isText {
				return peer{}, errTagsNotText
			}
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
	return p, nil
}

// peerStats is getPeerStats(): {total, online, banned, offline}.
func (e *Engine) peerStats(ctx context.Context) (*goapi.Object, error) {
	var total, online, banned float64
	if e.db.IsPostgres() {
		rows, err := e.rows(ctx, `
                SELECT
                    COUNT(*) FILTER (WHERE NOT is_deleted) as total,
                    COUNT(*) FILTER (WHERE NOT is_deleted AND status_online) as online,
                    COUNT(*) FILTER (WHERE NOT is_deleted AND is_banned) as banned
                FROM peer
            `)
		if err != nil {
			return nil, fmt.Errorf("count the devices: %w", err)
		}
		r := rows[0]
		total, online, banned = e.clock.Number(r.Get("total")), e.clock.Number(r.Get("online")), e.clock.Number(r.Get("banned"))
	} else {
		var err error
		if total, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0"); err != nil {
			return nil, err
		}
		if online, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0 AND status_online = 1"); err != nil {
			return nil, err
		}
		if banned, err = e.count(ctx, "SELECT COUNT(*) as c FROM peer WHERE is_deleted = 0 AND is_banned = 1"); err != nil {
			return nil, err
		}
	}
	return goapi.NewObject("total", total, "online", online, "banned", banned, "offline", total-online), nil
}

// count runs a SQLite statement whose one row has the count in column c.
func (e *Engine) count(ctx context.Context, query string) (float64, error) {
	rows, err := e.rows(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	n, _ := rows[0].Get("c").(float64)
	return n, nil
}

// rangeArgs binds the leading values and those of a time range the way
// the old drivers bound them; it returns the conditions to add. A value
// of the range is used when it is true for JavaScript.
func (e *Engine) rangeArgs(from, to jsbind.Value, fromCondition, toCondition string, leading ...jsbind.Value) (string, []any, error) {
	conditions := ""
	values := leading
	if from.Truthy() {
		conditions += fromCondition
		values = append(values, from)
	}
	if to.Truthy() {
		conditions += toCondition
		values = append(values, to)
	}
	args, err := jsbind.Args(e.db.Dialect(), len(values), values...)
	if err != nil {
		return "", nil, err
	}
	return conditions, args, nil
}

// bindValue is a value read from a row, as a statement parameter.
func bindValue(v any) jsbind.Value {
	switch t := v.(type) {
	case nil:
		return jsbind.Null
	case bool:
		return jsbind.Bool(t)
	case float64:
		return jsbind.Number(t)
	case string:
		return jsbind.String(t)
	}
	return jsbind.String(fmt.Sprint(v))
}

// allActivitySummaries is getAllActivitySummaries({from, to}): every
// summary of the range, newest received first.
func (e *Engine) allActivitySummaries(ctx context.Context, from, to jsbind.Value) ([]*goapi.Object, error) {
	conditions, args, err := e.rangeArgs(from, to, " AND s.reported_at >= ?", " AND s.reported_at <= ?")
	if err != nil {
		return nil, err
	}
	query := `SELECT s.*, (
                SELECT COUNT(*) FROM activity_sessions a
                WHERE a.device_id = s.device_id
                  AND a.started_at >= s.reported_at
            ) as detail_count
            FROM activity_summaries s WHERE 1=1` + conditions + " ORDER BY s.received_at DESC"
	rows, err := e.rows(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read the activity summaries: %w", err)
	}
	return rows, nil
}

// topApps is getTopApps(deviceId, {from, to}): the time per app of one
// device. SQLite groups by app, PostgreSQL by app and category.
func (e *Engine) topApps(ctx context.Context, deviceID any, from, to jsbind.Value) ([]*goapi.Object, error) {
	conditions, args, err := e.rangeArgs(from, to, " AND started_at >= ?", " AND ended_at <= ?", bindValue(deviceID))
	if err != nil {
		return nil, err
	}
	query := `SELECT app_name, category,
                SUM(duration_secs) as total_secs,
                COUNT(*) as session_count
            FROM activity_sessions WHERE device_id = ?` + conditions +
		e.db.Pick(" GROUP BY app_name ORDER BY total_secs DESC", " GROUP BY app_name, category ORDER BY total_secs DESC")
	rows, err := e.rows(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read the apps of a device: %w", err)
	}
	return rows, nil
}

// dlpEventStats is getDlpEventStats(). On SQLite the sums of an empty
// table are null, on PostgreSQL every count is a number.
func (e *Engine) dlpEventStats(ctx context.Context) (*goapi.Object, error) {
	rows, err := e.rows(ctx, e.db.Pick(`
                SELECT
                    COUNT(*) AS total,
                    SUM(CASE WHEN action = 'block' THEN 1 ELSE 0 END) AS blocked,
                    SUM(CASE WHEN action = 'log' THEN 1 ELSE 0 END) AS logged,
                    SUM(CASE WHEN event_source = 'usb' THEN 1 ELSE 0 END) AS usb_events,
                    SUM(CASE WHEN event_source = 'file' THEN 1 ELSE 0 END) AS file_events
                FROM dlp_events
            `, `
                SELECT
                    COUNT(*)::INTEGER AS total,
                    COUNT(*) FILTER (WHERE action = 'block')::INTEGER AS blocked,
                    COUNT(*) FILTER (WHERE action = 'log')::INTEGER AS logged,
                    COUNT(*) FILTER (WHERE event_source = 'usb')::INTEGER AS usb_events,
                    COUNT(*) FILTER (WHERE event_source = 'file')::INTEGER AS file_events
                FROM dlp_events
            `))
	if err != nil {
		return nil, fmt.Errorf("count the data guard events: %w", err)
	}
	return rows[0], nil
}

// dlpEvents is getDlpEvents({limit}): the newest events. On SQLite
// created_at is declared DATETIME, which the Go driver would turn into a
// time; the second created_at (+created_at has no declared type) puts the
// stored text in its place.
func (e *Engine) dlpEvents(ctx context.Context, limit int) ([]*goapi.Object, error) {
	rows, err := e.rows(ctx, e.db.Pick(
		"SELECT *, +created_at AS created_at FROM dlp_events WHERE 1=1 ORDER BY dlp_events.created_at DESC LIMIT ?",
		"SELECT * FROM dlp_events WHERE 1=1 ORDER BY created_at DESC LIMIT ?",
	), limit)
	if err != nil {
		return nil, fmt.Errorf("read the data guard events: %w", err)
	}
	return rows, nil
}

// auditLogs is getAuditLogs(limit, offset): the newest entries with the
// account's name.
func (e *Engine) auditLogs(ctx context.Context, limit, offset int) ([]*goapi.Object, error) {
	rows, err := e.rows(ctx, `
                SELECT a.*, u.username FROM audit_log a
                LEFT JOIN users u ON a.user_id = u.id
                ORDER BY a.created_at DESC LIMIT ? OFFSET ?
            `, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("read the audit log: %w", err)
	}
	return rows, nil
}

// ticketStats is getTicketStats(): {total, open, in_progress, resolved,
// closed}.
func (e *Engine) ticketStats(ctx context.Context) (*goapi.Object, error) {
	if e.db.IsPostgres() {
		rows, err := e.rows(ctx, `
                SELECT COUNT(*) as total,
                    COUNT(*) FILTER (WHERE status = 'open') as open,
                    COUNT(*) FILTER (WHERE status = 'in_progress') as in_progress,
                    COUNT(*) FILTER (WHERE status = 'resolved') as resolved,
                    COUNT(*) FILTER (WHERE status = 'closed') as closed
                FROM tickets
            `)
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
		{"total", "SELECT COUNT(*) as c FROM tickets"},
		{"open", "SELECT COUNT(*) as c FROM tickets WHERE status = 'open'"},
		{"in_progress", "SELECT COUNT(*) as c FROM tickets WHERE status = 'in_progress'"},
		{"resolved", "SELECT COUNT(*) as c FROM tickets WHERE status = 'resolved'"},
		{"closed", "SELECT COUNT(*) as c FROM tickets WHERE status = 'closed'"},
	} {
		n, err := e.count(ctx, c.query)
		if err != nil {
			return nil, err
		}
		stats.Set(c.key, n)
	}
	return stats, nil
}

// allTickets is getAllTickets() without filters, newest first.
func (e *Engine) allTickets(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := e.rows(ctx, "SELECT * FROM tickets WHERE 1=1 ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("read the tickets: %w", err)
	}
	return rows, nil
}

// inventory is a row of getAllInventories() as the report reads it.
type inventory struct {
	hardware any
}

// allInventories is getAllInventories(): every report, newest received
// first, with hardware and software parsed. One that does not parse
// fails the whole list.
func (e *Engine) allInventories(ctx context.Context) ([]inventory, error) {
	rows, err := e.rows(ctx, "SELECT * FROM device_inventory ORDER BY received_at DESC")
	if err != nil {
		return nil, fmt.Errorf("read the inventory: %w", err)
	}
	out := make([]inventory, 0, len(rows))
	for _, row := range rows {
		hardware, err := e.inventoryDocument(row.Get("hardware"))
		if err != nil {
			return nil, fmt.Errorf("read the hardware of an inventory: %w", err)
		}
		if _, err := e.inventoryDocument(row.Get("software")); err != nil {
			return nil, fmt.Errorf("read the software of an inventory: %w", err)
		}
		out = append(out, inventory{hardware: hardware})
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

// alertHistory is getAlertHistory() without filters: every alert with its
// rule's name, newest first.
func (e *Engine) alertHistory(ctx context.Context) ([]*goapi.Object, error) {
	rows, err := e.rows(ctx, "SELECT h.*, r.name as rule_name FROM alert_history h LEFT JOIN alert_rules r ON h.rule_id = r.id WHERE 1=1 ORDER BY h.triggered_at DESC")
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
