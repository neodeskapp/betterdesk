package chatstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The kinds of a message's sender.
const (
	KindDevice = "device"
	KindUser   = "user"
	KindSystem = "system"
)

// Limits of a message.
const (
	// MaxText is the most characters a message's text keeps (the API
	// server's limit).
	MaxText = 4096
	// MaxAttachments is the most files one message carries.
	MaxAttachments = 10
	// EditWindow is how long a sender may edit a message (A242 g).
	EditWindow = 24 * time.Hour
	// maxEmoji is the most bytes of a reaction.
	maxEmoji = 32
)

// Errors of the store.
var (
	ErrNotFound   = errors.New("chat: no such message")
	ErrNotAllowed = errors.New("chat: not your message")
	ErrTooLate    = errors.New("chat: the time to edit has passed")
	ErrDeleted    = errors.New("chat: the message was deleted")
	ErrEmpty      = errors.New("chat: a message needs text or a file")
)

// Party is who wrote, read or reacted: a device by its id, a console
// account by its id (as text), or the system.
type Party struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// User is the console account with the id and name.
func User(id int64, name string) Party {
	return Party{Kind: KindUser, ID: strconv.FormatInt(id, 10), Name: name}
}

// Device is the device with the id and name.
func Device(id, name string) Party { return Party{Kind: KindDevice, ID: id, Name: name} }

// System is the console itself, for the lines it adds to a conversation.
func System() Party { return Party{Kind: KindSystem, Name: ""} }

// Key is the party as one text: "u:7", "d:900100001", "s:".
func (p Party) Key() string { return p.Kind[:1] + ":" + p.ID }

// UserID is the account id of a user, 0 for anyone else.
func (p Party) UserID() int64 {
	if p.Kind != KindUser {
		return 0
	}
	n, _ := strconv.ParseInt(p.ID, 10, 64)
	return n
}

// Attachment is a file a message carries: a chat file (routes/chat) by
// its 32 hex digit id.
type Attachment struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// Message is one message of a conversation.
type Message struct {
	ID             int64
	ConversationID string
	From           Party
	Text           string
	ReplyTo        int64
	Attachments    []Attachment
	CreatedMs      int64
	EditedMs       int64
	DeletedMs      int64
	DeletedBy      string
	ClientID       string
	// Reactions are who reacted, by emoji, in the order they did
	Reactions map[string][]Party
}

// Deleted reports whether the message was deleted.
func (m *Message) Deleted() bool { return m.DeletedMs != 0 }

// Store is the chat's data on the console's database.
type Store struct {
	db  *store.DB
	now func() time.Time
	// mu keeps one catch-up at a time
	mu sync.Mutex
}

