package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The logical dump of the database (console/data.json of a full backup):
//
//	{"_engine":"sqlite","tables":{"<table>":[{"<column>":<value>,...},...],...}}
//
// Every table of the database is in it, the server's tables too, sorted by
// name, each row with its columns in the table's order and each value as
// the old console's driver handed it to JSON.stringify:
//
//	                SQLite                    PostgreSQL
//	integer         number (inexact past 2^53) int2, int4: number; int8, numeric: string
//	real            number                    float4: its short form; float8: number
//	text            string                    string
//	blob, bytea     {"__buf__":"<base64>"}    {"__buf__":"<base64>"}
//	time            the stored text           ISO text in UTC with milliseconds
//	json, jsonb     the stored text           the parsed value
//	boolean         -                         true, false
//
// A table whose name is no plain identifier is left out, and so is one
// that cannot be read.

// safeIdent is lib/safeSqlIdent.js: letters, digits and underscores, not
// starting with a digit.
var safeIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isSafeIdent(name string) bool { return safeIdent.MatchString(name) }

// quoteIdent quotes a name for SQL.
func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// DumpAllTables returns the dump as JSON text.
func DumpAllTables(ctx context.Context, db *store.DB) ([]byte, error) {
	engine := "sqlite"
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`
	if db.IsPostgres() {
		engine = "postgres"
		query = `SELECT table_name FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_type = 'BASE TABLE' ORDER BY table_name`
	}
	names, err := readStrings(ctx, db, query)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	var out bytes.Buffer
	out.WriteString(`{"_engine":"` + engine + `","tables":{`)
	first := true
	for _, name := range names {
		if !isSafeIdent(name) {
			continue
		}
		rows, err := dumpTable(ctx, db, name)
		if err != nil {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		out.Write(jsQuote(nil, name))
		out.WriteByte(':')
		out.Write(rows)
	}
	out.WriteString("}}")
	return out.Bytes(), nil
}

func readStrings(ctx context.Context, q store.Querier, query string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// dumpTable returns the rows of one table as a JSON array.
func dumpTable(ctx context.Context, db *store.DB, table string) ([]byte, error) {
	if db.IsPostgres() {
		return dumpPostgresTable(ctx, db, table)
	}
	return dumpSQLiteTable(ctx, db, table)
}

// dumpSQLiteTable reads every column through unary plus, which leaves the
// value as stored but takes away the declared type: the SQLite driver
// turns text in a DATETIME column into a time otherwise, and the dump
// needs the text.
func dumpSQLiteTable(ctx context.Context, db *store.DB, table string) ([]byte, error) {
	probe, err := db.Query(ctx, `SELECT * FROM `+quoteIdent(table)+` LIMIT 0`)
	if err != nil {
		return nil, err
	}
	columns, err := probe.Columns()
	probe.Close()
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return []byte("[]"), nil
	}
	exprs := make([]string, len(columns))
	for i, c := range columns {
		exprs[i] = "+" + quoteIdent(c) + " AS " + quoteIdent(c)
	}
	rows, err := db.Query(ctx, `SELECT `+strings.Join(exprs, ", ")+` FROM `+quoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	var out bytes.Buffer
	out.WriteByte('[')
	n := 0
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		if n > 0 {
			out.WriteByte(',')
		}
		n++
		out.WriteByte('{')
		for i, c := range columns {
			if i > 0 {
				out.WriteByte(',')
			}
			out.Write(jsQuote(nil, c))
			out.WriteByte(':')
			out.Write(sqliteValueJSON(values[i]))
		}
		out.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}

// sqliteValueJSON is the JSON of a value better-sqlite3 returned.
func sqliteValueJSON(v any) []byte {
	switch x := v.(type) {
	case nil:
		return []byte("null")
	case int64:
		raw, _ := jsbind.CellOf(x).MarshalJSON()
		return raw
	case float64:
		return []byte(jsbind.Number(x).JSON())
	case string:
		return jsQuote(nil, nodeUTF8([]byte(x)))
	case []byte:
		return bufJSON(x)
	}
	return []byte("null")
}

// bufJSON is a Buffer as the dump writes it.
func bufJSON(b []byte) []byte {
	return []byte(`{"__buf__":"` + base64.StdEncoding.EncodeToString(b) + `"}`)
}

// dumpPostgresTable reads a table with SELECT * and turns each value into
// what node-postgres made of its type.
func dumpPostgresTable(ctx context.Context, db *store.DB, table string) ([]byte, error) {
	rows, err := store.Select(ctx, db, `SELECT * FROM `+quoteIdent(table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('[')
	n := 0
	for rows.Next() {
		dest := make([]any, len(types))
		for i, t := range types {
			dest[i] = postgresTarget(t.DatabaseTypeName())
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if n > 0 {
			out.WriteByte(',')
		}
		n++
		out.WriteByte('{')
		for i, t := range types {
			if i > 0 {
				out.WriteByte(',')
			}
			out.Write(jsQuote(nil, t.Name()))
			out.WriteByte(':')
			raw, err := postgresValueJSON(dest[i])
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", t.Name(), err)
			}
			out.Write(raw)
		}
		out.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}

// pgBytes is a bytea column.
type pgBytes struct {
	b     []byte
	valid bool
}

func (p *pgBytes) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*p = pgBytes{}
	case []byte:
		*p = pgBytes{b: append([]byte(nil), v...), valid: true}
	case string:
		*p = pgBytes{b: []byte(v), valid: true}
	default:
		return fmt.Errorf("cannot scan %T into bytea", src)
	}
	return nil
}

// pgText is a column node-postgres passed on as its text.
type pgText struct {
	s     string
	valid bool
}

func (p *pgText) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*p = pgText{}
	case string:
		*p = pgText{s: v, valid: true}
	case []byte:
		*p = pgText{s: string(v), valid: true}
	default:
		*p = pgText{s: fmt.Sprint(v), valid: true}
	}
	return nil
}

func postgresTarget(typeName string) any {
	switch strings.ToUpper(typeName) {
	case "INT2", "INT4", "OID":
		return new(sql.NullInt64)
	case "INT8", "NUMERIC":
		return new(store.Int64String)
	case "FLOAT4", "FLOAT8":
		return new(store.Float)
	case "BOOL":
		return new(sql.NullBool)
	case "TIMESTAMPTZ", "TIMESTAMP", "DATE":
		return new(store.Time)
	case "JSON", "JSONB":
		return new(store.JSONText)
	case "BYTEA":
		return new(pgBytes)
	}
	return new(pgText)
}

func postgresValueJSON(v any) ([]byte, error) {
	switch x := v.(type) {
	case *sql.NullInt64:
		if !x.Valid {
			return []byte("null"), nil
		}
		return strconv.AppendInt(nil, x.Int64, 10), nil
	case *sql.NullBool:
		if !x.Valid {
			return []byte("null"), nil
		}
		return strconv.AppendBool(nil, x.Bool), nil
	case *pgBytes:
		if !x.valid {
			return []byte("null"), nil
		}
		return bufJSON(x.b), nil
	case *pgText:
		if !x.valid {
			return []byte("null"), nil
		}
		return jsQuote(nil, x.s), nil
	case interface{ MarshalJSON() ([]byte, error) }:
		return x.MarshalJSON()
	}
	return []byte("null"), nil
}

// jsQuote appends s as JSON.stringify writes a string: only the quote,
// the backslash and control characters are escaped.
func jsQuote(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				buf = append(buf, '\\', c)
			case c == '\b':
				buf = append(buf, '\\', 'b')
			case c == '\f':
				buf = append(buf, '\\', 'f')
			case c == '\n':
				buf = append(buf, '\\', 'n')
			case c == '\r':
				buf = append(buf, '\\', 'r')
			case c == '\t':
				buf = append(buf, '\\', 't')
			case c < 0x20:
				buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			default:
				buf = append(buf, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf = append(buf, "\ufffd"...)
		} else {
			buf = append(buf, s[i:i+size]...)
		}
		i += size
	}
	return append(buf, '"')
}
