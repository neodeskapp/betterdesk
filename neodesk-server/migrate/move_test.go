package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	serverdb "github.com/neodeskapp/neodesk/neodesk-server/db"
)

// origin is the history of a test database: which program created the
// tables first.
type origin int

const (
	goServerFirst origin = iota
	consoleFirst
	nodeFirst
)

func (o origin) String() string {
	return [...]string{"go-server-first", "console-first", "node-first"}[o]
}

var origins = []origin{goServerFirst, consoleFirst, nodeFirst}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// prepareOrigin gives an empty database the tables of the server and the
// console as the origin's history leaves them, the console's session
// table included.
func prepareOrigin(t *testing.T, sdb *store.DB, o origin) *store.Shape {
	t.Helper()
	ctx := testCtx(t)
	migrate := func() {
		if err := sdb.Migrate(ctx); err != nil {
			t.Fatalf("console migration: %v", err)
		}
	}
	switch o {
	case goServerFirst:
		storetest.GoServerMigrate(t, sdb)
		migrate()
	case consoleFirst:
		migrate()
		storetest.GoServerMigrate(t, sdb)
	case nodeFirst:
		storetest.NodeSchema(t, sdb)
		migrate()
		storetest.GoServerMigrate(t, sdb)
	}
	if _, err := session.New(ctx, session.Options{DB: sdb, Secret: "test"}); err != nil {
		t.Fatalf("session table: %v", err)
	}
	shape, err := sdb.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return shape
}

// pgAddress returns the address of the PostgreSQL schema sdb works in.
func pgAddress(t *testing.T, sdb *store.DB) string {
	t.Helper()
	var database, schema string
	if err := sdb.QueryRow(testCtx(t), `SELECT current_database(), current_schema()`).Scan(&database, &schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv(storetest.PostgresEnv))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + database
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// sqliteFile opens a new SQLite database in a test folder.
func sqliteFile(t *testing.T, name string) (*store.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	sdb, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	return sdb, path
}

// openSQLiteStore opens a SQLite file the tool wrote, after the tool is done.
func openSQLiteStore(t *testing.T, path string) *store.DB {
	t.Helper()
	sdb, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	if _, err := sdb.Inspect(testCtx(t)); err != nil {
		t.Fatal(err)
	}
	return sdb
}

// reference is an empty database of the other kind with the same
// history: what a column of the source becomes there, and the foreign
// keys there.
type reference struct {
	shape *store.Shape
	fks   map[string]map[string][2]string
}

func referenceOf(t *testing.T, postgres bool, o origin) reference {
	t.Helper()
	var sdb *store.DB
	if postgres {
		sdb = storetest.Postgres(t)
	} else {
		sdb, _ = sqliteFile(t, "reference.sqlite3")
	}
	shape := prepareOrigin(t, sdb, o)
	return reference{shape: shape, fks: foreignKeys(t, sdb, shape)}
}

// foreignKeys returns, per table, the columns that point at another
// table's column.
func foreignKeys(t *testing.T, sdb *store.DB, shape *store.Shape) map[string]map[string][2]string {
	t.Helper()
	ctx := testCtx(t)
	out := map[string]map[string][2]string{}
	add := func(table, col, refTable, refCol string) {
		if out[table] == nil {
			out[table] = map[string][2]string{}
		}
		out[table][col] = [2]string{refTable, refCol}
	}
	if sdb.IsPostgres() {
		rows, err := sdb.Query(ctx, `
			SELECT c.relname, a.attname, r.relname, ra.attname
			FROM pg_constraint k
			JOIN pg_class c ON c.oid = k.conrelid
			JOIN pg_class r ON r.oid = k.confrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.conkey[1]
			JOIN pg_attribute ra ON ra.attrelid = r.oid AND ra.attnum = k.confkey[1]
			WHERE k.contype = 'f' AND n.nspname = current_schema()`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c, d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			add(a, b, c, d)
		}
		return out
	}
	for _, name := range shape.Tables() {
		rows, err := sdb.Query(ctx, `SELECT "from", "table", COALESCE("to", 'id') FROM pragma_foreign_key_list(?)`, name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var col, refTable, refCol string
			if err := rows.Scan(&col, &refTable, &refCol); err != nil {
				t.Fatal(err)
			}
			add(name, col, refTable, refCol)
		}
		rows.Close()
	}
	return out
}

// fillOrder puts every table after the tables it points at.
func fillOrder(shape *store.Shape, fks map[string]map[string][2]string) []string {
	var out []string
	done := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if done[name] {
			return
		}
		done[name] = true
		for _, ref := range fks[name] {
			if ref[0] != name {
				visit(ref[0])
			}
		}
		out = append(out, name)
	}
	for _, name := range shape.Tables() {
		visit(name)
	}
	return out
}

