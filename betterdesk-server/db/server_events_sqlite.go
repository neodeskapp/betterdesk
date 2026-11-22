package db

import (
	"fmt"
	"time"
)

// AddServerEvents stores events in one transaction.
func (s *SQLiteDB) AddServerEvents(events []*ServerEvent) error {
	if len(events) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("db: add server events: %w", err)
	}
	defer tx.Rollback()
	for _, e := range events {
		if _, err := tx.Exec(`INSERT INTO server_events (created_at, action, actor, target, details) VALUES (?, ?, ?, ?, ?)`,
			e.Time.UTC().Format(sqliteEventTime), e.Action, e.Actor, e.Target, eventDetails(e.Details)); err != nil {
			return fmt.Errorf("db: add server events: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: add server events: %w", err)
	}
	return nil
}

// ListServerEvents returns events newest first, those of one action when
// action is not "", skipping offset and at most limit.
func (s *SQLiteDB) ListServerEvents(action string, limit, offset int) ([]*ServerEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := `SELECT created_at, action, actor, target, details FROM server_events`
	args := []any{}
	if action != "" {
		query += ` WHERE action = ?`
		args = append(args, action)
	}
	query += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list server events: %w", err)
	}
	defer rows.Close()
	out := []*ServerEvent{}
	for rows.Next() {
		e := &ServerEvent{}
		var at, details string
		if err := rows.Scan(&at, &e.Action, &e.Actor, &e.Target, &details); err != nil {
			return nil, fmt.Errorf("db: list server events: %w", err)
		}
		e.Time, _ = time.ParseInLocation(sqliteEventTime, at, time.UTC)
		e.Details = parseEventDetails(details)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountServerEvents counts the stored events, those of one action when
// action is not "".
func (s *SQLiteDB) CountServerEvents(action string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query, args := `SELECT COUNT(*) FROM server_events`, []any{}
	if action != "" {
		query, args = query+` WHERE action = ?`, append(args, action)
	}
	var n int64
	if err := s.db.QueryRow(query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("db: count server events: %w", err)
	}
	return n, nil
}

// PruneServerEvents deletes the events recorded before cutoff and
// returns how many went.
func (s *SQLiteDB) PruneServerEvents(cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM server_events WHERE created_at < ?`, cutoff.UTC().Format(sqliteEventTime))
	if err != nil {
		return 0, fmt.Errorf("db: prune server events: %w", err)
	}
	return res.RowsAffected()
}
