package scaling

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The settings rows of the two lists.
const (
	keyRelays = "scaling_relay_nodes"
	keyRules  = "scaling_assignment_rules"
)

// getSetting reads a row of the settings table. ok is false when the row
// is missing or its value is NULL.
func getSetting(ctx context.Context, db *store.DB, key string) (value string, ok bool, err error) {
	var stored sql.NullString
	err = db.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return stored.String, stored.Valid, nil
}

// setSetting writes a row of the settings table and stamps it with the
// database's clock.
func setSetting(ctx context.Context, db *store.DB, key, value string) error {
	now := db.Pick("datetime('now')", "NOW()")
	_, err := db.Exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, "+now+") "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now, key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// listWrites lets one write of the lists at a time through. A write reads
// the whole list, changes it and stores it back, so two at once would
// store two lists and the second would replace the first. The group is
// registered at two mounts with handlers of their own, and both write the
// same rows: the lock is the package's. The console is the one process
// that writes these rows.
var listWrites sync.Mutex

// load reads a stored list. A row that is missing or empty is an empty
// list; a value that is no JSON array, or a failed read, is an error, so
// that no write replaces a list it could not read.
func (h *handlers) load(ctx context.Context, key string) ([]any, error) {
	raw, ok, err := getSetting(ctx, h.db, key)
	if err != nil {
		return nil, err
	}
	if !ok || raw == "" {
		return []any{}, nil
	}
	items, err := parseArray(raw)
	if err == nil && items == nil {
		err = errors.New("no list")
	}
	if err != nil {
		return nil, fmt.Errorf("read setting %s: %w", key, err)
	}
	return items, nil
}

// save stores a list as JSON.
func (h *handlers) save(ctx context.Context, key string, items []any) error {
	text, _ := list(items).MarshalJSON()
	return setSetting(ctx, h.db, key, string(text))
}
