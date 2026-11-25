package chatstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The import of the chat of version 1 (A242, the lead's call on question
// 3). Version 1 kept a direct message under its recipient's name
// ("operator:<name>", and sent it to every chat user, L492), groups in the
// API server's table chat_groups, and what a computer wrote without naming
// a conversation under "operator" (L493). On its first start version 2
// moves that history where it belongs, once and in one transaction:
//
//   - a direct message into the direct conversation of its two accounts,
//     which only they read;
//   - a group with two or more accounts among its members into a group of
//     those accounts (computers drop out: groups are colleagues');
//   - what a computer wrote without a conversation into the computer's;
//   - everything else (a person whose account is gone, a group of one,
//     a message to oneself) into an "old messages" conversation
//     "old:<old id>" that only admins read, so nothing disappears.

// ConvOld is history of version 1 that belongs to no two accounts:
// "old:<the conversation's id in version 1>". Admins read it; nobody
// writes in it.
const ConvOld = "old"

// keyLegacyImported notes that the import ran (its report as JSON).
const keyLegacyImported = "legacy_imported"

// LegacyReport counts the messages the import moved or kept.
type LegacyReport struct {
	// Direct went into direct conversations
	Direct int `json:"direct"`
	// Groups stayed in groups that now have members
	Groups int `json:"groups"`
	// Devices went into their computer's conversation
	Devices int `json:"devices"`
	// Old went into old messages
	Old int `json:"old"`
}

// legacyMessage is a row of version 1 that the import looks at.
type legacyMessage struct {
	id             int64
	conv, fromID   string
	fromName, kind string
}

// ImportLegacy moves the chat history of version 1 into the conversations
// of version 2, unless that happened before. It reports what it moved; a
// run that finds the import done reports nothing.
func (s *Store) ImportLegacy(ctx context.Context) (LegacyReport, error) {
	var report LegacyReport
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		var done string
		err := tx.QueryRow(ctx, `SELECT value FROM chat_settings WHERE key = ?`, keyLegacyImported).Scan(&done)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("chat: read the import's mark: %w", err)
		}
		imp := &legacyImport{tx: tx, ctx: ctx, touched: map[string]string{}, titles: map[string]string{}}
		if err := imp.run(); err != nil {
			return err
		}
		report = imp.report
		b, _ := json.Marshal(report)
		insert := tx.Pick(`INSERT INTO chat_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			`INSERT INTO chat_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
		if _, err := tx.Exec(ctx, insert, keyLegacyImported, string(b)); err != nil {
			return fmt.Errorf("chat: mark the import done: %w", err)
		}
		return nil
	})
	return report, err
}

type legacyImport struct {
	tx  *store.Tx
	ctx context.Context
	// accounts by user name
	accounts map[string]int64
	// touched are the conversations messages went into, with their kind
	touched map[string]string
	// titles of the old conversations and of the groups
	titles map[string]string
	report LegacyReport
}

