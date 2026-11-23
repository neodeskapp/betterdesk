package access

import (
	"context"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The tables this package reads and writes, as the Node console creates
// them (web-nodejs/services/dbAdapter.js, ensureAuthTables for SQLite and
// ensureSchema for PostgreSQL). The package itself never creates a table.

var sqliteSchema = []string{
	`CREATE TABLE IF NOT EXISTS users (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                username TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL,
                role TEXT DEFAULT 'admin',
                auth_provider TEXT DEFAULT 'local',
                created_at TEXT DEFAULT (datetime('now')),
                last_login TEXT,
                preferred_language TEXT DEFAULT NULL,
                totp_secret TEXT DEFAULT NULL,
                totp_enabled INTEGER DEFAULT 0,
                totp_recovery_codes TEXT DEFAULT NULL
            )`,
	`CREATE TABLE IF NOT EXISTS folders (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                name TEXT NOT NULL,
                color TEXT DEFAULT '#6366f1',
                icon TEXT DEFAULT 'folder',
                sort_order INTEGER DEFAULT 0,
                created_at TEXT DEFAULT (datetime('now'))
            )`,
	`CREATE TABLE IF NOT EXISTS settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TEXT DEFAULT (datetime('now'))
            )`,
	`CREATE TABLE IF NOT EXISTS user_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            )`,
	`CREATE TABLE IF NOT EXISTS panel_access_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                resource_key TEXT NOT NULL,
                access_level TEXT NOT NULL DEFAULT 'none',
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, resource_key)
            )`,
	`CREATE INDEX IF NOT EXISTS idx_panel_access_policies_subject
                ON panel_access_policies (subject_type, subject_id)`,
	`CREATE TABLE IF NOT EXISTS client_feature_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                feature_key TEXT NOT NULL,
                enabled INTEGER NOT NULL DEFAULT 0,
                updated_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (subject_type, subject_id, feature_key)
            )`,
	`CREATE INDEX IF NOT EXISTS idx_client_feature_policies_subject
                ON client_feature_policies (subject_type, subject_id)`,
	`CREATE TABLE IF NOT EXISTS user_group_members (
                user_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_group_id, user_id),
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )`,
	`CREATE TABLE IF NOT EXISTS device_groups (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                source_type TEXT DEFAULT 'manual',
                tag_filter TEXT DEFAULT '',
                created_at TEXT DEFAULT (datetime('now'))
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_members (
                device_group_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, peer_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_user_access (
                device_group_id INTEGER NOT NULL,
                user_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_user_group_access (
                device_group_id INTEGER NOT NULL,
                user_group_id INTEGER NOT NULL,
                created_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (device_group_id, user_group_id),
                FOREIGN KEY (device_group_id) REFERENCES device_groups(id) ON DELETE CASCADE,
                FOREIGN KEY (user_group_id) REFERENCES user_groups(id) ON DELETE CASCADE
            )`,
	`CREATE TABLE IF NOT EXISTS user_peer_grants (
                user_id INTEGER NOT NULL,
                peer_id TEXT NOT NULL,
                granted_at TEXT DEFAULT (datetime('now')),
                PRIMARY KEY (user_id, peer_id),
                FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
            )`,
	`CREATE INDEX IF NOT EXISTS idx_user_peer_grants_user ON user_peer_grants (user_id)`,
}

var postgresSchema = []string{
	`CREATE TABLE IF NOT EXISTS users (
                id SERIAL PRIMARY KEY,
                username TEXT UNIQUE NOT NULL,
                password_hash TEXT NOT NULL,
                role TEXT DEFAULT 'admin',
                auth_provider TEXT DEFAULT 'local',
                created_at TIMESTAMPTZ DEFAULT NOW(),
                last_login TIMESTAMPTZ,
                preferred_language TEXT DEFAULT NULL,
                totp_secret TEXT DEFAULT NULL,
                totp_enabled BOOLEAN DEFAULT FALSE,
                totp_recovery_codes TEXT DEFAULT NULL
            )`,
	`CREATE TABLE IF NOT EXISTS folders (
                id SERIAL PRIMARY KEY,
                name TEXT NOT NULL,
                color TEXT DEFAULT '#6366f1',
                icon TEXT DEFAULT 'folder',
                sort_order INTEGER DEFAULT 0,
                created_at TIMESTAMPTZ DEFAULT NOW()
            )`,
	`CREATE TABLE IF NOT EXISTS settings (
                key TEXT PRIMARY KEY,
                value TEXT NOT NULL,
                updated_at TIMESTAMPTZ DEFAULT NOW()
            )`,
	`CREATE TABLE IF NOT EXISTS user_groups (
                id SERIAL PRIMARY KEY,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                created_at TIMESTAMPTZ DEFAULT NOW()
            )`,
	`CREATE TABLE IF NOT EXISTS panel_access_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                resource_key TEXT NOT NULL,
                access_level TEXT NOT NULL DEFAULT 'none',
                updated_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (subject_type, subject_id, resource_key)
            )`,
	`CREATE INDEX IF NOT EXISTS idx_panel_access_policies_subject ON panel_access_policies (subject_type, subject_id)`,
	`CREATE TABLE IF NOT EXISTS client_feature_policies (
                subject_type TEXT NOT NULL,
                subject_id TEXT NOT NULL,
                feature_key TEXT NOT NULL,
                enabled BOOLEAN NOT NULL DEFAULT FALSE,
                updated_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (subject_type, subject_id, feature_key)
            )`,
	`CREATE INDEX IF NOT EXISTS idx_client_feature_policies_subject ON client_feature_policies (subject_type, subject_id)`,
	`CREATE TABLE IF NOT EXISTS user_group_members (
                user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
                user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (user_group_id, user_id)
            )`,
	`CREATE TABLE IF NOT EXISTS device_groups (
                id SERIAL PRIMARY KEY,
                guid TEXT UNIQUE NOT NULL,
                name TEXT NOT NULL,
                note TEXT DEFAULT '',
                team_id TEXT DEFAULT '',
                source_type TEXT DEFAULT 'manual',
                tag_filter TEXT DEFAULT '',
                created_at TIMESTAMPTZ DEFAULT NOW()
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_members (
                device_group_id INTEGER NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
                peer_id TEXT NOT NULL,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (device_group_id, peer_id)
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_user_access (
                device_group_id INTEGER NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
                user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (device_group_id, user_id)
            )`,
	`CREATE TABLE IF NOT EXISTS device_group_user_group_access (
                device_group_id INTEGER NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
                user_group_id INTEGER NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
                created_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (device_group_id, user_group_id)
            )`,
	`CREATE TABLE IF NOT EXISTS user_peer_grants (
                user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
                peer_id TEXT NOT NULL,
                granted_at TIMESTAMPTZ DEFAULT NOW(),
                PRIMARY KEY (user_id, peer_id)
            )`,
	`CREATE INDEX IF NOT EXISTS idx_user_peer_grants_user ON user_peer_grants (user_id)`,
}

// createSchema creates the tables of the Node console in an empty database.
func createSchema(t *testing.T, db *store.DB) {
	t.Helper()
	statements := sqliteSchema
	if db.IsPostgres() {
		statements = postgresSchema
	}
	for _, statement := range statements {
		if _, err := db.Exec(context.Background(), statement); err != nil {
			t.Fatalf("create schema: %v\n%s", err, statement)
		}
	}
}

// mustExec runs a statement and fails the test when it fails.
func mustExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}
