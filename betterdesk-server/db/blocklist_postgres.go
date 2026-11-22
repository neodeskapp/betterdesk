package db

import (
	"fmt"
	"time"
)

// SaveBlocklistEntry stores e, replacing the reason and time of an entry
// of the same kind and value.
func (pg *PostgresDB) SaveBlocklistEntry(e *BlocklistEntry) error {
	ctx, cancel := pg.opCtx()
	defer cancel()
	at := e.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err := pg.pool.Exec(ctx,
		`INSERT INTO blocklist_entries (kind, value, reason, created_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (kind, value) DO UPDATE SET reason = EXCLUDED.reason, created_at = EXCLUDED.created_at`,
		e.Kind, e.Value, e.Reason, at.UTC())
	if err != nil {
		return fmt.Errorf("db: save blocklist entry: %w", err)
	}
	return nil
}

// DeleteBlocklistEntry removes the entry of that kind and value, if any.
func (pg *PostgresDB) DeleteBlocklistEntry(kind, value string) error {
	ctx, cancel := pg.opCtx()
	defer cancel()
	if _, err := pg.pool.Exec(ctx, `DELETE FROM blocklist_entries WHERE kind = $1 AND value = $2`, kind, value); err != nil {
		return fmt.Errorf("db: delete blocklist entry: %w", err)
	}
	return nil
}

// ListBlocklistEntries returns every stored entry, oldest first.
func (pg *PostgresDB) ListBlocklistEntries() ([]*BlocklistEntry, error) {
	ctx, cancel := pg.opCtx()
	defer cancel()
	rows, err := pg.pool.Query(ctx, `SELECT kind, value, reason, created_at FROM blocklist_entries ORDER BY created_at, kind, value`)
	if err != nil {
		return nil, fmt.Errorf("db: list blocklist entries: %w", err)
	}
	defer rows.Close()
	var out []*BlocklistEntry
	for rows.Next() {
		e := &BlocklistEntry{}
		if err := rows.Scan(&e.Kind, &e.Value, &e.Reason, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: list blocklist entries: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