func (x *legacyImport) run() error {
	if err := x.loadAccounts(); err != nil {
		return err
	}
	rows, err := x.tx.Query(x.ctx, `SELECT id, conversation_id, from_id, from_name, from_kind FROM chat_messages
		WHERE conversation_id IN ('', 'operator', 'panel', 'default') OR conversation_id LIKE 'operator:%' OR conversation_id LIKE 'group:%'
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("chat: read the history of version 1: %w", err)
	}
	var list []legacyMessage
	for rows.Next() {
		var m legacyMessage
		var fromName, kind sql.NullString
		if err := rows.Scan(&m.id, &m.conv, &m.fromID, &fromName, &kind); err != nil {
			rows.Close()
			return fmt.Errorf("chat: read the history of version 1: %w", err)
		}
		m.fromName, m.kind = fromName.String, kind.String
		list = append(list, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	groups, err := x.groups()
	if err != nil {
		return err
	}
	for _, m := range list {
		if err := x.place(m, groups); err != nil {
			return err
		}
	}
	return x.finish(groups)
}

func (x *legacyImport) loadAccounts() error {
	rows, err := x.tx.Query(x.ctx, `SELECT id, username FROM users`)
	if err != nil {
		return fmt.Errorf("chat: read the accounts: %w", err)
	}
	defer rows.Close()
	x.accounts = map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		x.accounts[name] = id
	}
	return rows.Err()
}

// account is the account of a name of version 1 ("anna" or
// "operator:anna"), 0 when there is none.
func (x *legacyImport) account(name string) int64 {
	return x.accounts[strings.TrimPrefix(strings.TrimSpace(name), "operator:")]
}

// legacyGroup is a group of version 1 with its members' accounts.
type legacyGroup struct {
	name      string
	createdBy int64
	members   []int64
}

// groups are the groups of version 1 by id; none when the database has no
// table of them.
func (x *legacyImport) groups() (map[string]legacyGroup, error) {
	out := map[string]legacyGroup{}
	var n int
	if err := x.tx.QueryRow(x.ctx, x.tx.Pick(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'chat_groups'`,
		`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'chat_groups'`)).Scan(&n); err != nil {
		return nil, fmt.Errorf("chat: look for the groups of version 1: %w", err)
	}
	if n == 0 {
		return out, nil
	}
	rows, err := x.tx.Query(x.ctx, `SELECT id, name, members, created_by FROM chat_groups`)
	if err != nil {
		return nil, fmt.Errorf("chat: read the groups of version 1: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var name, members, createdBy sql.NullString
		if err := rows.Scan(&id, &name, &members, &createdBy); err != nil {
			return nil, err
		}
		g := legacyGroup{name: strings.TrimSpace(name.String), createdBy: x.account(createdBy.String)}
		for _, m := range strings.Split(members.String, ",") {
			if a := x.account(m); a != 0 && !slices.Contains(g.members, a) {
				g.members = append(g.members, a)
			}
		}
		slices.Sort(g.members)
		out[id] = g
	}
	return out, rows.Err()
}

// sender is the account that wrote a message of version 1, 0 for a
// computer, the system or a name without an account.
func (x *legacyImport) sender(m legacyMessage) int64 {
	if m.kind != "" && m.kind != KindUser {
		return 0
	}
	switch {
	case strings.HasPrefix(m.fromID, "operator:"):
		return x.account(m.fromID)
	case m.fromID == "operator" || m.fromID == "panel":
		return x.account(m.fromName)
	}
	return 0
}

// fromDevice reports whether a computer wrote the message.
func fromDevice(m legacyMessage) bool {
	return partyOf(m.kind, m.fromID, m.fromName, 0).Kind == KindDevice
}

// place moves one message where it belongs.
func (x *legacyImport) place(m legacyMessage, groups map[string]legacyGroup) error {
	sender := x.sender(m)
	switch {
	case strings.HasPrefix(m.conv, "group:"):
		g, ok := groups[m.conv]
		if !ok {
			// A group of version 2 has members; one of version 1 that is
			// gone has none and goes to the old messages
			var n int
			if err := x.tx.QueryRow(x.ctx, `SELECT COUNT(*) FROM chat_members WHERE conversation_id = ?`, m.conv).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return nil
			}
			return x.toOld(m, m.conv, sender)
		}
		if len(g.members) < 2 {
			name := g.name
			if name == "" {
				name = m.conv
			}
			return x.toOld(m, name, sender)
		}
		x.report.Groups++
		x.titles[m.conv] = g.name
		return x.move(m, m.conv, ConvGroup, sender)
	case strings.HasPrefix(m.conv, "operator:"):
		recipient := x.account(m.conv)
		if sender != 0 && recipient != 0 && sender != recipient {
			x.report.Direct++
			return x.move(m, DirectID(sender, recipient), ConvDirect, sender)
		}
		return x.toOld(m, strings.TrimPrefix(m.conv, "operator:"), sender)
	}
	// "operator", "panel", "default" or none: a computer's message is its
	// conversation's (L493)
	if fromDevice(m) && KindOf(m.fromID) == ConvSupport {
		x.report.Devices++
		return x.move(m, m.fromID, ConvSupport, 0)
	}
	title := m.conv
	if title == "" {
		title = "none"
	}
	return x.toOld(m, title, sender)
}

func (x *legacyImport) toOld(m legacyMessage, title string, sender int64) error {
	x.report.Old++
	id := "old:" + m.conv
	x.titles[id] = title
	return x.move(m, id, ConvOld, sender)
}

