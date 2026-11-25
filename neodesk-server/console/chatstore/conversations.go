package chatstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The kinds of a conversation.
const (
	// ConvSupport is a device's conversation with the console: its id is
	// the device id.
	ConvSupport = "support"
	// ConvDirect is between two console accounts: "dm:<low id>:<high id>".
	ConvDirect = "dm"
	// ConvGroup is a group of console accounts: "group:<id>".
	ConvGroup = "group"
	// ConvLegacy is a conversation of version 1 that is no device's:
	// "operator", "operator:<name>", "panel". The inbox doesn't list them.
	ConvLegacy = "legacy"
)

// The states of a conversation.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

// Settings keys of the store's own.
const (
	keyReadBaseline = "read_baseline"
	keyCaughtUp     = "caught_up"
)

// KindOf is the kind of a conversation by its id.
func KindOf(id string) string {
	switch {
	case strings.HasPrefix(id, "dm:"):
		return ConvDirect
	case strings.HasPrefix(id, "group:"):
		return ConvGroup
	case id == "" || id == "operator" || id == "panel" || id == "default" || strings.HasPrefix(id, "operator:"):
		return ConvLegacy
	}
	return ConvSupport
}

// DirectID is the id of the direct conversation of two accounts.
func DirectID(a, b int64) string {
	if a > b {
		a, b = b, a
	}
	return "dm:" + strconv.FormatInt(a, 10) + ":" + strconv.FormatInt(b, 10)
}

// DirectPeople are the two accounts of a direct conversation's id.
func DirectPeople(id string) (int64, int64, bool) {
	rest, ok := strings.CutPrefix(id, "dm:")
	if !ok {
		return 0, 0, false
	}
	a, b, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.ParseInt(a, 10, 64)
	y, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || x <= 0 || y <= 0 {
		return 0, 0, false
	}
	return x, y, true
}

// Conversation is the state of one conversation.
type Conversation struct {
	ID             string
	Kind           string
	State          string
	Title          string
	CreatedBy      int64
	AssigneeID     int64
	OpenedMs       int64
	ClosedMs       int64
	ClosedBy       string
	CloseNote      string
	LastMessageID  int64
	LastMessageMs  int64
	WaitingSinceMs int64
	MailSentMs     int64
	HelpRequestID  int64
	TicketID       int64
}

const convSelect = `SELECT id, kind, state, title, created_by, assignee_id, opened_ms, closed_ms, closed_by, close_note, last_message_id, last_message_ms, waiting_since_ms, mail_sent_ms, help_request_id, ticket_id FROM chat_conversations`

func scanConversation(row interface{ Scan(...any) error }) (Conversation, error) {
	var c Conversation
	var createdBy, assignee, closed, waiting, mailed, help, ticket sql.NullInt64
	err := row.Scan(&c.ID, &c.Kind, &c.State, &c.Title, &createdBy, &assignee, &c.OpenedMs, &closed, &c.ClosedBy, &c.CloseNote,
		&c.LastMessageID, &c.LastMessageMs, &waiting, &mailed, &help, &ticket)
	c.CreatedBy, c.AssigneeID, c.ClosedMs = createdBy.Int64, assignee.Int64, closed.Int64
	c.WaitingSinceMs, c.MailSentMs, c.HelpRequestID, c.TicketID = waiting.Int64, mailed.Int64, help.Int64, ticket.Int64
	return c, err
}

// setting reads a setting of the store, def when there is none.
func (s *Store) setting(ctx context.Context, key string, def int64) (int64, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM chat_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return 0, fmt.Errorf("chat: read setting %s: %w", key, err)
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def, nil
	}
	return n, nil
}