// New returns the store. now is time.Now when nil.
func New(db *store.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

// DB is the store's database.
func (s *Store) DB() *store.DB { return s.db }

func (s *Store) nowMs() int64 { return s.now().UnixMilli() }

// InsertFunc writes a new row of chat_messages with the columns the API
// server knows and returns its id. The relay passes one that goes through
// the API server for a message to a device, so an agent connected over
// CDAP gets it as before.
type InsertFunc func(ctx context.Context, conversation, fromID, fromName, toID, text string) (int64, error)

// Post stores a message and returns it as stored. The text is cut to
// MaxText characters; a message without text and files is refused.
// insert nil writes the row directly.
func (s *Store) Post(ctx context.Context, m Message, insert InsertFunc) (Message, error) {
	m.Text = cutRunes(strings.TrimRight(m.Text, " \t\r\n"), MaxText)
	if len(m.Attachments) > MaxAttachments {
		m.Attachments = m.Attachments[:MaxAttachments]
	}
	if strings.TrimSpace(m.Text) == "" && len(m.Attachments) == 0 {
		return Message{}, ErrEmpty
	}
	if m.ReplyTo != 0 {
		// A reply names a message of the same conversation, else none
		var conv string
		err := s.db.QueryRow(ctx, `SELECT conversation_id FROM chat_messages WHERE id = ?`, m.ReplyTo).Scan(&conv)
		if err != nil || conv != m.ConversationID {
			m.ReplyTo = 0
		}
	}
	m.CreatedMs = s.nowMs()
	fromID, fromName, toID := legacyColumns(m)
	text := m.Text
	if text == "" {
		// The API server refuses a row without text: a file-only message
		// goes in with its first file's name, which the update below
		// clears again
		text = m.Attachments[0].Name
	}
	var id int64
	var err error
	if insert != nil {
		id, err = insert(ctx, m.ConversationID, fromID, fromName, toID, text)
	} else {
		id, err = s.db.Insert(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, to_id, text) VALUES (?, ?, ?, ?, ?)`,
			m.ConversationID, fromID, fromName, toID, text)
	}
	if err != nil {
		return Message{}, fmt.Errorf("chat: store a message: %w", err)
	}
	var attachments string
	if len(m.Attachments) > 0 {
		b, _ := json.Marshal(m.Attachments)
		attachments = string(b)
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_messages SET text = ?, from_kind = ?, from_user_id = ?, reply_to = ?, attachments = ?, created_ms = ?, client_id = ? WHERE id = ?`,
		m.Text, m.From.Kind, nullInt(m.From.UserID()), nullInt(m.ReplyTo), attachments, m.CreatedMs, cutRunes(m.ClientID, 64), id); err != nil {
		return Message{}, fmt.Errorf("chat: store a message's details: %w", err)
	}
	m.ID = id
	if err := s.CatchUp(ctx); err != nil {
		return Message{}, err
	}
	return m, nil
}

// legacyColumns are the API server's columns of a message: a console user
// writes as "operator" under the user's name (as the relay of version 1
// did), a device as its id, to the device of a support conversation.
func legacyColumns(m Message) (fromID, fromName, toID string) {
	switch m.From.Kind {
	case KindUser:
		fromID, fromName = "operator", m.From.Name
		if KindOf(m.ConversationID) == ConvSupport {
			toID = m.ConversationID
		}
	case KindDevice:
		fromID, fromName = m.From.ID, m.From.Name
	default:
		fromID, fromName = "system", ""
	}
	return fromID, fromName, toID
}

// messageSelect reads the columns scanMessage takes.
const messageSelect = `SELECT id, conversation_id, from_id, from_name, text, created_at, from_kind, from_user_id, reply_to, attachments, created_ms, edited_ms, deleted_ms, deleted_by, client_id FROM chat_messages`

func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var fromID, fromName, kind, attachments, deletedBy, clientID sql.NullString
	var createdAt any
	var userID, replyTo, createdMs, editedMs, deletedMs sql.NullInt64
	if err := row.Scan(&m.ID, &m.ConversationID, &fromID, &fromName, &m.Text, &createdAt, &kind, &userID, &replyTo,
		&attachments, &createdMs, &editedMs, &deletedMs, &deletedBy, &clientID); err != nil {
		return m, err
	}
	m.From = partyOf(kind.String, fromID.String, fromName.String, userID.Int64)
	m.ReplyTo = replyTo.Int64
	if attachments.String != "" {
		_ = json.Unmarshal([]byte(attachments.String), &m.Attachments)
	}
	m.CreatedMs = createdMs.Int64
	if m.CreatedMs == 0 {
		m.CreatedMs = legacyTime(createdAt)
	}
	m.EditedMs = editedMs.Int64
	m.DeletedMs = deletedMs.Int64
	m.DeletedBy = deletedBy.String
	m.ClientID = clientID.String
	if m.DeletedMs != 0 {
		m.Text = ""
		m.Attachments = nil
	}
	return m, nil
}

// partyOf is the sender of a row: from its kind when version 2 wrote it,
// else from the API server's columns.
func partyOf(kind, fromID, fromName string, userID int64) Party {
	switch kind {
	case KindUser:
		return Party{Kind: KindUser, ID: strconv.FormatInt(userID, 10), Name: fromName}
	case KindDevice:
		return Device(fromID, fromName)
	case KindSystem:
		return System()
	}
	switch {
	case fromID == "operator" || fromID == "panel":
		return Party{Kind: KindUser, Name: fromName}
	case strings.HasPrefix(fromID, "operator:"):
		return Party{Kind: KindUser, Name: strings.TrimPrefix(fromID, "operator:")}
	case fromID == "system":
		return System()
	}
	if fromName == "" {
		fromName = fromID
	}
	return Device(fromID, fromName)
}

// legacyTime reads created_at: TEXT in UTC on SQLite, a time on
// PostgreSQL.
func legacyTime(v any) int64 {
	switch t := v.(type) {
	case time.Time:
		return t.UnixMilli()
	case string:
		if parsed, ok := store.ParseTime(t); ok {
			return parsed.UnixMilli()
		}
	case []byte:
		if parsed, ok := store.ParseTime(string(t)); ok {
			return parsed.UnixMilli()
		}
	}
	return 0
}

