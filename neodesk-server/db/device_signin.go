package db

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Saved sign-in details of devices (table device_signin). The values are
// sealed by the caller (crypto/signinvault); this layer stores them as
// they come and never sees a readable password.

const deviceSigninColumns = `device_id, user_nonce, user_cipher, pass_nonce, pass_cipher, key_id, form, auto_signin, updated_at, updated_by`

func (s *SQLiteDB) GetDeviceSignin(deviceID string) (*DeviceSignin, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var d DeviceSignin
	var auto int
	err := s.db.QueryRow(`SELECT `+deviceSigninColumns+` FROM device_signin WHERE device_id = ?`, deviceID).
		Scan(&d.DeviceID, &d.UserNonce, &d.UserCipher, &d.PassNonce, &d.PassCipher, &d.KeyID, &d.Form, &auto, &d.UpdatedAt, &d.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Auto = auto != 0
	return &d, nil
}

func (s *SQLiteDB) SaveDeviceSignin(d *DeviceSignin) error {
	if d == nil || d.DeviceID == "" {
		return errors.New("db: SaveDeviceSignin: no device")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	auto := 0
	if d.Auto {
		auto = 1
	}
	_, err := s.db.Exec(`INSERT INTO device_signin (`+deviceSigninColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
		  user_nonce = excluded.user_nonce, user_cipher = excluded.user_cipher,
		  pass_nonce = excluded.pass_nonce, pass_cipher = excluded.pass_cipher,
		  key_id = excluded.key_id, form = excluded.form, auto_signin = excluded.auto_signin,
		  updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		d.DeviceID, d.UserNonce, d.UserCipher, d.PassNonce, d.PassCipher, d.KeyID, d.Form, auto, d.UpdatedAt, d.UpdatedBy)
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
		Scan(&d.DeviceID, &d.UserNonce, &d.UserCipher, &d.PassNonce, &d.PassCipher, &d.KeyID, &d.Form, &d.Auto, &d.UpdatedAt, &d.UpdatedBy)
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
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (device_id) DO UPDATE SET
		  user_nonce = EXCLUDED.user_nonce, user_cipher = EXCLUDED.user_cipher,
		  pass_nonce = EXCLUDED.pass_nonce, pass_cipher = EXCLUDED.pass_cipher,
		  key_id = EXCLUDED.key_id, form = EXCLUDED.form, auto_signin = EXCLUDED.auto_signin,
		  updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		d.DeviceID, d.UserNonce, d.UserCipher, d.PassNonce, d.PassCipher, d.KeyID, d.Form, d.Auto, d.UpdatedAt, d.UpdatedBy)
	return err
}

func (pg *PostgresDB) DeleteDeviceSignin(deviceID string) (bool, error) {
	tag, err := pg.pool.Exec(pg.ctx, `DELETE FROM device_signin WHERE device_id = $1`, deviceID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
