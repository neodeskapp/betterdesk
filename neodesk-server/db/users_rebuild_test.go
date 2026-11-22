package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestUsersWithoutRoleCheck: the rebuilt definition is the old one under
// the name users_new without the role CHECK; a definition with no such
// CHECK, or with two, is left alone (BUGS D15).
func TestUsersWithoutRoleCheck(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`CREATE TABLE users (id INTEGER, role TEXT, x TEXT, CHECK (role IN ('admin', 'viewer')), y TEXT)`,
			`CREATE TABLE users_new (id INTEGER, role TEXT, x TEXT, y TEXT)`},
		{`CREATE TABLE "users"(id INTEGER, role TEXT CHECK(role IN ('admin','viewer')) NOT NULL)`,
			`CREATE TABLE users_new(id INTEGER, role TEXT NOT NULL)`},
		{`CREATE TABLE users (id INTEGER, role TEXT)`, ""},
		{`CREATE TABLE users (role TEXT CHECK(role IN ('admin')), CHECK (role IN ('viewer')))`, ""},
	} {
		got, ok := usersWithoutRoleCheck(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("%s: %q %v, want %q", c.in, got, ok, c.want)
		}
	}
}

// TestUsersRebuildKeepsColumnsAndReferences: dropping the old three-role
// CHECK from users keeps every column the table has (the console's too)
// and every row that references an account with ON DELETE CASCADE, also
// the table's indexes; the CHECK may be a table or a column constraint
// (BUGS D15).
func TestUsersRebuildKeepsColumnsAndReferences(t *testing.T) {
	for name, roleColumn := range map[string]string{
		"table constraint":  `role TEXT NOT NULL DEFAULT 'viewer', created_at TEXT DEFAULT (datetime('now')), CHECK (role IN ('admin', 'operator', 'viewer'))`,
		"column constraint": `role TEXT NOT NULL DEFAULT 'viewer' CHECK(role IN ('admin','operator','viewer')), created_at TEXT DEFAULT (datetime('now'))`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for _, stmt := range []string{
				`CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE NOT NULL, password_hash TEXT NOT NULL, last_login TEXT DEFAULT '', ` + roleColumn + `)`,
				`ALTER TABLE users ADD COLUMN email TEXT DEFAULT ''`,
				`CREATE INDEX idx_users_email ON users(email)`,
				`INSERT INTO users (username, password_hash, role, email) VALUES ('op', 'hash', 'operator', 'op@example.org')`,
				`CREATE TABLE user_notes (id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, note TEXT)`,
				`INSERT INTO user_notes (user_id, note) SELECT id, 'kept' FROM users WHERE username = 'op'`,
			} {
				if _, err := raw.Exec(stmt); err != nil {
					raw.Close()
					t.Fatalf("%s: %v", stmt, err)
				}
			}
			raw.Close()

			database, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if err := database.Migrate(); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if database.usersHasLegacyRoleCheck() {
				t.Fatal("the role CHECK is still there")
			}
			var email string
			if err := database.db.QueryRow(`SELECT email FROM users WHERE username = 'op'`).Scan(&email); err != nil || email != "op@example.org" {
				t.Errorf("email after the rebuild: %q %v", email, err)
			}
			var notes, indexes int
			if err := database.db.QueryRow(`SELECT COUNT(*) FROM user_notes`).Scan(&notes); err != nil || notes != 1 {
				t.Errorf("rows that reference users after the rebuild: %d %v", notes, err)
			}
			if err := database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_users_email'`).Scan(&indexes); err != nil || indexes != 1 {
				t.Errorf("index on users after the rebuild: %d %v", indexes, err)
			}
			if err := database.CreateUser(&User{Username: "ga", PasswordHash: "hash", Role: "global_admin"}); err != nil {
				t.Errorf("an account with a newer role after the rebuild: %v", err)
			}
		})
	}
}
