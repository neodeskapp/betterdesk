package authimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// conn is a connection on the main file with auth.db attached as
// "legacy", inside the import's transaction.
type conn struct {
	tx     *sql.Tx
	report *Report
	nowMs  int64
}

func (c *conn) exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := c.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// merge runs the import in one transaction on its own connection, with
// foreign keys off while rows move and checked before the commit.
func merge(ctx context.Context, opts Options, report *Report) error {
	handle, err := sql.Open("sqlite", "file:"+report.Target+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(0)&_txlock=immediate")
	if err != nil {
		return err
	}
	defer handle.Close()
	handle.SetMaxOpenConns(1)
	raw, err := handle.Conn(ctx)
	if err != nil {
		return err
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `ATTACH DATABASE ? AS legacy`, "file:"+report.Source+"?mode=ro"); err != nil {
		return fmt.Errorf("attach auth.db: %w", err)
	}
	defer func() { _, _ = raw.ExecContext(context.Background(), `DETACH DATABASE legacy`) }()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	c := &conn{tx: tx, report: report, nowMs: opts.Now().UnixMilli()}
	if err := c.run(ctx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// The tables that need their own rules; every other table of auth.db is
// copied as it is (copyTable).
var (
	groupTables  = []string{"user_groups", "device_groups", "strategies"}
	appendTables = map[string]bool{"peer_metrics": true, "audit_connections": true, "audit_files": true, "audit_alarms": true}
	ownRules     = map[string]bool{"sqlite_sequence": true, "users": true, "user_groups": true, "device_groups": true,
		"strategies": true, "strategy_assignments": true, "address_books": true}
)

func (c *conn) run(ctx context.Context) error {
	if _, err := c.exec(ctx, `CREATE TEMP TABLE import_map (tbl TEXT NOT NULL, old INTEGER NOT NULL, new INTEGER NOT NULL, PRIMARY KEY (tbl, old))`); err != nil {
		return err
	}
	if _, err := c.exec(ctx, `CREATE TEMP TABLE import_guid (old TEXT PRIMARY KEY, new TEXT NOT NULL)`); err != nil {
		return err
	}
	legacyTables, err := c.tables(ctx, "legacy")
	if err != nil {
		return err
	}
	mainTables, err := c.tables(ctx, "main")
	if err != nil {
		return err
	}
	if !legacyTables["users"] {
		return fmt.Errorf("auth.db has no users table")
	}
	if err := c.users(ctx); err != nil {
		return fmt.Errorf("accounts: %w", err)
	}
	for _, table := range groupTables {
		if legacyTables[table] && mainTables[table] {
			if err := c.groups(ctx, table); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}
	}
	order, err := c.copyOrder(ctx, legacyTables)
	if err != nil {
		return err
	}
	for _, table := range order {
		switch {
		case ownRules[table]:
			continue
		case !mainTables[table]:
			c.report.Unknown = append(c.report.Unknown, table)
		case appendTables[table]:
			if err := c.appendRows(ctx, table); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		default:
			if err := c.copyTable(ctx, table); err != nil {
				return fmt.Errorf("%s: %w", table, err)
			}
		}
	}
	if legacyTables["strategy_assignments"] && mainTables["strategy_assignments"] {
		if err := c.assignments(ctx); err != nil {
			return fmt.Errorf("strategy_assignments: %w", err)
		}
	}
	if legacyTables["address_books"] && mainTables["address_books"] {
		if err := c.addressBooks(ctx); err != nil {
			return fmt.Errorf("address_books: %w", err)
		}
	}
	if err := c.mainReferences(ctx, mainTables); err != nil {
		return err
	}
	for table := range legacyTables {
		if !mainTables[table] || table == "sqlite_sequence" {
			continue
		}
		if err := c.foreignKeyCheck(ctx, table); err != nil {
			return err
		}
	}
	return c.writeMarker(ctx)
}

// tables lists the tables of a schema.
func (c *conn) tables(ctx context.Context, schema string) (map[string]bool, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT name FROM `+schema+`.sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, fmt.Errorf("list the tables of %s: %w", schema, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// column is one column of a table as PRAGMA table_info describes it.
type column struct {
	name    string
	typ     string
	notNull bool
	dflt    sql.NullString
	pk      int
}

func (c *conn) columns(ctx context.Context, schema, table string) ([]column, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, fmt.Errorf("columns of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var col column
		if err := rows.Scan(&col.name, &col.typ, &col.notNull, &col.dflt, &col.pk); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// foreignKeys maps a table's columns to the tables they point at.
func (c *conn) foreignKeys(ctx context.Context, schema, table string) (map[string]string, error) {
	rows, err := c.tx.QueryContext(ctx, `SELECT "from", "table" FROM pragma_foreign_key_list(?, ?)`, table, schema)
	if err != nil {
		return nil, fmt.Errorf("foreign keys of %s.%s: %w", schema, table, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var from, parent string
		if err := rows.Scan(&from, &parent); err != nil {
			return nil, err
		}
		out[from] = parent
	}
	return out, rows.Err()
}

// copyOrder puts tables after the tables they point at.
func (c *conn) copyOrder(ctx context.Context, tables map[string]bool) ([]string, error) {
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	parents := map[string][]string{}
	for _, name := range names {
		fks, err := c.foreignKeys(ctx, "legacy", name)
		if err != nil {
			return nil, err
		}
		for _, parent := range fks {
			if parent != name && tables[parent] {
				parents[name] = append(parents[name], parent)
			}
		}
	}
	var order []string
	placed := map[string]bool{}
	var place func(name string, depth int)
	place = func(name string, depth int) {
		if placed[name] || depth > len(names) {
			return
		}
		for _, parent := range parents[name] {
			place(parent, depth+1)
		}
		if !placed[name] {
			placed[name] = true
			order = append(order, name)
		}
	}
	for _, name := range names {
		place(name, 0)
	}
	return order, nil
}

// quote writes an identifier for SQL.
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// intKey is the table's own integer key, when it has one that is no
// reference to another table.
func intKey(cols []column, fks map[string]string) string {
	var key string
	for _, col := range cols {
		if col.pk > 1 {
			return ""
		}
		if col.pk == 1 {
			if !strings.EqualFold(col.typ, "INTEGER") {
				return ""
			}
			key = col.name
		}
	}
	if _, isRef := fks[key]; isRef {
		return ""
	}
	return key
}

// userIDRefs are columns that hold an account id without a foreign key.
var userIDRefs = map[string]string{"audit_log": "user_id"}

// copyTable copies the rows of a table the main file has from auth.db:
// the columns both have; ids that point at accounts or remapped rows
// rewritten; rows that point at something that no longer exists left
// out. A table with its own integer key keeps each row's id when the main
// file does not use it, else the row gets a new one, which rows of later
// tables follow. Rows whose other unique keys the main file has already
// are left as they are there.
func (c *conn) copyTable(ctx context.Context, table string) error {
	legacyCols, err := c.columns(ctx, "legacy", table)
	if err != nil {
		return err
	}
	mainCols, err := c.columns(ctx, "main", table)
	if err != nil {
		return err
	}
	fks, err := c.foreignKeys(ctx, "legacy", table)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, col := range legacyCols {
		have[col.name] = true
	}
	var names, exprs, conds []string
	for _, col := range mainCols {
		if !have[col.name] {
			continue
		}
		expr := "s." + quote(col.name)
		if parent, ok := fks[col.name]; ok {
			expr = mapped(parent, expr)
			conds = append(conds, fmt.Sprintf("(s.%s IS NULL OR EXISTS (SELECT 1 FROM temp.import_map WHERE tbl = %s AND old = s.%s))",
				quote(col.name), sqlText(parent), quote(col.name)))
		} else if userIDRefs[table] == col.name {
			expr = mapped("users", expr)
		}
		expr = c.userSubject(table, col.name, expr, &conds)
		if col.notNull && col.dflt.Valid {
			expr = fmt.Sprintf("COALESCE(%s, %s)", expr, col.dflt.String)
		}
		names = append(names, quote(col.name))
		exprs = append(exprs, expr)
	}
	if len(names) == 0 {
		return nil
	}
	where := "1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}
	from := fmt.Sprintf("FROM legacy.%s s WHERE %s", quote(table), where)
	var total, eligible int64
	if err := c.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy.`+quote(table)).Scan(&total); err != nil {
		return err
	}
	if err := c.tx.QueryRowContext(ctx, `SELECT COUNT(*) `+from).Scan(&eligible); err != nil {
		return err
	}
	c.report.Skipped[table] += total - eligible
	cols := strings.Join(names, ", ")
	selected := strings.Join(exprs, ", ")

	key := intKey(mainCols, fks)
	if key == "" || !have[key] {
		n, err := c.exec(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s %s ON CONFLICT DO NOTHING`, quote(table), cols, selected, from))
		if err != nil {
			return err
		}
		c.report.Rows[table] += n
		c.report.Kept[table] += eligible - n
		return nil
	}
	free := fmt.Sprintf("%s AND s.%s NOT IN (SELECT %s FROM main.%s)", from, quote(key), quote(key), quote(table))
	if _, err := c.exec(ctx, fmt.Sprintf(`INSERT INTO temp.import_map (tbl, old, new) SELECT %s, s.%s, s.%s %s`,
		sqlText(table), quote(key), quote(key), free)); err != nil {
		return err
	}
	n, err := c.exec(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s %s`, quote(table), cols, selected, free))
	if err != nil {
		return err
	}
	c.report.Rows[table] += n
	// Rows whose id the main file uses already get a new one
	taken := fmt.Sprintf("%s AND s.%s NOT IN (SELECT old FROM temp.import_map WHERE tbl = %s)", from, quote(key), sqlText(table))
	var others []string
	var otherExprs []string
	for i, name := range names {
		if name != quote(key) {
			others = append(others, name)
			otherExprs = append(otherExprs, exprs[i])
		}
	}
	rows, err := c.tx.QueryContext(ctx, fmt.Sprintf(`SELECT s.%s, %s %s`, quote(key), strings.Join(otherExprs, ", "), taken))
	if err != nil {
		return err
	}
	type pending struct {
		old    int64
		values []any
	}
	var moved []pending
	for rows.Next() {
		values := make([]any, len(others)+1)
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			rows.Close()
			return err
		}
		old, _ := values[0].(int64)
		moved = append(moved, pending{old: old, values: values[1:]})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(others)), ", ")
	for _, p := range moved {
		res, err := c.tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) VALUES (%s)`, quote(table), strings.Join(others, ", "), placeholders), p.values...)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := c.exec(ctx, `INSERT INTO temp.import_map (tbl, old, new) VALUES (?, ?, ?)`, table, p.old, id); err != nil {
			return err
		}
		c.report.Rows[table]++
	}
	return nil
}

// userSubject rewrites the account ids that panel access rows, client
// feature rows and per-account settings keep in text, and leaves out rows
// of accounts that no longer exist.
//
// Only an id written as a plain decimal number counts as an account id;
// other text is kept as it is.
func (c *conn) userSubject(table, col, expr string, conds *[]string) string {
	isID := func(text string) string { return fmt.Sprintf("CAST(CAST(%s AS INTEGER) AS TEXT) = %s", text, text) }
	known := func(text string) string {
		return fmt.Sprintf("EXISTS (SELECT 1 FROM temp.import_map WHERE tbl = 'users' AND old = CAST(%s AS INTEGER))", text)
	}
	newID := func(text string) string { return "CAST(" + mapped("users", "CAST("+text+" AS INTEGER)") + " AS TEXT)" }
	switch {
	case (table == "panel_access_policies" || table == "client_feature_policies") && col == "subject_id":
		account := "s.subject_type = 'user' AND " + isID("s.subject_id")
		*conds = append(*conds, fmt.Sprintf("(NOT (%s) OR %s)", account, known("s.subject_id")))
		return fmt.Sprintf("CASE WHEN %s THEN %s ELSE s.subject_id END", account, newID("s.subject_id"))
	case table == "settings" && col == "key":
		var parts []string
		for _, prefix := range []string{"desktop_layout_", "desktop_wallpaper_"} {
			id := fmt.Sprintf("substr(s.key, %d)", len(prefix)+1)
			account := fmt.Sprintf("substr(s.key, 1, %d) = %s AND %s", len(prefix), sqlText(prefix), isID(id))
			*conds = append(*conds, fmt.Sprintf("(NOT (%s) OR %s)", account, known(id)))
			parts = append(parts, fmt.Sprintf("WHEN %s THEN %s || %s", account, sqlText(prefix), newID(id)))
		}
		return "CASE " + strings.Join(parts, " ") + " ELSE s.key END"
	}
	return expr
}

// mapped is the id an old id has in the main file, NULL when the old one
// is not known.
func mapped(table, expr string) string {
	return fmt.Sprintf("(SELECT new FROM temp.import_map WHERE tbl = %s AND old = %s)", sqlText(table), expr)
}

func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// appendRows adds the rows of a log table both programs write (metrics,
// client audit rows) with new ids: nothing points at them.
func (c *conn) appendRows(ctx context.Context, table string) error {
	legacyCols, err := c.columns(ctx, "legacy", table)
	if err != nil {
		return err
	}
	mainCols, err := c.columns(ctx, "main", table)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, col := range legacyCols {
		have[col.name] = true
	}
	var names, exprs []string
	for _, col := range mainCols {
		if !have[col.name] || col.pk == 1 {
			continue
		}
		expr := "s." + quote(col.name)
		if col.notNull && col.dflt.Valid {
			expr = fmt.Sprintf("COALESCE(%s, %s)", expr, col.dflt.String)
		}
		names = append(names, quote(col.name))
		exprs = append(exprs, expr)
	}
	n, err := c.exec(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s FROM legacy.%s s ORDER BY s.rowid`,
		quote(table), strings.Join(names, ", "), strings.Join(exprs, ", "), quote(table)))
	c.report.Rows[table] += n
	return err
}

// normalName is how accounts are matched across the two files: trimmed
// and in lower case.
func normalName(s string) string {
	return strings.ToLower(strings.TrimFunc(s, unicode.IsSpace))
}

// writeMarker records that the import is complete, with its report.
func (c *conn) writeMarker(ctx context.Context) error {
	if _, err := c.exec(ctx, `
		CREATE TABLE IF NOT EXISTS neodesk_migrations (
			name TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			source_sha256 TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT (datetime('now')),
			details TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("create the marker table: %w", err)
	}
	details, err := json.Marshal(c.report)
	if err != nil {
		return err
	}
	_, err = c.exec(ctx, `
		INSERT INTO neodesk_migrations (name, status, source_sha256, details)
		VALUES (?, 'complete', ?, ?)
		ON CONFLICT(name) DO UPDATE SET status = 'complete', source_sha256 = excluded.source_sha256,
			completed_at = datetime('now'), details = excluded.details`,
		MarkerName, c.report.SourceSHA256, string(details))
	if err != nil {
		return fmt.Errorf("write the marker: %w", err)
	}
	return nil
}

// foreignKeyCheck fails when a table has rows that point at nothing.
func (c *conn) foreignKeyCheck(ctx context.Context, table string) error {
	rows, err := c.tx.QueryContext(ctx, `SELECT "table", "parent" FROM pragma_foreign_key_check(?, 'main')`, table)
	if err != nil {
		return fmt.Errorf("check the foreign keys of %s: %w", table, err)
	}
	defer rows.Close()
	if rows.Next() {
		var child, parent string
		_ = rows.Scan(&child, &parent)
		return fmt.Errorf("rows of %s point at missing rows of %s", child, parent)
	}
	return rows.Err()
}
