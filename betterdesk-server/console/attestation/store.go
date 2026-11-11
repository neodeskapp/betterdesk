package attestation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Settings is the settings table as the attestation uses it: two rows,
// the last result (ResultKey) and the badge texts (BadgeConfigKey).
type Settings interface {
	// Get is db.getSetting: the value of a row; found is false when there
	// is no row or its value is NULL.
	Get(ctx context.Context, key string) (value string, found bool, err error)
	// Set is db.setSetting: it writes the row, in place of one that is
	// there, with the time of the write.
	Set(ctx context.Context, key, value string) error
}

// StoreSettings is the settings table of the console's database.
func StoreSettings(db *store.DB) Settings { return dbSettings{db} }

type dbSettings struct{ db *store.DB }

func (s dbSettings) Get(ctx context.Context, key string) (string, bool, error) {
	var value sql.NullString
	err := s.db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return value.String, value.Valid, nil
}

func (s dbSettings) Set(ctx context.Context, key, value string) error {
	query := s.db.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = NOW()`,
	)
	if _, err := s.db.Exec(ctx, query, key, value); err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}
