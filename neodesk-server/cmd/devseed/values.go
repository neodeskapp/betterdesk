package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The seed writes the same values on both databases, each in the form
// that database keeps them in (DECISIONS D3). On SQLite that is what the
// rows always held: times as text ("2026-11-19 10:00:00", or ISO with
// milliseconds where the console stores ISO), flags 0 and 1, JSON as
// text, and every number as a float64, which the column's affinity turns
// into an integer where the column is one. On PostgreSQL the column's type
// decides: a timestamp gets the instant, a boolean true or false, an
// integer column a whole number.

// stampForm is how a time looks on SQLite.
type stampForm int

const (
	// sqlSeconds is SQLite's datetime('now') form: UTC to the second.
	sqlSeconds stampForm = iota
	// isoMillis is JavaScript's toISOString: UTC with milliseconds.
	isoMillis
	// epochMillis is the number of milliseconds since 1970.
	epochMillis
)

// stamp is a time the seed writes: milliseconds since 1970, as computed,
// and the form it has on SQLite.
type stamp struct {
	ms   float64
	form stampForm
}

// instant is the moment the stamp stands for, cut to the precision of
// its SQLite form.
func (s stamp) instant() time.Time {
	t := jsDate(s.ms)
	if s.form == sqlSeconds {
		t = t.Truncate(time.Second)
	}
	return t
}

// text is the stamp as SQLite text (or the number, for epochMillis).
func (s stamp) sqlite() any {
	switch s.form {
	case sqlSeconds:
		return sqliteText(s.ms)
	case isoMillis:
		return isoText(s.ms)
	}
	return s.ms
}

// sqliteText is the time as SQLite's datetime('now') writes it.
func sqliteText(ms float64) string { return jsDate(ms).Format("2006-01-02 15:04:05") }

// isoText is the time as toISOString writes it.
func isoText(ms float64) string { return store.ISOTime(jsDate(ms)) }

// flag is a yes or no column: 0 or 1 on SQLite, true or false in a
// PostgreSQL boolean column.
type flag bool

// jsonText is a JSON document: text on SQLite, jsonb on PostgreSQL.
type jsonText string

// now is the current time of the database, as the statement's own
// datetime('now') or NOW().
type now struct{}

// col is a column of a row and the value it gets.
type col struct {
	name  string
	value any
}

// writer writes the seed's rows into one database.
type writer struct {
	db *store.DB
	// types are the column types of every table on PostgreSQL, as
	// information_schema names them.
	types map[string]map[string]string
	// tables are the names of the tables there are.
	tables map[string]bool
}

func newWriter(ctx context.Context, db *store.DB) (*writer, error) {
	w := &writer{db: db, types: map[string]map[string]string{}, tables: map[string]bool{}}
	if err := w.refresh(ctx); err != nil {
		return nil, err
	}
	return w, nil
}

// refresh reads which tables and columns there are.
func (w *writer) refresh(ctx context.Context) error {
	w.tables = map[string]bool{}
	w.types = map[string]map[string]string{}
	query := `SELECT name, '', '' FROM sqlite_master WHERE type = 'table'`
	if w.db.IsPostgres() {
		query = `SELECT c.table_name, c.column_name, c.data_type FROM information_schema.columns c
			JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
			WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE'`
	}
	rows, err := w.db.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("list the tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, kind string
		if err := rows.Scan(&table, &column, &kind); err != nil {
			return fmt.Errorf("list the tables: %w", err)
		}
		w.tables[table] = true
		if column != "" {
			if w.types[table] == nil {
				w.types[table] = map[string]string{}
			}
			w.types[table][column] = kind
		}
	}
	return rows.Err()
}

// has reports whether the table exists.
func (w *writer) has(table string) bool { return w.tables[table] }

