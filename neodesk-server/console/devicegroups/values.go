package devicegroups

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Rows of the console's tables are *goapi.Object values: the columns in
// the order the statement gave them, each as the old console's driver for
// the database handed it to JSON (store.Int64String for a PostgreSQL
// bigint, store.Time for a time, jsbind.Cell for everything else), and
// the keys the old code added behind them as plain JSON values.

// readRows runs a query and returns its rows.
func readRows(ctx context.Context, q store.Querier, query string, args ...any) ([]*goapi.Object, error) {
	rows, err := store.Select(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := []*goapi.Object{}
	for rows.Next() {
		dest := make([]any, len(types))
		for i, t := range types {
			dest[i] = columnValue(q.Dialect(), t.DatabaseTypeName())
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := goapi.NewObject()
		for i, t := range types {
			row.Set(t.Name(), deref(dest[i]))
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// readRow is readRows for at most one row; nil when there is none.
func readRow(ctx context.Context, q store.Querier, query string, args ...any) (*goapi.Object, error) {
	rows, err := readRows(ctx, q, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// columnValue is what a column of the database type is scanned into.
func columnValue(dialect store.Dialect, typeName string) any {
	switch strings.ToUpper(typeName) {
	case "TIMESTAMPTZ", "TIMESTAMP", "DATE", "DATETIME":
		return new(store.Time)
	case "INT8", "NUMERIC":
		if dialect == store.Postgres {
			return new(store.Int64String)
		}
	case "JSON", "JSONB":
		if dialect == store.Postgres {
			return new(store.JSONText)
		}
	case "FLOAT4":
		return new(store.Float)
	}
	return new(jsbind.Cell)
}

func deref(v any) any {
	switch x := v.(type) {
	case *store.Time:
		return *x
	case *store.Int64String:
		return *x
	case *store.JSONText:
		return *x
	case *store.Float:
		return *x
	case *jsbind.Cell:
		return *x
	}
	return v
}

// Plain is a value of a row or of the API server as JavaScript held it:
// nil, bool, float64, string, []any or *goapi.Object.
func Plain(v any) any {
	switch x := v.(type) {
	case nil, bool, float64, string, *goapi.Object:
		return v
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = Plain(item)
		}
		return out
	case json.Marshaler:
		raw, err := x.MarshalJSON()
		if err != nil {
			return nil
		}
		parsed, err := goapi.Parse(raw)
		if err != nil {
			return nil
		}
		return parsed
	}
	return v
}

// jsTruthy is !!v for a row value.
func jsTruthy(v any) bool { return goapi.Truthy(Plain(v)) }

// jsText is String(v) for a row value.
func jsText(v any) string {
	if o, ok := Plain(v).(*goapi.Object); ok && o != nil {
		return "[object Object]"
	}
	return jsString(Plain(v))
}

// textOr is String(v || fallback).
func textOr(v any, fallback string) string {
	if !jsTruthy(v) {
		return fallback
	}
	return jsText(v)
}

// orValue is v || fallback.
func orValue(v any, fallback any) any {
	if jsTruthy(v) {
		return v
	}
	return fallback
}

// parseIntOf is Number.parseInt(v, 10): the whole number at the start of
// String(v) after white space; ok is false for NaN.
func parseIntOf(v any) (float64, bool) {
	s := strings.TrimLeftFunc(jsText(v), isJSSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	f := jsbind.ParseNumber(s[:end])
	if negative {
		f = -f
	}
	return f, true
}

// argOf is a row value as a statement parameter: the number or text the
// old console passed back to the database.
func argOf(v any) any {
	switch x := Plain(v).(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
		return x
	case string, nil, bool:
		return x
	}
	return jsText(v)
}

// valueOf is a plain JSON value as a request value for jsbind.Args. Lists
// and objects have no counterpart there; they come out as their text,
// which no caller of this package passes.
func valueOf(v any) jsbind.Value {
	switch x := Plain(v).(type) {
	case nil:
		return jsbind.Null
	case bool:
		return jsbind.Bool(x)
	case float64:
		return jsbind.Number(x)
	case string:
		return jsbind.String(x)
	}
	return jsbind.String(jsText(v))
}

// isFinite is Number.isFinite for a number.
func isFinite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
