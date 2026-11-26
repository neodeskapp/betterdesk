package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// AccountSignInRules is what an admin of the web console requires of one
// account's sign-in, beside the server-wide rules. The web console keeps
// them in its table account_security (console/authn) and sets them; the
// API's sign-in and the desktop client's only read them: they have no step
// to choose a new password or to set up two-factor sign-in, so they refuse
// the account until it did that in the web console.
type AccountSignInRules struct {
	// MustChangePassword: the account must choose a new password.
	MustChangePassword bool
	// TwoFactorRequired: the account must set up two-factor sign-in when
	// it has none, whatever the server-wide switch says.
	TwoFactorRequired bool
}

// noRulesTable reports the error of a database where the web console never
// made its table: no account has rules there.
func noRulesTable(err error) bool {
	text := err.Error()
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist")
}

const accountRulesQuery = `SELECT must_change_password, two_factor_required FROM account_security WHERE user_id = `

// AccountSignInRules reads the account's rules; none without a row or
// without the table.
func (s *SQLiteDB) AccountSignInRules(userID int64) (AccountSignInRules, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var change, twoFactor int64
	err := s.db.QueryRow(accountRulesQuery+`?`, userID).Scan(&change, &twoFactor)
	switch {
	case err == nil:
		return AccountSignInRules{MustChangePassword: change != 0, TwoFactorRequired: twoFactor != 0}, nil
	case errors.Is(err, sql.ErrNoRows), noRulesTable(err):
		return AccountSignInRules{}, nil
	}
	return AccountSignInRules{}, fmt.Errorf("db: AccountSignInRules: %w", err)
}

// AccountSignInRules reads the account's rules; none without a row or
// without the table.
func (pg *PostgresDB) AccountSignInRules(userID int64) (AccountSignInRules, error) {
	var change, twoFactor int64
	err := pg.pool.QueryRow(pg.ctx, accountRulesQuery+`$1`, userID).Scan(&change, &twoFactor)
	switch {
	case err == nil:
		return AccountSignInRules{MustChangePassword: change != 0, TwoFactorRequired: twoFactor != 0}, nil
	case errors.Is(err, pgx.ErrNoRows), noRulesTable(err):
		return AccountSignInRules{}, nil
	}
	return AccountSignInRules{}, fmt.Errorf("db: AccountSignInRules: %w", err)
}
