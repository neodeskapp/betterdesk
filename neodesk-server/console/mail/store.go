package mail

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// getSetting reads a row of the settings table (dbAdapter getSetting). ok
// is false when the row is missing, and when its value is not text (a
// blob in a SQLite file), which the console could not use either.
func getSetting(ctx context.Context, q store.Querier, key string) (value string, ok bool, err error) {
	var stored any
	err = q.QueryRow(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	text, isText := stored.(string)
	return text, isText, nil
}

// setSetting writes a row of the settings table and stamps it with the
// database's clock (dbAdapter setSetting).
func setSetting(ctx context.Context, q store.Querier, key, value string) error {
	now := store.NowSQL(q.Dialect())
	_, err := q.Exec(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, "+now+") "+
		"ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = "+now, key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// HasEmailNotificationSent reports whether the notification log has an
// entry for the event and the recipient (dbAdapter
// hasEmailNotificationSent). The three texts are compared as they are:
// case and white space count.
func HasEmailNotificationSent(ctx context.Context, q store.Querier, eventType, eventID, recipient string) (bool, error) {
	var one int
	err := q.QueryRow(ctx, `SELECT 1 FROM email_notification_log
		WHERE event_type = ? AND event_id = ? AND recipient = ?`, eventType, eventID, recipient).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read the notification log: %w", err)
	}
	return true, nil
}

// LogEmailNotificationSent notes in the notification log that the
// recipient was told about the event, with the database's clock (dbAdapter
// logEmailNotificationSent). A second note for the same three texts
// changes nothing.
func LogEmailNotificationSent(ctx context.Context, q store.Querier, eventType, eventID, recipient string) error {
	query := q.Pick(
		`INSERT OR IGNORE INTO email_notification_log (event_type, event_id, recipient, sent_at)
		 VALUES (?, ?, ?, datetime('now'))`,
		`INSERT INTO email_notification_log (event_type, event_id, recipient, sent_at)
		 VALUES (?, ?, ?, NOW())
		 ON CONFLICT (event_type, event_id, recipient) DO NOTHING`)
	if _, err := q.Exec(ctx, query, eventType, eventID, recipient); err != nil {
		return fmt.Errorf("write the notification log: %w", err)
	}
	return nil
}

// HasEmailNotificationSent is the package function on the service's
// database.
func (s *Service) HasEmailNotificationSent(ctx context.Context, eventType, eventID, recipient string) (bool, error) {
	return HasEmailNotificationSent(ctx, s.opts.DB, eventType, eventID, recipient)
}

// LogEmailNotificationSent is the package function on the service's
// database.
func (s *Service) LogEmailNotificationSent(ctx context.Context, eventType, eventID, recipient string) error {
	return LogEmailNotificationSent(ctx, s.opts.DB, eventType, eventID, recipient)
}
