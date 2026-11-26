// Package chatstore keeps the console's chat: the messages (the table
// chat_messages the API server made and still writes for agents), and
// what the chat of version 2 adds to them: conversations with a state and
// an assignee, the members of direct messages, groups and teams (and the
// user groups a team includes), read marks per account, the trail of
// edits and deletions, reactions, canned replies and the chat's settings.
//
// The console and the API server share one database, so the store works
// on the console's handle (store.DB). It only adds: new tables, and new
// columns on chat_messages that the API server's queries never name, so
// its answers (the history the version 1 sockets get) keep their shape.
//
// Times are milliseconds since 1970 (UTC) in BIGINT columns. Rows the API
// server wrote have no created_ms; their created_at is read instead.
package chatstore

import (
	"context"
	"fmt"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// tables are the chat's own tables, for each database.
var tables = []struct{ name, sqlite, postgres string }{
	{"chat_messages",
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			from_id TEXT NOT NULL,
			from_name TEXT DEFAULT '',
			to_id TEXT DEFAULT '',
			text TEXT NOT NULL,
			read INTEGER DEFAULT 0,
			created_at TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS chat_messages (
			id BIGSERIAL PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			from_id TEXT NOT NULL,
			from_name TEXT NOT NULL DEFAULT '',
			to_id TEXT NOT NULL DEFAULT '',
			text TEXT NOT NULL,
			read BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`},
	{"chat_conversations",
		`CREATE TABLE IF NOT EXISTS chat_conversations (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'open',
			title TEXT NOT NULL DEFAULT '',
			created_by INTEGER,
			assignee_id INTEGER,
			opened_ms INTEGER NOT NULL DEFAULT 0,
			closed_ms INTEGER,
			closed_by TEXT NOT NULL DEFAULT '',
			close_note TEXT NOT NULL DEFAULT '',
			last_message_id INTEGER NOT NULL DEFAULT 0,
			last_message_ms INTEGER NOT NULL DEFAULT 0,
			waiting_since_ms INTEGER,
			mail_sent_ms INTEGER,
			help_request_id INTEGER,
			ticket_id INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS chat_conversations (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'open',
			title TEXT NOT NULL DEFAULT '',
			created_by BIGINT,
			assignee_id BIGINT,
			opened_ms BIGINT NOT NULL DEFAULT 0,
			closed_ms BIGINT,
			closed_by TEXT NOT NULL DEFAULT '',
			close_note TEXT NOT NULL DEFAULT '',
			last_message_id BIGINT NOT NULL DEFAULT 0,
			last_message_ms BIGINT NOT NULL DEFAULT 0,
			waiting_since_ms BIGINT,
			mail_sent_ms BIGINT,
			help_request_id BIGINT,
			ticket_id BIGINT
		)`},
	{"chat_members",
		`CREATE TABLE IF NOT EXISTS chat_members (
			conversation_id TEXT NOT NULL,
			account_id INTEGER NOT NULL,
			PRIMARY KEY (conversation_id, account_id)
		)`,
		`CREATE TABLE IF NOT EXISTS chat_members (
			conversation_id TEXT NOT NULL,
			account_id BIGINT NOT NULL,
			PRIMARY KEY (conversation_id, account_id)
		)`},
	{"chat_team_groups",
		`CREATE TABLE IF NOT EXISTS chat_team_groups (
			conversation_id TEXT NOT NULL,
			group_guid TEXT NOT NULL,
			PRIMARY KEY (conversation_id, group_guid)
		)`,
		`CREATE TABLE IF NOT EXISTS chat_team_groups (
			conversation_id TEXT NOT NULL,
			group_guid TEXT NOT NULL,
			PRIMARY KEY (conversation_id, group_guid)
		)`},
	{"chat_reads",
		`CREATE TABLE IF NOT EXISTS chat_reads (
			conversation_id TEXT NOT NULL,
			reader TEXT NOT NULL,
			last_read_id INTEGER NOT NULL DEFAULT 0,
			read_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (conversation_id, reader)
		)`,
		`CREATE TABLE IF NOT EXISTS chat_reads (
			conversation_id TEXT NOT NULL,
			reader TEXT NOT NULL,
			last_read_id BIGINT NOT NULL DEFAULT 0,
			read_ms BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (conversation_id, reader)
		)`},
	{"chat_message_edits",
		`CREATE TABLE IF NOT EXISTS chat_message_edits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id INTEGER NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			actor_name TEXT NOT NULL DEFAULT '',
			old_text TEXT NOT NULL DEFAULT '',
			old_attachments TEXT NOT NULL DEFAULT '',
			at_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chat_message_edits (
			id BIGSERIAL PRIMARY KEY,
			message_id BIGINT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL,
			actor_name TEXT NOT NULL DEFAULT '',
			old_text TEXT NOT NULL DEFAULT '',
			old_attachments TEXT NOT NULL DEFAULT '',
			at_ms BIGINT NOT NULL
		)`},
	{"chat_reactions",
		`CREATE TABLE IF NOT EXISTS chat_reactions (
			message_id INTEGER NOT NULL,
			who TEXT NOT NULL,
			who_name TEXT NOT NULL DEFAULT '',
			emoji TEXT NOT NULL,
			at_ms INTEGER NOT NULL,
			PRIMARY KEY (message_id, who, emoji)
		)`,
		`CREATE TABLE IF NOT EXISTS chat_reactions (
			message_id BIGINT NOT NULL,
			who TEXT NOT NULL,
			who_name TEXT NOT NULL DEFAULT '',
			emoji TEXT NOT NULL,
			at_ms BIGINT NOT NULL,
			PRIMARY KEY (message_id, who, emoji)
		)`},
	{"chat_canned",
		`CREATE TABLE IF NOT EXISTS chat_canned (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			owner_id INTEGER,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			created_by INTEGER NOT NULL DEFAULT 0,
			updated_ms INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chat_canned (
			id BIGSERIAL PRIMARY KEY,
			owner_id BIGINT,
			title TEXT NOT NULL,
			body TEXT NOT NULL,
			created_by BIGINT NOT NULL DEFAULT 0,
			updated_ms BIGINT NOT NULL
		)`},
	{"chat_settings",
		`CREATE TABLE IF NOT EXISTS chat_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS chat_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`},
}

// Tables are the names of the chat's tables, chat_messages and
// chat_groups (the API server's) not among them: the backup's list.
var Tables = []string{"chat_conversations", "chat_members", "chat_team_groups", "chat_reads", "chat_message_edits", "chat_reactions", "chat_canned", "chat_settings"}

// messageColumns are the columns version 2 adds to chat_messages.
var messageColumns = []struct{ name, sqlite, postgres string }{
	{"from_kind", "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ''"},
	{"from_user_id", "INTEGER", "BIGINT"},
	{"reply_to", "INTEGER", "BIGINT"},
	{"attachments", "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ''"},
	{"created_ms", "INTEGER", "BIGINT"},
	{"edited_ms", "INTEGER", "BIGINT"},
	{"deleted_ms", "INTEGER", "BIGINT"},
	{"deleted_by", "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ''"},
	{"client_id", "TEXT NOT NULL DEFAULT ''", "TEXT NOT NULL DEFAULT ''"},
}

var indexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_chat_messages_conv_id ON chat_messages(conversation_id, id)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_conversations_state ON chat_conversations(kind, state, last_message_ms)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_members_account ON chat_members(account_id)`,
	`CREATE INDEX IF NOT EXISTS idx_chat_message_edits_message ON chat_message_edits(message_id)`,
}

// Ensure creates the chat's tables and columns that are missing. Running
// it again changes nothing. The first run notes the newest message as the
// read baseline: what came before it counts as read for every account, so
// an upgrade doesn't mark years of history unread.
func Ensure(ctx context.Context, db *store.DB) error {
	for _, t := range tables {
		if _, err := db.Exec(ctx, db.Pick(t.sqlite, t.postgres)); err != nil {
			return fmt.Errorf("chat: create %s: %w", t.name, err)
		}
	}
	for _, c := range messageColumns {
		if err := addColumn(ctx, db, "chat_messages", c.name, db.Pick(c.sqlite, c.postgres)); err != nil {
			return err
		}
	}
	for _, q := range indexes {
		if _, err := db.Exec(ctx, q); err != nil {
			return fmt.Errorf("chat: index: %w", err)
		}
	}
	var newest int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM chat_messages`).Scan(&newest); err != nil {
		return fmt.Errorf("chat: newest message: %w", err)
	}
	insert := db.Pick(`INSERT OR IGNORE INTO chat_settings (key, value) VALUES (?, ?)`,
		`INSERT INTO chat_settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`)
	if _, err := db.Exec(ctx, insert, keyReadBaseline, strconv.FormatInt(newest, 10)); err != nil {
		return fmt.Errorf("chat: read baseline: %w", err)
	}
	return nil
}

// addColumn adds a column to a table when it is missing.
func addColumn(ctx context.Context, db *store.DB, table, column, def string) error {
	if db.IsPostgres() {
		if _, err := db.Exec(ctx, `ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS `+column+` `+def); err != nil {
			return fmt.Errorf("chat: add %s.%s: %w", table, column, err)
		}
		return nil
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM pragma_table_info('`+table+`') WHERE name = ?`, column).Scan(&n); err != nil {
		return fmt.Errorf("chat: look for %s.%s: %w", table, column, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.Exec(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+def); err != nil {
		return fmt.Errorf("chat: add %s.%s: %w", table, column, err)
	}
	return nil
}
