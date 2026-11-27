package tickets

import (
	"context"
	"fmt"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// public is the condition of a public comment c, for each database.
func public(db *store.DB) string {
	return db.Pick("COALESCE(c.is_internal, 0) = 0", "COALESCE(c.is_internal, FALSE) = FALSE")
}

// deviceTickets lists the tickets the device opened, newest first and
// maxListed at most, each with the time of its newest public comment and
// the number of them; with id set only that ticket (undefined for all).
func deviceTickets(ctx context.Context, db *store.DB, device string, id jsbind.Value) ([]deviceTicket, error) {
	query := "SELECT " + ticketColumns +
		", (SELECT MAX(c.created_at) FROM ticket_comments c WHERE c.ticket_id = t.id AND " + public(db) + ")" +
		", (SELECT COUNT(*) FROM ticket_comments c WHERE c.ticket_id = t.id AND " + public(db) + ")" +
		" FROM tickets t WHERE source = ? AND device_id = ?"
	values := []jsbind.Value{jsbind.String(sourceDevice), jsbind.String(device)}
	if !id.IsUndefined() {
		query += " AND id = ?"
		values = append(values, id)
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT " + strconv.Itoa(maxListed)
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		return nil, fmt.Errorf("list the tickets of device %s: %w", device, err)
	}
	rows, err := store.Select(ctx, db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list the tickets of device %s: %w", device, err)
	}
	defer rows.Close()
	out := []deviceTicket{}
	for rows.Next() {
		var t deviceTicket
		if err := rows.Scan(append(t.dest(), &t.LastReplyAt, &t.Replies)...); err != nil {
			return nil, fmt.Errorf("read a ticket of device %s: %w", device, err)
		}
		t.Unread = t.DeviceRead.IsNull()
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list the tickets of device %s: %w", device, err)
	}
	return out, nil
}

// markDeviceRead notes that the computer has read the ticket (read), or
// that staff wrote something it has not read yet.
func markDeviceRead(ctx context.Context, db *store.DB, id jsbind.Value, read bool) error {
	set := "NULL"
	if read {
		set = store.NowSQL(db.Dialect())
	}
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err == nil {
		_, err = db.Exec(ctx, "UPDATE tickets SET device_read_at = "+set+" WHERE id = ?", args...)
	}
	if err != nil {
		return fmt.Errorf("mark ticket %s read by its device: %w", id.ToString(), err)
	}
	return nil
}
