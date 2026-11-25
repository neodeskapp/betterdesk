// Package passwordlinks keeps the one-time links that let a person choose
// the password of a console account: the invite a new account gets by
// mail, and a password reset sent by mail.
//
// A link is <console>/set-password#<token>. The token is 32 random bytes;
// the table password_links keeps only its SHA-256, the account, what the
// link is for and until when it works. An account has at most one link:
// a new one replaces the one before. Using a link sets the password and
// spends the link; so does any other new password of the account.
package passwordlinks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// What a link is for.
const (
	// PurposeInvite is the link of a new account, which has no password
	// anyone knows yet.
	PurposeInvite = "invite"
	// PurposeReset is a new password for an account that had one.
	PurposeReset = "reset"
)

// How long a link works.
const (
	InviteLifetime = 7 * 24 * time.Hour
	ResetLifetime  = 24 * time.Hour
	// SelfResetLifetime is a reset the person asked for on the sign-in
	// page, where nobody checked who asked.
	SelfResetLifetime = time.Hour
)

// Lifetime is how long a link for the purpose works.
func Lifetime(purpose string) time.Duration {
	if purpose == PurposeInvite {
		return InviteLifetime
	}
	return ResetLifetime
}

// ErrUnusable is a link that is unknown, used, expired, or whose account
// is gone or disabled. The routes answer all of them the same.
var ErrUnusable = errors.New("password link unknown, used or expired")

// keep is how long a used or expired link stays in the table: the users
// page says an invite expired for that long.
const keep = 30 * 24 * time.Hour

var tables = []struct{ sqlite, postgres string }{
	{`CREATE TABLE IF NOT EXISTS password_links (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			purpose TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			created_ms INTEGER NOT NULL,
			expires_ms INTEGER NOT NULL,
			used_ms INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS password_links (
			token_hash TEXT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			purpose TEXT NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			created_ms BIGINT NOT NULL,
			expires_ms BIGINT NOT NULL,
			used_ms BIGINT
		)`},
	{`CREATE INDEX IF NOT EXISTS idx_password_links_user ON password_links(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_password_links_user ON password_links(user_id)`},
}

// Ensure creates the table when it is missing. Running it again changes
// nothing.
func Ensure(ctx context.Context, db *store.DB) error {
	for _, t := range tables {
		if _, err := db.Exec(ctx, db.Pick(t.sqlite, t.postgres)); err != nil {
			return fmt.Errorf("password links: %w", err)
		}
	}
	return nil
}

// Store issues, checks and spends links.
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

// Link is a usable link.
type Link struct {
	UserID    int64
	Username  string
	Purpose   string
	ExpiresAt time.Time
}

// hash is what the table keeps of a token.
func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Issue makes a new link for the account and returns its token, which
// exists only in the answer and the mail. The account's earlier links stop
// working.
func (s *Store) Issue(ctx context.Context, userID int64, purpose, by string) (token string, expires time.Time, err error) {
	return s.IssueFor(ctx, userID, purpose, by, Lifetime(purpose))
}

// IssueFor is Issue with a lifetime of its own.
func (s *Store) IssueFor(ctx context.Context, userID int64, purpose, by string, lifetime time.Duration) (token string, expires time.Time, err error) {
	if purpose != PurposeInvite && purpose != PurposeReset {
		return "", time.Time{}, fmt.Errorf("password links: unknown purpose %q", purpose)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	expires = now.Add(lifetime)
	err = s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM password_links WHERE user_id = ? OR expires_ms < ?`, userID, now.Add(-keep).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO password_links (token_hash, user_id, purpose, created_by, created_ms, expires_ms) VALUES (?, ?, ?, ?, ?, ?)`,
			hash(token), userID, purpose, by, now.UnixMilli(), expires.UnixMilli())
		return err
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("password links: issue: %w", err)
	}
	return token, expires, nil
}

// lookup reads the usable link of a token on q.
func (s *Store) lookup(ctx context.Context, q store.Querier, token string) (Link, error) {
	if token == "" || len(token) > 200 {
		return Link{}, ErrUnusable
	}
	var (
		l        Link
		expires  int64
		used     sql.NullInt64
		name     sql.NullString
		disabled store.Bool
	)
	err := q.QueryRow(ctx, `SELECT p.user_id, p.purpose, p.expires_ms, p.used_ms, u.username, u.disabled FROM password_links p
		JOIN users u ON u.id = p.user_id WHERE p.token_hash = ?`,
		hash(token)).Scan(&l.UserID, &l.Purpose, &expires, &used, &name, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrUnusable
	}
	if err != nil {
		return Link{}, fmt.Errorf("password links: look up: %w", err)
	}
	l.ExpiresAt = time.UnixMilli(expires).UTC()
	l.Username = name.String
	if used.Valid || disabled.True() || !s.now().Before(l.ExpiresAt) {
		return Link{}, ErrUnusable
	}
	return l, nil
}

// Lookup returns the link of a token, or ErrUnusable.
func (s *Store) Lookup(ctx context.Context, token string) (Link, error) {
	return s.lookup(ctx, s.db, token)
}

// Redeem spends the link of a token and gives its account the password
// hash, in one transaction: of two requests with the same token one wins.
// It returns the link as it was, or ErrUnusable.
func (s *Store) Redeem(ctx context.Context, token, passwordHash string) (Link, error) {
	var link Link
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		l, err := s.lookup(ctx, tx, token)
		if err != nil {
			return err
		}
		res, err := tx.Exec(ctx, `UPDATE password_links SET used_ms = ? WHERE token_hash = ? AND used_ms IS NULL`, s.now().UnixMilli(), hash(token))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrUnusable
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, l.UserID); err != nil {
			return err
		}
		link = l
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrUnusable) {
			return Link{}, ErrUnusable
		}
		return Link{}, fmt.Errorf("password links: redeem: %w", err)
	}
	return link, nil
}

// IssuedSince reports whether the account got a link that is not used yet
// after t, so a request for another one can be left alone.
func (s *Store) IssuedSince(ctx context.Context, userID int64, t time.Time) (bool, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM password_links WHERE user_id = ? AND used_ms IS NULL AND created_ms > ?`, userID, t.UnixMilli()).Scan(&n); err != nil {
		return false, fmt.Errorf("password links: recent: %w", err)
	}
	return n > 0, nil
}

// Revoke ends the account's links, for a password set another way or an
// account that is deleted.
func (s *Store) Revoke(ctx context.Context, userID int64) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM password_links WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("password links: revoke: %w", err)
	}
	return nil
}

// Invite is the state of an account's invite that has not been used.
type Invite struct {
	ExpiresAt time.Time
	Expired   bool
}

// Invites returns the invites not used yet, by account. An expired one is
// in the list for 30 days, so the users page can say so.
func (s *Store) Invites(ctx context.Context) (map[int64]Invite, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id, expires_ms FROM password_links WHERE purpose = ? AND used_ms IS NULL`, PurposeInvite)
	if err != nil {
		return nil, fmt.Errorf("password links: invites: %w", err)
	}
	defer rows.Close()
	now := s.now()
	out := map[int64]Invite{}
	for rows.Next() {
		var id, expires int64
		if err := rows.Scan(&id, &expires); err != nil {
			return nil, err
		}
		at := time.UnixMilli(expires).UTC()
		out[id] = Invite{ExpiresAt: at, Expired: !now.Before(at)}
	}
	return out, rows.Err()
}

// SetPasswordLink is the address of a link on the console at base.
func SetPasswordLink(base, token string) string {
	return base + "/set-password#" + token
}
