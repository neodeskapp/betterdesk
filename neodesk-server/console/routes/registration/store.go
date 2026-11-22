package registration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// getSetting is db.getSetting: the value of a console setting as the
// driver returned it, null when the setting is missing.
func getSetting(ctx context.Context, db *store.DB, key string) (jsbind.Value, error) {
	var cell jsbind.Cell
	err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&cell)
	if errors.Is(err, sql.ErrNoRows) {
		return jsbind.Null, nil
	}
	if err != nil {
		return jsbind.Null, fmt.Errorf("load setting %s: %w", key, err)
	}
	return cell.Value(), nil
}

// auditEntry is the time and the account of an audit log entry: the
// instant PostgreSQL read, or the text SQLite stored.
type auditEntry struct {
	instant  *time.Time
	text     string
	username string
}

// sqliteClock is the start of the text SQLite's datetime('now') writes.
var sqliteClock = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

// latestAuditEntry is db.getLatestAuditEntry: the newest audit entry of
// the action whose details start with prefix, with the name of its
// account ("" without one); nil when there is none.
func latestAuditEntry(ctx context.Context, db *store.DB, action, prefix string) (*auditEntry, error) {
	query := db.Pick(`
		SELECT a.created_at, u.username FROM audit_log a
		LEFT JOIN users u ON a.user_id = u.id
		WHERE a.action = ? AND substr(a.details, 1, length(?)) = ?
		ORDER BY a.created_at DESC, a.id DESC LIMIT 1`, `
		SELECT a.created_at, u.username FROM audit_log a LEFT JOIN users u ON a.user_id = u.id
		WHERE a.action = ? AND left(a.details, length(?)) = ?
		ORDER BY a.created_at DESC, a.id DESC LIMIT 1`)
	var created store.Time
	var username sql.NullString
	err := store.SelectRow(ctx, db, query, action, prefix, prefix).Scan(&created, &username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load the latest audit entry %s: %w", action, err)
	}
	e := &auditEntry{text: created.String(), username: username.String}
	if db.IsPostgres() {
		if t, ok := created.Instant(); ok {
			e.instant = &t
		}
	}
	return e, nil
}
