package helpmail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// getSetting reads a row of the settings table (dbAdapter getSetting). ok
// is false when the row is missing or its value is null.
func getSetting(ctx context.Context, q store.Querier, key string) (value string, ok bool, err error) {
	var stored sql.NullString
	err = q.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return stored.String, stored.Valid, nil
}

// setSetting writes a row of the settings table and stamps it with the
// database's clock (dbAdapter setSetting).
func setSetting(ctx context.Context, q store.Querier, key, value string) error {
	now := store.NowSQL(q.Dialect())
	_, err := q.Exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, "+now+") "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now, key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}
