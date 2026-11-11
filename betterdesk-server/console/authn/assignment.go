package authn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
)

// AssignmentError is a reference to an account that names none; its text
// is what the client API answered.
type AssignmentError struct{ Text string }

func (e AssignmentError) Error() string { return e.Text }

// ResolveUserAssignmentKey is resolveUserAssignmentKey of the client API's
// strategies: the guid of the account whose guid, name or id is ref
// (trimmed). An account without a guid gets a random one.
func (s *Service) ResolveUserAssignmentKey(ctx context.Context, ref string) (string, error) {
	key := jsTrim(ref)
	if key == "" {
		return "", AssignmentError{Text: "empty user reference"}
	}
	var id int64
	var guid string
	err := s.db.QueryRow(ctx, s.db.Pick(
		`SELECT id, COALESCE(guid, '') AS guid FROM users WHERE guid = ? OR username = ? OR CAST(id AS TEXT) = ? LIMIT 1`,
		`SELECT id, COALESCE(guid, '') AS guid FROM users WHERE guid = ? OR username = ? OR id::text = ? LIMIT 1`,
	), key, key, key).Scan(&id, &guid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", AssignmentError{Text: "user not found"}
	}
	if err != nil {
		return "", fmt.Errorf("resolve user %q: %w", key, err)
	}
	if guid != "" {
		return guid, nil
	}
	guid, err = randomUUID()
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(ctx, `UPDATE users SET guid = ? WHERE id = ?`, guid, id); err != nil {
		return "", fmt.Errorf("give user %d a guid: %w", id, err)
	}
	return guid, nil
}

// randomUUID is crypto.randomUUID: a version 4 UUID in lower case.
func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("make a guid: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
