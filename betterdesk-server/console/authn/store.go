package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries of sign-in on users, access_tokens, login_attempts,
// account_lockouts and device_tokens, each in the wording of the database
// it runs on. Times the database compares are its own clock
// (datetime('now'), NOW()).

type nullText = store.NullString

// Account is a row of users as sign-in reads it.
type Account struct {
	// ID goes out as a number, or as text where the column is a
	// PostgreSQL bigint (a table the signal server created).
	ID                store.Int64String `json:"id"`
	Username          string            `json:"username"`
	PasswordHash      nullText          `json:"-"`
	Role              nullText          `json:"role"`
	AuthProvider      nullText          `json:"auth_provider"`
	PreferredLanguage nullText          `json:"preferred_language"`
	TOTPSecret        nullText          `json:"-"`
	TOTPEnabled       store.Bool        `json:"totp_enabled"`
	TOTPRecoveryCodes nullText          `json:"-"`
}

const accountColumns = `id, username, password_hash, role, auth_provider, preferred_language,
	totp_secret, totp_enabled, totp_recovery_codes`

func scanAccount(row *store.Row) (*Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Role, &a.AuthProvider, &a.PreferredLanguage,
		&a.TOTPSecret, &a.TOTPEnabled, &a.TOTPRecoveryCodes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// getUserByUsername returns the account with exactly this name, or nil.
func getUserByUsername(ctx context.Context, q store.Querier, username string) (*Account, error) {
	a, err := scanAccount(store.SelectRow(ctx, q, `SELECT `+accountColumns+` FROM users WHERE username = ?`, username))
	if err != nil {
		return nil, fmt.Errorf("load user by name: %w", err)
	}
	return a, nil
}

// getUserByID returns the account with this id, or nil.
func getUserByID(ctx context.Context, q store.Querier, id int64) (*Account, error) {
	a, err := scanAccount(store.SelectRow(ctx, q, `SELECT `+accountColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("load user %d: %w", id, err)
	}
	return a, nil
}

// createUser adds an account. An empty provider is "local".
func createUser(ctx context.Context, q store.Querier, username, passwordHash, role, provider string) error {
	if provider == "" {
		provider = providerLocal
	}
	_, err := q.Insert(ctx, `INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, ?, ?, ?)`,
		username, passwordHash, role, provider)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// userSync is what a sign-in through the signal server changes on the
// local row. A nil field stays as it is.
type userSync struct {
	role         *string
	authProvider *string
	passwordHash *string
}

func (u userSync) empty() bool {
	return u.role == nil && u.authProvider == nil && u.passwordHash == nil
}

// syncUserFromGo writes the fields of sync that are set.
func syncUserFromGo(ctx context.Context, q store.Querier, id int64, sync userSync) error {
	var sets []string
	var values []any
	if sync.role != nil {
		sets = append(sets, "role = ?")
		values = append(values, *sync.role)
	}
	if sync.authProvider != nil {
		sets = append(sets, "auth_provider = ?")
		values = append(values, *sync.authProvider)
	}
	if sync.passwordHash != nil {
		sets = append(sets, "password_hash = ?")
		values = append(values, *sync.passwordHash)
	}
	if len(sets) == 0 {
		return nil
	}
	values = append(values, id)
	if _, err := q.Exec(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, values...); err != nil {
		return fmt.Errorf("sync user %d: %w", id, err)
	}
	return nil
}

func updateUserPassword(ctx context.Context, q store.Querier, id int64, passwordHash string) error {
	if _, err := q.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id); err != nil {
		return fmt.Errorf("store the password hash of user %d: %w", id, err)
	}
	return nil
}

func touchLastLogin(ctx context.Context, q store.Querier, id int64) error {
	query := `UPDATE users SET last_login = ` + store.NowSQL(q.Dialect()) + ` WHERE id = ?`
	if _, err := q.Exec(ctx, query, id); err != nil {
		return fmt.Errorf("note the sign-in of user %d: %w", id, err)
	}
	return nil
}

func hasUsers(ctx context.Context, q store.Querier) (bool, error) {
	var n int64
	if err := q.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, fmt.Errorf("count users: %w", err)
	}
	return n > 0, nil
}

// ---- Two-factor columns ----

func saveTOTPSecret(ctx context.Context, q store.Querier, id int64, secret string) error {
	if _, err := q.Exec(ctx, `UPDATE users SET totp_secret = ? WHERE id = ?`, secret, id); err != nil {
		return fmt.Errorf("store the two-factor secret of user %d: %w", id, err)
	}
	return nil
}

func enableTOTP(ctx context.Context, q store.Querier, id int64, codesJSON string) error {
	query := q.Pick(
		`UPDATE users SET totp_enabled = 1, totp_recovery_codes = ? WHERE id = ?`,
		`UPDATE users SET totp_enabled = TRUE, totp_recovery_codes = ? WHERE id = ?`)
	if _, err := q.Exec(ctx, query, codesJSON, id); err != nil {
		return fmt.Errorf("turn on two-factor sign-in for user %d: %w", id, err)
	}
	return nil
}

func disableTOTP(ctx context.Context, q store.Querier, id int64) error {
	query := q.Pick(
		`UPDATE users SET totp_enabled = 0, totp_secret = NULL, totp_recovery_codes = NULL WHERE id = ?`,
		`UPDATE users SET totp_enabled = FALSE, totp_secret = NULL, totp_recovery_codes = NULL WHERE id = ?`)
	if _, err := q.Exec(ctx, query, id); err != nil {
		return fmt.Errorf("turn off two-factor sign-in for user %d: %w", id, err)
	}
	return nil
}

func useRecoveryCode(ctx context.Context, q store.Querier, id int64, codesJSON string) error {
	if _, err := q.Exec(ctx, `UPDATE users SET totp_recovery_codes = ? WHERE id = ?`, codesJSON, id); err != nil {
		return fmt.Errorf("store the recovery codes of user %d: %w", id, err)
	}
	return nil
}

// ---- Access tokens ----

// AccessToken is a row of access_tokens.
type AccessToken struct {
	ID         store.Int64String `json:"id"`
	Token      string            `json:"token"`
	TokenHash  nullText          `json:"token_hash"`
	UserID     store.Int64String `json:"user_id"`
	ClientID   nullText          `json:"client_id"`
	ClientUUID nullText          `json:"client_uuid"`
	CreatedAt  store.Time        `json:"created_at"`
	ExpiresAt  store.Time        `json:"expires_at"`
	LastUsed   store.Time        `json:"last_used"`
	IPAddress  nullText          `json:"ip_address"`
	Revoked    store.Bool        `json:"revoked"`
}

const accessTokenColumns = `id, token, token_hash, user_id, client_id, client_uuid, created_at, expires_at,
	last_used, ip_address, revoked`

func createAccessToken(ctx context.Context, q store.Querier, token string, userID int64, clientID, clientUUID, expiresAt, ip string) error {
	_, err := q.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		token, HashAccessToken(token), userID, clientID, clientUUID, expiresAt, ip)
	if err != nil {
		return fmt.Errorf("store an access token: %w", err)
	}
	return nil
}

