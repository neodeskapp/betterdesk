package branding

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The queries on branding_config (one row per setting) and
// branding_profiles (saved sets of settings). Both tables belong to the
// console alone.

type configRow struct{ key, value string }

// getBrandingConfig reads every stored setting.
func getBrandingConfig(ctx context.Context, q store.Querier) ([]configRow, error) {
	rows, err := q.Query(ctx, "SELECT key, value FROM branding_config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []configRow
	for rows.Next() {
		var key, value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out = append(out, configRow{decodeUTF8(key), decodeUTF8(value)})
	}
	return out, rows.Err()
}

// saveBrandingConfigBatch writes settings in one transaction: new keys are
// added, known keys get the new value, and both get the current time.
func saveBrandingConfigBatch(ctx context.Context, db *store.DB, entries []configRow) error {
	now := store.NowSQL(db.Dialect())
	return db.InTx(ctx, func(tx *store.Tx) error {
		for _, e := range entries {
			_, err := tx.Exec(ctx,
				"INSERT INTO branding_config (key, value, updated_at) VALUES (?, ?, "+now+") "+
					"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now,
				e.key, wellFormed(e.value))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// resetBrandingConfig deletes every stored setting.
func resetBrandingConfig(ctx context.Context, q store.Querier) error {
	_, err := q.Exec(ctx, "DELETE FROM branding_config")
	return err
}

// rowsRevision is the revision of stored settings: the first eight hex
// digits of the SHA-256 of every key and value in key order, each ended by
// a zero byte. Two saves that store other values give other revisions,
// however close in time, and the text is the same on both databases and
// in every time zone.
func rowsRevision(rows []configRow) string {
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b configRow) int { return strings.Compare(a.key, b.key) })
	h := sha256.New()
	for _, row := range sorted {
		h.Write([]byte(row.key + "\x00" + row.value + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// Profile is a saved set of settings as the profile list shows it. The
// JSON has the columns as the database returns them: the times as text on
// SQLite and as ISO strings on PostgreSQL.
type Profile struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Description store.NullString `json:"description"`
	IsActive    store.Bool       `json:"is_active"`
	CreatedAt   store.Time       `json:"created_at"`
	UpdatedAt   store.Time       `json:"updated_at"`
}

// MarshalJSON writes the profile with its text as JavaScript writes text.
func (p Profile) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString(`{"id":` + strconv.FormatInt(p.ID, 10) + `,"name":`)
	quoteJSON(&b, p.Name)
	for _, field := range []struct {
		name  string
		value jsonMarshaler
	}{
		{"description", p.Description}, {"is_active", p.IsActive}, {"created_at", p.CreatedAt}, {"updated_at", p.UpdatedAt},
	} {
		raw, err := field.value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.WriteString(`,"` + field.name + `":`)
		b.Write(raw)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// profileRow is a profile with its stored settings.
type profileRow struct {
	Profile
	Data string
}

// listBrandingProfiles lists the profiles by name. SQLite ignores the
// case of ASCII letters when sorting; PostgreSQL sorts by the collation of
// the database.
func listBrandingProfiles(ctx context.Context, q store.Querier) ([]Profile, error) {
	rows, err := store.Select(ctx, q,
		"SELECT id, name, description, is_active, created_at, updated_at FROM branding_profiles ORDER BY name"+
			q.Pick(" COLLATE NOCASE", ""))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		var name []byte
		if err := rows.Scan(&p.ID, &name, &p.Description, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Name = decodeUTF8(name)
		p.Description.String = decodeUTF8([]byte(p.Description.String))
		out = append(out, p)
	}
	return out, rows.Err()
}

// getBrandingProfile reads one profile. found is false when there is none
// with that id.
func getBrandingProfile(ctx context.Context, q store.Querier, id int64) (p profileRow, found bool, err error) {
	if err := checkProfileID(q, id); err != nil {
		return p, false, err
	}
	var name, data []byte
	err = store.SelectRow(ctx, q,
		"SELECT id, name, description, data, is_active, created_at, updated_at FROM branding_profiles WHERE id = ?", id).
		Scan(&p.ID, &name, &p.Description, &data, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	if err != nil {
		return p, false, err
	}
	p.Name, p.Data = decodeUTF8(name), decodeUTF8(data)
	p.Description.String = decodeUTF8([]byte(p.Description.String))
	return p, true, nil
}

// checkProfileID refuses, on PostgreSQL, an id the integer column cannot
// hold, with the message the database gave for it.
func checkProfileID(q store.Querier, id int64) error {
	if q.Dialect() == store.Postgres && (id > math.MaxInt32 || id < math.MinInt32) {
		return &Error{Name: "error", Message: `value "` + strconv.FormatInt(id, 10) + `" is out of range for type integer`}
	}
	return nil
}

// createBrandingProfile adds a profile and returns its id.
func createBrandingProfile(ctx context.Context, q store.Querier, name, description, data string) (int64, error) {
	now := store.NowSQL(q.Dialect())
	id, err := q.Insert(ctx,
		"INSERT INTO branding_profiles (name, description, data, created_at, updated_at) VALUES (?, ?, ?, "+now+", "+now+")",
		wellFormed(name), wellFormed(description), wellFormed(data))
	return id, uniqueNameError(err)
}

// updateBrandingProfile replaces name, description and settings of a
// profile.
func updateBrandingProfile(ctx context.Context, q store.Querier, id int64, name, description, data string) error {
	if err := checkProfileID(q, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx,
		"UPDATE branding_profiles SET name = ?, description = ?, data = ?, updated_at = "+store.NowSQL(q.Dialect())+" WHERE id = ?",
		wellFormed(name), wellFormed(description), wellFormed(data), id)
	return uniqueNameError(err)
}

// setActiveBrandingProfile marks one profile as the active one.
func setActiveBrandingProfile(ctx context.Context, db *store.DB, id int64) error {
	if err := checkProfileID(db, id); err != nil {
		return err
	}
	return db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE branding_profiles SET is_active = 0"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			"UPDATE branding_profiles SET is_active = 1, updated_at = "+store.NowSQL(tx.Dialect())+" WHERE id = ?", id)
		return err
	})
}

// deleteBrandingProfile deletes a profile.
func deleteBrandingProfile(ctx context.Context, q store.Querier, id int64) error {
	if err := checkProfileID(q, id); err != nil {
		return err
	}
	_, err := q.Exec(ctx, "DELETE FROM branding_profiles WHERE id = ?", id)
	return err
}

// uniqueNameError is ErrProfileNameTaken for a write of a profile that
// another profile's name refused (the unique constraint: PostgreSQL's code
// 23505, SQLite's constraint code), and err otherwise.
func uniqueNameError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrProfileNameTaken
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return ErrProfileNameTaken
	}
	return err
}
