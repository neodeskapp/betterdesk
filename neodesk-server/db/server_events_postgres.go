package db

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AddServerEvents stores events in one transaction.
func (pg *PostgresDB) AddServerEvents(events []*ServerEvent) error {
	if len(events) == 0 {
		return nil
	}
	ctx, cancel := pg.opCtx()
	defer cancel()
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(`INSERT INTO server_events (created_at, action, actor, target, details) VALUES ($1, $2, $3, $4, $5)`,
			e.Time.UTC(), e.Action, e.Actor, e.Target, eventDetails(e.Details))
	}
	tx, err := pg.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: add server events: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("db: add server events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: add server events: %w", err)
	}
	return nil
}

// ListServerEvents returns events newest first, those of one action when
// action is not "", skipping offset and at most limit.
func (pg *PostgresDB) ListServerEvents(action string, limit, offset int) ([]*ServerEvent, error) {
	ctx, cancel := pg.opCtx()
	defer cancel()
	query := `SELECT created_at, action, actor, target, details FROM server_events`
	args := []any{}
	if action != "" {
		query += ` WHERE action = $1`
		args = append(args, action)
	}
	query += fmt.Sprintf(` ORDER BY id DESC LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2)
	args = append(args, limit, offset)
	rows, err := pg.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list server events: %w", err)
	}
	defer rows.Close()
	out := []*ServerEvent{}
	for rows.Next() {
		e := &ServerEvent{}
		var details string
		if err := rows.Scan(&e.Time, &e.Action, &e.Actor, &e.Target, &details); err != nil {
			return nil, fmt.Errorf("db: list server events: %w", err)
		}
		e.Time = e.Time.UTC()
		e.Details = parseEventDetails(details)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountServerEvents counts the stored events, those of one action when
// action is not "".
func (pg *PostgresDB) CountServerEvents(action string) (int64, error) {
	ctx, cancel := pg.opCtx()
	defer cancel()
	query, args := `SELECT COUNT(*) FROM server_events`, []any{}
	if action != "" {
		query, args = query+` WHERE action = $1`, append(args, action)
	}
	var n int64
	if err := pg.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("db: count server events: %w", err)
	}
	return n, nil
}

// PruneServerEvents deletes the events recorded before cutoff and
// returns how many went.
func (pg *PostgresDB) PruneServerEvents(cutoff time.Time) (int64, error) {
	ctx, cancel := pg.opCtx()
	defer cancel()
	tag, err := pg.pool.Exec(ctx, `DELETE FROM server_events WHERE created_at < $1`, cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("db: prune server events: %w", err)
	}
	return tag.RowsAffected(), nil
}