// Get returns a message, ErrNotFound when there is none.
func (s *Store) Get(ctx context.Context, id int64) (Message, error) {
	m, err := scanMessage(s.db.QueryRow(ctx, messageSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, fmt.Errorf("chat: read message %d: %w", id, err)
	}
	if err := s.loadReactions(ctx, []*Message{&m}); err != nil {
		return m, err
	}
	return m, nil
}

// History returns up to limit messages of a conversation before the
// message before (0: the newest), oldest first, with their reactions.
func (s *Store) History(ctx context.Context, conversation string, before int64, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := messageSelect + ` WHERE conversation_id = ?`
	args := []any{conversation}
	if before > 0 {
		query += ` AND id < ?`
		args = append(args, before)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("chat: read history: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("chat: read history: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	ptrs := make([]*Message, len(out))
	for i := range out {
		ptrs[i] = &out[i]
	}
	if err := s.loadReactions(ctx, ptrs); err != nil {
		return nil, err
	}
	return out, nil
}

// loadReactions fills the messages' reactions.
func (s *Store) loadReactions(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := map[int64]*Message{}
	marks := make([]string, 0, len(msgs))
	args := make([]any, 0, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
		marks = append(marks, "?")
		args = append(args, m.ID)
	}
	rows, err := s.db.Query(ctx, `SELECT message_id, who, who_name, emoji FROM chat_reactions WHERE message_id IN (`+strings.Join(marks, ",")+`) ORDER BY at_ms, who`, args...)
	if err != nil {
		return fmt.Errorf("chat: read reactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var who, name, emoji string
		if err := rows.Scan(&id, &who, &name, &emoji); err != nil {
			return fmt.Errorf("chat: read reactions: %w", err)
		}
		m := byID[id]
		if m == nil || m.Deleted() {
			continue
		}
		if m.Reactions == nil {
			m.Reactions = map[string][]Party{}
		}
		m.Reactions[emoji] = append(m.Reactions[emoji], partyOfKey(who, name))
	}
	return rows.Err()
}

// partyOfKey is the party of a key ("u:7") and name.
func partyOfKey(key, name string) Party {
	kind, id, _ := strings.Cut(key, ":")
	switch kind {
	case "u":
		return Party{Kind: KindUser, ID: id, Name: name}
	case "d":
		return Party{Kind: KindDevice, ID: id, Name: name}
	}
	return System()
}

// sameSender reports whether p wrote m. A user of a row the API server
// wrote is known by name only.
func sameSender(m Message, p Party) bool {
	if m.From.Kind != p.Kind {
		return false
	}
	if m.From.Kind == KindUser && m.From.ID == "" {
		return m.From.Name != "" && m.From.Name == p.Name
	}
	return m.From.ID == p.ID
}

// Edit changes the text of a message its sender wrote less than
// EditWindow ago, and keeps the old text in the trail.
func (s *Store) Edit(ctx context.Context, id int64, actor Party, text string) (Message, error) {
	text = cutRunes(strings.TrimRight(text, " \t\r\n"), MaxText)
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, err
	}
	switch {
	case m.Deleted():
		return m, ErrDeleted
	case !sameSender(m, actor):
		return m, ErrNotAllowed
	case s.now().Sub(time.UnixMilli(m.CreatedMs)) > EditWindow:
		return m, ErrTooLate
	case strings.TrimSpace(text) == "" && len(m.Attachments) == 0:
		return m, ErrEmpty
	case text == m.Text:
		return m, nil
	}
	now := s.nowMs()
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_message_edits (message_id, action, actor, actor_name, old_text, at_ms) VALUES (?, 'edit', ?, ?, ?, ?)`,
			id, actor.Key(), actor.Name, m.Text, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE chat_messages SET text = ?, edited_ms = ? WHERE id = ?`, text, now, id)
		return err
	})
	if err != nil {
		return m, fmt.Errorf("chat: edit message %d: %w", id, err)
	}
	return s.Get(ctx, id)
}

