package people

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The time zone of an account's person: an IANA name such as
// Europe/Berlin, kept in the console's own table account_profile (the
// signal server never reads it). An account without a row, or with an
// empty name, has none: the mails to the person give times in UTC then.
// The person sets it on Your account; an admin who may change the account
// sets it on the user's page.

var profileTable = []struct{ sqlite, postgres string }{
	{`CREATE TABLE IF NOT EXISTS account_profile (
			user_id INTEGER PRIMARY KEY,
			time_zone TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS account_profile (
			user_id BIGINT PRIMARY KEY,
			time_zone TEXT NOT NULL DEFAULT ''
		)`},
}

// Ensure creates the table when it is missing. Running it again changes
// nothing.
func Ensure(ctx context.Context, db *store.DB) error {
	for _, t := range profileTable {
		if _, err := db.Exec(ctx, db.Pick(t.sqlite, t.postgres)); err != nil {
			return fmt.Errorf("account profile: %w", err)
		}
	}
	return nil
}

// ErrTimeZone is the refusal of a name that is no time zone.
var ErrTimeZone = errors.New("unknown time zone")

// CheckTimeZone returns the name trimmed when it names a time zone the
// server knows (an IANA name with a slash, such as Europe/Berlin, or UTC),
// "" for "" (none), and ErrTimeZone for anything else. "Local" and names
// with spaces or .. are refused.
func CheckTimeZone(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	if len(name) > 64 || name == "Local" || strings.Contains(name, "..") || strings.ContainsAny(name, " \\") {
		return "", ErrTimeZone
	}
	if _, err := time.LoadLocation(name); err != nil {
		return "", ErrTimeZone
	}
	return name, nil
}

// TimeZone returns the account's time zone, "" for none. A database
// without the table has none.
func TimeZone(ctx context.Context, q store.Querier, userID int64) string {
	if q == nil {
		return ""
	}
	var zone string
	if err := q.QueryRow(ctx, `SELECT time_zone FROM account_profile WHERE user_id = ?`, userID).Scan(&zone); err != nil {
		return ""
	}
	return zone
}

// TimeZones returns the time zone of every account that has one.
func TimeZones(ctx context.Context, q store.Querier) (map[int64]string, error) {
	rows, err := q.Query(ctx, `SELECT user_id, time_zone FROM account_profile WHERE time_zone <> ''`)
	if err != nil {
		return nil, fmt.Errorf("account profile: list: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var zone string
		if err := rows.Scan(&id, &zone); err != nil {
			return nil, err
		}
		out[id] = zone
	}
	return out, rows.Err()
}

// SetTimeZone gives the account the time zone, "" for none. The name must
// have passed CheckTimeZone.
func SetTimeZone(ctx context.Context, q store.Querier, userID int64, zone string) error {
	_, err := q.Exec(ctx, `INSERT INTO account_profile (user_id, time_zone) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET time_zone = excluded.time_zone`, userID, zone)
	if err != nil {
		return fmt.Errorf("account profile: write %d: %w", userID, err)
	}
	return nil
}

// Forget deletes the row of an account that is deleted.
func Forget(ctx context.Context, q store.Querier, userID int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM account_profile WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("account profile: forget %d: %w", userID, err)
	}
	return nil
}

// TimeIn formats t for a mail to a person in the zone: "2026-11-26 15:04"
// and the zone's name, or UTC for none or a zone the server no longer
// knows.
func TimeIn(t time.Time, zone string) string {
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return t.In(loc).Format("2006-01-02 15:04") + " " + zone
		}
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}
