package trends

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// storedSession is a remote session as the signal server stored it: on
// which device (host) it ran, and when it started and ended, in
// milliseconds since 1970. End is nil while it runs, and when it cannot
// be read as a time.
type storedSession struct {
	ID     int64
	HostID string
	Start  int64
	End    *int64
}

// sessionSource reads the sessions that started at or after since.
type sessionSource func(ctx context.Context, since int64) ([]storedSession, error)

// sessionsSince reads the remote sessions of every device that started at
// or after since from the signal server's table in one query. The table
// is missing until the signal server has run; then there are none.
func sessionsSince(db *store.DB) sessionSource {
	return func(ctx context.Context, since int64) ([]storedSession, error) {
		from := time.UnixMilli(since).UTC()
		query, bound := `SELECT id, host_id, started_at, ended_at FROM peer_connection_sessions WHERE started_at >= ?`, any(from.Format(time.DateTime))
		if db.IsPostgres() {
			query, bound = `SELECT id, host_id, started_at, ended_at FROM peer_connection_sessions WHERE started_at >= CAST(? AS TIMESTAMPTZ)`, from.Format(time.RFC3339Nano)
		}
		rows, err := db.Query(ctx, query, bound)
		if err != nil {
			if message := jsbind.Message(err); strings.Contains(message, "no such table") || strings.Contains(message, "does not exist") {
				return nil, nil
			}
			return nil, fmt.Errorf("read the remote sessions: %w", err)
		}
		defer rows.Close()
		var out []storedSession
		for rows.Next() {
			var s storedSession
			var started, ended any
			var host sql.NullString
			if err := rows.Scan(&s.ID, &host, &started, &ended); err != nil {
				return nil, fmt.Errorf("read a remote session: %w", err)
			}
			start, ok := millis(started)
			if !ok {
				continue
			}
			s.HostID, s.Start = host.String, start
			if end, ok := millis(ended); ok {
				s.End = &end
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read the remote sessions: %w", err)
		}
		return out, nil
	}
}

// millis reads a time column: a time from PostgreSQL, the text SQLite
// stored (UTC without a zone).
func millis(v any) (int64, bool) {
	switch t := v.(type) {
	case time.Time:
		return t.UnixMilli(), true
	case []byte:
		return toTime(string(t))
	case string:
		return toTime(t)
	}
	return 0, false
}