// Delete removes a message's text and files for everyone, leaving a
// deleted mark; its sender may, and an admin (any true). The old text and
// files go into the trail.
func (s *Store) Delete(ctx context.Context, id int64, actor Party, admin bool) (Message, error) {
	var oldText, oldAttachments string
	var deleted sql.NullInt64
	err := s.db.QueryRow(ctx, `SELECT text, attachments, deleted_ms FROM chat_messages WHERE id = ?`, id).Scan(&oldText, &oldAttachments, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("chat: read message %d: %w", id, err)
	}
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, err
	}
	if deleted.Valid {
		return m, ErrDeleted
	}
	if !admin && !sameSender(m, actor) {
		return m, ErrNotAllowed
	}
	now := s.nowMs()
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO chat_message_edits (message_id, action, actor, actor_name, old_text, old_attachments, at_ms) VALUES (?, 'delete', ?, ?, ?, ?, ?)`,
			id, actor.Key(), actor.Name, oldText, oldAttachments, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE chat_messages SET text = '', attachments = '', deleted_ms = ?, deleted_by = ? WHERE id = ?`, now, actor.Name, id)
		return err
	})
	if err != nil {
		return m, fmt.Errorf("chat: delete message %d: %w", id, err)
	}
	return s.Get(ctx, id)
}

// Change is a step of a message's trail: an edit or the deletion.
type Change struct {
	Action         string       `json:"action"`
	Actor          Party        `json:"actor"`
	OldText        string       `json:"old_text"`
	OldAttachments []Attachment `json:"old_attachments,omitempty"`
	AtMs           int64        `json:"at"`
}

// Trail returns the edits and the deletion of a message, oldest first.
func (s *Store) Trail(ctx context.Context, id int64) ([]Change, error) {
	rows, err := s.db.Query(ctx, `SELECT action, actor, actor_name, old_text, old_attachments, at_ms FROM chat_message_edits WHERE message_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("chat: read the trail of %d: %w", id, err)
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var c Change
		var actor, name, attachments string
		if err := rows.Scan(&c.Action, &actor, &name, &c.OldText, &attachments, &c.AtMs); err != nil {
			return nil, err
		}
		c.Actor = partyOfKey(actor, name)
		if attachments != "" {
			_ = json.Unmarshal([]byte(attachments), &c.OldAttachments)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// React adds (on) or takes back a reaction to a message that was not
// deleted, and returns the message.
func (s *Store) React(ctx context.Context, id int64, who Party, emoji string, on bool) (Message, error) {
	emoji = strings.TrimSpace(emoji)
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, err
	}
	if m.Deleted() {
		return m, ErrDeleted
	}
	if emoji == "" || len(emoji) > maxEmoji || !utf8.ValidString(emoji) {
		return m, ErrEmpty
	}
	if on {
		insert := s.db.Pick(`INSERT OR IGNORE INTO chat_reactions (message_id, who, who_name, emoji, at_ms) VALUES (?, ?, ?, ?, ?)`,
			`INSERT INTO chat_reactions (message_id, who, who_name, emoji, at_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`)
		_, err = s.db.Exec(ctx, insert, id, who.Key(), who.Name, emoji, s.nowMs())
	} else {
		_, err = s.db.Exec(ctx, `DELETE FROM chat_reactions WHERE message_id = ? AND who = ? AND emoji = ?`, id, who.Key(), emoji)
	}
	if err != nil {
		return m, fmt.Errorf("chat: react to %d: %w", id, err)
	}
	return s.Get(ctx, id)
}

// cutRunes keeps the first n characters of s.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// nullInt is nil for 0, the value otherwise.
func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// Search narrows Find.
type Search struct {
	// Text is what a message's text has: case does not count for Latin
	// letters, % and _ are text
	Text string
	// Conversation limits the search to one conversation ("" for all)
	Conversation string
	// Before finds messages older than this one (0: from the newest)
	Before int64
	// Limit is the most messages (at most 500, default 200)
	Limit int
}

// Find returns the messages whose text has s.Text, newest first: neither
// deleted ones nor lines of the system. It does not check who may read
// them.
func (s *Store) Find(ctx context.Context, q Search) ([]Message, error) {
	if err := s.CatchUp(ctx); err != nil {
		return nil, err
	}
	where := []string{s.db.Pick(`text LIKE ? ESCAPE '\'`, `text ILIKE ? ESCAPE '\'`), `deleted_ms IS NULL`, `from_kind <> ?`, `from_id <> 'system'`}
	args := []any{"%" + store.EscapeLike(q.Text) + "%", KindSystem}
	if q.Conversation != "" {
		where = append(where, `conversation_id = ?`)
		args = append(args, q.Conversation)
	}
	if q.Before > 0 {
		where = append(where, `id < ?`)
		args = append(args, q.Before)
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit)
	rows, err := s.db.Query(ctx, messageSelect+` WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("chat: search: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("chat: search: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
