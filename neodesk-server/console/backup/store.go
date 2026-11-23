package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The queries of the backup helpers of the old adapter (dbAdapter.js,
// "Backup Helpers", and the methods the settings snapshot calls), for
// both databases.

// backupStats is getBackupStats: row counts, 0 for a table that cannot be
// counted.
func backupStats(ctx context.Context, db *store.DB) Stats {
	count := func(table string) int64 {
		var n int64
		if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			return 0
		}
		return n
	}
	return Stats{
		Users:        count("users"),
		Settings:     count("settings"),
		Folders:      count("folders"),
		UserGroups:   count("user_groups"),
		DeviceGroups: count("device_groups"),
		Strategies:   count("strategies"),
		AddressBooks: count("address_books"),
	}
}

// allSettings is getAllSettings: {key: value} in the order the rows come.
func allSettings(ctx context.Context, db *store.DB) (*goapi.Object, error) {
	rows, err := db.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	out := goapi.NewObject()
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if value.Valid {
			out.Set(nodeUTF8([]byte(key)), nodeUTF8([]byte(value.String)))
		} else {
			out.Set(key, nil)
		}
	}
	return out, rows.Err()
}

// usersForBackup is getAllUsersForBackup: every column of every account,
// password hashes and two-factor secrets included.
func usersForBackup(ctx context.Context, db *store.DB) ([]*goapi.Object, error) {
	return devicegroups.Rows(ctx, db, `SELECT * FROM users ORDER BY id`)
}

// usernameAddressBooks reports whether address_books is keyed by user
// name (the signal server made it) rather than by user id.
func usernameAddressBooks(ctx context.Context, q store.Querier) bool {
	cols, err := sqliteColumns(ctx, q, "address_books")
	if err != nil {
		return false
	}
	for _, c := range cols {
		if c == "username" {
			return true
		}
	}
	return false
}

// allAddressBooks is getAllAddressBooks. SQLite answers user_id whatever
// the table is keyed by; PostgreSQL answers username.
func allAddressBooks(ctx context.Context, db *store.DB) ([]*goapi.Object, error) {
	query := `SELECT username, ab_type, data, updated_at FROM address_books ORDER BY username`
	if !db.IsPostgres() {
		query = `SELECT user_id, ab_type, data, updated_at FROM address_books ORDER BY user_id`
		if usernameAddressBooks(ctx, db) {
			query = `SELECT u.id AS user_id, ab.ab_type, ab.data, ab.updated_at
				FROM address_books ab INNER JOIN users u ON u.username = ab.username
				ORDER BY u.id`
		}
	}
	return devicegroups.Rows(ctx, db, query)
}

// allStrategies is getAllStrategies: every rule ordered by name, with
// its permissions parsed ({} when they do not parse).
func allStrategies(ctx context.Context, db *store.DB) ([]*goapi.Object, error) {
	rows, err := devicegroups.Rows(ctx, db, `SELECT * FROM strategies ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("load strategies: %w", err)
	}
	for _, row := range rows {
		for _, key := range row.Keys() {
			row.Set(key, devicegroups.Plain(row.Get(key)))
		}
		row.Set("permissions", parseStoredJSON(db, row.Get("permissions"), goapi.NewObject()))
	}
	return rows, nil
}

// parseStoredJSON is the adapters' safeJsonParse: on PostgreSQL a value
// the driver parsed already stays as it is.
func parseStoredJSON(db *store.DB, v any, fallback any) any {
	if db.IsPostgres() {
		switch v.(type) {
		case *goapi.Object, []any:
			return v
		}
	}
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		parsed, err := goapi.Parse([]byte(t))
		if err != nil {
			return fallback
		}
		return parsed
	case bool, float64:
		return t
	}
	return fallback
}

// setSetting stores a setting; the value is bound as the driver bound it.
func setSetting(ctx context.Context, db *store.DB, key string, value any) error {
	query := db.Pick(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, NOW())
		 ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`)
	args, err := bindArgs(db, 2, []any{key, value})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, query, args...)
	return err
}

