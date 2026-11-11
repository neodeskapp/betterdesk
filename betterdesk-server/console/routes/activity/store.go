package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The queries take the window of a request as the query string gave it
// (jsbind.Value) and bind it as the old console's driver for the database
// did. A from or to that is a list or an object fails on SQLite unless
// its values happen to fill the statement, and fails on PostgreSQL always,
// as does text PostgreSQL cannot read as a time.

// record is a row as the old console's driver returned it: the columns
// in the order of the result, each with the value that goes into JSON.
//
// On SQLite a column goes out with whatever it holds: activity columns
// declared INTEGER can hold fractions and text. On PostgreSQL a time is
// an ISO text with milliseconds, and a count or a sum is a text of
// digits, because it is a bigint there.
type record struct {
	columns []string
	values  []any // each a json.Marshaler and an sql.Scanner
}

// MarshalJSON writes the row as an object.
func (r record) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, column := range r.columns {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(jsjson.AppendQuoted(nil, column))
		b.WriteByte(':')
		value, err := r.values[i].(json.Marshaler).MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// value returns what the column holds, nil when the row has no such
// column.
func (r record) value(column string) any {
	for i, name := range r.columns {
		if name == column {
			return r.values[i]
		}
	}
	return nil
}

// text returns a column that holds text.
func (r record) text(column string) string {
	if cell, ok := r.value(column).(*jsbind.Cell); ok {
		return cell.Value().Str()
	}
	return ""
}

// query is a statement that is put together from parts, with the values
// for its placeholders.
type query struct {
	text   strings.Builder
	values []jsbind.Value
}

// add appends a part and the values of its placeholders.
func (q *query) add(part string, values ...jsbind.Value) {
	q.text.WriteString(part)
	q.values = append(q.values, values...)
}

// between appends the two conditions of a window, each when its end is
// given ("if (from)", "if (to)").
func (q *query) between(fromColumn string, from jsbind.Value, toColumn string, to jsbind.Value) {
	if from.Truthy() {
		q.add(" AND "+fromColumn+" >= ?", from)
	}
	if to.Truthy() {
		q.add(" AND "+toColumn+" <= ?", to)
	}
}

// run reads the rows of the statement.
func (q *query) run(ctx context.Context, db *store.DB, doing string) ([]record, error) {
	text := q.text.String()
	args, err := jsbind.Args(db.Dialect(), strings.Count(text, "?"), q.values...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	rows, err := store.Select(ctx, db, text, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	out := []record{}
	for rows.Next() {
		values := make([]any, len(columns))
		for i := range values {
			values[i] = new(jsbind.Cell)
			if !db.IsPostgres() {
				continue
			}
			switch strings.ToUpper(types[i].DatabaseTypeName()) {
			case "TIMESTAMPTZ", "TIMESTAMP", "DATE":
				values[i] = new(store.Time)
			case "INT8", "NUMERIC":
				values[i] = new(store.Int64String)
			}
		}
		if err := rows.Scan(values...); err != nil {
			return nil, fmt.Errorf("%s: %w", doing, err)
		}
		out = append(out, record{columns: columns, values: values})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", doing, err)
	}
	return out, nil
}

// allSummaries returns the summaries of all devices reported in the
// window, the one received last first, each with the number of the
// device's sessions that started at or after its report time
// (detail_count).
func allSummaries(ctx context.Context, db *store.DB, from, to jsbind.Value) ([]record, error) {
	var q query
	q.add(`SELECT s.*, (
                SELECT COUNT(*) FROM activity_sessions a
                WHERE a.device_id = s.device_id
                  AND a.started_at >= s.reported_at
            ) as detail_count
            FROM activity_summaries s WHERE 1=1`)
	q.between("s.reported_at", from, "s.reported_at", to)
	q.add(" ORDER BY s.received_at DESC")
	return q.run(ctx, db, "list activity summaries")
}

// deviceSessions returns the sessions of a device that started at or
// after from and ended at or before to, the one started last first. A
// limit that is 0 or no number does not limit.
func deviceSessions(ctx context.Context, db *store.DB, id string, from, to jsbind.Value, limit float64) ([]record, error) {
	var q query
	q.add("SELECT * FROM activity_sessions WHERE device_id = ?", jsbind.String(id))
	q.between("started_at", from, "ended_at", to)
	q.add(" ORDER BY started_at DESC")
	if counts(limit) {
		q.add(" LIMIT ?", jsbind.Number(limit))
	}
	return q.run(ctx, db, "list activity sessions of device "+id)
}

// deviceSummaries returns the summaries of a device reported in the
// window, the one reported last first.
func deviceSummaries(ctx context.Context, db *store.DB, id string, from, to jsbind.Value) ([]record, error) {
	var q query
	q.add("SELECT * FROM activity_summaries WHERE device_id = ?", jsbind.String(id))
	q.between("reported_at", from, "reported_at", to)
	q.add(" ORDER BY reported_at DESC")
	return q.run(ctx, db, "list activity summaries of device "+id)
}

// topAppsOf returns the apps of a device's sessions in the window with
// the seconds spent in each and the number of sessions, the app with the
// most seconds first. SQLite makes one row per app and shows one of its
// categories; PostgreSQL makes one row per app and category.
func topAppsOf(ctx context.Context, db *store.DB, id string, from, to jsbind.Value, limit float64) ([]record, error) {
	var q query
	q.add(`SELECT app_name, category,
                SUM(duration_secs) as total_secs,
                COUNT(*) as session_count
            FROM activity_sessions WHERE device_id = ?`, jsbind.String(id))
	q.between("started_at", from, "ended_at", to)
	q.add(db.Pick(" GROUP BY app_name", " GROUP BY app_name, category") + " ORDER BY total_secs DESC")
	if counts(limit) {
		q.add(" LIMIT ?", jsbind.Number(limit))
	}
	return q.run(ctx, db, "list top apps of device "+id)
}