// getAccessToken looks a token up, first by its hash, then by the token
// itself for rows from before the hash column. Revoked and expired rows do
// not count. It returns nil when there is none.
func getAccessToken(ctx context.Context, q store.Querier, token string) (*AccessToken, error) {
	live := q.Pick(`revoked = 0 AND expires_at > datetime('now')`, `NOT revoked AND expires_at > NOW()`)
	for _, lookup := range []struct{ column, value string }{
		{"token_hash", HashAccessToken(token)},
		{"token", token},
	} {
		var t AccessToken
		err := store.SelectRow(ctx, q,
			`SELECT `+accessTokenColumns+` FROM access_tokens WHERE `+lookup.column+` = ? AND `+live, lookup.value).
			Scan(&t.ID, &t.Token, &t.TokenHash, &t.UserID, &t.ClientID, &t.ClientUUID, &t.CreatedAt, &t.ExpiresAt,
				&t.LastUsed, &t.IPAddress, &t.Revoked)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("look up an access token: %w", err)
		}
		return &t, nil
	}
	return nil, nil
}

func touchAccessToken(ctx context.Context, q store.Querier, token string) error {
	query := `UPDATE access_tokens SET last_used = ` + store.NowSQL(q.Dialect()) + ` WHERE token_hash = ? OR token = ?`
	if _, err := q.Exec(ctx, query, HashAccessToken(token), token); err != nil {
		return fmt.Errorf("note the use of an access token: %w", err)
	}
	return nil
}

func revokeUserClientTokens(ctx context.Context, q store.Querier, userID int64, clientUUID string) error {
	query := q.Pick(
		`UPDATE access_tokens SET revoked = 1 WHERE user_id = ? AND client_uuid = ?`,
		`UPDATE access_tokens SET revoked = TRUE WHERE user_id = ? AND client_uuid = ?`)
	if _, err := q.Exec(ctx, query, userID, clientUUID); err != nil {
		return fmt.Errorf("revoke the tokens of user %d for one client: %w", userID, err)
	}
	return nil
}

func revokeAllUserTokens(ctx context.Context, q store.Querier, userID int64) error {
	query := q.Pick(
		`UPDATE access_tokens SET revoked = 1 WHERE user_id = ?`,
		`UPDATE access_tokens SET revoked = TRUE WHERE user_id = ?`)
	if _, err := q.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("revoke the tokens of user %d: %w", userID, err)
	}
	return nil
}

func cleanupExpiredTokens(ctx context.Context, q store.Querier) error {
	query := q.Pick(
		`DELETE FROM access_tokens WHERE expires_at < datetime('now') OR revoked = 1`,
		`DELETE FROM access_tokens WHERE expires_at < NOW() OR revoked`)
	if _, err := q.Exec(ctx, query); err != nil {
		return fmt.Errorf("delete old access tokens: %w", err)
	}
	return nil
}