// Setting returns a setting ("" when unset).
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRow(ctx, `SELECT value FROM chat_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("chat: read setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting writes a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	q := s.db.Pick(`INSERT INTO chat_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		`INSERT INTO chat_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	if _, err := s.db.Exec(ctx, q, key, value); err != nil {
		return fmt.Errorf("chat: write setting %s: %w", key, err)
	}
	return nil
}

// CatchUp brings the conversations up to the newest message: messages the
// relay stored and the ones the API server stored for agents (CDAP, the
// device chat route) alike. For a support conversation a message from the
// device starts the wait for an answer and reopens a closed conversation
// without an assignee; one from the console ends the wait. Messages up to
// the read baseline (the history before version 2) only make closed
// conversations.
func (s *Store) CatchUp(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	baseline, err := s.setting(ctx, keyReadBaseline, 0)
	if err != nil {
		return err
	}
	for {
		done, err := s.setting(ctx, keyCaughtUp, 0)
		if err != nil {
			return err
		}
		rows, err := s.db.Query(ctx, messageSelect+` WHERE id > ? ORDER BY id LIMIT 500`, done)
		if err != nil {
			return fmt.Errorf("chat: catch up: %w", err)
		}
		var batch []Message
		for rows.Next() {
			m, err := scanMessage(rows)
			if err != nil {
				rows.Close()
				return fmt.Errorf("chat: catch up: %w", err)
			}
			batch = append(batch, m)
		}
		rows.Close()
		if len(batch) == 0 {
			return nil
		}
		for _, m := range batch {
			if err := s.apply(ctx, m, m.ID <= baseline); err != nil {
				return err
			}
		}
		if err := s.SetSetting(ctx, keyCaughtUp, strconv.FormatInt(batch[len(batch)-1].ID, 10)); err != nil {
			return err
		}
	}
}

// apply is what one message does to its conversation.
func (s *Store) apply(ctx context.Context, m Message, old bool) error {
	kind := KindOf(m.ConversationID)
	if kind == ConvLegacy {
		return nil
	}
	c, err := s.ensure(ctx, m.ConversationID, kind, m.CreatedMs, old)
	if err != nil {
		return err
	}
	c.LastMessageID, c.LastMessageMs = m.ID, m.CreatedMs
	if kind == ConvSupport && !old {
		switch m.From.Kind {
		case KindDevice:
			if c.State == StateClosed {
				c.State, c.AssigneeID, c.ClosedMs, c.ClosedBy, c.CloseNote = StateOpen, 0, 0, "", ""
				c.OpenedMs, c.MailSentMs = m.CreatedMs, 0
			}
			if c.WaitingSinceMs == 0 {
				c.WaitingSinceMs = m.CreatedMs
			}
			if !s.helpRequestOpen(ctx, c.HelpRequestID) {
				if id := s.openHelpRequest(ctx, c.ID); id != 0 {
					c.HelpRequestID = id
				}
			}
		case KindUser:
			c.WaitingSinceMs, c.MailSentMs = 0, 0
		}
	}
	return s.save(ctx, c)
}

// The help requests (the API server's table help_requests) that are still
// open: raised by the device, or picked up by an operator.
const openHelpStates = `status IN ('pending', 'acknowledged')`

// helpRequestOpen reports whether the help request id is still open; 0
// and a failed check are not.
func (s *Store) helpRequestOpen(ctx context.Context, id int64) bool {
	if id == 0 {
		return false
	}
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM help_requests WHERE id = ? AND `+openHelpStates, id).Scan(&n)
	return err == nil && n > 0
}

// openHelpRequest is the newest open help request of a device, 0 when it
// has none. A database without help requests has none.
func (s *Store) openHelpRequest(ctx context.Context, device string) int64 {
	var id int64
	err := s.db.QueryRow(ctx, `SELECT id FROM help_requests WHERE device_id = ? AND `+openHelpStates+` ORDER BY id DESC LIMIT 1`, device).Scan(&id)
	if err != nil {
		return 0
	}
	return id
}

// ensure returns the conversation, made when it is missing: open, or
// closed for history from before version 2.
func (s *Store) ensure(ctx context.Context, id, kind string, at int64, old bool) (Conversation, error) {
	c, err := scanConversation(s.db.QueryRow(ctx, convSelect+` WHERE id = ?`, id))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("chat: read conversation %s: %w", id, err)
	}
	c = Conversation{ID: id, Kind: kind, State: StateOpen, OpenedMs: at}
	if old && kind == ConvSupport {
		c.State, c.ClosedMs = StateClosed, at
	}
	insert := s.db.Pick(`INSERT OR IGNORE INTO chat_conversations (id, kind, state, opened_ms, closed_ms) VALUES (?, ?, ?, ?, ?)`,
		`INSERT INTO chat_conversations (id, kind, state, opened_ms, closed_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`)
	if _, err := s.db.Exec(ctx, insert, id, kind, c.State, at, nullInt(c.ClosedMs)); err != nil {
		return c, fmt.Errorf("chat: make conversation %s: %w", id, err)
	}
	if kind == ConvDirect {
		if a, b, ok := DirectPeople(id); ok {
			if err := s.addMembers(ctx, id, []int64{a, b}); err != nil {
				return c, err
			}
		}
	}
	return scanConversation(s.db.QueryRow(ctx, convSelect+` WHERE id = ?`, id))
}

