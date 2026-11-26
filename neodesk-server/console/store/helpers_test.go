package store_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func mustExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(testContext(t), query, args...); err != nil {
		t.Fatalf("exec %s: %v", firstWords(query), err)
	}
}

func mustMigrate(t *testing.T, db *store.DB) {
	t.Helper()
	if err := db.Migrate(testContext(t)); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func firstWords(query string) string {
	fields := strings.Fields(query)
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return strings.Join(fields, " ")
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// queryStrings runs a query and returns each row as its values in text.
//
// A statement a connection prepared before Migrate widened a column (BUGS
// AE4) fails once on that connection with SQLSTATE 0A000, after which pgx
// prepares it again. The console migrates before its first query and
// never meets this; tests that read a table before and after Migrate do,
// and send the query a second time.
func queryStrings(t *testing.T, db *store.DB, query string, args ...any) [][]string {
	t.Helper()
	rows, err := db.Query(testContext(t), query, args...)
	if err != nil && strings.Contains(err.Error(), "(SQLSTATE 0A000)") {
		rows, err = db.Query(testContext(t), query, args...)
	}
	if err != nil {
		t.Fatalf("query %s: %v", firstWords(query), err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", firstWords(query), err)
		}
		row := make([]string, len(cols))
		for i, v := range values {
			row[i] = valueText(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("query %s: %v", firstWords(query), err)
	}
	return out
}

func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return "x" + hex.EncodeToString(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	case string:
		return x
	}
	return fmt.Sprintf("%v", v)
}

// ---- catalog ----

// catalog is everything the database knows about its tables, one entry per
// table, column, index, constraint, sequence and trigger, and on PostgreSQL
// one per function. Keys are "<kind>:<table>.<name>", "function:<name>"
// for a function.
type catalog map[string]string

func entryTable(key string) string {
	_, rest, _ := strings.Cut(key, ":")
	table, _, _ := strings.Cut(rest, ".")
	return table
}

// without returns the catalog without the entries of the given tables.
func (c catalog) without(tables ...string) catalog {
	skip := map[string]bool{}
	for _, t := range tables {
		skip[t] = true
	}
	out := catalog{}
	for k, v := range c {
		if !skip[entryTable(k)] {
			out[k] = v
		}
	}
	return out
}

// only returns the entries of the given tables.
func (c catalog) only(tables []string) catalog {
	keep := map[string]bool{}
	for _, t := range tables {
		keep[t] = true
	}
	out := catalog{}
	for k, v := range c {
		if keep[entryTable(k)] {
			out[k] = v
		}
	}
	return out
}

// tables returns the names of the tables in the catalog, sorted.
func (c catalog) tables() []string {
	var out []string
	for k := range c {
		if strings.HasPrefix(k, "table:") {
			out = append(out, entryTable(k))
		}
	}
	sort.Strings(out)
	return out
}

// loose drops what an ALTER TABLE ADD COLUMN on an older table cannot
// reproduce: the position of a column and the stored text of the CREATE
// statement.
func (c catalog) loose() catalog {
	out := catalog{}
	for k, v := range c {
		switch {
		case strings.HasPrefix(k, "table:"):
			v = ""
		case strings.HasPrefix(k, "column:"):
			_, v, _ = strings.Cut(v, " ")
		}
		out[k] = v
	}
	return out
}

// wideConditionValue is the catalog of a database the Node console made,
// as the Go console leaves it: on PostgreSQL alert_rules.condition_value is
// double precision where Node made a four-byte real (BUGS AE4).
func (c catalog) wideConditionValue() catalog {
	out := catalog{}
	for k, v := range c {
		if k == "column:alert_rules.condition_value" {
			v = strings.Replace(v, " type=real ", " type=double precision ", 1)
		}
		out[k] = v
	}
	return out
}

// withTokenKind is the catalog of a database as the Go console leaves it:
// access_tokens has the column token_kind, which Node did not have (BUGS
// RV1). On SQLite the column stands in the table's statement text where
// ALTER TABLE puts it, after the last column.
func (c catalog) withTokenKind(postgres bool) catalog {
	out := catalog{}
	columns := 0
	for k, v := range c {
		if strings.HasPrefix(k, "column:access_tokens.") {
			columns++
		}
		out[k] = v
	}
	if columns == 0 {
		return out
	}
	if postgres {
		out["column:access_tokens.token_kind"] = fmt.Sprintf(`#%d type=text notnull=true default='account'::text identity="" generated=""`, columns)
		return out
	}
	out["column:access_tokens.token_kind"] = fmt.Sprintf("#%d type=TEXT notnull=1 default='account' pk=0", columns)
	out["table:access_tokens"] = strings.Replace(c["table:access_tokens"], "revoked INTEGER DEFAULT 0,",
		"revoked INTEGER DEFAULT 0, token_kind TEXT NOT NULL DEFAULT 'account',", 1)
	return out
}

// withDisabled is the catalog of a database as the Go console leaves it:
// users has the column disabled, which Node did not have (L107). On SQLite
// the column stands in the table's statement text where ALTER TABLE puts
// it, after the last column.
func (c catalog) withDisabled(postgres bool) catalog {
	out := catalog{}
	columns := 0
	for k, v := range c {
		if strings.HasPrefix(k, "column:users.") {
			columns++
		}
		out[k] = v
	}
	if columns == 0 {
		return out
	}
	if postgres {
		out["column:users.disabled"] = fmt.Sprintf(`#%d type=boolean notnull=true default=false identity="" generated=""`, columns)
		return out
	}
	out["column:users.disabled"] = fmt.Sprintf("#%d type=INTEGER notnull=1 default=0 pk=0", columns)
	text := c["table:users"]
	out["table:users"] = text[:strings.LastIndex(text, ")")] + ", disabled INTEGER NOT NULL DEFAULT 0)"
	return out
}

// laterColumn is a column the Go console adds that the Node console never
// had, with what the catalog says of it on each database. sqliteText is
// what ALTER TABLE appends to the table's statement text on SQLite.
type laterColumn struct {
	table, column      string
	sqlite, sqliteText string
	postgres           string
	// sqliteRow and postgresRow are what an existing row reads in the new
	// column ("NULL" for none).
	sqliteRow, postgresRow string
}

// laterColumns are the columns after users.disabled, in the order Migrate
// adds them. A step that adds a column to a table Node had adds a line
// here, and its index (if any) to laterIndexes.
var laterColumns = []laterColumn{
	{"audit_log", "target_user_id", "type=INTEGER notnull=0 default=NULL pk=0", "target_user_id INTEGER DEFAULT NULL",
		`type=bigint notnull=false default=<none> identity="" generated=""`, "NULL", "NULL"},
}

// withLaterRows is a snapshot's rows as Migrate leaves them: every row of
// a table in laterColumns gets the new column's value at its end.
func withLaterRows(rows snapshot, postgres bool) {
	for _, col := range laterColumns {
		value := col.sqliteRow
		if postgres {
			value = col.postgresRow
		}
		for i, row := range rows[col.table] {
			rows[col.table][i] = row + " | " + value
		}
	}
}

// laterIndexes are the indexes of laterColumns: the catalog key after
// "index:" and the entry on SQLite and on PostgreSQL.
var laterIndexes = []struct{ key, sqlite, postgres string }{
	{"audit_log.idx_audit_log_target",
		"unique=0 origin=c partial=0 columns=(target_user_id) sql=CREATE INDEX idx_audit_log_target ON audit_log(target_user_id)",
		"CREATE INDEX idx_audit_log_target ON audit_log USING btree (target_user_id)"},
}

// withLater is the catalog of a database as the Go console leaves it with
// laterColumns and laterIndexes added. A table the catalog lacks gets
// nothing.
func (c catalog) withLater(postgres bool) catalog {
	out := catalog{}
	for k, v := range c {
		out[k] = v
	}
	for _, col := range laterColumns {
		columns := 0
		for k := range out {
			if strings.HasPrefix(k, "column:"+col.table+".") {
				columns++
			}
		}
		if columns == 0 {
			continue
		}
		key := "column:" + col.table + "." + col.column
		if postgres {
			out[key] = fmt.Sprintf("#%d %s", columns, col.postgres)
			continue
		}
		out[key] = fmt.Sprintf("#%d %s", columns, col.sqlite)
		text := out["table:"+col.table]
		out["table:"+col.table] = text[:strings.LastIndex(text, ")")] + ", " + col.sqliteText + ")"
	}
	for _, idx := range laterIndexes {
		table, _, _ := strings.Cut(idx.key, ".")
		if _, ok := out["table:"+table]; !ok && !postgres {
			continue
		}
		if postgres {
			out["index:"+idx.key] = idx.postgres
		} else {
			out["index:"+idx.key] = idx.sqlite
		}
	}
	return out
}

// diffCatalogs lists what got has differently from want.
func diffCatalogs(want, got catalog) []string {
	var out []string
	for k, w := range want {
		g, ok := got[k]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("missing %s = %s", k, w))
		case g != w:
			out = append(out, fmt.Sprintf("differs %s\n      want %s\n      got  %s", k, w, g))
		}
	}
	for k, g := range got {
		if _, ok := want[k]; !ok {
			out = append(out, fmt.Sprintf("extra   %s = %s", k, g))
		}
	}
	sort.Strings(out)
	return out
}

func requireSameCatalog(t *testing.T, what string, want, got catalog) {
	t.Helper()
	if diff := diffCatalogs(want, got); len(diff) > 0 {
		t.Errorf("%s: %d differences:\n  %s", what, len(diff), strings.Join(diff, "\n  "))
	}
}

func readCatalog(t *testing.T, db *store.DB) catalog {
	t.Helper()
	if db.IsPostgres() {
		return readPostgresCatalog(t, db)
	}
	return readSQLiteCatalog(t, db)
}

const sqliteTables = `sqlite_master m WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`

func readSQLiteCatalog(t *testing.T, db *store.DB) catalog {
	t.Helper()
	c := catalog{}
	for _, r := range queryStrings(t, db, `SELECT m.name, m.sql FROM `+sqliteTables) {
		c["table:"+r[0]] = r[1]
	}
	for _, r := range queryStrings(t, db, `
		SELECT m.name, p.cid, p.name, p.type, p."notnull", COALESCE(p.dflt_value, '<none>'), p.pk
		FROM sqlite_master m JOIN pragma_table_info(m.name) p
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`) {
		c["column:"+r[0]+"."+r[2]] = fmt.Sprintf("#%s type=%s notnull=%s default=%s pk=%s", r[1], r[3], r[4], r[5], r[6])
	}
	for _, r := range queryStrings(t, db, `
		SELECT m.name, il.name, il."unique", il.origin, il.partial,
		       (SELECT group_concat(name, ',') FROM (SELECT COALESCE(name, '<expression>') AS name FROM pragma_index_info(il.name) ORDER BY seqno)),
		       COALESCE((SELECT sql FROM sqlite_master WHERE type = 'index' AND name = il.name), '<automatic>')
		FROM sqlite_master m JOIN pragma_index_list(m.name) il
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`) {
		c["index:"+r[0]+"."+r[1]] = fmt.Sprintf("unique=%s origin=%s partial=%s columns=(%s) sql=%s", r[2], r[3], r[4], r[5], r[6])
	}
	type fk struct{ from, to []string }
	fks := map[string]*fk{}
	heads := map[string]string{}
	for _, r := range queryStrings(t, db, `
		SELECT m.name, f.id, f."table", f."from", COALESCE(f."to", '<primary key>'), f.on_update, f.on_delete
		FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY m.name, f.id, f.seq`) {
		id := r[0] + "#" + r[1]
		if fks[id] == nil {
			fks[id] = &fk{}
			heads[id] = fmt.Sprintf("%s|%s|on update %s on delete %s", r[0], r[2], r[5], r[6])
		}
		fks[id].from = append(fks[id].from, r[3])
		fks[id].to = append(fks[id].to, r[4])
	}
	for id, f := range fks {
		parts := strings.Split(heads[id], "|")
		c["fk:"+parts[0]+".("+strings.Join(f.from, ",")+")"] = fmt.Sprintf("references %s(%s) %s", parts[1], strings.Join(f.to, ","), parts[2])
	}
	for _, r := range queryStrings(t, db, `SELECT tbl_name, name, sql FROM sqlite_master WHERE type = 'trigger'`) {
		c["trigger:"+r[0]+"."+r[1]] = r[2]
	}
	return c
}

func readPostgresCatalog(t *testing.T, db *store.DB) catalog {
	t.Helper()
	c := catalog{}
	schema := queryStrings(t, db, `SELECT current_schema()`)[0][0]
	position := map[string]int{}
	for _, r := range queryStrings(t, db, `
		SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
		       COALESCE(pg_get_expr(d.adbin, d.adrelid), '<none>'), a.attidentity::text, a.attgenerated::text
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
		WHERE n.nspname = current_schema() AND c.relkind = 'r'
		ORDER BY c.relname, a.attnum`) {
		c["table:"+r[0]] = ""
		c["column:"+r[0]+"."+r[1]] = fmt.Sprintf("#%d type=%s notnull=%s default=%s identity=%q generated=%q",
			position[r[0]], r[2], r[3], strings.ReplaceAll(r[4], schema+".", ""), r[5], r[6])
		position[r[0]]++
	}
	for _, r := range queryStrings(t, db, `
		SELECT c.relname, con.conname, con.contype::text, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema()`) {
		c["constraint:"+r[0]+"."+r[1]] = r[2] + " " + strings.ReplaceAll(r[3], schema+".", "")
	}
	for _, r := range queryStrings(t, db, `SELECT tablename, indexname, indexdef FROM pg_indexes WHERE schemaname = current_schema()`) {
		c["index:"+r[0]+"."+r[1]] = strings.ReplaceAll(r[2], " ON "+schema+".", " ON ")
	}
	for _, r := range queryStrings(t, db, `
		SELECT COALESCE(t.relname, '<nothing>'), s.sequencename, s.data_type::text, s.start_value, s.min_value, s.max_value,
		       s.increment_by, s.cycle, COALESCE(a.attname, '<nothing>')
		FROM pg_sequences s
		JOIN pg_namespace n ON n.nspname = s.schemaname
		JOIN pg_class sc ON sc.relname = s.sequencename AND sc.relnamespace = n.oid
		LEFT JOIN pg_depend d ON d.objid = sc.oid AND d.classid = 'pg_class'::regclass AND d.deptype IN ('a', 'i')
		LEFT JOIN pg_class t ON t.oid = d.refobjid
		LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = d.refobjsubid
		WHERE s.schemaname = current_schema()`) {
		c["sequence:"+r[0]+"."+r[1]] = fmt.Sprintf("type=%s start=%s min=%s max=%s increment=%s cycle=%s owned by column %s", r[2], r[3], r[4], r[5], r[6], r[7], r[8])
	}
	for _, r := range queryStrings(t, db, `
		SELECT c.relname, tg.tgname, pg_get_triggerdef(tg.oid)
		FROM pg_trigger tg
		JOIN pg_class c ON c.oid = tg.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND NOT tg.tgisinternal`) {
		c["trigger:"+r[0]+"."+r[1]] = strings.ReplaceAll(r[2], " ON "+schema+".", " ON ")
	}
	for _, r := range queryStrings(t, db, `
		SELECT p.proname, pg_get_functiondef(p.oid)
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = current_schema()`) {
		c["function:"+r[0]] = strings.ReplaceAll(r[1], schema+".", "")
	}
	return c
}

// ---- data ----

// snapshot holds the rows of tables as text, sorted, so two states of a
// database can be compared.
type snapshot map[string][]string

// readRows reads the given columns of a table, all when columns is empty.
func readRows(t *testing.T, db *store.DB, table string, columns []string) []string {
	t.Helper()
	list := "*"
	if len(columns) > 0 {
		quoted := make([]string, len(columns))
		for i, c := range columns {
			quoted[i] = `"` + c + `"`
		}
		list = strings.Join(quoted, ", ")
	}
	var out []string
	for _, r := range queryStrings(t, db, `SELECT `+list+` FROM "`+table+`"`) {
		out = append(out, strings.Join(r, " | "))
	}
	sort.Strings(out)
	return out
}

func readSnapshot(t *testing.T, db *store.DB, tables []string) snapshot {
	t.Helper()
	s := snapshot{}
	for _, table := range tables {
		s[table] = readRows(t, db, table, nil)
	}
	return s
}

func requireSameSnapshot(t *testing.T, what string, want, got snapshot) {
	t.Helper()
	for table, w := range want {
		g := got[table]
		if strings.Join(w, "\n") != strings.Join(g, "\n") {
			t.Errorf("%s: rows of %s changed\n  before: %s\n  after:  %s", what, table, strings.Join(w, "\n          "), strings.Join(g, "\n          "))
		}
	}
	for table := range got {
		if _, ok := want[table]; !ok {
			t.Errorf("%s: unexpected table %s", what, table)
		}
	}
}

// ---- rows for every table ----

type foreignKey struct{ table, column, refTable, refColumn string }

func readForeignKeys(t *testing.T, db *store.DB) []foreignKey {
	t.Helper()
	query := `
		SELECT m.name, f."from", f."table", COALESCE(f."to", 'id')
		FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`
	if db.IsPostgres() {
		query = `
			SELECT c.relname, a.attname, rc.relname, ra.attname
			FROM pg_constraint con
			JOIN pg_class c ON c.oid = con.conrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_class rc ON rc.oid = con.confrelid
			JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = con.conkey[1]
			JOIN pg_attribute ra ON ra.attrelid = con.confrelid AND ra.attnum = con.confkey[1]
			WHERE con.contype = 'f' AND n.nspname = current_schema()`
	}
	var out []foreignKey
	for _, r := range queryStrings(t, db, query) {
		out = append(out, foreignKey{r[0], r[1], r[2], r[3]})
	}
	return out
}

// fixtureOverrides are the columns whose value is not free: Migrate
// rewrites every product type that is not the current one, as the Node
// console did.
var fixtureOverrides = map[string]any{
	"agent_bundles.product_type": "neodesk-support",
}

// fillTables puts rowsPerTable rows into each of the given tables, parents
// before children, with a different value in every column of every row.
// It reads the tables' shape from the database as it is now.
func fillTables(t *testing.T, db *store.DB, tables []string, rowsPerTable int) {
	t.Helper()
	ctx := testContext(t)
	shape, err := db.Inspect(ctx)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	wanted := map[string]bool{}
	for _, table := range tables {
		wanted[table] = true
	}
	parents := map[string][]foreignKey{}
	for _, fk := range readForeignKeys(t, db) {
		parents[fk.table] = append(parents[fk.table], fk)
	}

	var order []string
	done := map[string]bool{}
	var visit func(table string)
	visit = func(table string) {
		if done[table] || !wanted[table] {
			return
		}
		done[table] = true
		for _, fk := range parents[table] {
			visit(fk.refTable)
		}
		order = append(order, table)
	}
	sorted := append([]string(nil), tables...)
	sort.Strings(sorted)
	for _, table := range sorted {
		visit(table)
	}

	// One transaction for all rows: a commit per row costs a disk sync each.
	err = db.InTx(ctx, func(tx *store.Tx) error {
		for _, table := range order {
			tbl := shape.Table(table)
			for i := 0; i < rowsPerTable; i++ {
				var cols, marks []string
				var args []any
				for _, col := range tbl.Columns() {
					if col.Serial {
						continue
					}
					value := fixtureValue(db, table, col, i)
					for _, fk := range parents[table] {
						if fk.column == col.Name {
							var ref any
							lookup := fmt.Sprintf(`SELECT "%s" FROM "%s" ORDER BY 1 LIMIT 1 OFFSET %d`, fk.refColumn, fk.refTable, i)
							if err := tx.QueryRow(ctx, lookup).Scan(&ref); err != nil {
								return fmt.Errorf("row %d of %s for %s.%s: %w", i, fk.refTable, table, col.Name, err)
							}
							value = ref
						}
					}
					cols = append(cols, `"`+col.Name+`"`)
					marks = append(marks, "?")
					args = append(args, value)
				}
				query := fmt.Sprintf(`INSERT INTO "%s" (%s) VALUES (%s)`, table, strings.Join(cols, ", "), strings.Join(marks, ", "))
				if _, err := tx.Exec(ctx, query, args...); err != nil {
					return fmt.Errorf("insert row %d into %s: %w", i, table, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("fill tables: %v", err)
	}
}

func fixtureValue(db *store.DB, table string, col store.Column, i int) any {
	if v, ok := fixtureOverrides[table+"."+col.Name]; ok {
		return v
	}
	if table == "agent_bundles" && col.Name == "revoked" {
		// The console lists only bundles that are not revoked.
		return store.BoolArg(col.Type, false)
	}
	stamp := time.Date(2026, 1, 2+i, 3, 4, 5, 123456000, time.UTC)
	switch col.Type {
	case store.TypeInteger, store.TypeBigInt:
		return int64(i + 1)
	case store.TypeBoolean:
		return i%2 == 0
	case store.TypeReal, store.TypeDouble:
		return 1.5 + float64(i)
	case store.TypeBlob:
		return []byte{0xde, 0xad, byte(i)}
	case store.TypeTimestampTZ, store.TypeTimestamp, store.TypeDate:
		return stamp
	case store.TypeDateTime:
		return store.SQLiteTime(stamp.Truncate(time.Second))
	case store.TypeJSON, store.TypeJSONB:
		return fmt.Sprintf(`{"row": %d}`, i)
	case store.TypeText:
		switch {
		case strings.Contains(col.Default, "'{"):
			return fmt.Sprintf(`{"row":%d}`, i)
		case strings.Contains(col.Default, "'["):
			return fmt.Sprintf(`[%d]`, i)
		case !db.IsPostgres() && looksLikeTime(col.Name):
			return store.SQLiteTime(stamp.Truncate(time.Second))
		}
		return fmt.Sprintf("%s.%s #%d", table, col.Name, i)
	}
	return nil
}

func looksLikeTime(column string) bool {
	switch column {
	case "last_login", "last_online", "last_used", "locked_until", "timestamp":
		return true
	}
	return strings.HasSuffix(column, "_at")
}
