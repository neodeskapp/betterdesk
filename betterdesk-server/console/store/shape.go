package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// ColumnType is the kind of a column, reduced to what changes how a value
// is read, written or sent as JSON.
type ColumnType int

// The column types the console's tables use.
const (
	// TypeMissing is the type of a column that does not exist.
	TypeMissing     ColumnType = iota
	TypeInteger                // SQLite INTEGER; PostgreSQL integer and smallint
	TypeBigInt                 // PostgreSQL bigint
	TypeBoolean                // PostgreSQL boolean
	TypeText                   // text
	TypeReal                   // SQLite REAL; PostgreSQL real
	TypeDouble                 // PostgreSQL double precision
	TypeNumeric                // PostgreSQL numeric
	TypeBlob                   // SQLite BLOB; PostgreSQL bytea
	TypeTimestampTZ            // PostgreSQL timestamp with time zone
	TypeTimestamp              // PostgreSQL timestamp without time zone
	TypeDate                   // PostgreSQL date
	TypeDateTime               // SQLite text declared DATETIME, DATE or TIMESTAMP
	TypeJSON                   // PostgreSQL json
	TypeJSONB                  // PostgreSQL jsonb
	TypeOther                  // anything else
)

var columnTypeNames = [...]string{
	"missing", "integer", "bigint", "boolean", "text", "real", "double", "numeric", "blob",
	"timestamptz", "timestamp", "date", "datetime", "json", "jsonb", "other",
}

func (t ColumnType) String() string {
	if int(t) < len(columnTypeNames) {
		return columnTypeNames[t]
	}
	return "other"
}

// Column describes one column of a table as the database has it.
type Column struct {
	Name string
	// Declared is the type as the database spells it: "INTEGER", "bigint",
	// "timestamp with time zone".
	Declared string
	Type     ColumnType
	NotNull  bool
	// Default is the default expression, empty when there is none.
	Default    string
	PrimaryKey bool
	// Serial reports that the database assigns the value: INTEGER PRIMARY KEY
	// on SQLite, SERIAL, BIGSERIAL or an identity column on PostgreSQL.
	Serial bool
}

// Table is the shape of one table. The zero Table stands for a table that
// does not exist.
type Table struct {
	name    string
	columns []Column
	byName  map[string]int
}

// Name returns the table's name.
func (t *Table) Name() string { return t.name }

// Exists reports whether the table is in the database.
func (t *Table) Exists() bool { return len(t.columns) > 0 }

// Has reports whether the table has the column.
func (t *Table) Has(column string) bool {
	_, ok := t.byName[column]
	return ok
}

// Type returns the type of the column, TypeMissing when there is none.
func (t *Table) Type(column string) ColumnType {
	return t.Column(column).Type
}

// Column returns the column, and a Column of TypeMissing when there is
// none.
func (t *Table) Column(column string) Column {
	if i, ok := t.byName[column]; ok {
		return t.columns[i]
	}
	return Column{Name: column}
}

// Columns returns the columns in the order the table has them.
func (t *Table) Columns() []Column {
	return append([]Column(nil), t.columns...)
}

// Shape is what the tables of a database looked like when it was read.
// The tables the console shares with the signal server come in two
// variants, depending on which of the two created them, and code that
// reads or writes them asks here which one it has:
//
//	if db.Shape().Table("address_books").Has("username") { ... }
//	if db.Shape().Table("audit_files").Type("is_file") == store.TypeBoolean { ... }
//
// A Shape does not change after it was made.
type Shape struct {
	dialect Dialect
	tables  map[string]*Table
}

// Dialect reports which database the shape was read from.
func (s *Shape) Dialect() Dialect { return s.dialect }

// Table returns the shape of a table. For a table that does not exist the
// result has no columns and Exists reports false.
func (s *Shape) Table(name string) *Table {
	if t, ok := s.tables[name]; ok {
		return t
	}
	return &Table{name: name}
}