// columns are the names of a table's columns.
func (w *writer) columns(ctx context.Context, table string) ([]string, error) {
	if w.db.IsPostgres() {
		var names []string
		for name := range w.types[table] {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	rows, err := w.db.Query(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("columns of %s: %w", table, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("columns of %s: %w", table, err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// value turns a seed value into the parameter for a column.
func (w *writer) value(table, column string, v any) (any, error) {
	if !w.db.IsPostgres() {
		return sqliteValue(v)
	}
	return postgresValue(w.types[table][column], v)
}

func sqliteValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, undefinedValue:
		return nil, nil
	case string, float64:
		return x, nil
	case flag:
		if x {
			return 1.0, nil
		}
		return 0.0, nil
	case stamp:
		return x.sqlite(), nil
	case jsonText:
		return string(x), nil
	}
	return nil, fmt.Errorf("cannot write %T (%v) to SQLite", v, v)
}

// postgresValue is the parameter for a PostgreSQL column of the type kind.
func postgresValue(kind string, v any) (any, error) {
	integer := kind == "integer" || kind == "bigint" || kind == "smallint"
	switch x := v.(type) {
	case nil, undefinedValue:
		return nil, nil
	case string:
		return x, nil
	case jsonText:
		return string(x), nil
	case float64:
		switch {
		case integer:
			if x != math.Trunc(x) || math.IsInf(x, 0) {
				return nil, fmt.Errorf("%v is not a whole number for a %s column", x, kind)
			}
			return int64(x), nil
		case kind == "text" || kind == "character varying":
			return numberString(x), nil
		}
		return x, nil
	case flag:
		if kind == "boolean" {
			return bool(x), nil
		}
		if x {
			return int64(1), nil
		}
		return int64(0), nil
	case stamp:
		switch {
		case strings.HasPrefix(kind, "timestamp"):
			return x.instant(), nil
		case integer:
			return x.instant().UnixMilli(), nil
		}
		if s, ok := x.sqlite().(string); ok {
			return s, nil
		}
		return numberString(x.ms), nil
	}
	return nil, fmt.Errorf("cannot write %T (%v) to PostgreSQL", v, v)
}

// insert writes one row. suffix follows the VALUES list (an ON CONFLICT
// clause); it may use ? placeholders for extra, which are passed as they
// are.
func (w *writer) insert(ctx context.Context, q store.Querier, table string, cols []col, suffix string, extra ...any) error {
	query, args, err := w.insertStatement(table, cols, suffix)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, query, append(args, extra...)...); err != nil {
		return fmt.Errorf("insert into %s: %w", table, err)
	}
	return nil
}

// insertID writes one row into a table with an integer id and returns the
// id.
func (w *writer) insertID(ctx context.Context, q store.Querier, table string, cols []col) (int64, error) {
	query, args, err := w.insertStatement(table, cols, "")
	if err != nil {
		return 0, err
	}
	id, err := q.Insert(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("insert into %s: %w", table, err)
	}
	return id, nil
}

func (w *writer) insertStatement(table string, cols []col, suffix string) (string, []any, error) {
	names := make([]string, len(cols))
	marks := make([]string, len(cols))
	var args []any
	for i, c := range cols {
		names[i] = quoteName(c.name)
		if _, ok := c.value.(now); ok {
			marks[i] = store.NowSQL(w.db.Dialect())
			continue
		}
		v, err := w.value(table, c.name, c.value)
		if err != nil {
			return "", nil, fmt.Errorf("%s.%s: %w", table, c.name, err)
		}
		marks[i] = "?"
		args = append(args, v)
	}
	query := "INSERT INTO " + table + " (" + strings.Join(names, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if suffix != "" {
		query += " " + suffix
	}
	return query, args, nil
}

// args turns values for the named columns of a table into parameters.
func (w *writer) args(table string, cols []col) ([]any, error) {
	out := make([]any, 0, len(cols))
	for _, c := range cols {
		v, err := w.value(table, c.name, c.value)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", table, c.name, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// quoteName quotes the one column name both databases reserve.
func quoteName(name string) string {
	if name == "user" {
		return `"user"`
	}
	return name
}

// marks is "?, ?, ?" for n parameters.
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func anys(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}
