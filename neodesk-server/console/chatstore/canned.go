package chatstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Canned is a canned reply: shared (OwnerID 0), which accounts with
// chat.canned_manage keep, or an account's own.
type Canned struct {
	ID        int64  `json:"id"`
	OwnerID   int64  `json:"owner_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	UpdatedMs int64  `json:"updated_at"`
}

// Shared reports whether every account sees the reply.
func (c Canned) Shared() bool { return c.OwnerID == 0 }

// CannedFor returns the shared replies and the account's own, by title.
func (s *Store) CannedFor(ctx context.Context, account int64) ([]Canned, error) {
	rows, err := s.db.Query(ctx, `SELECT id, owner_id, title, body, updated_ms FROM chat_canned WHERE owner_id IS NULL OR owner_id = ? ORDER BY LOWER(title), id`, account)
	if err != nil {
		return nil, fmt.Errorf("chat: canned replies: %w", err)
	}
	defer rows.Close()
	out := []Canned{}
	for rows.Next() {
		c, err := scanCanned(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanCanned(row interface{ Scan(...any) error }) (Canned, error) {
	var c Canned
	var owner sql.NullInt64
	err := row.Scan(&c.ID, &owner, &c.Title, &c.Body, &c.UpdatedMs)
	c.OwnerID = owner.Int64
	return c, err
}

// CannedByID returns one reply, ErrNotFound when there is none.
func (s *Store) CannedByID(ctx context.Context, id int64) (Canned, error) {
	c, err := scanCanned(s.db.QueryRow(ctx, `SELECT id, owner_id, title, body, updated_ms FROM chat_canned WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// cannedText cuts a reply's title and body; both are needed.
func cannedText(title, body string) (string, string, error) {
	title = cutRunes(strings.TrimSpace(title), 80)
	body = cutRunes(strings.TrimSpace(body), MaxText)
	if title == "" || body == "" {
		return "", "", ErrEmpty
	}
	return title, body, nil
}

// AddCanned stores a reply: shared when owner is 0.
func (s *Store) AddCanned(ctx context.Context, owner, creator int64, title, body string) (Canned, error) {
	title, body, err := cannedText(title, body)
	if err != nil {
		return Canned{}, err
	}
	id, err := s.db.Insert(ctx, `INSERT INTO chat_canned (owner_id, title, body, created_by, updated_ms) VALUES (?, ?, ?, ?, ?)`,
		nullInt(owner), title, body, creator, s.nowMs())
	if err != nil {
		return Canned{}, fmt.Errorf("chat: add a canned reply: %w", err)
	}
	return s.CannedByID(ctx, id)
}

// UpdateCanned changes a reply's title and body.
func (s *Store) UpdateCanned(ctx context.Context, id int64, title, body string) (Canned, error) {
	title, body, err := cannedText(title, body)
	if err != nil {
		return Canned{}, err
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_canned SET title = ?, body = ?, updated_ms = ? WHERE id = ?`, title, body, s.nowMs(), id); err != nil {
		return Canned{}, fmt.Errorf("chat: change canned reply %d: %w", id, err)
	}
	return s.CannedByID(ctx, id)
}

// DeleteCanned removes a reply.
func (s *Store) DeleteCanned(ctx context.Context, id int64) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM chat_canned WHERE id = ?`, id); err != nil {
		return fmt.Errorf("chat: delete canned reply %d: %w", id, err)
	}
	return nil
}
