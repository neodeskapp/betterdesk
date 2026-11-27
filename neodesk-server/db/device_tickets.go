package db

// The console keeps the helpdesk tickets in the same database (its tables
// tickets, ticket_comments and ticket_attachments). A ticket the app on a
// computer opened has the source "device"; its device_read_at is NULL
// while staff wrote something the computer has not read. The heartbeat
// answer carries the count, so the app shows it also while its chat socket
// is down. A database without the console's tables, or without those
// columns, answers an error, and the heartbeat leaves the count out.

const unreadDeviceTicketsQuery = `SELECT COUNT(*) FROM tickets WHERE device_id = $1 AND source = 'device' AND device_read_at IS NULL`

// UnreadDeviceTickets counts the device's tickets with something unread.
func (s *SQLiteDB) UnreadDeviceTickets(deviceID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tickets WHERE device_id = ? AND source = 'device' AND device_read_at IS NULL`, deviceID).Scan(&n)
	return n, err
}

// UnreadDeviceTickets counts the device's tickets with something unread.
func (pg *PostgresDB) UnreadDeviceTickets(deviceID string) (int, error) {
	var n int
	err := pg.pool.QueryRow(pg.ctx, unreadDeviceTicketsQuery, deviceID).Scan(&n)
	return n, err
}
