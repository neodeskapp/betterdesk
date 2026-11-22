package authimport

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// row is one row read with its column names.
type row map[string]any

func (c *conn) readRows(ctx context.Context, query string, args ...any) ([]row, error) {
	rows, err := c.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []row
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := row{}
		for i, name := range names {
			if b, ok := values[i].([]byte); ok {
				values[i] = append([]byte(nil), b...)
			}
			r[name] = values[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

func id(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}

// The columns the console owns on an account row. When an account is in
// both files they come from auth.db: the console is where people signed
// in, so its role and sign-in provider, profiles, languages, two-factor
// sign-in and passwords are the ones that held (D5, A87).
var consoleUserColumns = []string{"role", "auth_provider", "first_name", "last_name", "email", "phone", "role_display", "avatar_url",
	"preferred_language", "totp_secret", "totp_enabled", "totp_recovery_codes", "password_hash"}

// users merges the accounts. An account of auth.db is the main file's
// account of the same name; else, when it is the only account of auth.db
// with that name in lower case and the main file has exactly one such
// account that no exact name took, that one. The console's names are
// case-sensitive, so two accounts it told apart never meet in one row.
// A matched account keeps the main file's row, id and name; it takes the
// console's columns above, the later last sign-in and the earlier
// creation time, and the console's guid when the main row has none. One
// only in auth.db is added with its id, or a new one when the main file
// uses that id.
func (c *conn) users(ctx context.Context) error {
	legacyCols, err := c.columns(ctx, "legacy", "users")
	if err != nil {
		return err
	}
	mainCols, err := c.columns(ctx, "main", "users")
	if err != nil {
		return err
	}
	inMain := map[string]column{}
	for _, col := range mainCols {
		inMain[col.name] = col
	}
	legacy, err := c.readRows(ctx, `SELECT * FROM legacy.users ORDER BY id`)
	if err != nil {
		return err
	}
	current, err := c.readRows(ctx, `SELECT id, username FROM main.users ORDER BY id`)
	if err != nil {
		return err
	}
	byName := map[string]int64{}
	byNormal := map[string][]int64{}
	for _, u := range current {
		byName[text(u["username"])] = id(u["id"])
		byNormal[normalName(text(u["username"]))] = append(byNormal[normalName(text(u["username"]))], id(u["id"]))
	}
	legacyNormal := map[string]int{}
	for _, u := range legacy {
		legacyNormal[normalName(text(u["username"]))]++
	}
	// Exact names first, so a name in another case never takes the row of
	// an account that has the exact name
	claimed := map[int64]bool{}
	matched := make([]int64, len(legacy))
	for i, u := range legacy {
		if mid, ok := byName[text(u["username"])]; ok {
			matched[i], claimed[mid] = mid, true
		}
	}
	for i, u := range legacy {
		normal := normalName(text(u["username"]))
		if ids := byNormal[normal]; matched[i] == 0 && legacyNormal[normal] == 1 && len(ids) == 1 && !claimed[ids[0]] {
			matched[i], claimed[ids[0]] = ids[0], true
		}
	}
	for i, u := range legacy {
		old := id(u["id"])
		if mid := matched[i]; mid != 0 {
			if err := c.mergeUser(ctx, mid, u, inMain); err != nil {
				return fmt.Errorf("merge account %d: %w", old, err)
			}
			c.report.UserIDs[old] = mid
			c.report.UsersMerged++
			continue
		}
		var names []string
		var values []any
		for _, col := range legacyCols {
			target, ok := inMain[col.name]
			if !ok || (col.name == "id" && c.idTaken(ctx, "users", old)) {
				continue
			}
			v := u[col.name]
			if v == nil && target.notNull && target.dflt.Valid {
				continue
			}
			names = append(names, quote(col.name))
			values = append(values, v)
		}
		res, err := c.tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.users (%s) VALUES (%s)`,
			strings.Join(names, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")), values...)
		if err != nil {
			return fmt.Errorf("add account %d: %w", old, err)
		}
		mid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		c.report.UserIDs[old] = mid
		c.report.UsersAdded++
	}
	for old, mid := range c.report.UserIDs {
		if _, err := c.exec(ctx, `INSERT INTO temp.import_map (tbl, old, new) VALUES ('users', ?, ?)`, old, mid); err != nil {
			return err
		}
	}
	// Strategy assignments of accounts name their guid
	for _, u := range legacy {
		oldGUID := text(u["guid"])
		if oldGUID == "" {
			continue
		}
		var newGUID sql.NullString
		if err := c.tx.QueryRowContext(ctx, `SELECT guid FROM main.users WHERE id = ?`, c.report.UserIDs[id(u["id"])]).Scan(&newGUID); err != nil {
			return err
		}
		if newGUID.String != "" && newGUID.String != oldGUID {
			if _, err := c.exec(ctx, `INSERT OR REPLACE INTO temp.import_guid (old, new) VALUES (?, ?)`, oldGUID, newGUID.String); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *conn) idTaken(ctx context.Context, table string, n int64) bool {
	var found int
	err := c.tx.QueryRowContext(ctx, `SELECT 1 FROM main.`+quote(table)+` WHERE id = ?`, n).Scan(&found)
	return err == nil
}

// mergeUser writes the console's side of an account into the main row.
func (c *conn) mergeUser(ctx context.Context, mid int64, u row, inMain map[string]column) error {
	mine, err := c.readRows(ctx, `SELECT * FROM main.users WHERE id = ?`, mid)
	if err != nil || len(mine) != 1 {
		return fmt.Errorf("read account %d: %v", mid, err)
	}
	m := mine[0]
	var sets []string
	var values []any
	set := func(col string, v any) {
		if _, ok := inMain[col]; !ok {
			return
		}
		if target := inMain[col]; v == nil && target.notNull {
			return
		}
		sets = append(sets, quote(col)+" = ?")
		values = append(values, v)
	}
	for _, col := range consoleUserColumns {
		v, ok := u[col]
		if !ok {
			continue
		}
		if col == "password_hash" && text(v) != text(m["password_hash"]) {
			c.report.PasswordsTaken++
		}
		set(col, v)
	}
	if v, ok := u["last_login"]; ok && later(text(v), text(m["last_login"])) {
		set("last_login", v)
	}
	if v, ok := u["created_at"]; ok && v != nil && later(text(m["created_at"]), text(v)) {
		set("created_at", v)
	}
	if v, ok := u["guid"]; ok && text(m["guid"]) == "" && text(v) != "" {
		set("guid", v)
	}
	if len(sets) == 0 {
		return nil
	}
	values = append(values, mid)
	_, err = c.exec(ctx, `UPDATE main.users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, values...)
	return err
}

// timeLayouts are the forms the two programs wrote times in.
var timeLayouts = []string{"2006-01-02 15:04:05", time.RFC3339Nano, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05.999999999-07:00"}

// later reports whether time a is after time b. An empty or unreadable
// time is before any other; two unreadable ones compare as text.
func later(a, b string) bool {
	parse := func(s string) (time.Time, bool) {
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	}
	ta, okA := parse(a)
	tb, okB := parse(b)
	switch {
	case okA && okB:
		return ta.After(tb)
	case okA:
		return true
	case okB:
		return false
	}
	return a != "" && a > b
}

// seedNotes are the notes of the "Default" groups the console's migration
// puts into an empty group table.
var seedNotes = map[string]string{"user_groups": "Default user group", "device_groups": "Default device group"}

// groups merges user groups, device groups and strategies by guid. A guid
// both files have keeps the main file's row and id with the console's
// values written over it (the console is where they are edited); a new
// one is added with its id, or a new one when the main file uses it. The
// "Default" group the migration put into a table that was empty before
// gives way to the console's groups.
func (c *conn) groups(ctx context.Context, table string) error {
	if err := c.dropSeed(ctx, table); err != nil {
		return err
	}
	legacyCols, err := c.columns(ctx, "legacy", table)
	if err != nil {
		return err
	}
	mainCols, err := c.columns(ctx, "main", table)
	if err != nil {
		return err
	}
	inMain := map[string]bool{}
	for _, col := range mainCols {
		inMain[col.name] = true
	}
	var common []string
	for _, col := range legacyCols {
		if inMain[col.name] {
			common = append(common, col.name)
		}
	}
	legacy, err := c.readRows(ctx, `SELECT * FROM legacy.`+quote(table)+` ORDER BY id`)
	if err != nil {
		return err
	}
	ids := map[int64]int64{}
	for _, g := range legacy {
		old := id(g["id"])
		var mid int64
		err := c.tx.QueryRowContext(ctx, `SELECT id FROM main.`+quote(table)+` WHERE guid = ?`, g["guid"]).Scan(&mid)
		switch {
		case err == nil:
			var sets []string
			var values []any
			for _, col := range common {
				if col == "id" || col == "guid" {
					continue
				}
				sets = append(sets, quote(col)+" = ?")
				values = append(values, g[col])
			}
			values = append(values, mid)
			n, err := c.exec(ctx, `UPDATE main.`+quote(table)+` SET `+strings.Join(sets, ", ")+` WHERE id = ? AND NOT (`+sameAs(sets)+`)`,
				append(values, values[:len(values)-1]...)...)
			if err != nil {
				return err
			}
			c.report.Kept[table] += n
		case err == sql.ErrNoRows:
			var names []string
			var values []any
			for _, col := range common {
				if col == "id" && c.idTaken(ctx, table, old) {
					continue
				}
				names = append(names, quote(col))
				values = append(values, g[col])
			}
			res, err := c.tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) VALUES (%s)`, quote(table),
				strings.Join(names, ", "), strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")), values...)
			if err != nil {
				return err
			}
			if mid, err = res.LastInsertId(); err != nil {
				return err
			}
			c.report.Rows[table]++
		default:
			return err
		}
		ids[old] = mid
		if _, err := c.exec(ctx, `INSERT INTO temp.import_map (tbl, old, new) VALUES (?, ?, ?)`, table, old, mid); err != nil {
			return err
		}
	}
	c.report.GroupIDs[table] = ids
	return nil
}

// sameAs is the condition that a row already holds the values of an
// update's SET list, given once more as arguments.
func sameAs(sets []string) string {
	var parts []string
	for _, s := range sets {
		col := strings.TrimSuffix(s, " = ?")
		parts = append(parts, col+" IS ?")
	}
	if len(parts) == 0 {
		return "1"
	}
	return strings.Join(parts, " AND ")
}

// dropSeed deletes the migration's "Default" group when it is all the
// main table holds, nothing refers to it and auth.db has groups of its
// own.
func (c *conn) dropSeed(ctx context.Context, table string) error {
	note, ok := seedNotes[table]
	if !ok {
		return nil
	}
	var legacyRows int
	if err := c.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy.`+quote(table)).Scan(&legacyRows); err != nil {
		return err
	}
	rows, err := c.readRows(ctx, `SELECT id, guid, name, note, team_id FROM main.`+quote(table))
	if err != nil {
		return err
	}
	if legacyRows == 0 || len(rows) != 1 {
		return nil
	}
	seed := rows[0]
	if text(seed["name"]) != "Default" || text(seed["note"]) != note || text(seed["team_id"]) != "" {
		return nil
	}
	refs := map[string][]string{
		"user_groups":   {`SELECT COUNT(*) FROM main.user_group_members WHERE user_group_id = ?`, `SELECT COUNT(*) FROM main.device_group_user_group_access WHERE user_group_id = ?`},
		"device_groups": {`SELECT COUNT(*) FROM main.device_group_members WHERE device_group_id = ?`, `SELECT COUNT(*) FROM main.device_group_user_access WHERE device_group_id = ?`, `SELECT COUNT(*) FROM main.device_group_user_group_access WHERE device_group_id = ?`},
	}
	for _, q := range refs[table] {
		var n int
		if err := c.tx.QueryRowContext(ctx, q, seed["id"]).Scan(&n); err != nil || n > 0 {
			return nil
		}
	}
	for _, col := range []string{"user_group_guid", "device_group_guid"} {
		var n int
		if err := c.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.strategies WHERE `+col+` = ?`, seed["guid"]).Scan(&n); err != nil || n > 0 {
			return nil
		}
	}
	_, err = c.exec(ctx, `DELETE FROM main.`+quote(table)+` WHERE id = ?`, seed["id"])
	return err
}

// assignments copies strategy assignments by their target. The main
// file's assignment of a target stays (the console wrote each one to the
// server as well). Assignments of accounts follow the account's guid in
// the main file.
func (c *conn) assignments(ctx context.Context) error {
	legacy, err := c.readRows(ctx, `SELECT * FROM legacy.strategy_assignments ORDER BY id`)
	if err != nil {
		return err
	}
	for _, a := range legacy {
		key := a["target_key"]
		if text(a["target_type"]) == "user" {
			var guid string
			if err := c.tx.QueryRowContext(ctx, `SELECT new FROM temp.import_guid WHERE old = ?`, text(key)).Scan(&guid); err == nil {
				key = guid
			}
		}
		n, err := c.exec(ctx, `INSERT INTO main.strategy_assignments (target_type, target_key, strategy_guid, updated_at)
			VALUES (?, ?, ?, COALESCE(?, datetime('now'))) ON CONFLICT DO NOTHING`, a["target_type"], key, a["strategy_guid"], a["updated_at"])
		if err != nil {
			return err
		}
		c.report.Rows["strategy_assignments"] += n
		c.report.Kept["strategy_assignments"] += 1 - n
	}
	return nil
}

// addressBooks copies the address books. The server's table keys them by
// account name, the console's by account id; a book the main file has
// for the same account and type stays.
func (c *conn) addressBooks(ctx context.Context) error {
	mainCols, err := c.columns(ctx, "main", "address_books")
	if err != nil {
		return err
	}
	byName := false
	for _, col := range mainCols {
		byName = byName || col.name == "username"
	}
	legacy, err := c.readRows(ctx, `SELECT * FROM legacy.address_books ORDER BY rowid`)
	if err != nil {
		return err
	}
	for _, b := range legacy {
		var owner any
		if _, ok := b["user_id"]; ok {
			mid, known := c.report.UserIDs[id(b["user_id"])]
			if !known {
				c.report.Skipped["address_books"]++
				continue
			}
			owner = mid
			if byName {
				var name string
				if err := c.tx.QueryRowContext(ctx, `SELECT username FROM main.users WHERE id = ?`, mid).Scan(&name); err != nil {
					return err
				}
				owner = name
			}
		} else {
			owner = b["username"]
		}
		column := "user_id"
		if byName {
			column = "username"
		}
		n, err := c.exec(ctx, `INSERT INTO main.address_books (`+column+`, ab_type, data, updated_at)
			VALUES (?, COALESCE(?, 'legacy'), ?, COALESCE(?, datetime('now'))) ON CONFLICT DO NOTHING`, owner, b["ab_type"], b["data"], b["updated_at"])
		if err != nil {
			return err
		}
		c.report.Rows["address_books"] += n
		c.report.Kept["address_books"] += 1 - n
	}
	return nil
}

// mainReferences carries changed account ids into the main file's own
// tables that held the console's ids: organizations of tenants, creators
// of agent bundles, and signed-in sessions, which end for every account
// whose id changed or that no longer exists.
func (c *conn) mainReferences(ctx context.Context, mainTables map[string]bool) error {
	const changed = `SELECT old FROM temp.import_map WHERE tbl = 'users' AND old <> new`
	const known = `SELECT old FROM temp.import_map WHERE tbl = 'users'`
	if mainTables["tenant_users"] {
		n, err := c.exec(ctx, `DELETE FROM main.tenant_users WHERE user_id NOT IN (`+known+`)`)
		if err != nil {
			return fmt.Errorf("tenant_users: %w", err)
		}
		c.report.Skipped["tenant_users"] += n
		if _, err := c.exec(ctx, `UPDATE main.tenant_users SET user_id = -`+mapped("users", "user_id")+` WHERE user_id IN (`+changed+`)`); err != nil {
			return fmt.Errorf("tenant_users: %w", err)
		}
		if _, err := c.exec(ctx, `UPDATE main.tenant_users SET user_id = -user_id WHERE user_id < 0`); err != nil {
			return fmt.Errorf("tenant_users: %w", err)
		}
	}
	if mainTables["agent_bundles"] {
		if _, err := c.exec(ctx, `UPDATE main.agent_bundles SET created_by = `+mapped("users", "created_by")+
			` WHERE created_by IS NOT NULL AND created_by NOT IN (SELECT old FROM temp.import_map WHERE tbl = 'users' AND old = new)`); err != nil {
			return fmt.Errorf("agent_bundles: %w", err)
		}
	}
	if mainTables["panel_sessions"] {
		cols, err := c.columns(ctx, "main", "panel_sessions")
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, col := range cols {
			have[col.name] = true
		}
		if have["user_id"] && have["is_active"] {
			sets := []string{"is_active = 0"}
			if have["ended_at"] {
				sets = append(sets, fmt.Sprintf("ended_at = COALESCE(ended_at, %d)", c.nowMs))
			}
			if have["end_reason"] {
				sets = append(sets, "end_reason = COALESCE(end_reason, 'revoked')")
			}
			if have["updated_at"] {
				sets = append(sets, "updated_at = datetime('now')")
			}
			n, err := c.exec(ctx, `UPDATE main.panel_sessions SET `+strings.Join(sets, ", ")+
				` WHERE is_active = 1 AND user_id IS NOT NULL AND user_id NOT IN (SELECT old FROM temp.import_map WHERE tbl = 'users' AND old = new)`)
			if err != nil {
				return fmt.Errorf("panel_sessions: %w", err)
			}
			c.report.SessionsEnded = n
		}
	}
	return nil
}

// sortedTables lists the keys of a count map, for stable logs.
func sortedTables(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
