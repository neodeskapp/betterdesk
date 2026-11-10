package scaling

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

// load reads a stored list. A row that is missing, empty, no JSON or no
// JSON array is an empty list, and so is a list that cannot be read: the
// reason goes to the log.
func (h *handlers) load(ctx context.Context, key string) []any {
	raw, ok, err := getSetting(ctx, h.db, key)
	if err != nil {
		h.log.Error("[Scaling] Failed to parse "+key+":", err)
		return []any{}
	}
	if !ok || raw == "" {
		return []any{}
	}
	items, err := parseArray(raw)
	if err != nil {
		h.log.Error("[Scaling] Failed to parse "+key+":", err)
		return []any{}
	}
	if items == nil {
		return []any{}
	}
	return items
}

// save stores a list as JSON.
func (h *handlers) save(ctx context.Context, key string, items []any) error {
	text, _ := list(items).MarshalJSON()
	return setSetting(ctx, h.db, key, string(text))
}
