package db

import (
	"strings"
	"testing"
	"time"
)

// rawExec runs a statement on the store's own connection, the way the
// console and the command line write the users table without this
// package. The statement uses ? for its arguments.
func rawExec(t *testing.T, store Database, stmt string, args ...any) {
	t.Helper()
	var err error
	switch d := store.(type) {
	case *SQLiteDB:
		_, err = d.db.Exec(stmt, args...)
	case *PostgresDB:
		for i := 1; strings.Contains(stmt, "?"); i++ {
			stmt = strings.Replace(stmt, "?", "$"+string(rune('0'+i)), 1)
		}
		_, err = d.pool.Exec(d.ctx, stmt, args...)
	default:
		t.Fatalf("no raw connection for %T", store)
	}
	if err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// TestCredentialsChangedAtFollowsPasswordAndSecondFactor: a new account has
// no credentials-changed time; a role change and a second factor that is
// set up but not confirmed leave it; a password change (through UpdateUser
// or a plain UPDATE of another writer), switching the second factor on or
// off and a new secret of a second factor that is on set it (RV181).
func TestCredentialsChangedAtFollowsPasswordAndSecondFactor(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		u := &User{Username: "cred-anna", PasswordHash: "hash-1", Role: "operator"}
		if err := store.CreateUser(u); err != nil {
			t.Fatal(err)
		}
		changedAt := func() int64 {
			t.Helper()
			got, err := store.GetUser("cred-anna")
			if err != nil || got == nil {
				t.Fatalf("GetUser: %v, %v", got, err)
			}
			*u = *got
			return got.CredentialsChangedAt
		}
		reset := func() { rawExec(t, store, `UPDATE users SET credentials_changed_at = NULL WHERE id = ?`, u.ID) }
		stamped := func(what string, before int64) {
			t.Helper()
			if got := changedAt(); got < before || got > time.Now().Unix() {
				t.Errorf("%s: credentials changed at %d, want between %d and now", what, got, before)
			}
			reset()
			changedAt()
		}
		left := func(what string) {
			t.Helper()
			if got := changedAt(); got != 0 {
				t.Errorf("%s: credentials changed at %d, want none", what, got)
			}
		}

		left("new account")
		u.Role = "viewer"
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		left("role change")
		u.TOTPSecret = "SETUPSECRET"
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		left("second factor set up, not confirmed")

		before := time.Now().Unix()
		u.PasswordHash = "hash-2"
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		stamped("password through UpdateUser", before)

		before = time.Now().Unix()
		rawExec(t, store, `UPDATE users SET password_hash = ? WHERE id = ?`, "hash-3", u.ID)
		stamped("password by another writer", before)

		before = time.Now().Unix()
		u.TOTPEnabled = true
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		stamped("second factor on", before)

		before = time.Now().Unix()
		u.TOTPSecret = "NEWSECRET"
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		stamped("new secret while on", before)

		before = time.Now().Unix()
		u.TOTPSecret, u.TOTPEnabled = "", false
		if err := store.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		stamped("second factor off", before)
	})
}