// baseTime is the time of row i in column field: whole seconds, with
// milliseconds where the column's SQLite form keeps them.
func baseTime(i int, field string) time.Time {
	at := time.Date(2026, 11, 17, 1, 5, 51+i, 0, time.UTC)
	switch sqliteTimeForms[field] {
	case isoTimeForm, millisTimeForm:
		at = at.Add(694 * time.Millisecond)
	}
	return at
}

// fill writes two rows into every table of the database: the first with
// a value in every column, the second with NULL in every column both
// databases leave nullable. Values follow the forms the server writes:
// times as text in the column's form on SQLite, flags as 0 and 1, JSON as
// text. Ids are set, starting at 40.
func fill(t *testing.T, sdb *store.DB, shape *store.Shape, ref reference) {
	t.Helper()
	ctx := testCtx(t)
	other := ref.shape
	// The rows point at existing rows by the foreign keys of both sides.
	fks := foreignKeys(t, sdb, shape)
	for name, cols := range ref.fks {
		for col, to := range cols {
			if shape.Table(name).Has(col) && shape.Table(to[0]).Has(to[1]) {
				if fks[name] == nil {
					fks[name] = map[string][2]string{}
				}
				fks[name][col] = to
			}
		}
	}
	ids := map[string][]any{}
	for _, name := range fillOrder(shape, fks) {
		tbl := shape.Table(name)
		ot := other.Table(name)
		for i := 0; i < 2; i++ {
			var cols, marks []string
			var args []any
			row := map[string]any{}
			for _, c := range tbl.Columns() {
				oc := ot.Column(c.Name)
				field := name + "." + c.Name
				var v any
				if ref, ok := fks[name][c.Name]; ok {
					v = ids[ref[0]+"."+ref[1]][i]
				} else {
					v = value(sdb.IsPostgres(), c, oc, field, i)
				}
				nullable := !c.NotNull && !c.PrimaryKey && (oc.Type == store.TypeMissing || !oc.NotNull)
				_, isRef := fks[name][c.Name]
				sqliteEmptyTime := !sdb.IsPostgres() && oc.Type == store.TypeTimestampTZ && c.Default == "''"
				if i == 1 && nullable && !isRef && !sqliteEmptyTime {
					v = nil
				}
				switch field {
				case "remote_targets.protocol":
					v = "rdp"
				case "address_books.username":
					v = fmt.Sprintf("users.username#%d", i)
				case "settings.value":
					v = fmt.Sprintf(`{"theme":"dark","row":%d}`, i)
				}
				row[c.Name] = v
				cols = append(cols, quoteIdent(c.Name))
				marks = append(marks, "?")
				args = append(args, v)
			}
			for _, c := range tbl.Columns() {
				ids[name+"."+c.Name] = append(ids[name+"."+c.Name], row[c.Name])
			}
			insert := "INSERT INTO " + quoteIdent(name) + " (" + strings.Join(cols, ", ") + ")"
			if sdb.IsPostgres() {
				insert += " OVERRIDING SYSTEM VALUE"
			}
			insert += " VALUES (" + strings.Join(marks, ", ") + ")"
			if _, err := sdb.Exec(ctx, insert, args...); err != nil {
				t.Fatalf("fill %s row %d: %v", name, i, err)
			}
		}
	}
}