// Tables returns the names of all tables, sorted.
func (s *Shape) Tables() []string {
	names := make([]string, 0, len(s.tables))
	for name := range s.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (d *DB) setShape(s *Shape) { d.shape.Store(s) }

// Shape returns the table shapes read by the last Migrate or Inspect. It
// panics when neither has run: asking before the schema is known is a
// mistake in the start order.
func (d *DB) Shape() *Shape {
	if s := d.shape.Load(); s != nil {
		return s
	}
	panic("store: Shape called before Migrate or Inspect")
}

// Inspect reads the shape of every table and keeps it for Shape. Migrate
// does this at its end; call Inspect when something else changed the
// schema afterwards.
func (d *DB) Inspect(ctx context.Context) (*Shape, error) {
	shape, err := inspect(ctx, d.read, d.dialect)
	if err != nil {
		return nil, err
	}
	d.setShape(shape)
	return shape, nil
}

type contextQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const sqliteShapeQuery = `
	SELECT m.name, p.name, p.type, p."notnull", p.dflt_value, p.pk,
	       (SELECT COUNT(*) FROM pragma_table_info(m.name) k WHERE k.pk > 0)
	FROM sqlite_master m JOIN pragma_table_info(m.name) p
	WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
	ORDER BY m.name, p.cid`

const postgresShapeQuery = `
	SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
	       pg_get_expr(d.adbin, d.adrelid),
	       CASE WHEN a.attnum = ANY (COALESCE(pk.conkey, '{}')) THEN 1 ELSE 0 END,
	       CASE WHEN a.attidentity <> '' THEN 1 ELSE 0 END
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
	LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
	LEFT JOIN pg_constraint pk ON pk.conrelid = c.oid AND pk.contype = 'p'
	WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p')
	ORDER BY c.relname, a.attnum`

func inspect(ctx context.Context, q contextQuerier, dialect Dialect) (*Shape, error) {
	query := sqliteShapeQuery
	if dialect == Postgres {
		query = postgresShapeQuery
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read the table shapes: %w", err)
	}
	defer rows.Close()

	shape := &Shape{dialect: dialect, tables: map[string]*Table{}}
	for rows.Next() {
		var table string
		var col Column
		var def sql.NullString
		var pk, extra int
		if err := rows.Scan(&table, &col.Name, &col.Declared, &col.NotNull, &def, &pk, &extra); err != nil {
			return nil, fmt.Errorf("read the table shapes: %w", err)
		}
		col.Default = def.String
		col.PrimaryKey = pk > 0
		if dialect == Postgres {
			col.Type = postgresColumnType(col.Declared)
			col.Serial = extra > 0 || strings.HasPrefix(col.Default, "nextval(")
		} else {
			col.Type = sqliteColumnType(col.Declared)
			// A lone INTEGER PRIMARY KEY is the rowid, which SQLite assigns.
			col.Serial = col.PrimaryKey && extra == 1 && strings.EqualFold(col.Declared, "INTEGER")
		}
		t := shape.tables[table]
		if t == nil {
			t = &Table{name: table, byName: map[string]int{}}
			shape.tables[table] = t
		}
		t.byName[col.Name] = len(t.columns)
		t.columns = append(t.columns, col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the table shapes: %w", err)
	}
	return shape, nil
}

// sqliteColumnType follows SQLite's own rules for the affinity of a
// declared type, with the declarations the driver turns into time values
// kept apart.
func sqliteColumnType(declared string) ColumnType {
	d := strings.ToUpper(strings.TrimSpace(declared))
	switch d {
	case "DATETIME", "DATE", "TIMESTAMP":
		return TypeDateTime
	case "":
		return TypeBlob
	}
	switch {
	case strings.Contains(d, "INT"):
		return TypeInteger
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return TypeText
	case strings.Contains(d, "BLOB"):
		return TypeBlob
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return TypeReal
	}
	return TypeOther
}

func postgresColumnType(declared string) ColumnType {
	switch declared {
	case "integer", "smallint":
		return TypeInteger
	case "bigint":
		return TypeBigInt
	case "boolean":
		return TypeBoolean
	case "text":
		return TypeText
	case "real":
		return TypeReal
	case "double precision":
		return TypeDouble
	case "bytea":
		return TypeBlob
	case "timestamp with time zone":
		return TypeTimestampTZ
	case "timestamp without time zone":
		return TypeTimestamp
	case "date":
		return TypeDate
	case "json":
		return TypeJSON
	case "jsonb":
		return TypeJSONB
	}
	switch {
	case strings.HasPrefix(declared, "character"):
		return TypeText
	case strings.HasPrefix(declared, "numeric"):
		return TypeNumeric
	case strings.HasPrefix(declared, "timestamp") && strings.HasSuffix(declared, "with time zone"):
		return TypeTimestampTZ
	case strings.HasPrefix(declared, "timestamp"):
		return TypeTimestamp
	}
	return TypeOther
}
