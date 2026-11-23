package activity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// record is a row as the old console's driver returned it: the columns
// in the order of the result, each with the value that goes into JSON.
//
// On SQLite a column goes out with whatever it holds: activity columns
// declared INTEGER can hold fractions and text. On PostgreSQL a time is
// an ISO text with milliseconds, and a count or a sum is a number, as on
// SQLite.
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

// between appends the conditions of the ends of a window that are set.
// The times go in as the ISO text with milliseconds that the agent sends
// and the route stores, which PostgreSQL reads as the time it is, and
// which on SQLite compares with the stored texts as the times do: from
// rounded up to the millisecond, to rounded down.
func (q *query) between(fromColumn, toColumn string, s span) {
	if s.hasFrom {
		from := s.from.Truncate(time.Millisecond)
		if from.Before(s.from) {
			from = from.Add(time.Millisecond)
		}
		q.add(" AND "+fromColumn+" >= ?", jsbind.String(store.ISOTime(from)))
	}
	if s.hasTo {
		q.add(" AND "+toColumn+" <= ?", jsbind.String(store.ISOTime(s.to.Truncate(time.Millisecond))))
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

// allSummaries returns the summaries reported in the window of the
// devices in the scope (every device for a nil scope), the one received
// last first and at most limit of them, each with the number of the
// device's sessions that started at or after its report time and before
// the device's next report (detail_count).
func allSummaries(ctx context.Context, db *store.DB, window span, scope *access.DeviceScope, limit int) ([]record, error) {
	if scope != nil && scope.Size() == 0 {
		return []record{}, nil
	}
	var q query
	q.add(`SELECT s.*, (
                SELECT COUNT(*) FROM activity_sessions a
                WHERE a.device_id = s.device_id
                  AND a.started_at >= s.reported_at
                  AND NOT EXISTS (
                      SELECT 1 FROM activity_summaries n
                      WHERE n.device_id = s.device_id
                        AND n.reported_at > s.reported_at
                        AND n.reported_at <= a.started_at
                  )
            ) as detail_count
            FROM activity_summaries s WHERE 1=1`)
	q.between("s.reported_at", "s.reported_at", window)
	if scope != nil {
		ids := scope.IDs()
		values := make([]jsbind.Value, len(ids))
		for i, id := range ids {
			values[i] = jsbind.String(id)
		}
		q.add(" AND s.device_id IN (?"+strings.Repeat(", ?", len(ids)-1)+")", values...)
	}
	q.add(" ORDER BY s.received_at DESC LIMIT ?", jsbind.Number(float64(limit)))
	return q.run(ctx, db, "list activity summaries")
}

// deviceSessions returns the sessions of a device that started at or
// after from and ended at or before to, the one started last first, at
// most limit of them.
func deviceSessions(ctx context.Context, db *store.DB, id string, window span, limit int) ([]record, error) {
	var q query
	q.add("SELECT * FROM activity_sessions WHERE device_id = ?", jsbind.String(id))
	q.between("started_at", "ended_at", window)
	q.add(" ORDER BY started_at DESC LIMIT ?", jsbind.Number(float64(limit)))
	return q.run(ctx, db, "list activity sessions of device "+id)
}

// deviceSummaries returns the summaries of a device reported in the
// window, the one reported last first.
func deviceSummaries(ctx context.Context, db *store.DB, id string, window span) ([]record, error) {
	var q query
	q.add("SELECT * FROM activity_summaries WHERE device_id = ?", jsbind.String(id))
	q.between("reported_at", "reported_at", window)
	q.add(" ORDER BY reported_at DESC")
	return q.run(ctx, db, "list activity summaries of device "+id)
}

// topAppsOf returns the apps of a device's sessions in the window with
// the seconds spent in each and the number of sessions, the app with the
// most seconds first, at most limit of them. Each app comes once, under
// the category it has the most seconds in (on a tie the one with more
// sessions, then the first by name).
func topAppsOf(ctx context.Context, db *store.DB, id string, window span, limit int) ([]record, error) {
	var q query
	q.add(`SELECT s.app_name, (
                SELECT c.category FROM activity_sessions c
                WHERE c.device_id = ? AND c.app_name = s.app_name`, jsbind.String(id))
	q.between("c.started_at", "c.ended_at", window)
	q.add(`
                GROUP BY c.category
                ORDER BY COALESCE(SUM(c.duration_secs), 0) DESC, COUNT(*) DESC, c.category
                LIMIT 1
            ) as category,
                SUM(s.duration_secs) as total_secs,
                COUNT(*) as session_count
            FROM activity_sessions s WHERE s.device_id = ?`, jsbind.String(id))
	q.between("s.started_at", "s.ended_at", window)
	q.add(" GROUP BY s.app_name ORDER BY total_secs DESC LIMIT ?", jsbind.Number(float64(limit)))
	return q.run(ctx, db, "list top apps of device "+id)
}

// insertActivitySessions is insertActivitySessions(deviceId, sessions):
// one row per session, all of them or none. Each value goes in as the
// driver bound it, with the defaults of the old adapter: the empty text
// for an app name and a window title, "other" for a category and 0 for a
// duration, where the session's value is not true.
//
// SQLite keeps what it is given (a number in a text column as "5.0") and
// the driver refuses a flag or an object; PostgreSQL refuses a time it
// cannot read and a fraction in the integer column, and stores a list or
// an object in a text column as its array or JSON text.
func insertActivitySessions(ctx context.Context, db *store.DB, device string, sessions []middleware.JSONObject) error {
	empty := jsbind.String("")
	return db.InTx(ctx, func(tx *store.Tx) error {
		for i, s := range sessions {
			args, err := jsbind.Args(tx.Dialect(), 7,
				jsbind.String(device),
				jsbind.Of(s.Get("app_name")).Or(empty),
				jsbind.Of(s.Get("window_title")).Or(empty),
				jsbind.Of(s.Get("category")).Or(jsbind.String("other")),
				jsbind.Of(s.Get("started_at")),
				jsbind.Of(s.Get("ended_at")),
				jsbind.Of(s.Get("duration_secs")).Or(jsbind.Number(0)),
			)
			if err == nil {
				_, err = tx.Exec(ctx, `
                INSERT INTO activity_sessions (device_id, app_name, window_title, category, started_at, ended_at, duration_secs)
                VALUES (?, ?, ?, ?, ?, ?, ?)`, args...)
			}
			if err != nil {
				return fmt.Errorf("store session %d of device %s: %w", i, device, err)
			}
		}
		return nil
	})
}

// summary is what upsertActivitySummary stores: the values of the report
// with the route's defaults filled in.
type summary struct {
	IdleSeconds, SessionCount, TotalActiveSecs, ReportedAt jsbind.Value
}

// upsertActivitySummary is upsertActivitySummary(deviceId, data): the
// summary of a report, which replaces the one of the same device and time
// of report, with the time of arrival.
func upsertActivitySummary(ctx context.Context, db *store.DB, device string, s summary) error {
	query := db.Pick(`
                INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at, received_at)
                VALUES (?, ?, ?, ?, ?, datetime('now'))
                ON CONFLICT(device_id, reported_at) DO UPDATE SET
                    idle_seconds = excluded.idle_seconds,
                    session_count = excluded.session_count,
                    total_active_secs = excluded.total_active_secs,
                    received_at = datetime('now')`, `
                INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at, received_at)
                VALUES ($1, $2, $3, $4, $5, NOW())
                ON CONFLICT(device_id, reported_at) DO UPDATE SET
                    idle_seconds = $2, session_count = $3, total_active_secs = $4, received_at = NOW()`)
	args, err := jsbind.Args(db.Dialect(), 5, jsbind.String(device), s.IdleSeconds, s.SessionCount, s.TotalActiveSecs, s.ReportedAt)
	if err == nil {
		_, err = db.Exec(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("store the activity summary of device %s: %w", device, err)
	}
	return nil
}