// ---- Sign-in attempts and lockouts ----

func recordLoginAttempt(ctx context.Context, q store.Querier, username any, ip string, success bool) error {
	var flag any = success
	if q.Dialect() == store.SQLite {
		flag = 0
		if success {
			flag = 1
		}
	}
	if _, err := q.Exec(ctx, `INSERT INTO login_attempts (username, ip_address, success) VALUES (?, ?, ?)`,
		username, ip, flag); err != nil {
		return fmt.Errorf("record a sign-in attempt: %w", err)
	}
	return nil
}

// countRecentFailed counts the failed attempts of the last windowMinutes
// minutes whose column (username or ip_address) has the value.
func countRecentFailed(ctx context.Context, q store.Querier, column string, value any, windowMinutes int) (int64, error) {
	query := q.Pick(
		`SELECT COUNT(*) FROM login_attempts WHERE `+column+` = ? AND success = 0
			AND created_at > datetime('now', '-' || ? || ' minutes')`,
		`SELECT COUNT(*) FROM login_attempts WHERE `+column+` = ? AND NOT success
			AND created_at > NOW() - INTERVAL '1 minute' * ?`)
	var n int64
	if err := q.QueryRow(ctx, query, value, float64(windowMinutes)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count failed sign-in attempts: %w", err)
	}
	return n, nil
}

func lockAccount(ctx context.Context, q store.Querier, username any, lockedUntil string, attemptCount int64) error {
	_, err := q.Exec(ctx, `INSERT INTO account_lockouts (username, locked_until, attempt_count) VALUES (?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET locked_until = excluded.locked_until, attempt_count = excluded.attempt_count`,
		username, lockedUntil, attemptCount)
	if err != nil {
		return fmt.Errorf("lock an account: %w", err)
	}
	return nil
}

// getAccountLockout returns the end of the account's lockout when it is
// still in the future by the database's clock. found is false otherwise.
func getAccountLockout(ctx context.Context, q store.Querier, username any) (lockedUntil store.Time, found bool, err error) {
	query := `SELECT locked_until FROM account_lockouts WHERE username = ? AND locked_until > ` + store.NowSQL(q.Dialect())
	err = store.SelectRow(ctx, q, query, username).Scan(&lockedUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedUntil, false, nil
	}
	if err != nil {
		return lockedUntil, false, fmt.Errorf("look up an account lockout: %w", err)
	}
	return lockedUntil, true, nil
}

func clearAccountLockout(ctx context.Context, q store.Querier, username any) error {
	if _, err := q.Exec(ctx, `DELETE FROM account_lockouts WHERE username = ?`, username); err != nil {
		return fmt.Errorf("clear an account lockout: %w", err)
	}
	return nil
}

func cleanupOldLoginAttempts(ctx context.Context, q store.Querier) error {
	query := q.Pick(
		`DELETE FROM login_attempts WHERE created_at < datetime('now', '-24 hours')`,
		`DELETE FROM login_attempts WHERE created_at < NOW() - INTERVAL '24 hours'`)
	if _, err := q.Exec(ctx, query); err != nil {
		return fmt.Errorf("delete old sign-in attempts: %w", err)
	}
	return nil
}

// ---- Enrollment tokens (a table of the signal server) ----

// enrollmentToken is the part of a device_tokens row the check reads.
type enrollmentToken struct {
	peerID    nullText
	status    nullText
	maxUses   sql.NullInt64
	useCount  sql.NullInt64
	expiresAt store.Time
}

// lookupEnrollmentToken returns the enrollment token with this hash, or
// nil. On SQLite a database without the table has no tokens; on PostgreSQL
// that is an error.
func lookupEnrollmentToken(ctx context.Context, q store.Querier, tokenHash string) (*enrollmentToken, error) {
	var t enrollmentToken
	err := store.SelectRow(ctx, q,
		`SELECT peer_id, status, max_uses, use_count, expires_at FROM device_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&t.peerID, &t.status, &t.maxUses, &t.useCount, &t.expiresAt)
	switch {
	case err == nil:
		return &t, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case q.Dialect() == store.SQLite && strings.Contains(err.Error(), "no such table"):
		return nil, nil
	}
	return nil, fmt.Errorf("look up an enrollment token: %w", err)
}

// deviceKnown reports whether the signal server has a device row with this
// id in peers, a soft-deleted one included. A database without the table
// has no devices.
func deviceKnown(ctx context.Context, q store.Querier, id string) (bool, error) {
	var one int
	err := store.SelectRow(ctx, q, `SELECT 1 FROM peers WHERE id = ?`, id).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case strings.Contains(err.Error(), q.Pick("no such table", "does not exist")):
		return false, nil
	}
	return false, fmt.Errorf("look up a device: %w", err)
}
