package tickets

import (
	"context"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The columns the receiver app's tickets add to the tables the Node
// console made (A277):
//
//   - tickets.source: who opened the ticket, "staff" in the console or
//     "device" from the app on the computer; reporter: the name of the
//     person at the computer, as the app sent it; device_read_at: when the
//     computer last read the ticket, NULL while staff wrote something it
//     has not read (a public comment, a change of status).
//   - ticket_comments.from_kind: "staff" or "device".
//   - ticket_attachments.comment_id: the comment a file came with, NULL
//     for a file of the ticket itself.
var addedColumns = []struct{ table, name, sqlite, postgres string }{
	{"tickets", "source", "TEXT NOT NULL DEFAULT 'staff'", "TEXT NOT NULL DEFAULT 'staff'"},
	{"tickets", "reporter", "TEXT DEFAULT NULL", "TEXT DEFAULT NULL"},
	{"tickets", "device_read_at", "TEXT DEFAULT NULL", "TIMESTAMPTZ DEFAULT NULL"},
	{"ticket_comments", "from_kind", "TEXT NOT NULL DEFAULT 'staff'", "TEXT NOT NULL DEFAULT 'staff'"},
	{"ticket_attachments", "comment_id", "INTEGER DEFAULT NULL", "INTEGER DEFAULT NULL"},
}

// Ensure adds the columns that are missing. Running it again changes
// nothing. Tickets the desktop agent opened before there was a source
// ("agent:<device>" made them) count as the computer's.
func Ensure(ctx context.Context, db *store.DB) error {
	for _, c := range addedColumns {
		if err := addColumn(ctx, db, c.table, c.name, db.Pick(c.sqlite, c.postgres)); err != nil {
			return err
		}
	}
	if _, err := db.Exec(ctx, `UPDATE tickets SET source = 'device' WHERE source = 'staff' AND created_by LIKE 'agent:%'`); err != nil {
		return fmt.Errorf("tickets: mark the computers' tickets: %w", err)
	}
	if _, err := db.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_tickets_device_source ON tickets(device_id, source)`); err != nil {
		return fmt.Errorf("tickets: index: %w", err)
	}
	return nil
}

// addColumn adds a column to a table when it is missing.
func addColumn(ctx context.Context, db *store.DB, table, column, def string) error {
	if db.IsPostgres() {
		if _, err := db.Exec(ctx, `ALTER TABLE `+table+` ADD COLUMN IF NOT EXISTS `+column+` `+def); err != nil {
			return fmt.Errorf("tickets: add %s.%s: %w", table, column, err)
		}
		return nil
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM pragma_table_info('`+table+`') WHERE name = ?`, column).Scan(&n); err != nil {
		return fmt.Errorf("tickets: look for %s.%s: %w", table, column, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := db.Exec(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+def); err != nil {
		return fmt.Errorf("tickets: add %s.%s: %w", table, column, err)
	}
	return nil
}
