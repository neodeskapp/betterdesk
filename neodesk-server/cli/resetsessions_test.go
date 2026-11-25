package cli

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestResetPasswordEndsTheAccountsConsoleSessions: a password set on the
// server's command line ends the console sessions of that account, as a
// reset in the console does (A239): whoever held the old password is
// signed out everywhere. Another account's sessions stay.
func TestResetPasswordEndsTheAccountsConsoleSessions(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "db_v2.sqlite3")
	// The first run makes the users table and the account
	if status, out := runReset(t, map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}, root); status != 0 {
		t.Fatalf("first reset: %d %q", status, out)
	}
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`INSERT INTO users (username, password_hash, role) VALUES ('other', 'x', 'viewer')`,
		`CREATE TABLE panel_sessions (sid TEXT PRIMARY KEY, session_json TEXT NOT NULL, expires_at INTEGER NOT NULL,
			updated_at TEXT, user_id INTEGER, username TEXT, ended_at INTEGER, end_reason TEXT, is_active INTEGER NOT NULL DEFAULT 1)`,
		`INSERT INTO panel_sessions (sid, session_json, expires_at, user_id, username) SELECT 'a1', '{"userId":1}', 9999999999999, id, username FROM users WHERE username = 'admin'`,
		`INSERT INTO panel_sessions (sid, session_json, expires_at, user_id, username) SELECT 'a2', '{"userId":1}', 9999999999999, id, username FROM users WHERE username = 'admin'`,
		`INSERT INTO panel_sessions (sid, session_json, expires_at, user_id, username) SELECT 'o1', '{"userId":2}', 9999999999999, id, username FROM users WHERE username = 'other'`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if status, out := runReset(t, map[string]string{"DB_PATH": file, "CONSOLE_DIR": root}, root); status != 0 {
		t.Fatalf("reset: %d %q", status, out)
	}
	rows, err := db.Query(`SELECT sid, is_active, COALESCE(end_reason, ''), session_json FROM panel_sessions ORDER BY sid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{"a1": "0 revoked {}", "a2": "0 revoked {}", "o1": `1  {"userId":2}`}
	for rows.Next() {
		var sid, reason, text string
		var active int
		if err := rows.Scan(&sid, &active, &reason, &text); err != nil {
			t.Fatal(err)
		}
		if got := fmtRow(active, reason, text); got != want[sid] {
			t.Errorf("session %s: %q, want %q", sid, got, want[sid])
		}
	}
}

func fmtRow(active int, reason, text string) string {
	return string(rune('0'+active)) + " " + reason + " " + text
}
