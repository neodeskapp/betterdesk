package authn

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Browsers an account signed in from, for the notice of a sign-in from a
// new one. A browser is a long-lived random cookie the console sets at
// sign-in (routes/auth); known_browsers keeps the SHA-256 of its value per
// account, and when it was first and last seen. Nothing else about the
// browser is kept, and a browser that sends no cookie (a private window,
// cleared cookies) is a new one. A browser unseen for 400 days is
// forgotten.

// browserHash is what the table keeps of a cookie value.
func browserHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// NoteBrowser records that the account signed in from the browser whose
// cookie value is given. It reports whether the browser is new to an
// account that signed in from another browser before: the first browser
// of an account (and the first sign-in after the console learnt to tell
// browsers apart) is no news.
func (s *Service) NoteBrowser(ctx context.Context, userID int64, browser string) (bool, error) {
	if browser == "" {
		return false, nil
	}
	now := s.now()
	hash := browserHash(browser)
	isNew := false
	err := s.db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM known_browsers WHERE user_id = ? AND last_ms < ?`, userID, now.Add(-knownBrowserLife).UnixMilli()); err != nil {
			return err
		}
		var first int64
		err := tx.QueryRow(ctx, `SELECT first_ms FROM known_browsers WHERE user_id = ? AND browser_hash = ?`, userID, hash).Scan(&first)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE known_browsers SET last_ms = ? WHERE user_id = ? AND browser_hash = ?`, now.UnixMilli(), userID, hash)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var others int64
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM known_browsers WHERE user_id = ?`, userID).Scan(&others); err != nil {
			return err
		}
		isNew = others > 0
		_, err = tx.Exec(ctx, `INSERT INTO known_browsers (user_id, browser_hash, first_ms, last_ms) VALUES (?, ?, ?, ?)`,
			userID, hash, now.UnixMilli(), now.UnixMilli())
		return err
	})
	if noTable(s.db, err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("note the browser of user %d: %w", userID, err)
	}
	return isNew, nil
}
