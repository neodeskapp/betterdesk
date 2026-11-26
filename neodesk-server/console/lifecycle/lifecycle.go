// Package lifecycle keeps what the console knows about an account's life
// beyond the users table: an optional end date (for contractors), whether
// the rule for inactive accounts leaves it alone, when it counts as active
// again after an admin enabled it, which notices it got, and an email
// address change that waits for its confirmation.
//
// The tables are the console's own (account_lifecycle, email_changes);
// the signal server never reads them. An account without a row has no end
// date and no exception. A confirmation link is <console>/confirm-email#<token>:
// the token is 32 random bytes, the table keeps only its SHA-256, and a
// link works once and until it expires. An account has at most one
// pending change: a new one replaces the one before.
package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// EmailChangeLifetime is how long a confirmation link works.
const EmailChangeLifetime = 24 * time.Hour

var tables = []struct{ sqlite, postgres string }{
	{`CREATE TABLE IF NOT EXISTS account_lifecycle (
			user_id INTEGER PRIMARY KEY,
			expires_ms INTEGER,
			expiry_notice_ms INTEGER,
			expired_ms INTEGER,
			inactive_exempt INTEGER NOT NULL DEFAULT 0,
			active_from_ms INTEGER,
			inactive_notice_ms INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS account_lifecycle (
			user_id BIGINT PRIMARY KEY,
			expires_ms BIGINT,
			expiry_notice_ms BIGINT,
			expired_ms BIGINT,
			inactive_exempt INTEGER NOT NULL DEFAULT 0,
			active_from_ms BIGINT,
			inactive_notice_ms BIGINT
		)`},
	{`CREATE TABLE IF NOT EXISTS email_changes (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			new_email TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			created_ms INTEGER NOT NULL,
			expires_ms INTEGER NOT NULL,
			used_ms INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS email_changes (
			token_hash TEXT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			new_email TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			created_ms BIGINT NOT NULL,
			expires_ms BIGINT NOT NULL,
			used_ms BIGINT
		)`},
	{`CREATE INDEX IF NOT EXISTS idx_email_changes_user ON email_changes(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_email_changes_user ON email_changes(user_id)`},
}

// Ensure creates the tables when they are missing. Running it again
// changes nothing.
func Ensure(ctx context.Context, db *store.DB) error {
	for _, t := range tables {
		if _, err := db.Exec(ctx, db.Pick(t.sqlite, t.postgres)); err != nil {
			return fmt.Errorf("account lifecycle: %w", err)
		}
	}
	return nil
}

// Store reads and writes the tables.
type Store struct {
	db  *store.DB
	now func() time.Time
}

// New returns a Store on db. A nil now is time.Now.
func New(db *store.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

// Now is the store's clock.
func (s *Store) Now() time.Time { return s.now() }

// State is an account's row. The zero State is an account without one.
type State struct {
	// ExpiresAt is the end date; zero for none.
	ExpiresAt time.Time
	// ExpiryNoticeAt is when the admins were told the end date is near.
	ExpiryNoticeAt time.Time
	// ExpiredAt is when the account was disabled for its end date.
	ExpiredAt time.Time
	// InactiveExempt keeps the rule for inactive accounts away from it.
	InactiveExempt bool
	// ActiveFrom is when the account was last enabled: inactivity counts
	// from here when that is later than its last sign-in.
	ActiveFrom time.Time
	// InactiveNoticeAt is when the person was told the account will be
	// disabled for inactivity.
	InactiveNoticeAt time.Time
}

func fromMS(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64).UTC()
}

func toMS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

const stateColumns = `user_id, expires_ms, expiry_notice_ms, expired_ms, inactive_exempt, active_from_ms, inactive_notice_ms`

func scanState(scan func(...any) error) (int64, State, error) {
	var (
		id                                   int64
		expires, notice, expired, from, idle sql.NullInt64
		exempt                               int64
	)
	if err := scan(&id, &expires, &notice, &expired, &exempt, &from, &idle); err != nil {
		return 0, State{}, err
	}
	return id, State{
		ExpiresAt: fromMS(expires), ExpiryNoticeAt: fromMS(notice), ExpiredAt: fromMS(expired),
		InactiveExempt: exempt != 0, ActiveFrom: fromMS(from), InactiveNoticeAt: fromMS(idle),
	}, nil
}

