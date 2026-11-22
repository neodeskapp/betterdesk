package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A device's device tokens are the console's, in access_tokens, a table
// the console creates and migrates in the database it shares with this
// server (rows whose token_kind is "device", bound to a device by
// client_id), and the ones the server issued for it in device_tokens
// (created_by DeviceCredentialCreator). The console's device routes and
// sockets accept both. They do not expire by time. A delete, soft or hard,
// a ban and a change of the device's id revoke both kinds in the same
// transaction as the change to peers, whichever port or command made it; a
// restore or an unban gives none back, and a renamed device gets a new
// token under its new id the way it got its first. A database the console
// has not migrated yet (no access_tokens, or one without token_kind, which
// holds no device tokens) has no console tokens to revoke.

// sqliteHasDeviceAccessTokens reports whether access_tokens is there with
// its token_kind column.
func sqliteHasDeviceAccessTokens(tx *sql.Tx) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('access_tokens') WHERE name = 'token_kind'`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("db: look for access_tokens: %w", err)
	}
	return n > 0, nil
}

// sqliteEndDeviceTokens revokes the device tokens of the device, both
// kinds.
func sqliteEndDeviceTokens(tx *sql.Tx, id string) error {
	if err := sqliteRevokeConsoleDeviceTokens(tx, id); err != nil {
		return err
	}
	return sqliteRevokeDeviceCredentials(tx, id, 0)
}

// sqliteRevokeConsoleDeviceTokens revokes the console's device tokens of
// the device.
func sqliteRevokeConsoleDeviceTokens(tx *sql.Tx, id string) error {
	ok, err := sqliteHasDeviceAccessTokens(tx)
	if err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE access_tokens SET revoked = 1 WHERE token_kind = 'device' AND client_id = ?`, id); err != nil {
		return fmt.Errorf("db: revoke the device tokens of %s: %w", id, err)
	}
	return nil
}

// sqliteEndRenamedDeviceTokens revokes the device tokens of a device
// whose id changes, both kinds, and any still bound to the new id, of a
// device that had it before. It runs after the rename moved the rows of
// device_tokens to the new id.
func sqliteEndRenamedDeviceTokens(tx *sql.Tx, oldID, newID string) error {
	for _, id := range []string{oldID, newID} {
		if err := sqliteRevokeDeviceCredentials(tx, id, 0); err != nil {
			return err
		}
	}
	ok, err := sqliteHasDeviceAccessTokens(tx)
	if err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE access_tokens SET revoked = 1 WHERE token_kind = 'device' AND client_id IN (?, ?)`, oldID, newID); err != nil {
		return fmt.Errorf("db: revoke the device tokens of %s renamed to %s: %w", oldID, newID, err)
	}
	return nil
}

// pgHasDeviceAccessTokens reports whether access_tokens is in the current
// schema with its token_kind column.
func pgHasDeviceAccessTokens(ctx context.Context, tx pgx.Tx) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'access_tokens' AND column_name = 'token_kind'
	)`).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("db: look for access_tokens: %w", err)
	}
	return ok, nil
}

// pgEndDeviceTokens revokes the device tokens of the device, both kinds.
func pgEndDeviceTokens(ctx context.Context, tx pgx.Tx, id string) error {
	if err := pgRevokeConsoleDeviceTokens(ctx, tx, id); err != nil {
		return err
	}
	return pgRevokeDeviceCredentials(ctx, tx, id, 0)
}

// pgRevokeConsoleDeviceTokens revokes the console's device tokens of the
// device.
func pgRevokeConsoleDeviceTokens(ctx context.Context, tx pgx.Tx, id string) error {
	ok, err := pgHasDeviceAccessTokens(ctx, tx)
	if err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE access_tokens SET revoked = TRUE WHERE token_kind = 'device' AND client_id = $1`, id); err != nil {
		return fmt.Errorf("db: revoke the device tokens of %s: %w", id, err)
	}
	return nil
}

// pgEndRenamedDeviceTokens revokes the device tokens of a device whose id
// changes, both kinds, and any still bound to the new id, of a device that
// had it before. It runs after the rename moved the rows of device_tokens
// to the new id.
func pgEndRenamedDeviceTokens(ctx context.Context, tx pgx.Tx, oldID, newID string) error {
	for _, id := range []string{oldID, newID} {
		if err := pgRevokeDeviceCredentials(ctx, tx, id, 0); err != nil {
			return err
		}
	}
	ok, err := pgHasDeviceAccessTokens(ctx, tx)
	if err != nil || !ok {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE access_tokens SET revoked = TRUE WHERE token_kind = 'device' AND client_id IN ($1, $2)`, oldID, newID); err != nil {
		return fmt.Errorf("db: revoke the device tokens of %s renamed to %s: %w", oldID, newID, err)
	}
	return nil
}