// move puts a message into a conversation, naming its account when an
// account wrote it.
func (x *legacyImport) move(m legacyMessage, conv, kind string, sender int64) error {
	x.touched[conv] = kind
	q := `UPDATE chat_messages SET conversation_id = ? WHERE id = ?`
	args := []any{conv, m.id}
	if sender != 0 {
		q = `UPDATE chat_messages SET conversation_id = ?, from_kind = ?, from_user_id = ? WHERE id = ?`
		args = []any{conv, KindUser, sender, m.id}
	}
	if _, err := x.tx.Exec(x.ctx, q, args...); err != nil {
		return fmt.Errorf("chat: move message %d: %w", m.id, err)
	}
	return nil
}

// finish makes the conversations the messages went into, with their
// newest message, members and titles, and removes the conversations of
// groups that became old messages.
func (x *legacyImport) finish(groups map[string]legacyGroup) error {
	ids := make([]string, 0, len(x.touched))
	for id := range x.touched {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	insert := x.tx.Pick(`INSERT OR IGNORE INTO chat_conversations (id, kind, state, opened_ms, closed_ms) VALUES (?, ?, ?, ?, ?)`,
		`INSERT INTO chat_conversations (id, kind, state, opened_ms, closed_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`)
	member := x.tx.Pick(`INSERT OR IGNORE INTO chat_members (conversation_id, account_id) VALUES (?, ?)`,
		`INSERT INTO chat_members (conversation_id, account_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	for _, id := range ids {
		kind := x.touched[id]
		newest, err := scanMessage(x.tx.QueryRow(x.ctx, messageSelect+` WHERE conversation_id = ? ORDER BY id DESC LIMIT 1`, id))
		if err != nil {
			return fmt.Errorf("chat: newest message of %s: %w", id, err)
		}
		// History: a computer's conversation is made closed, the others open
		state, closed := StateOpen, any(nil)
		if kind == ConvSupport {
			state, closed = StateClosed, newest.CreatedMs
		}
		if _, err := x.tx.Exec(x.ctx, insert, id, kind, state, newest.CreatedMs, closed); err != nil {
			return fmt.Errorf("chat: make conversation %s: %w", id, err)
		}
		if _, err := x.tx.Exec(x.ctx, `UPDATE chat_conversations SET kind = ?, last_message_id = ?, last_message_ms = ?
			WHERE id = ? AND last_message_id <= ?`, kind, newest.ID, newest.CreatedMs, id, newest.ID); err != nil {
			return fmt.Errorf("chat: update conversation %s: %w", id, err)
		}
		var people []int64
		switch kind {
		case ConvDirect:
			a, b, _ := DirectPeople(id)
			people = []int64{a, b}
		case ConvGroup:
			g := groups[id]
			people = g.members
			if _, err := x.tx.Exec(x.ctx, `UPDATE chat_conversations SET title = ?, created_by = ? WHERE id = ? AND title = ''`,
				cutRunes(g.name, 120), nullInt(g.createdBy), id); err != nil {
				return err
			}
		case ConvOld:
			if _, err := x.tx.Exec(x.ctx, `UPDATE chat_conversations SET title = ? WHERE id = ?`, cutRunes(x.titles[id], 120), id); err != nil {
				return err
			}
		}
		for _, p := range people {
			if _, err := x.tx.Exec(x.ctx, member, id, p); err != nil {
				return fmt.Errorf("chat: add a member to %s: %w", id, err)
			}
		}
	}
	// A group of version 1 whose messages all became old messages is no
	// conversation of its own any more
	for id, kind := range x.touched {
		if kind != ConvOld || !strings.HasPrefix(id, "old:group:") {
			continue
		}
		gone := strings.TrimPrefix(id, "old:")
		if _, err := x.tx.Exec(x.ctx, `DELETE FROM chat_conversations WHERE id = ? AND NOT EXISTS (SELECT 1 FROM chat_messages WHERE conversation_id = ?)`, gone, gone); err != nil {
			return fmt.Errorf("chat: remove group %s: %w", gone, err)
		}
	}
	return nil
}