// save writes a conversation's state.
func (s *Store) save(ctx context.Context, c Conversation) error {
	_, err := s.db.Exec(ctx, `UPDATE chat_conversations SET state = ?, title = ?, assignee_id = ?, opened_ms = ?, closed_ms = ?, closed_by = ?, close_note = ?,
		last_message_id = ?, last_message_ms = ?, waiting_since_ms = ?, mail_sent_ms = ?, help_request_id = ?, ticket_id = ? WHERE id = ?`,
		c.State, c.Title, nullInt(c.AssigneeID), c.OpenedMs, nullInt(c.ClosedMs), c.ClosedBy, c.CloseNote,
		c.LastMessageID, c.LastMessageMs, nullInt(c.WaitingSinceMs), nullInt(c.MailSentMs), nullInt(c.HelpRequestID), nullInt(c.TicketID), c.ID)
	if err != nil {
		return fmt.Errorf("chat: save conversation %s: %w", c.ID, err)
	}
	return nil
}

// Conversation returns a conversation's state, made when it has none yet
// (a device nobody wrote with is an open support conversation without
// messages). A legacy id has none: ErrNotFound.
func (s *Store) Conversation(ctx context.Context, id string) (Conversation, error) {
	if err := s.CatchUp(ctx); err != nil {
		return Conversation{}, err
	}
	kind := KindOf(id)
	if kind == ConvLegacy {
		return Conversation{}, ErrNotFound
	}
	if kind == ConvDirect {
		if _, _, ok := DirectPeople(id); !ok {
			return Conversation{}, ErrNotFound
		}
	}
	if kind == ConvGroup {
		c, err := scanConversation(s.db.QueryRow(ctx, convSelect+` WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return c, ErrNotFound
		}
		return c, err
	}
	return s.ensure(ctx, id, kind, s.nowMs(), false)
}

// Filter narrows List.
type Filter struct {
	// Kinds are the kinds to list; none lists every kind
	Kinds []string
	// State is open or closed; "" lists both
	State string
	// Assignee lists the conversations assigned to the account, -1 the
	// unassigned ones, 0 any
	Assignee int64
	// Member lists the direct and group conversations of the account
	// (0: of anyone)
	Member int64
	// WithMessages leaves out conversations without a message
	WithMessages bool
	// Limit is the most conversations listed (default 500)
	Limit int
}

// List returns conversations, the latest message first.
func (s *Store) List(ctx context.Context, f Filter) ([]Conversation, error) {
	if err := s.CatchUp(ctx); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if len(f.Kinds) > 0 {
		marks := make([]string, len(f.Kinds))
		for i, k := range f.Kinds {
			marks[i] = "?"
			args = append(args, k)
		}
		where = append(where, "kind IN ("+strings.Join(marks, ",")+")")
	}
	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, f.State)
	}
	switch {
	case f.Assignee > 0:
		where = append(where, "assignee_id = ?")
		args = append(args, f.Assignee)
	case f.Assignee < 0:
		where = append(where, "assignee_id IS NULL")
	}
	if f.Member > 0 {
		where = append(where, "(kind = 'support' OR id IN (SELECT conversation_id FROM chat_members WHERE account_id = ?))")
		args = append(args, f.Member)
	}
	if f.WithMessages {
		where = append(where, "last_message_id > 0")
	}
	query := convSelect
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	query += " ORDER BY last_message_ms DESC, id LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chat: list conversations: %w", err)
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, fmt.Errorf("chat: list conversations: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Assign gives a support conversation to an account (0: to nobody) and
// returns it. A closed one stays closed.
func (s *Store) Assign(ctx context.Context, id string, account int64) (Conversation, error) {
	c, err := s.supportConv(ctx, id)
	if err != nil {
		return c, err
	}
	c.AssigneeID = account
	return c, s.save(ctx, c)
}

// Close closes a support conversation with an optional note.
func (s *Store) Close(ctx context.Context, id, by, note string) (Conversation, error) {
	c, err := s.supportConv(ctx, id)
	if err != nil {
		return c, err
	}
	c.State, c.ClosedMs, c.ClosedBy, c.CloseNote = StateClosed, s.nowMs(), by, cutRunes(strings.TrimSpace(note), 1000)
	c.WaitingSinceMs, c.MailSentMs = 0, 0
	return c, s.save(ctx, c)
}

// Reopen opens a closed support conversation again, keeping its
// assignee.
func (s *Store) Reopen(ctx context.Context, id string) (Conversation, error) {
	c, err := s.supportConv(ctx, id)
	if err != nil {
		return c, err
	}
	if c.State == StateOpen {
		return c, nil
	}
	c.State, c.OpenedMs, c.ClosedMs, c.ClosedBy, c.CloseNote = StateOpen, s.nowMs(), 0, "", ""
	return c, s.save(ctx, c)
}

// Link notes the help request and the ticket of a support conversation
// (0 leaves one as it is).
func (s *Store) Link(ctx context.Context, id string, helpRequest, ticket int64) (Conversation, error) {
	c, err := s.supportConv(ctx, id)
	if err != nil {
		return c, err
	}
	if helpRequest != 0 {
		c.HelpRequestID = helpRequest
	}
	if ticket != 0 {
		c.TicketID = ticket
	}
	return c, s.save(ctx, c)
}

// MarkMailed notes that the no-answer mail of a conversation went out.
func (s *Store) MarkMailed(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `UPDATE chat_conversations SET mail_sent_ms = ? WHERE id = ?`, s.nowMs(), id)
	return err
}

// Unanswered are the open support conversations whose person has waited
// since before cutoff (ms) and that had no mail for that wait.
func (s *Store) Unanswered(ctx context.Context, cutoff int64) ([]Conversation, error) {
	if err := s.CatchUp(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, convSelect+` WHERE kind = 'support' AND state = 'open' AND waiting_since_ms IS NOT NULL AND waiting_since_ms <= ? AND mail_sent_ms IS NULL`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("chat: unanswered: %w", err)
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) supportConv(ctx context.Context, id string) (Conversation, error) {
	if KindOf(id) != ConvSupport {
		return Conversation{}, ErrNotFound
	}
	return s.Conversation(ctx, id)
}

// Direct returns the direct conversation of two accounts, made when
// missing.
func (s *Store) Direct(ctx context.Context, a, b int64) (Conversation, error) {
	if a <= 0 || b <= 0 || a == b {
		return Conversation{}, ErrNotFound
	}
	return s.ensure(ctx, DirectID(a, b), ConvDirect, s.nowMs(), false)
}

// NewGroup makes a group of accounts with a title; the creator is a
// member.
func (s *Store) NewGroup(ctx context.Context, title string, creator int64, members []int64) (Conversation, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return Conversation{}, err
	}
	id := "group:" + hex.EncodeToString(b)
	title = cutRunes(strings.TrimSpace(title), 120)
	if _, err := s.db.Exec(ctx, `INSERT INTO chat_conversations (id, kind, state, title, created_by, opened_ms) VALUES (?, 'group', 'open', ?, ?, ?)`,
		id, title, creator, s.nowMs()); err != nil {
		return Conversation{}, fmt.Errorf("chat: make a group: %w", err)
	}
	if !slices.Contains(members, creator) {
		members = append(members, creator)
	}
	if err := s.addMembers(ctx, id, members); err != nil {
		return Conversation{}, err
	}
	return s.Conversation(ctx, id)
}

// SetGroup changes a group's title and members.
func (s *Store) SetGroup(ctx context.Context, id, title string, members []int64) (Conversation, error) {
	c, err := s.Conversation(ctx, id)
	if err != nil {
		return c, err
	}
	if c.Kind != ConvGroup {
		return c, ErrNotFound
	}
	if t := cutRunes(strings.TrimSpace(title), 120); t != "" {
		c.Title = t
	}
	if err := s.save(ctx, c); err != nil {
		return c, err
	}
	if members != nil {
		if _, err := s.db.Exec(ctx, `DELETE FROM chat_members WHERE conversation_id = ?`, id); err != nil {
			return c, err
		}
		if err := s.addMembers(ctx, id, members); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (s *Store) addMembers(ctx context.Context, id string, members []int64) error {
	insert := s.db.Pick(`INSERT OR IGNORE INTO chat_members (conversation_id, account_id) VALUES (?, ?)`,
		`INSERT INTO chat_members (conversation_id, account_id) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	for _, m := range members {
		if m <= 0 {
			continue
		}
		if _, err := s.db.Exec(ctx, insert, id, m); err != nil {
			return fmt.Errorf("chat: add a member to %s: %w", id, err)
		}
	}
	return nil
}

// Members are the accounts of a direct or group conversation.
func (s *Store) Members(ctx context.Context, id string) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT account_id FROM chat_members WHERE conversation_id = ? ORDER BY account_id`, id)
	if err != nil {
		return nil, fmt.Errorf("chat: members of %s: %w", id, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IsMember reports whether an account is in a direct or group
// conversation.
func (s *Store) IsMember(ctx context.Context, id string, account int64) (bool, error) {
	if a, b, ok := DirectPeople(id); ok {
		return account == a || account == b, nil
	}
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM chat_members WHERE conversation_id = ? AND account_id = ?`, id, account).Scan(&n)
	return n > 0, err
}