// value is the value of row i in column c, whose counterpart in the other
// database is oc.
func value(postgres bool, c, oc store.Column, field string, i int) any {
	pgType, otherType := c.Type, oc.Type
	if !postgres {
		pgType = oc.Type
	}
	switch c.Type {
	case store.TypeInteger, store.TypeBigInt:
		switch {
		case c.Serial || c.PrimaryKey:
			return int64(40 + i)
		case otherType == store.TypeBoolean:
			return int64(1 - i)
		case otherType == store.TypeTimestampTZ:
			return baseTime(i, field).UnixMilli()
		case pgType == store.TypeBigInt:
			return int64(5000000000 + i)
		}
		return int64(100 + i)
	case store.TypeBoolean:
		return i == 0
	case store.TypeReal, store.TypeDouble:
		// Not exact as a four-byte float: PostgreSQL's real keeps 12.1
		// as 12.100000381469727.
		return 12.1 + float64(i)
	case store.TypeNumeric:
		return fmt.Sprintf("12.%d0", 5+i)
	case store.TypeBlob:
		return []byte{1, 2, 3, byte(i)}
	case store.TypeTimestampTZ:
		return baseTime(i, field)
	case store.TypeDateTime:
		return baseTime(i, field).Format(sqliteTimeForm)
	case store.TypeJSON, store.TypeJSONB:
		return fmt.Sprintf(`{"a":[1,"x"],"row":%d}`, i)
	}
	switch otherType {
	case store.TypeTimestampTZ:
		form := sqliteTimeForm
		if f, ok := sqliteTimeForms[field]; ok {
			form = f
		}
		return baseTime(i, field).Format(form)
	case store.TypeJSON, store.TypeJSONB:
		return fmt.Sprintf(`{"a":[1,"x"],"row":%d}`, i)
	}
	return fmt.Sprintf("%s#%d", field, i)
}

// normalized turns a value read from column own, whose counterpart on the
// other side is other, into one both sides can be compared by: times as
// microseconds cut to step (the precision of the SQLite text when the two
// sides are different databases), flags as bool, JSON parsed, four-byte
// floats as float32.
func normalized(t *testing.T, v any, own, other store.Column, pgSide store.ColumnType, step int64) any {
	t.Helper()
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok && own.Type != store.TypeBlob {
		v = string(b)
	}
	has := func(ct ...store.ColumnType) bool {
		for _, c := range ct {
			if own.Type == c || other.Type == c {
				return true
			}
		}
		return false
	}
	switch {
	case has(store.TypeTimestampTZ, store.TypeTimestamp):
		var micro int64
		switch x := v.(type) {
		case time.Time:
			micro = x.UnixMicro()
		case int64:
			micro = x * 1000
		case string:
			if x == "" {
				return nil
			}
			at, ok := store.ParseTime(x)
			if !ok {
				t.Fatalf("%s: %q is no time", own.Name, x)
			}
			micro = at.UnixMicro()
		default:
			t.Fatalf("%s: a %T is no time", own.Name, v)
		}
		return micro - micro%step
	case has(store.TypeBoolean):
		switch x := v.(type) {
		case bool:
			return x
		case int64:
			return x != 0
		}
	case has(store.TypeJSON, store.TypeJSONB):
		var out any
		if err := json.Unmarshal([]byte(v.(string)), &out); err != nil {
			t.Fatalf("%s: %q: %v", own.Name, v, err)
		}
		return out
	case pgSide == store.TypeReal:
		if f, ok := v.(float64); ok {
			return float32(f)
		}
	case own.Type == store.TypeDateTime:
		if at, ok := v.(time.Time); ok {
			return at.UTC().Format(sqliteTimeForm)
		}
	}
	return v
}

