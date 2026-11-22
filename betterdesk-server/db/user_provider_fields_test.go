package db

import (
	"database/sql"
	"strings"
	"testing"
)

// rawStrings reads the text columns of one row on the store's own
// connection.
func rawStrings(t *testing.T, store Database, query string, args ...any) []string {
	t.Helper()
	var row interface{ Scan(dest ...any) error }
	switch d := store.(type) {
	case *SQLiteDB:
		row = d.db.QueryRow(query, args...)
	case *PostgresDB:
		for i := 1; strings.Contains(query, "?"); i++ {
			query = strings.Replace(query, "?", "$"+string(rune('0'+i)), 1)
		}
		row = d.pool.QueryRow(d.ctx, query, args...)
	default:
		t.Fatalf("no raw connection for %T", store)
	}
	values := make([]sql.NullString, strings.Count(query, ",")+1)
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := row.Scan(dest...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v.String
	}
	return out
}

// TestUserFieldsFromProvider: the email address and display name a
// directory or single sign-on gives at sign-in go into the console's
// profile columns (the name whole as the first name), and an empty value
// leaves the column as it is. A database without the console's columns
// (the server without its console) takes the call without an error (L113).
func TestUserFieldsFromProvider(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		u := &User{Username: "prov-anna", PasswordHash: "x", Role: "viewer", AuthProvider: AuthProviderLDAP}
		if err := store.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateUserFromProvider(u.ID, "anna@example.org", "Anna Berg"); err != nil {
			t.Fatalf("without the console's columns: %v", err)
		}

		for _, column := range []string{"email", "first_name", "last_name"} {
			rawExec(t, store, `ALTER TABLE users ADD COLUMN `+column+` TEXT DEFAULT ''`)
		}
		rawExec(t, store, `UPDATE users SET last_name = 'Old' WHERE id = ?`, u.ID)
		read := func() string {
			return strings.Join(rawStrings(t, store, `SELECT email, first_name, last_name FROM users WHERE id = ?`, u.ID), "|")
		}

		if err := store.UpdateUserFromProvider(u.ID, "anna@example.org", "Anna Berg"); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != "anna@example.org|Anna Berg|" {
			t.Errorf("after the sign-in: %q", got)
		}
		if err := store.UpdateUserFromProvider(u.ID, "", ""); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != "anna@example.org|Anna Berg|" {
			t.Errorf("after a sign-in without the values: %q", got)
		}
		if err := store.UpdateUserFromProvider(u.ID, "anna.berg@example.org", ""); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != "anna.berg@example.org|Anna Berg|" {
			t.Errorf("after a new address: %q", got)
		}
		long := strings.Repeat("é", 250)
		if err := store.UpdateUserFromProvider(u.ID, "", long); err != nil {
			t.Fatal(err)
		}
		if got := rawStrings(t, store, `SELECT first_name FROM users WHERE id = ?`, u.ID)[0]; got != strings.Repeat("é", 200) {
			t.Errorf("a long name is kept as %d characters, want 200", len([]rune(got)))
		}
	})
}
