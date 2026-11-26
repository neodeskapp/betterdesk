package db

import (
	"path/filepath"
	"testing"
)

// The rules the web console keeps per account: none where the console
// never made its table, none for an account without a row, and the row's
// flags otherwise.
func TestAccountSignInRules(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if rules, err := s.AccountSignInRules(1); err != nil || rules != (AccountSignInRules{}) {
		t.Fatalf("without the table: %+v %v", rules, err)
	}
	if _, err := s.db.Exec(`CREATE TABLE account_security (user_id INTEGER PRIMARY KEY, must_change_password INTEGER NOT NULL DEFAULT 0,
		two_factor_required INTEGER NOT NULL DEFAULT 0, updated_ms INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO account_security (user_id, must_change_password, two_factor_required) VALUES (7, 1, 0), (8, 0, 1)`); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]AccountSignInRules{6: {}, 7: {MustChangePassword: true}, 8: {TwoFactorRequired: true}} {
		if got, err := s.AccountSignInRules(id); err != nil || got != want {
			t.Errorf("user %d: %+v %v, want %+v", id, got, err, want)
		}
	}
}
