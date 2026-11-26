package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/reports"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// auditLogs is getAuditLogs(limit): the newest entries of the audit log
// with the name of the account, as the JSON of the rows the drivers
// returned. limit is bound as the driver bound the JavaScript number, so
// a negative limit lists everything on SQLite and fails on PostgreSQL,
// and a limit beyond 64 bits fails on both.
func auditLogs(ctx context.Context, db *store.DB, limit float64) (json.RawMessage, error) {
	args, err := jsbind.Args(db.Dialect(), 2, jsbind.Number(limit), jsbind.Number(0))
	if err != nil {
		return nil, fmt.Errorf("bind the audit log limit: %w", err)
	}
	rows, err := reports.SelectRows(ctx, db, nil, `
                SELECT a.id, a.user_id, a.action, a.details, a.ip_address, a.created_at, u.username FROM audit_log a
                LEFT JOIN users u ON a.user_id = u.id
                ORDER BY a.created_at DESC LIMIT ? OFFSET ?
            `, args...)
	if err != nil {
		return nil, fmt.Errorf("read the audit log: %w", err)
	}
	list := make([]any, len(rows))
	for i, row := range rows {
		list[i] = row
	}
	return reports.Stringify(list), nil
}

// getSetting is db.getSetting(key): the stored value, ok false when the
// key has none.
func getSetting(ctx context.Context, db *store.DB, key string) (value string, ok bool, err error) {
	err = db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return value, true, nil
}

// setSetting is db.setSetting(key, value).
func setSetting(ctx context.Context, db *store.DB, key, value string) error {
	now := store.NowSQL(db.Dialect())
	_, err := db.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, `+now+`)
                ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = `+now, key, value)
	if err != nil {
		return fmt.Errorf("store setting %s: %w", key, err)
	}
	return nil
}
