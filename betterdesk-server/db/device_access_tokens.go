package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// The console's device tokens live in access_tokens, a table the console
// creates and migrates in the database it shares with this server: rows
// whose token_kind is "device", bound to a device by client_id. The
// console's device routes and sockets accept them. A delete, soft or hard,
// and a ban revoke them in the same transaction as the change to peers,
// whichever port or command made it; a restore or an unban gives none
// back. A database the console has not migrated yet (no access_tokens, or
// one without token_kind, which holds no device tokens) has nothing to
// revoke.

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

// sqliteRevokeDeviceAccessTokens revokes the console's device tokens of
// the device.
func sqliteRevokeDeviceAccessTokens(tx *sql.Tx, id string) error {
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

// pgRevokeDeviceAccessTokens revokes the console's device tokens of the
// device.
func pgRevokeDeviceAccessTokens(ctx context.Context, tx pgx.Tx, id string) error {
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