// readTable reads the columns of a table, each row normalized and printed.
func readTable(t *testing.T, sdb *store.DB, name string, cols []string, query string, own, other *store.Shape) []string {
	t.Helper()
	ctx := testCtx(t)
	if query == "" {
		quoted := make([]string, len(cols))
		for i, c := range cols {
			quoted[i] = quoteIdent(c)
		}
		query = "SELECT " + strings.Join(quoted, ", ") + " FROM " + quoteIdent(name)
	}
	rows, err := sdb.Query(ctx, query)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, c := range cols {
			oc, ownCol := other.Table(name).Column(c), own.Table(name).Column(c)
			pgSide := ownCol.Type
			if !sdb.IsPostgres() {
				pgSide = oc.Type
			}
			step := int64(1)
			if own.Dialect() == other.Dialect() {
				pgSide = store.TypeMissing
			} else {
				// SQLite keeps whole seconds, or milliseconds in the
				// columns whose form has them.
				step = 1000000
				switch sqliteTimeForms[name+"."+c] {
				case isoTimeForm, millisTimeForm:
					step = 1000
				}
			}
			parts[i] = fmt.Sprintf("%s=%#v", c, normalized(t, values[i], ownCol, oc, pgSide, step))
		}
		out = append(out, strings.Join(parts, " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// requireSameRows compares every table of the source with the
// destination, row by row, through the same reads on both sides; the
// columns named in skip ("table.column") are left out.
func requireSameRows(t *testing.T, src, dst *store.DB, skip ...string) {
	t.Helper()
	ctx := testCtx(t)
	srcShape, err := src.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dstShape, err := dst.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range srcShape.Tables() {
		if name == "neodesk_migrations" {
			continue
		}
		dt := dstShape.Table(name)
		if !dt.Exists() {
			t.Errorf("table %s is missing in the destination", name)
			continue
		}
		var cols []string
		for _, c := range srcShape.Table(name).Columns() {
			if dt.Has(c.Name) && !slices.Contains(skip, name+"."+c.Name) {
				cols = append(cols, c.Name)
			}
		}
		srcQuery, dstQuery := "", ""
		if name == "address_books" {
			cols = []string{"username", "ab_type", "data", "updated_at"}
			owner := func(db *store.DB, shape *store.Shape) string {
				if shape.Table(name).Has("username") {
					return `SELECT username, ab_type, data, updated_at FROM address_books`
				}
				return `SELECT u.username, a.ab_type, a.data, a.updated_at FROM address_books a JOIN users u ON u.id = a.user_id`
			}
			srcQuery, dstQuery = owner(src, srcShape), owner(dst, dstShape)
		}
		want := readTable(t, src, name, cols, srcQuery, srcShape, dstShape)
		got := readTable(t, dst, name, cols, dstQuery, dstShape, srcShape)
		if len(want) == 0 {
			t.Errorf("%s: the source has no rows, the test does not cover it", name)
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: rows differ\nsource:      %v\ndestination: %v", name, want, got)
		}
	}
}

// requireServerReads reads users and device groups through the signal
// server's own code on both sides.
func requireServerReads(t *testing.T, srcAddr, dstAddr string) {
	t.Helper()
	src, err := serverdb.Open(srcAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := serverdb.Open(dstAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	// The first row has a value in every column. The second has NULL where
	// a column takes it, which the signal server's reads of users do not
	// expect from a row it wrote.
	for _, name := range []string{"users.username#0"} {
		a, errA := src.GetUser(name)
		b, errB := dst.GetUser(name)
		if errA != nil || errB != nil || a == nil || b == nil {
			t.Fatalf("GetUser(%s): %v, %v / %v, %v", name, a, errA, b, errB)
		}
		for _, p := range []*string{&a.CreatedAt, &b.CreatedAt, &a.LastLogin, &b.LastLogin} {
			if at, ok := serverTime(*p); ok {
				*p = at.UTC().String()
			}
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("GetUser(%s) reads differently:\nsource:      %+v\ndestination: %+v", name, *a, *b)
		}
	}
	for _, guid := range []string{"device_groups.guid#0"} {
		ga, errA := src.GetDeviceGroup(guid)
		gb, errB := dst.GetDeviceGroup(guid)
		if errA != nil || errB != nil || ga == nil || gb == nil {
			t.Fatalf("GetDeviceGroup(%s): %v, %v / %v, %v", guid, ga, errA, gb, errB)
		}
		for _, p := range []*string{&ga.CreatedAt, &gb.CreatedAt} {
			if at, ok := serverTime(*p); ok {
				*p = at.UTC().String()
			}
		}
		if !reflect.DeepEqual(ga, gb) {
			t.Errorf("GetDeviceGroup(%s) reads differently:\nsource:      %+v\ndestination: %+v", guid, *ga, *gb)
		}
	}
	// New rows get fresh ids.
	if err := dst.CreateUser(&serverdb.User{Username: "after-the-move", PasswordHash: "hash", Role: "viewer"}); err != nil {
		t.Fatalf("create a user after the move: %v", err)
	}
	u, err := dst.GetUser("after-the-move")
	if err != nil || u == nil || u.ID <= 41 {
		t.Errorf("a user created after the move got id %v (%v), want one after the copied ids", u, err)
	}
}

func requirePostgres(t *testing.T) {
	t.Helper()
	if os.Getenv(storetest.PostgresEnv) == "" {
		t.Skipf("%s is not set", storetest.PostgresEnv)
	}
}

func TestSQLite2PGMovesTheWholeInstall(t *testing.T) {
	requirePostgres(t)
	for _, o := range origins {
		t.Run(o.String(), func(t *testing.T) {
			src, srcPath := sqliteFile(t, "db_v2.sqlite3")
			shape := prepareOrigin(t, src, o)
			fill(t, src, shape, referenceOf(t, true, o))

			dst := storetest.Postgres(t)
			if err := runSQLite2PG(srcPath, pgAddress(t, dst), moveOptions{}); err != nil {
				t.Fatal(err)
			}
			requireSameRows(t, src, dst)
			requireServerReads(t, srcPath, pgAddress(t, dst))
		})
	}
}

func TestPG2SQLiteMovesTheWholeInstall(t *testing.T) {
	requirePostgres(t)
	for _, o := range origins {
		t.Run(o.String(), func(t *testing.T) {
			src := storetest.Postgres(t)
			shape := prepareOrigin(t, src, o)
			fill(t, src, shape, referenceOf(t, false, o))

			dstPath := filepath.Join(t.TempDir(), "db_v2.sqlite3")
			if err := runPG2SQLite(pgAddress(t, src), dstPath, moveOptions{}); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if exists(dstPath + suffix) {
					t.Errorf("the new file has a %s next to it", suffix)
				}
			}
			dst := openSQLiteStore(t, dstPath)
			requireSameRows(t, src, dst)
			requireServerReads(t, pgAddress(t, src), dstPath)
			if done, err := serverdb.SQLiteAuthConsolidated(dstPath); err != nil || !done {
				t.Errorf("the new file does not say auth.db is imported: %v, %v", done, err)
			}
		})
	}
}

func TestSQLiteToPostgresAndBackKeepsEveryRow(t *testing.T) {
	requirePostgres(t)
	for _, o := range origins {
		t.Run(o.String(), func(t *testing.T) {
			src, srcPath := sqliteFile(t, "db_v2.sqlite3")
			shape := prepareOrigin(t, src, o)
			fill(t, src, shape, referenceOf(t, true, o))

			pg := storetest.Postgres(t)
			if err := runSQLite2PG(srcPath, pgAddress(t, pg), moveOptions{}); err != nil {
				t.Fatal(err)
			}
			backPath := filepath.Join(t.TempDir(), "back.sqlite3")
			if err := runPG2SQLite(pgAddress(t, pg), backPath, moveOptions{}); err != nil {
				t.Fatal(err)
			}
			back := openSQLiteStore(t, backPath)
			// PostgreSQL's peer table has no tags (BUGS D4): peer.tags
			// comes back as its default.
			requireSameRows(t, src, back, "peer.tags")
			if a, b := schemaText(t, srcPath), schemaText(t, backPath); a != b {
				t.Errorf("the tables differ after the round trip:\nbefore: %s\nafter:  %s", a, b)
			}
		})
	}
}

// schemaText lists the tables and columns of a SQLite file, without the
// auth.db import marker.
func schemaText(t *testing.T, path string) string {
	t.Helper()
	handle, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	cat, err := readCatalog(testCtx(t), handle, false)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, name := range cat.names() {
		if name == "neodesk_migrations" {
			continue
		}
		b.WriteString(name + "(")
		for _, c := range cat.tables[name].cols {
			fmt.Fprintf(&b, "%s %s %v %s,", c.name, c.declared, c.notNull, c.dflt)
		}
		b.WriteString(") ")
	}
	return b.String()
}

// serverTime reads a time the signal server's code returns as text: the
// console's forms, or PostgreSQL's own text of a timestamptz.
func serverTime(s string) (time.Time, bool) {
	if at, ok := store.ParseTime(s); ok {
		return at, true
	}
	at, err := time.Parse("2006-01-02 15:04:05.999999-07", s)
	return at, err == nil
}
