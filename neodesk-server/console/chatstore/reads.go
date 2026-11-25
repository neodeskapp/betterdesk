package chatstore

import (
	"context"
	"fmt"
	"strings"
)

// Read marks are per reader: a console account ("u:<id>") or a device
// ("d:<id>"), never per browser, so every session of an account and every
// reload agree (L492: the old store had one read flag for everyone). A
// reader without a mark has read up to the read baseline.

// MarkRead notes that the reader has read a conversation up to a message
// (0: its newest). A mark only moves forward. It returns the mark.
func (s *Store) MarkRead(ctx context.Context, conversation string, reader Party, upTo int64) (int64, error) {
	var newest int64
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM chat_messages WHERE conversation_id = ?`, conversation).Scan(&newest); err != nil {
		return 0, fmt.Errorf("chat: newest of %s: %w", conversation, err)
	}
	if upTo <= 0 || upTo > newest {
		upTo = newest
	}
	q := s.db.Pick(
		`INSERT INTO chat_reads (conversation_id, reader, last_read_id, read_ms) VALUES (?, ?, ?, ?)
		 ON CONFLICT (conversation_id, reader) DO UPDATE SET last_read_id = MAX(chat_reads.last_read_id, excluded.last_read_id), read_ms = excluded.read_ms`,
		`INSERT INTO chat_reads (conversation_id, reader, last_read_id, read_ms) VALUES (?, ?, ?, ?)
		 ON CONFLICT (conversation_id, reader) DO UPDATE SET last_read_id = GREATEST(chat_reads.last_read_id, EXCLUDED.last_read_id), read_ms = EXCLUDED.read_ms`)
	if _, err := s.db.Exec(ctx, q, conversation, reader.Key(), upTo, s.nowMs()); err != nil {
		return 0, fmt.Errorf("chat: mark %s read: %w", conversation, err)
	}
	var mark int64
	err := s.db.QueryRow(ctx, `SELECT last_read_id FROM chat_reads WHERE conversation_id = ? AND reader = ?`, conversation, reader.Key()).Scan(&mark)
	return mark, err
}

// notMine is the condition that leaves out a reader's own messages, and
// its arguments.
func notMine(reader Party) (string, []any) {
	switch reader.Kind {
	case KindUser:
		// Rows of version 1 name a console user by name only
		return `NOT ((m.from_kind = 'user' AND m.from_user_id = ?) OR (m.from_kind = '' AND m.from_id = 'operator' AND m.from_name = ?))`,
			[]any{reader.UserID(), reader.Name}
	case KindDevice:
		return `NOT (m.from_id = ? AND m.from_kind IN ('', 'device'))`, []any{reader.ID}
	}
	return `1 = 1`, nil
}

// Unread counts, per conversation, the messages a reader has not read:
// newer than its mark (or the baseline), not its own, not deleted, not
// lines of the system. conversations limits the count to those ids
// (none: every conversation).
func (s *Store) Unread(ctx context.Context, reader Party, conversations []string) (map[string]int, error) {
	baseline, err := s.setting(ctx, keyReadBaseline, 0)
	if err != nil {
		return nil, err
	}
	mine, mineArgs := notMine(reader)
	query := `SELECT m.conversation_id, COUNT(*) FROM chat_messages m
		LEFT JOIN chat_reads r ON r.conversation_id = m.conversation_id AND r.reader = ?
		WHERE m.id > COALESCE(r.last_read_id, ?) AND m.deleted_ms IS NULL AND m.from_kind <> 'system' AND m.from_id <> 'system' AND ` + mine
	args := append([]any{reader.Key(), baseline}, mineArgs...)
	if len(conversations) > 0 {
		marks := make([]string, len(conversations))
		for i, c := range conversations {
			marks[i] = "?"
			args = append(args, c)
		}
		query += ` AND m.conversation_id IN (` + strings.Join(marks, ",") + `)`
	}
	query += ` GROUP BY m.conversation_id`
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chat: count unread: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// ReadMark is how far a reader has read a conversation.
type ReadMark struct {
	Reader     Party
	LastReadID int64
	ReadMs     int64
}

// Reads are the marks of everyone who read a conversation. names gives a
// reader's name (nil: none).
func (s *Store) Reads(ctx context.Context, conversation string) ([]ReadMark, error) {
	rows, err := s.db.Query(ctx, `SELECT reader, last_read_id, read_ms FROM chat_reads WHERE conversation_id = ? ORDER BY reader`, conversation)
	if err != nil {
		return nil, fmt.Errorf("chat: reads of %s: %w", conversation, err)
	}
	defer rows.Close()
	var out []ReadMark
	for rows.Next() {
		var key string
		var m ReadMark
		if err := rows.Scan(&key, &m.LastReadID, &m.ReadMs); err != nil {
			return nil, err
		}
		m.Reader = partyOfKey(key, "")
		out = append(out, m)
	}
	return out, rows.Err()
}
