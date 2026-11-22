package backup

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// The API refuses a sign-in token issued before the account's
// users.credentials_changed_at (Unix seconds), which a trigger of the
// server's schema sets when an update changes the password hash or the
// second factor. A restore writes the accounts with INSERTs, which the
// trigger does not see, so the restore sets the column itself
// (endReplacedCredentials): an account whose password hash or second
// factor the restore replaced, or that the database did not have before,
// gets the time of the restore, and every other one the later of its time
// in the archive and its time before. No token issued before the restore
// for credentials the restore replaced stays good, and an account's time
// never goes back.

// credentialsChangedColumn is the column the API compares a token with.
const credentialsChangedColumn = "credentials_changed_at"

// credential is what of an account ends its tokens when it changes.
type credential struct {
	// key is the password hash and, while the second factor is on, its
	// secret: the changes the trigger counts
	key       string
	changedAt int64 // 0 for NULL
}

type queryFunc func(ctx context.Context, query string, args ...any) (*sql.Rows, error)
type execFunc func(ctx context.Context, query string, args ...any) (sql.Result, error)

// readCredentials reads the credentials of every account by user name.
// withTime is false for a users table without credentials_changed_at.
func readCredentials(ctx context.Context, query queryFunc, withTime bool) (map[string]credential, error) {
	changedAt := "NULL"
	if withTime {
		changedAt = credentialsChangedColumn
	}
	rows, err := query(ctx, `SELECT username, COALESCE(password_hash, ''), COALESCE(CAST(totp_enabled AS TEXT), ''),
		COALESCE(totp_secret, ''), `+changedAt+` FROM users`)
	if err != nil {
		return nil, fmt.Errorf("read the accounts' credentials: %w", err)
	}
	defer rows.Close()
	out := map[string]credential{}
	for rows.Next() {
		var name, hash, enabled, secret string
		var at any
		if err := rows.Scan(&name, &hash, &enabled, &secret, &at); err != nil {
			return nil, fmt.Errorf("read the accounts' credentials: %w", err)
		}
		key := hash + "\x00off"
		if secondFactorOn(enabled) {
			key = hash + "\x00on\x00" + secret
		}
		out[name] = credential{key: key, changedAt: unixSeconds(at)}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the accounts' credentials: %w", err)
	}
	return out, nil
}

// secondFactorOn reads totp_enabled as text: an integer on SQLite, a
// boolean on PostgreSQL.
func secondFactorOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "0.0", "false", "f":
		return false
	}
	return true
}

// unixSeconds reads credentials_changed_at as the driver returns it; a
// value that is no number counts as none.
func unixSeconds(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseFloat(x, 64)
		return int64(n)
	case []byte:
		n, _ := strconv.ParseFloat(string(x), 64)
		return int64(n)
	}
	return 0
}

// endReplacedCredentials sets credentials_changed_at of every account the
// table holds after a restore, against before, the accounts as they were
// before it: now for an account whose credentials differ or that was not
// there, else the later of the two times.
func endReplacedCredentials(ctx context.Context, query queryFunc, exec execFunc, before map[string]credential, now int64) error {
	after, err := readCredentials(ctx, query, true)
	if err != nil {
		return err
	}
	for name, a := range after {
		at := a.changedAt
		b, had := before[name]
		switch {
		case !had || b.key != a.key:
			at = now
		case b.changedAt > at:
			at = b.changedAt
		}
		if at == a.changedAt {
			continue
		}
		if _, err := exec(ctx, `UPDATE users SET `+credentialsChangedColumn+` = ? WHERE username = ?`, at, name); err != nil {
			return fmt.Errorf("end the sign-in tokens of %s: %w", name, err)
		}
	}
	return nil
}
