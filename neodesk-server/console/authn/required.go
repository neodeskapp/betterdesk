package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// RequireTwoFactorKey is the key in the server's configuration
// (server_config) of the switch that makes two-factor sign-in mandatory
// for local accounts (Settings, Sign-in, "Require two-factor sign-in").
// Directory and single sign-on accounts are left to their provider.
const RequireTwoFactorKey = "require_two_factor"

// The answer of a desktop client's or the API's sign-in for an account
// whose mandatory second factor is not set up yet: those sign-ins have no
// step for the set-up, which the web console's sign-in does.
const (
	TOTPSetupDueCode    = "totp_setup_required"
	TOTPSetupDueMessage = "Two-factor sign-in is required for this account. Sign in to the web console once to set it up."
)

// TwoFactorRequired reports whether the switch is on. It reads the
// server's configuration table, which the console shares; a database
// without the table has it off.
func (s *Service) TwoFactorRequired(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, RequireTwoFactorKey).Scan(&value)
	switch {
	case err == nil:
		return value == "true", nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case strings.Contains(err.Error(), s.db.Pick("no such table", "does not exist")):
		return false, nil
	}
	return false, fmt.Errorf("read whether two-factor sign-in is required: %w", err)
}
