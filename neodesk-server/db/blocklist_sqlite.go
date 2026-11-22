package db

import (
	"fmt"
	"time"
)

// SaveBlocklistEntry stores e, replacing the reason and time of an entry
// of the same kind and value.
func (s *SQLiteDB) SaveBlocklistEntry(e *BlocklistEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := e.CreatedAt
	if at.IsZero() {
		at = time.Now()
	}
	_, err := s.db.Exec(
		`INSERT INTO blocklist_entries (kind, value, reason, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (kind, value) DO UPDATE SET reason = excluded.reason, created_at = excluded.created_at`,
		e.Kind, e.Value, e.Reason, at.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("db: save blocklist entry: %w", err)
	}
	return nil
}

// DeleteBlocklistEntry removes the entry of that kind and value, if any.
func (s *SQLiteDB) DeleteBlocklistEntry(kind, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM blocklist_entries WHERE kind = ? AND value = ?`, kind, value); err != nil {
		return fmt.Errorf("db: delete blocklist entry: %w", err)
	}
	return nil
}

// ListBlocklistEntries returns every stored entry, oldest first.
func (s *SQLiteDB) ListBlocklistEntries() ([]*BlocklistEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT kind, value, reason, created_at FROM blocklist_entries ORDER BY created_at, kind, value`)
	if err != nil {
		return nil, fmt.Errorf("db: list blocklist entries: %w", err)
	}
	defer rows.Close()
	var out []*BlocklistEntry
	for rows.Next() {
		e := &BlocklistEntry{}
		var at string
		if err := rows.Scan(&e.Kind, &e.Value, &e.Reason, &at); err != nil {
			return nil, fmt.Errorf("db: list blocklist entries: %w", err)
		}
		e.CreatedAt = parseSQLiteDateTime(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
