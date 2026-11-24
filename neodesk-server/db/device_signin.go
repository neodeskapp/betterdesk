package db

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Saved sign-in details of devices (table device_signin). The values are
// sealed by the caller (crypto/devicevault); this layer stores them as
// they come and never sees a readable password.

const deviceSigninColumns = `device_id, user_nonce, user_cipher, pass_nonce, pass_cipher, key_id, form, auto_signin, sealed_only, updated_at, updated_by`

func (s *SQLiteDB) GetDeviceSignin(deviceID string) (*DeviceSignin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var d DeviceSignin
	var auto, sealed int
	err := s.db.QueryRow(`SELECT `+deviceSigninColumns+` FROM device_signin WHERE device_id = ?`, deviceID).
		Scan(&d.DeviceID, &d.UserNonce, &d.UserCipher, &d.PassNonce, &d.PassCipher, &d.KeyID, &d.Form, &auto, &sealed, &d.UpdatedAt, &d.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Auto = auto != 0
	d.SealedOnly = sealed != 0
	return &d, nil
}

func (s *SQLiteDB) SaveDeviceSignin(d *DeviceSignin) error {
	if d == nil || d.DeviceID == "" {
		return errors.New("db: SaveDeviceSignin: no device")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	auto, sealed := 0, 0
	if d.Auto {
		auto = 1
	}
	if d.SealedOnly {
		sealed = 1
	}
	_, err := s.db.Exec(`INSERT INTO device_signin (`+deviceSigninColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
		  user_nonce = excluded.user_nonce, user_cipher = excluded.user_cipher,
		  pass_nonce = excluded.pass_nonce, pass_cipher = excluded.pass_cipher,
		  key_id = excluded.key_id, form = excluded.form, auto_signin = excluded.auto_signin,
		  sealed_only = excluded.sealed_only,
		  updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		d.DeviceID, d.UserNonce, d.UserCipher, d.PassNonce, d.PassCipher, d.KeyID, d.Form, auto, sealed, d.UpdatedAt, d.UpdatedBy)
	return err
}

func (s *SQLiteDB) MarkDeviceSigninSealed(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE device_signin SET sealed_only = 1 WHERE device_id = ?`, deviceID)
	return err
}

func (s *SQLiteDB) DeleteDeviceSignin(deviceID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM device_signin WHERE device_id = ?`, deviceID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (pg *PostgresDB) GetDeviceSignin(deviceID string) (*DeviceSignin, error) {
	var d DeviceSignin
	err := pg.pool.QueryRow(pg.ctx, `SELECT `+deviceSigninColumns+` FROM device_signin WHERE device_id = $1`, deviceID).
		Scan(&d.DeviceID, &d.UserNonce, &d.UserCipher, &d.PassNonce, &d.PassCipher, &d.KeyID, &d.Form, &d.Auto, &d.SealedOnly, &d.UpdatedAt, &d.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (pg *PostgresDB) SaveDeviceSignin(d *DeviceSignin) error {
	if d == nil || d.DeviceID == "" {
		return errors.New("db: SaveDeviceSignin: no device")
	}
	_, err := pg.pool.Exec(pg.ctx, `INSERT INTO device_signin (`+deviceSigninColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (device_id) DO UPDATE SET
		  user_nonce = EXCLUDED.user_nonce, user_cipher = EXCLUDED.user_cipher,
		  pass_nonce = EXCLUDED.pass_nonce, pass_cipher = EXCLUDED.pass_cipher,
		  key_id = EXCLUDED.key_id, form = EXCLUDED.form, auto_signin = EXCLUDED.auto_signin,
		  sealed_only = EXCLUDED.sealed_only,
		  updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		d.DeviceID, d.UserNonce, d.UserCipher, d.PassNonce, d.PassCipher, d.KeyID, d.Form, d.Auto, d.SealedOnly, d.UpdatedAt, d.UpdatedBy)
	return err
}

func (pg *PostgresDB) MarkDeviceSigninSealed(deviceID string) error {
	_, err := pg.pool.Exec(pg.ctx, `UPDATE device_signin SET sealed_only = TRUE WHERE device_id = $1`, deviceID)
	return err
}

func (pg *PostgresDB) DeleteDeviceSignin(deviceID string) (bool, error) {
	tag, err := pg.pool.Exec(pg.ctx, `DELETE FROM device_signin WHERE device_id = $1`, deviceID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Organizations' device passwords: the rows not sealed with keyID, and
// their move to the key file in one transaction (MigrateOrgPeerCredentials
// in package api).

func (s *SQLiteDB) ListOrgPeerCredentialsNotSealedWith(keyID string) ([]*OrgPeerCredential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT org_id, peer_id, ciphertext, nonce, key_id, COALESCE(updated_at,''), COALESCE(updated_by,'')
		 FROM org_peer_credentials WHERE key_id <> ? ORDER BY org_id, peer_id`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrgPeerCredential
	for rows.Next() {
		var c OrgPeerCredential
		if err := rows.Scan(&c.OrgID, &c.PeerID, &c.Ciphertext, &c.Nonce, &c.KeyID, &c.UpdatedAt, &c.UpdatedBy); err != nil {
			return nil, err
		}
		c.PasswordSet = c.Ciphertext != ""
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (s *SQLiteDB) ResealOrgPeerCredentials(list []OrgPeerCredentialReseal) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	changed := 0
	for _, r := range list {
		res, err := tx.Exec(`UPDATE org_peer_credentials SET ciphertext = ?, nonce = ?, key_id = ?
			WHERE org_id = ? AND peer_id = ? AND key_id = ?`,
			r.Ciphertext, r.Nonce, r.KeyID, r.OrgID, r.PeerID, r.OldKeyID)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		changed += int(n)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return changed, nil
}

func (pg *PostgresDB) ListOrgPeerCredentialsNotSealedWith(keyID string) ([]*OrgPeerCredential, error) {
	rows, err := pg.pool.Query(pg.ctx,
		`SELECT org_id, peer_id, ciphertext, nonce, key_id, COALESCE(updated_at::text,''), COALESCE(updated_by,'')
		 FROM org_peer_credentials WHERE key_id <> $1 ORDER BY org_id, peer_id`, keyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*OrgPeerCredential
	for rows.Next() {
		var c OrgPeerCredential
		if err := rows.Scan(&c.OrgID, &c.PeerID, &c.Ciphertext, &c.Nonce, &c.KeyID, &c.UpdatedAt, &c.UpdatedBy); err != nil {
			return nil, err
		}
		c.PasswordSet = c.Ciphertext != ""
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (pg *PostgresDB) ResealOrgPeerCredentials(list []OrgPeerCredentialReseal) (int, error) {
	if len(list) == 0 {
		return 0, nil
	}
	tx, err := pg.pool.Begin(pg.ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(pg.ctx)
	changed := 0
	for _, r := range list {
		tag, err := tx.Exec(pg.ctx, `UPDATE org_peer_credentials SET ciphertext = $1, nonce = $2, key_id = $3
			WHERE org_id = $4 AND peer_id = $5 AND key_id = $6`,
			r.Ciphertext, r.Nonce, r.KeyID, r.OrgID, r.PeerID, r.OldKeyID)
		if err != nil {
			return 0, err
		}
		changed += int(tag.RowsAffected())
	}
	if err := tx.Commit(pg.ctx); err != nil {
		return 0, err
	}
	return changed, nil
}
