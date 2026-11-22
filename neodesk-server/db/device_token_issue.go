package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DeviceCredentialCreator is created_by of a device token in
// device_tokens that the server or the console issued for a device at an
// enrollment, a pairing, a registration, an approval or a join: the
// device's own credential, held by no account, which the console's device
// routes and sockets accept like the device tokens an older console kept
// in access_tokens. Enrollment tokens an operator makes carry the
// operator's name and are no device tokens. Device tokens do not expire by
// time; a delete, a ban, a change of the device's id and a new token for
// the device end them.
const DeviceCredentialCreator = "system"

// sqliteRevokeDeviceCredentials revokes the device's device tokens in
// device_tokens but the row keep (0 for none).
func sqliteRevokeDeviceCredentials(tx *sql.Tx, id string, keep int64) error {
	if _, err := tx.Exec(`UPDATE device_tokens SET status = 'revoked', revoked_at = datetime('now')
		WHERE peer_id = ? AND created_by = ? AND id <> ? AND status NOT IN ('revoked', 'expired')`,
		id, DeviceCredentialCreator, keep); err != nil {
		return fmt.Errorf("db: revoke the device credentials of %s: %w", id, err)
	}
	return nil
}

// pgRevokeDeviceCredentials revokes the device's device tokens in
// device_tokens but the row keep (0 for none).
func pgRevokeDeviceCredentials(ctx context.Context, tx pgx.Tx, id string, keep int64) error {
	if _, err := tx.Exec(ctx, `UPDATE device_tokens SET status = 'revoked', revoked_at = NOW()
		WHERE peer_id = $1 AND created_by = $2 AND id <> $3 AND status NOT IN ('revoked', 'expired')`,
		id, DeviceCredentialCreator, keep); err != nil {
		return fmt.Errorf("db: revoke the device credentials of %s: %w", id, err)
	}
	return nil
}

// EndDeviceTokensExcept ends every device token of the device but the
// device_tokens row keep, in one transaction: its other credentials in
// device_tokens and the console's device tokens. A new token for a device
// calls it after storing the token.
func (s *SQLiteDB) EndDeviceTokensExcept(peerID string, keep int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := sqliteRevokeDeviceCredentials(tx, peerID, keep); err != nil {
		return err
	}
	if err := sqliteRevokeConsoleDeviceTokens(tx, peerID); err != nil {
		return err
	}
	return tx.Commit()
}

// EndDeviceTokensExcept ends every device token of the device but the
// device_tokens row keep, in one transaction: its other credentials in
// device_tokens and the console's device tokens. A new token for a device
// calls it after storing the token.
func (pg *PostgresDB) EndDeviceTokensExcept(peerID string, keep int64) error {
	tx, err := pg.pool.Begin(pg.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(pg.ctx)
	if err := pgRevokeDeviceCredentials(pg.ctx, tx, peerID, keep); err != nil {
		return err
	}
	if err := pgRevokeConsoleDeviceTokens(pg.ctx, tx, peerID); err != nil {
		return err
	}
	return tx.Commit(pg.ctx)
}
