package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries only this group runs: a device's system information and
// metrics, the audit entry of its ban, what a delete clears, and the
// access rules for RustDesk clients with their assignments.

// sysinfo returns the system information row of a device in the shape
// the device page reads (parseSysinfoRow of the adapter), nil when there
// is none.
func sysinfo(ctx context.Context, db *store.DB, peerID string) (*goapi.Object, error) {
	rows, err := devicegroups.Rows(ctx, db, `SELECT * FROM peer_sysinfo WHERE peer_id = ?`, peerID)
	if err != nil {
		return nil, fmt.Errorf("load the system information of device %s: %w", peerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	get := func(name string) any { return devicegroups.Plain(row.Get(name)) }
	// The PostgreSQL adapter read the two sizes with unary plus: a NULL is 0
	number := func(name string) any {
		v := get(name)
		if !db.IsPostgres() {
			return v
		}
		switch t := v.(type) {
		case nil:
			return float64(0)
		case bool:
			if t {
				return float64(1)
			}
			return float64(0)
		case string:
			return jsNumber(t)
		}
		return v
	}
	return goapi.NewObject(
		"peer_id", get("peer_id"),
		"hostname", get("hostname"),
		"username", get("username"),
		"platform", get("platform"),
		"version", get("version"),
		"cpu_name", get("cpu_name"),
		"cpu_cores", get("cpu_cores"),
		"cpu_freq_ghz", number("cpu_freq_ghz"),
		"memory_gb", number("memory_gb"),
		"os_full", get("os_full"),
		"displays", safeJSONParse(db, get("displays"), []any{}),
		"encoding", safeJSONParse(db, get("encoding"), []any{}),
		"features", safeJSONParse(db, get("features"), goapi.NewObject()),
		"platform_additions", safeJSONParse(db, get("platform_additions"), goapi.NewObject()),
		"updated_at", row.Get("updated_at"),
	), nil
}

// jsNumber is +text.
func jsNumber(s string) any {
	return jsreq.Number(s)
}

// safeJSONParse is the adapter's safeJsonParse(value, fallback): the
// parsed JSON of text, and the fallback for NULL and where JSON.parse
// throws. On PostgreSQL a value the driver parsed already (an object or a
// list) is taken as it is.
func safeJSONParse(db *store.DB, v any, fallback any) any {
	if db.IsPostgres() {
		switch v.(type) {
		case *goapi.Object, []any:
			return v
		}
	}
	switch t := v.(type) {
	case nil:
		return fallback
	case string:
		parsed, err := goapi.Parse([]byte(t))
		if err != nil {
			return fallback
		}
		return parsed
	case bool, float64:
		return t
	}
	// JSON.parse(String(v)) of anything else
	parsed, err := goapi.Parse([]byte(jsreq.String(v)))
	if err != nil {
		return fallback
	}
	return parsed
}

// metric is one row of peer_metrics.
type metric struct {
	CPU, Memory, Disk store.Float
	CreatedAt         store.Time
}

// metrics returns the newest rows of peer_metrics of a device, newest
// first.
func metrics(ctx context.Context, db *store.DB, peerID string, limit int) ([]metric, error) {
	rows, err := store.Select(ctx, db, `SELECT cpu_usage, memory_usage, disk_usage, created_at FROM peer_metrics WHERE peer_id = ? ORDER BY created_at DESC LIMIT ?`, peerID, limit)
	if err != nil {
		return nil, fmt.Errorf("load the metrics of device %s: %w", peerID, err)
	}
	defer rows.Close()
	var out []metric
	for rows.Next() {
		var m metric
		if err := rows.Scan(&m.CPU, &m.Memory, &m.Disk, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("load the metrics of device %s: %w", peerID, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load the metrics of device %s: %w", peerID, err)
	}
	return out, nil
}

// auditEntry is the time and the account of an audit log entry.
type auditEntry struct {
	CreatedAt store.Time
	Username  sql.NullString
}

// latestAuditEntry returns the newest audit entry of the action whose
// details start with prefix, nil when there is none.
func latestAuditEntry(ctx context.Context, db *store.DB, action, prefix string) (*auditEntry, error) {
	query := db.Pick(`
		SELECT a.created_at, u.username FROM audit_log a
		LEFT JOIN users u ON a.user_id = u.id
		WHERE a.action = ? AND substr(a.details, 1, length(?)) = ?
		ORDER BY a.created_at DESC, a.id DESC LIMIT 1`, `
		SELECT a.created_at, u.username FROM audit_log a LEFT JOIN users u ON a.user_id = u.id
		WHERE a.action = ? AND left(a.details, length(?)) = ?
		ORDER BY a.created_at DESC, a.id DESC LIMIT 1`)
	var e auditEntry
	err := store.SelectRow(ctx, db, query, action, prefix, prefix).Scan(&e.CreatedAt, &e.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load the latest audit entry %s: %w", action, err)
	}
	return &e, nil
}

// clearDeletedDevice removes what the console keeps of a device that was
// deleted for good: its system information, its metrics, its folder, its
// manual device groups and its row in the console's device table.
func clearDeletedDevice(ctx context.Context, db *store.DB, id string) error {
	return db.InTx(ctx, func(tx *store.Tx) error {
		for _, query := range []string{
			`DELETE FROM peer_sysinfo WHERE peer_id = ?`,
			`DELETE FROM peer_metrics WHERE peer_id = ?`,
			`DELETE FROM device_folder_assignments WHERE device_id = ?`,
			`DELETE FROM device_group_members WHERE peer_id = ?`,
			`DELETE FROM peer WHERE id = ?`,
		} {
			if _, err := tx.Exec(ctx, query, id); err != nil {
				return fmt.Errorf("clear the rows of deleted device %s: %w", id, err)
			}
		}
		return nil
	})
}

// accountRole is the role of the users row named username, read the way
// the API server's db.GetUser reads it for the panel trust bridge: found
// is false when there is no such row, when its role is NULL, and when the
// query fails, which the API server all took as an account it does not
// have.
func accountRole(ctx context.Context, db *store.DB, username string) (role string, found bool) {
	var value sql.NullString
	if err := db.QueryRow(ctx, `SELECT role FROM users WHERE username = ?`, username).Scan(&value); err != nil || !value.Valid {
		return "", false
	}
	return value.String, true
}