// restoreUsers is restoreUsers of the adapters: in one transaction every
// account is deleted (which deletes what hangs on accounts with ON DELETE
// CASCADE) and the backup's accounts are written with the columns the
// table has. The same transaction sets the accounts' sign-in token times
// (endReplacedCredentials) when the table has credentials_changed_at.
func restoreUsers(ctx context.Context, db *store.DB, users []any, now func() time.Time) error {
	return db.InTx(ctx, func(tx *store.Tx) error {
		var names []string
		var err error
		if db.IsPostgres() {
			names, err = readStrings(ctx, tx, `SELECT column_name FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'users'`)
		} else {
			names, err = sqliteColumns(ctx, tx, "users")
		}
		if err != nil {
			return err
		}
		cols := map[string]bool{}
		for _, n := range names {
			cols[n] = true
		}
		var before map[string]credential
		if cols[credentialsChangedColumn] {
			if before, err = readCredentials(ctx, tx.Query, true); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
			return err
		}
		for _, u := range users {
			keys, err := objectKeys(u)
			if err != nil {
				return err
			}
			var quoted, updates []string
			var values []any
			for _, k := range keys {
				if !cols[k] {
					continue
				}
				quoted = append(quoted, quoteIdent(k))
				values = append(values, prop(u, k))
				if k != "id" {
					updates = append(updates, quoteIdent(k)+"=EXCLUDED."+quoteIdent(k))
				}
			}
			if len(quoted) == 0 {
				continue
			}
			args, err := bindArgs(tx, len(quoted), values)
			if err != nil {
				return err
			}
			marks := joinComma(repeat("?", len(quoted)))
			query := `INSERT OR REPLACE INTO users (` + joinComma(quoted) + `) VALUES (` + marks + `)`
			if db.IsPostgres() {
				set := joinComma(updates)
				if set == "" {
					set = `"username"=EXCLUDED."username"`
				}
				query = `INSERT INTO users (` + joinComma(quoted) + `) VALUES (` + marks + `) ON CONFLICT(id) DO UPDATE SET ` + set
			}
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
		if before == nil {
			return nil
		}
		return endReplacedCredentials(ctx, tx.Query, tx.Exec, before, now().Unix())
	})
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// orValue is v || fallback.
func orValue(v, fallback any) any {
	if truthy(v) {
		return v
	}
	return fallback
}

// createFolder is the facade's createFolder(name, color, icon).
func createFolder(ctx context.Context, db *store.DB, name, color, icon any) error {
	args, err := bindArgs(db, 4, []any{name, orValue(color, "#6366f1"), orValue(icon, "folder"), float64(0)})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO folders (name, color, icon, sort_order) VALUES (?, ?, ?, ?)`, args...)
	return err
}

// createUserGroup is createUserGroup({guid, name, note}).
func createUserGroup(ctx context.Context, db *store.DB, guid, name, note any) error {
	args, err := bindArgs(db, 4, []any{guid, name, orValue(note, ""), ""})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO user_groups (guid, name, note, team_id) VALUES (?, ?, ?, ?)`, args...)
	return err
}

// createDeviceGroup is createDeviceGroup({guid, name, note}): a manual
// group.
func createDeviceGroup(ctx context.Context, db *store.DB, guid, name, note any) error {
	args, err := bindArgs(db, 6, []any{guid, name, orValue(note, ""), "", "manual", ""})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES (?, ?, ?, ?, ?, ?)`, args...)
	return err
}

// createStrategy is createStrategy(data) of the adapters: SQLite stores
// enabled as 1 or 0 by its truth, PostgreSQL binds it as it is. The
// permissions are stored as JSON text once: text that is not empty is
// that text already, anything else is JSON.stringify(permissions || {}).
func createStrategy(ctx context.Context, db *store.DB, guid, name, userGroupGUID, deviceGroupGUID, enabled, permissions any) error {
	var stored any = true
	if enabled != branding.Undefined {
		stored = enabled
	}
	if !db.IsPostgres() {
		stored = float64(1)
		if enabled != branding.Undefined && !truthy(enabled) {
			stored = float64(0)
		}
	}
	text, isText := permissions.(string)
	if !isText || text == "" {
		text = branding.Stringify(orValue(permissions, branding.NewObject()))
	}
	args, err := bindArgs(db, 6, []any{guid, name, orValue(userGroupGUID, ""), orValue(deviceGroupGUID, ""), stored, text})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions) VALUES (?, ?, ?, ?, ?, ?)`, args...)
	return err
}

// saveAddressBookOf saves the address book of the account named username;
// nothing happens for an unknown name.
func saveAddressBookOf(ctx context.Context, db *store.DB, username, abType, data any) error {
	args, err := bindArgs(db, 1, []any{username})
	if err != nil {
		return err
	}
	var id int64
	err = db.QueryRow(ctx, `SELECT id FROM users WHERE username = ?`, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find the account of an address book: %w", err)
	}
	return saveAddressBook(ctx, db, float64(id), abType, data)
}

// saveAddressBook is the adapters' saveAddressBook(userId, abType, data):
// nothing happens for an unknown user.
func saveAddressBook(ctx context.Context, db *store.DB, userID, abType, data any) error {
	byName := db.IsPostgres() || usernameAddressBooks(ctx, db)
	if byName {
		args, err := bindArgs(db, 1, []any{userID})
		if err != nil {
			return err
		}
		var username string
		err = db.QueryRow(ctx, `SELECT username FROM users WHERE id = ?`, args...).Scan(&username)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		query := db.Pick(
			`INSERT INTO address_books (username, ab_type, data, updated_at) VALUES (?, ?, ?, datetime('now'))
			 ON CONFLICT(username, ab_type) DO UPDATE SET data = excluded.data, updated_at = datetime('now')`,
			`INSERT INTO address_books (username, ab_type, data, updated_at) VALUES (?, ?, ?, NOW())
			 ON CONFLICT(username, ab_type) DO UPDATE SET data = EXCLUDED.data, updated_at = NOW()`)
		args, err = bindArgs(db, 3, []any{username, abType, data})
		if err != nil {
			return err
		}
		_, err = db.Exec(ctx, query, args...)
		return err
	}
	args, err := bindArgs(db, 3, []any{userID, abType, data})
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO address_books (user_id, ab_type, data, updated_at) VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(user_id, ab_type) DO UPDATE SET data = excluded.data, updated_at = datetime('now')`, args...)
	return err
}
