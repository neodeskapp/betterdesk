package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The sign-in rules of one account, beside the server-wide ones: an admin
// can require that a local account chooses a new password at its next
// sign-in (a temporary password does that too), and that it sets up
// two-factor sign-in there. The console keeps them in its own table
// account_security, one row per account that has a rule; no row is no
// rule. The API server reads the same table for the desktop client's and
// the API's sign-ins.
//
// Browsers an account signed in from are in known_browsers (browsers.go).

// The answer of a desktop client's or the API's sign-in for an account
// that must choose a new password first: those sign-ins have no step for
// it, which the web console's sign-in does.
const (
	PasswordChangeDueCode    = "password_change_required"
	PasswordChangeDueMessage = "This account must choose a new password. Sign in to the web console once to set it."
)

var accountTables = []struct{ sqlite, postgres string }{
	{`CREATE TABLE IF NOT EXISTS account_security (
			user_id INTEGER PRIMARY KEY,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			two_factor_required INTEGER NOT NULL DEFAULT 0,
			updated_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS account_security (
			user_id BIGINT PRIMARY KEY,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			two_factor_required INTEGER NOT NULL DEFAULT 0,
			updated_ms BIGINT NOT NULL DEFAULT 0
		)`},
	{`CREATE TABLE IF NOT EXISTS known_browsers (
			user_id INTEGER NOT NULL,
			browser_hash TEXT NOT NULL,
			first_ms INTEGER NOT NULL,
			last_ms INTEGER NOT NULL,
			PRIMARY KEY (user_id, browser_hash)
		)`,
		`CREATE TABLE IF NOT EXISTS known_browsers (
			user_id BIGINT NOT NULL,
			browser_hash TEXT NOT NULL,
			first_ms BIGINT NOT NULL,
			last_ms BIGINT NOT NULL,
			PRIMARY KEY (user_id, browser_hash)
		)`},
}

// EnsureAccountTables creates account_security and known_browsers when
// they are missing. Running it again changes nothing.
func EnsureAccountTables(ctx context.Context, db *store.DB) error {
	for _, t := range accountTables {
		if _, err := db.Exec(ctx, db.Pick(t.sqlite, t.postgres)); err != nil {
			return fmt.Errorf("account security tables: %w", err)
		}
	}
	return nil
}

// AccountPolicy is what an admin requires of one account's sign-in.
type AccountPolicy struct {
	// MustChangePassword: the next sign-in asks for a new password
	// before the account gets a session.
	MustChangePassword bool
	// TwoFactorRequired: the account sets up two-factor sign-in at its
	// next sign-in when it has none, whatever the server-wide switch says.
	TwoFactorRequired bool
}

// noTable reports an error of a database that has no such table yet: a
// database the console has not set up, read by the API server.
func noTable(db *store.DB, err error) bool {
	return err != nil && missingTable(db, err)
}

// AccountPolicy reads the rules of an account; none for an account
// without a row.
func (s *Service) AccountPolicy(ctx context.Context, userID int64) (AccountPolicy, error) {
	var change, twoFactor int64
	err := s.db.QueryRow(ctx, `SELECT must_change_password, two_factor_required FROM account_security WHERE user_id = ?`, userID).Scan(&change, &twoFactor)
	switch {
	case err == nil:
		return AccountPolicy{MustChangePassword: change != 0, TwoFactorRequired: twoFactor != 0}, nil
	case errors.Is(err, sql.ErrNoRows), noTable(s.db, err):
		return AccountPolicy{}, nil
	}
	return AccountPolicy{}, fmt.Errorf("read the sign-in rules of user %d: %w", userID, err)
}

// AccountPolicies returns the rules of every account that has one.
func (s *Service) AccountPolicies(ctx context.Context) (map[int64]AccountPolicy, error) {
	out := map[int64]AccountPolicy{}
	rows, err := s.db.Query(ctx, `SELECT user_id, must_change_password, two_factor_required FROM account_security`)
	if noTable(s.db, err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the sign-in rules of the accounts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, change, twoFactor int64
		if err := rows.Scan(&id, &change, &twoFactor); err != nil {
			return nil, err
		}
		if change != 0 || twoFactor != 0 {
			out[id] = AccountPolicy{MustChangePassword: change != 0, TwoFactorRequired: twoFactor != 0}
		}
	}
	return out, rows.Err()
}

// setAccountRule writes one column of the account's rules.
func (s *Service) setAccountRule(ctx context.Context, userID int64, column string, on bool) error {
	flag := 0
	if on {
		flag = 1
	}
	_, err := s.db.Exec(ctx, `INSERT INTO account_security (user_id, `+column+`, updated_ms) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET `+column+` = excluded.`+column+`, updated_ms = excluded.updated_ms`,
		userID, flag, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store the sign-in rules of user %d: %w", userID, err)
	}
	return nil
}

// SetMustChangePassword makes the account's next sign-in ask for a new
// password (on), or not (off).
func (s *Service) SetMustChangePassword(ctx context.Context, userID int64, on bool) error {
	return s.setAccountRule(ctx, userID, "must_change_password", on)
}

// SetTwoFactorRequiredFor makes the account set up two-factor sign-in at
// its next sign-in when it has none (on), or leaves that to the
// server-wide switch (off).
func (s *Service) SetTwoFactorRequiredFor(ctx context.Context, userID int64, on bool) error {
	return s.setAccountRule(ctx, userID, "two_factor_required", on)
}

// ForgetAccount deletes the rules and the known browsers of an account
// that is deleted.
func (s *Service) ForgetAccount(ctx context.Context, userID int64) error {
	for _, table := range []string{"account_security", "known_browsers"} {
		if _, err := s.db.Exec(ctx, `DELETE FROM `+table+` WHERE user_id = ?`, userID); err != nil && !noTable(s.db, err) {
			return fmt.Errorf("forget the sign-in rules of user %d: %w", userID, err)
		}
	}
	return nil
}

// TwoFactorDue reports whether two-factor sign-in is required of the
// account: by the server-wide switch for a local account, or by its own
// rule.
func (s *Service) TwoFactorDue(ctx context.Context, userID int64, local bool) (bool, error) {
	policy, err := s.AccountPolicy(ctx, userID)
	if err != nil || policy.TwoFactorRequired {
		return policy.TwoFactorRequired, err
	}
	if !local {
		return false, nil
	}
	return s.TwoFactorRequired(ctx)
}

// knownBrowserLife is how long a browser stays known without a sign-in.
const knownBrowserLife = 400 * 24 * time.Hour
