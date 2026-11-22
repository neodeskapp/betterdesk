package db

import (
	"encoding/json"
	"time"
)

// ServerEvent is an event the server records itself (package audit): a
// sign-in, a failed one, a ban, a change of the configuration. Actor is
// the account that acted, or the client's address where no account did.
type ServerEvent struct {
	Time    time.Time
	Action  string
	Actor   string
	Target  string
	Details map[string]string
}

// The server_events table. id orders the events as they were recorded.
const (
	sqliteServerEventsTable = `CREATE TABLE IF NOT EXISTS server_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TEXT NOT NULL,
			action TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			target TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT ''
		)`
	postgresServerEventsTable = `CREATE TABLE IF NOT EXISTS server_events (
			id         BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL,
			action     TEXT NOT NULL,
			actor      TEXT NOT NULL DEFAULT '',
			target     TEXT NOT NULL DEFAULT '',
			details    TEXT NOT NULL DEFAULT ''
		)`
	serverEventsCreatedIndex = `CREATE INDEX IF NOT EXISTS idx_server_events_created ON server_events(created_at)`
	serverEventsActionIndex  = `CREATE INDEX IF NOT EXISTS idx_server_events_action ON server_events(action, id)`
)

// sqliteEventTime is how server_events.created_at holds a time on SQLite:
// UTC, so it compares as text with datetime('now', ...).
const sqliteEventTime = "2006-01-02 15:04:05.000000"

// eventDetails is the details column: JSON, "" for none.
func eventDetails(details map[string]string) string {
	if len(details) == 0 {
		return ""
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(raw)
}

// parseEventDetails reads the details column back; text that is no JSON
// object of strings gives none.
func parseEventDetails(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var details map[string]string
	if err := json.Unmarshal([]byte(raw), &details); err != nil {
		return nil
	}
	return details
}
