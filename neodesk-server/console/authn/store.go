package authn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
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
	// Disabled says the account may not sign in and its tokens are
	// refused.
	Disabled store.Bool `json:"-"`
}

const accountColumns = `id, username, password_hash, role, auth_provider, preferred_language,
	totp_secret, totp_enabled, totp_recovery_codes, disabled`

func scanAccount(row *store.Row) (*Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.Role, &a.AuthProvider, &a.PreferredLanguage,
		&a.TOTPSecret, &a.TOTPEnabled, &a.TOTPRecoveryCodes, &a.Disabled)
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

// directoryAccountNamed returns the directory account whose name is
// username lower-cased and without spaces at the ends, or nil when there
// is none or username is already that name. Directory accounts are named
// in lower case, and a directory compares names that way.
func directoryAccountNamed(ctx context.Context, q store.Querier, username string) (*Account, error) {
	name := strings.ToLower(strings.TrimSpace(username))
	if name == "" || name == username {
		return nil, nil
	}
	a, err := getUserByUsername(ctx, q, name)
	if err != nil || a == nil || normalizeAuthProvider(a.AuthProvider.String) != providerLDAP {
		return nil, err
	}
	return a, nil
}

// otherSpellingExists reports whether an account exists whose name is
// username in other letter case or with other spaces at the ends. SQLite
// folds the case of ASCII letters only.
func otherSpellingExists(ctx context.Context, q store.Querier, username string) (bool, error) {
	var n int64
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE LOWER(TRIM(username)) = LOWER(TRIM(?)) AND username <> ?`,
		username, username).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("look for another spelling of a user name: %w", err)
	}
	return n > 0, nil
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

// setRecoveryCodes stores new recovery codes (their hashes as JSON).
func setRecoveryCodes(ctx context.Context, q store.Querier, id int64, codesJSON string) error {
	if _, err := q.Exec(ctx, `UPDATE users SET totp_recovery_codes = ? WHERE id = ?`, codesJSON, id); err != nil {
		return fmt.Errorf("store new recovery codes of user %d: %w", id, err)
	}
	return nil
}

// disableTOTP deletes the secret and the recovery codes. The API server's
// users table takes no NULL secret: there the secret is written as "",
// which reads as none on both sides (DECISIONS D2).
func disableTOTP(ctx context.Context, db *store.DB, id int64) error {
	var secret any
	if db.Shape().Table("users").Column("totp_secret").NotNull {
		secret = ""
	}
	query := db.Pick(
		`UPDATE users SET totp_enabled = 0, totp_secret = ?, totp_recovery_codes = NULL WHERE id = ?`,
		`UPDATE users SET totp_enabled = FALSE, totp_secret = ?, totp_recovery_codes = NULL WHERE id = ?`)
	if _, err := db.Exec(ctx, query, secret, id); err != nil {
		return fmt.Errorf("turn off two-factor sign-in for user %d: %w", id, err)
	}
	return nil
}

// useRecoveryCode replaces the recovery codes read as old with left. It is
// false when the stored list is no longer old.
func useRecoveryCode(ctx context.Context, q store.Querier, id int64, old, left string) (bool, error) {
	result, err := q.Exec(ctx, `UPDATE users SET totp_recovery_codes = ? WHERE id = ? AND totp_recovery_codes = ?`, left, id, old)
	if err != nil {
		return false, fmt.Errorf("store the recovery codes of user %d: %w", id, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store the recovery codes of user %d: %w", id, err)
	}
	return n == 1, nil
}

// ---- Access tokens ----

// AccessToken is a row of access_tokens.
type AccessToken struct {
	ID store.Int64String `json:"id"`
	// Token is what the row holds in place of the token
	// (store.StoredAccessToken of the hash), never the token itself.
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
	// Kind is TokenKindDevice for a token an older console issued for a
	// device, TokenKindAccount for one an account's sign-in made.
	Kind nullText `json:"token_kind"`
}

// The kinds of access tokens (access_tokens.token_kind). A row written by
// anything that does not name the kind, an older console among them, is an
// account's token: the column's default.
const (
	TokenKindAccount = "account"
	TokenKindDevice  = "device"
)

// IsDeviceToken reports whether the console issued the token for a device.
// Only such a token proves a device; an account's sign-in never makes one,
// whatever client id it stored.
func (t *AccessToken) IsDeviceToken() bool {
	return t != nil && t.Kind.String == TokenKindDevice
}

const accessTokenColumns = `id, token, token_hash, user_id, client_id, client_uuid, created_at, expires_at,
	last_used, ip_address, revoked, token_kind`

// createAccessToken stores a token as its hash only: the token column holds
// store.StoredAccessToken of the hash, never the token.
func createAccessToken(ctx context.Context, q store.Querier, kind, token string, userID int64, clientID, clientUUID, expiresAt, ip string) error {
	hash := HashAccessToken(token)
	_, err := q.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address, token_kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		store.StoredAccessToken(hash), hash, userID, clientID, clientUUID, expiresAt, ip, kind)
	if err != nil {
		return fmt.Errorf("store an access token: %w", err)
	}
	return nil
}

// getAccessToken looks a token up by its hash. Revoked and expired rows do
// not count. It returns nil when there is none.
func getAccessToken(ctx context.Context, q store.Querier, token string) (*AccessToken, error) {
	live := q.Pick(`revoked = 0 AND expires_at > datetime('now')`, `NOT revoked AND expires_at > NOW()`)
	var t AccessToken
	err := store.SelectRow(ctx, q,
		`SELECT `+accessTokenColumns+` FROM access_tokens WHERE token_hash = ? AND `+live, HashAccessToken(token)).
		Scan(&t.ID, &t.Token, &t.TokenHash, &t.UserID, &t.ClientID, &t.ClientUUID, &t.CreatedAt, &t.ExpiresAt,
			&t.LastUsed, &t.IPAddress, &t.Revoked, &t.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up an access token: %w", err)
	}
	return &t, nil
}

func touchAccessToken(ctx context.Context, q store.Querier, token string) error {
	query := `UPDATE access_tokens SET last_used = ` + store.NowSQL(q.Dialect()) + ` WHERE token_hash = ?`
	if _, err := q.Exec(ctx, query, HashAccessToken(token)); err != nil {
		return fmt.Errorf("note the use of an access token: %w", err)
	}
	return nil
}

// The revocations of an account's tokens (a new sign-in of the client, a
// sign-out, a new password) leave the device tokens alone: their user is
// only the account that had them issued.

func revokeUserClientTokens(ctx context.Context, q store.Querier, userID int64, clientUUID string) error {
	query := q.Pick(
		`UPDATE access_tokens SET revoked = 1 WHERE user_id = ? AND client_uuid = ? AND token_kind <> ?`,
		`UPDATE access_tokens SET revoked = TRUE WHERE user_id = ? AND client_uuid = ? AND token_kind <> ?`)
	if _, err := q.Exec(ctx, query, userID, clientUUID, TokenKindDevice); err != nil {
		return fmt.Errorf("revoke the tokens of user %d for one client: %w", userID, err)
	}
	return nil
}

// revokeDeviceTokens revokes the device tokens issued for the device: the
// console's and the API server's.
func revokeDeviceTokens(ctx context.Context, q store.Querier, deviceID string) error {
	query := q.Pick(
		`UPDATE access_tokens SET revoked = 1 WHERE token_kind = ? AND client_id = ?`,
		`UPDATE access_tokens SET revoked = TRUE WHERE token_kind = ? AND client_id = ?`)
	if _, err := q.Exec(ctx, query, TokenKindDevice, deviceID); err != nil {
		return fmt.Errorf("revoke the device tokens of %s: %w", deviceID, err)
	}
	return revokeServerDeviceTokens(ctx, q, deviceID)
}

// ServerDeviceTokenCreator is created_by of a device token in
// device_tokens: one the API server or the console issued for a device it
// admitted (db.DeviceCredentialCreator). Enrollment tokens an operator
// made carry the operator's name and are no device tokens.
const ServerDeviceTokenCreator = "system"

// revokeServerDeviceTokens revokes the device tokens in device_tokens
// issued for the device. A database without the table has none.
func revokeServerDeviceTokens(ctx context.Context, q store.Querier, deviceID string) error {
	query := `UPDATE device_tokens SET status = 'revoked', revoked_at = ` + store.NowSQL(q.Dialect()) + `
		WHERE peer_id = ? AND created_by = ? AND status NOT IN ('revoked', 'expired')`
	if _, err := q.Exec(ctx, query, deviceID, ServerDeviceTokenCreator); err != nil && !missingTable(q, err) {
		return fmt.Errorf("revoke the server's device tokens of %s: %w", deviceID, err)
	}
	return nil
}

// createServerDeviceToken stores a device token in device_tokens for the
// device, as the API server stores the ones it issues: its hash, the first
// eight characters, active, without a limit of uses and without an expiry.
func createServerDeviceToken(ctx context.Context, q store.Querier, deviceID, token, note string) error {
	_, err := q.Exec(ctx, `INSERT INTO device_tokens (token_hash, token_prefix, name, peer_id, status, max_uses, use_count, created_by, note)
		VALUES (?, ?, ?, ?, 'active', 0, 0, ?, ?)`,
		HashDeviceToken(token), token[:8], "Auto-"+deviceID, deviceID, ServerDeviceTokenCreator, note)
	if err != nil {
		return fmt.Errorf("store a device token of %s: %w", deviceID, err)
	}
	return nil
}

// lookupServerDeviceToken returns the device token in device_tokens with
// this hash that was issued for a device, or nil. A database without the
// table has none.
func lookupServerDeviceToken(ctx context.Context, q store.Querier, tokenHash string) (*enrollmentToken, error) {
	var t enrollmentToken
	err := store.SelectRow(ctx, q,
		`SELECT peer_id, status, max_uses, use_count, expires_at FROM device_tokens WHERE token_hash = ? AND created_by = ?`,
		tokenHash, ServerDeviceTokenCreator).
		Scan(&t.peerID, &t.status, &t.maxUses, &t.useCount, &t.expiresAt)
	switch {
	case err == nil:
		return &t, nil
	case errors.Is(err, sql.ErrNoRows), missingTable(q, err):
		return nil, nil
	}
	return nil, fmt.Errorf("look up a device token of the server: %w", err)
}

// missingTable reports whether err says a table the statement names is not
// there.
func missingTable(q store.Querier, err error) bool {
	return strings.Contains(err.Error(), q.Pick("no such table", "does not exist"))
}

func revokeAllUserTokens(ctx context.Context, q store.Querier, userID int64) error {
	query := q.Pick(
		`UPDATE access_tokens SET revoked = 1 WHERE user_id = ? AND token_kind <> ?`,
		`UPDATE access_tokens SET revoked = TRUE WHERE user_id = ? AND token_kind <> ?`)
	if _, err := q.Exec(ctx, query, userID, TokenKindDevice); err != nil {
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

// recentFailures returns the times of the newest failed attempts of the
// name from the address since its last successful one from there, newest
// first, at most limit.
func recentFailures(ctx context.Context, q store.Querier, username any, ip string, limit int) ([]store.Time, error) {
	query := q.Pick(
		`SELECT created_at FROM login_attempts WHERE username = ? AND ip_address = ? AND success = 0
			AND id > (SELECT COALESCE(MAX(id), 0) FROM login_attempts WHERE username = ? AND ip_address = ? AND success = 1)
			ORDER BY id DESC LIMIT ?`,
		`SELECT created_at FROM login_attempts WHERE username = ? AND ip_address = ? AND NOT success
			AND id > (SELECT COALESCE(MAX(id), 0) FROM login_attempts WHERE username = ? AND ip_address = ? AND success)
			ORDER BY id DESC LIMIT ?`)
	rows, err := store.Select(ctx, q, query, username, ip, username, ip, limit)
	if err != nil {
		return nil, fmt.Errorf("read the failed sign-in attempts: %w", err)
	}
	defer rows.Close()
	var times []store.Time
	for rows.Next() {
		var at store.Time
		if err := rows.Scan(&at); err != nil {
			return nil, fmt.Errorf("read a failed sign-in attempt: %w", err)
		}
		times = append(times, at)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the failed sign-in attempts: %w", err)
	}
	return times, nil
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

// deviceShutOut reports whether the signal server holds the device as
// banned or soft-deleted in peers. A device without a row there (one the
// console admitted before it reached the signal server) and a database
// without the table or its columns are not shut out.
func deviceShutOut(ctx context.Context, q store.Querier, id string) (bool, error) {
	var one int
	err := store.SelectRow(ctx, q, `SELECT 1 FROM peers WHERE id = ? AND (banned OR soft_deleted)`, id).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case q.Dialect() == store.SQLite && (strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such column")),
		q.Dialect() != store.SQLite && strings.Contains(err.Error(), "does not exist"):
		return false, nil
	}
	return false, fmt.Errorf("look up the ban of a device: %w", err)
}
