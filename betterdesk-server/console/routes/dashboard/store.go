package dashboard

import (
	"context"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// deviceCounts is the console's own device table as the device layer
// reads it for this group. GetStats is the one call the dashboard's use
// of the layer leads to; the embedded Store is nil.
type deviceCounts struct {
	goapi.Store
	db *store.DB
	// sync brings the table up to date with the server's first, as
	// getPeerStats did (syncGoPeers); nil reads the table as it is.
	sync func(context.Context)
}

// GetStats counts the devices in the console's table that are not
// deleted: {total, online, banned, offline}. The device layer falls back
// on it when the server gives no statistics.
func (c deviceCounts) GetStats(ctx context.Context) (any, error) {
	if c.sync != nil {
		c.sync(ctx)
	}
	query := c.db.Pick(
		`SELECT
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0),
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0 AND status_online = 1),
			(SELECT COUNT(*) FROM peer WHERE is_deleted = 0 AND is_banned = 1)`,
		`SELECT
			COUNT(*) FILTER (WHERE NOT is_deleted),
			COUNT(*) FILTER (WHERE NOT is_deleted AND status_online),
			COUNT(*) FILTER (WHERE NOT is_deleted AND is_banned)
		FROM peer`,
	)
	var total, online, banned int64
	if err := c.db.QueryRow(ctx, query).Scan(&total, &online, &banned); err != nil {
		return nil, fmt.Errorf("count the devices: %w", err)
	}
	return goapi.NewObject(
		"total", float64(total),
		"online", float64(online),
		"banned", float64(banned),
		"offline", float64(total-online),
	), nil
}

// scopedCounts counts the devices of the server's list that are in the
// scope, in the form of the server's counts.
func scopedCounts(peers []*goapi.Object, scope *access.DeviceScope) *goapi.Object {
	var total, online, banned, withNotes float64
	for _, p := range peers {
		if p == nil || !scope.Has(p.Str("id")) {
			continue
		}
		total++
		if p.Bool("online") {
			online++
		}
		if p.Bool("banned") {
			banned++
		}
		if p.Bool("note") {
			withNotes++
		}
	}
	return goapi.NewObject("total", total, "online", online, "offline", total-online, "banned", banned, "withNotes", withNotes)
}

// auditRow is an entry of the console's audit log as the activity feed
// reads it.
type auditRow struct {
	Action    string
	Details   store.NullString
	IPAddress store.NullString
	CreatedAt store.Time
}

// recentAuditRows returns the newest entries of the console's audit log.
func recentAuditRows(ctx context.Context, db *store.DB, limit int) ([]auditRow, error) {
	rows, err := store.Select(ctx, db, `SELECT a.action, a.details, a.ip_address, a.created_at
		FROM audit_log a LEFT JOIN users u ON a.user_id = u.id
		ORDER BY a.created_at DESC LIMIT ? OFFSET 0`, limit)
	if err != nil {
		return nil, fmt.Errorf("read the audit log: %w", err)
	}
	defer rows.Close()
	var entries []auditRow
	for rows.Next() {
		var e auditRow
		if err := rows.Scan(&e.Action, &e.Details, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("read an audit entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the audit log: %w", err)
	}
	return entries, nil
}