// Get returns the account's state.
func (s *Store) Get(ctx context.Context, userID int64) (State, error) {
	row := s.db.QueryRow(ctx, `SELECT `+stateColumns+` FROM account_lifecycle WHERE user_id = ?`, userID)
	_, st, err := scanState(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("account lifecycle: read %d: %w", userID, err)
	}
	return st, nil
}

// All returns the state of every account that has a row.
func (s *Store) All(ctx context.Context) (map[int64]State, error) {
	rows, err := s.db.Query(ctx, `SELECT `+stateColumns+` FROM account_lifecycle`)
	if err != nil {
		return nil, fmt.Errorf("account lifecycle: list: %w", err)
	}
	defer rows.Close()
	out := map[int64]State{}
	for rows.Next() {
		id, st, err := scanState(rows.Scan)
		if err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// put writes the account's state, making the row when it is missing.
func (s *Store) put(ctx context.Context, userID int64, st State) error {
	exempt := 0
	if st.InactiveExempt {
		exempt = 1
	}
	_, err := s.db.Exec(ctx, `INSERT INTO account_lifecycle (`+stateColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET
			expires_ms = excluded.expires_ms,
			expiry_notice_ms = excluded.expiry_notice_ms,
			expired_ms = excluded.expired_ms,
			inactive_exempt = excluded.inactive_exempt,
			active_from_ms = excluded.active_from_ms,
			inactive_notice_ms = excluded.inactive_notice_ms`,
		userID, toMS(st.ExpiresAt), toMS(st.ExpiryNoticeAt), toMS(st.ExpiredAt), exempt, toMS(st.ActiveFrom), toMS(st.InactiveNoticeAt))
	if err != nil {
		return fmt.Errorf("account lifecycle: write %d: %w", userID, err)
	}
	return nil
}

// Update reads the account's state, lets change alter it and writes it.
func (s *Store) Update(ctx context.Context, userID int64, change func(*State)) error {
	st, err := s.Get(ctx, userID)
	if err != nil {
		return err
	}
	change(&st)
	return s.put(ctx, userID, st)
}

// SetExpiry gives the account an end date, or takes it away for the zero
// time. A new date gets its own notice.
func (s *Store) SetExpiry(ctx context.Context, userID int64, at time.Time) error {
	return s.Update(ctx, userID, func(st *State) {
		st.ExpiresAt, st.ExpiryNoticeAt, st.ExpiredAt = at, time.Time{}, time.Time{}
	})
}

// Enabled notes that an admin enabled the account: inactivity counts from
// now, an end date that passed is taken away (the account would be
// disabled again at once otherwise), and the notices start over.
func (s *Store) Enabled(ctx context.Context, userID int64) error {
	now := s.now()
	return s.Update(ctx, userID, func(st *State) {
		st.ActiveFrom, st.InactiveNoticeAt = now.UTC(), time.Time{}
		if !st.ExpiresAt.IsZero() && !now.Before(st.ExpiresAt) {
			st.ExpiresAt, st.ExpiryNoticeAt, st.ExpiredAt = time.Time{}, time.Time{}, time.Time{}
		}
	})
}

// Forget deletes what the tables hold of an account that is deleted.
func (s *Store) Forget(ctx context.Context, userID int64) error {
	for _, q := range []string{`DELETE FROM account_lifecycle WHERE user_id = ?`, `DELETE FROM email_changes WHERE user_id = ?`} {
		if _, err := s.db.Exec(ctx, q, userID); err != nil {
			return fmt.Errorf("account lifecycle: forget %d: %w", userID, err)
		}
	}
	return nil
}

// Expired reports whether the account's end date has passed, for the
// sign-in. A database without the table has no end dates.
func Expired(ctx context.Context, db *store.DB, userID int64, now time.Time) (bool, error) {
	var expires sql.NullInt64
	err := db.QueryRow(ctx, `SELECT expires_ms FROM account_lifecycle WHERE user_id = ?`, userID).Scan(&expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil && strings.Contains(err.Error(), db.Pick("no such table", "does not exist")):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("account lifecycle: end date of %d: %w", userID, err)
	}
	return expires.Valid && now.UnixMilli() >= expires.Int64, nil
}

// ---- Email address changes ----

// ErrUnusable is a confirmation link that is unknown, used or expired, or
// whose account is gone or disabled.
var ErrUnusable = errors.New("email change link unknown, used or expired")

// keep is how long a used or expired change stays in the table.
const keep = 30 * 24 * time.Hour

// Change is an address change that waits for its confirmation.
type Change struct {
	UserID    int64
	Username  string
	Email     string
	ExpiresAt time.Time
	Expired   bool
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// IssueEmailChange makes the link that confirms the new address and
// returns its token, which exists only in the mail. The account's earlier
// change ends.
func (s *Store) IssueEmailChange(ctx context.Context, userID int64, email, by string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	expires := now.Add(EmailChangeLifetime)
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM email_changes WHERE user_id = ? OR expires_ms < ?`, userID, now.Add(-keep).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO email_changes (token_hash, user_id, new_email, created_by, created_ms, expires_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			hash(token), userID, email, by, now.UnixMilli(), expires.UnixMilli())
		return err
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("email change: issue: %w", err)
	}
	return token, expires, nil
}

// PendingEmails returns the changes not confirmed yet, by account; an
// expired one is there with Expired, until a new one or the clean-up.
func (s *Store) PendingEmails(ctx context.Context) (map[int64]Change, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id, new_email, expires_ms FROM email_changes WHERE used_ms IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("email change: list: %w", err)
	}
	defer rows.Close()
	now := s.now()
	out := map[int64]Change{}
	for rows.Next() {
		var c Change
		var expires int64
		if err := rows.Scan(&c.UserID, &c.Email, &expires); err != nil {
			return nil, err
		}
		c.ExpiresAt = time.UnixMilli(expires).UTC()
		c.Expired = !now.Before(c.ExpiresAt)
		out[c.UserID] = c
	}
	return out, rows.Err()
}

// PendingEmail returns the account's change that is not confirmed yet, or
// nil.
func (s *Store) PendingEmail(ctx context.Context, userID int64) (*Change, error) {
	all, err := s.PendingEmails(ctx)
	if err != nil {
		return nil, err
	}
	if c, ok := all[userID]; ok {
		return &c, nil
	}
	return nil, nil
}

// CancelEmailChange ends the account's pending change. It reports whether
// there was one.
func (s *Store) CancelEmailChange(ctx context.Context, userID int64) (bool, error) {
	res, err := s.db.Exec(ctx, `DELETE FROM email_changes WHERE user_id = ? AND used_ms IS NULL`, userID)
	if err != nil {
		return false, fmt.Errorf("email change: cancel: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) lookup(ctx context.Context, q store.Querier, token string) (Change, error) {
	if token == "" || len(token) > 200 {
		return Change{}, ErrUnusable
	}
	var (
		c        Change
		expires  int64
		used     sql.NullInt64
		name     sql.NullString
		disabled store.Bool
	)
	err := q.QueryRow(ctx, `SELECT e.user_id, e.new_email, e.expires_ms, e.used_ms, u.username, u.disabled FROM email_changes e
		JOIN users u ON u.id = e.user_id WHERE e.token_hash = ?`, hash(token)).Scan(&c.UserID, &c.Email, &expires, &used, &name, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Change{}, ErrUnusable
	}
	if err != nil {
		return Change{}, fmt.Errorf("email change: look up: %w", err)
	}
	c.ExpiresAt = time.UnixMilli(expires).UTC()
	c.Username = name.String
	if used.Valid || disabled.True() || !s.now().Before(c.ExpiresAt) {
		return Change{}, ErrUnusable
	}
	return c, nil
}

// LookupEmailChange returns the change of a token, or ErrUnusable.
func (s *Store) LookupEmailChange(ctx context.Context, token string) (Change, error) {
	return s.lookup(ctx, s.db, token)
}

// ConfirmEmailChange spends the link of a token and gives its account the
// new address, in one transaction: of two requests with the same token one
// wins. It returns the change, or ErrUnusable.
func (s *Store) ConfirmEmailChange(ctx context.Context, token string) (Change, error) {
	var change Change
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		c, err := s.lookup(ctx, tx, token)
		if err != nil {
			return err
		}
		res, err := tx.Exec(ctx, `UPDATE email_changes SET used_ms = ? WHERE token_hash = ? AND used_ms IS NULL`, s.now().UnixMilli(), hash(token))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrUnusable
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET email = ? WHERE id = ?`, c.Email, c.UserID); err != nil {
			return err
		}
		change = c
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrUnusable) {
			return Change{}, ErrUnusable
		}
		return Change{}, fmt.Errorf("email change: confirm: %w", err)
	}
	return change, nil
}

// ConfirmEmailLink is the address of a confirmation link on the console at
// base.
func ConfirmEmailLink(base, token string) string {
	return base + "/confirm-email#" + token
}
