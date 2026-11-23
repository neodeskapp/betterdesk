package desktop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// getSetting returns the value stored under key in the settings table, ""
// when there is none.
func getSetting(ctx context.Context, db *store.DB, key string) (string, error) {
	var value sql.NullString
	err := db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}
	return value.String, nil
}

// setSetting stores value under key, in place of what was there. value is
// text; nil is refused by the table, which takes no NULL.
func setSetting(ctx context.Context, db *store.DB, key string, value any) error {
	query := db.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = NOW()`,
	)
	if _, err := db.Exec(ctx, query, key, value); err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}
