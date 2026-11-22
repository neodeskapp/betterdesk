package db

import "time"

// BlocklistEntry is a blocklist entry added at run time: an admin's block
// of an address, a network or a device id, and the id block of Revoke
// device. The database keeps them so they survive a restart; entries of
// the blocklist file stay in the file.
type BlocklistEntry struct {
	// Kind is "ip", "id" or "cidr".
	Kind      string
	Value     string
	Reason    string
	CreatedAt time.Time
}

// The blocklist_entries table: one row per kind and value.
const (
	sqliteBlocklistTable = `CREATE TABLE IF NOT EXISTS blocklist_entries (
			kind TEXT NOT NULL,
			value TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (kind, value)
		)`
	postgresBlocklistTable = `CREATE TABLE IF NOT EXISTS blocklist_entries (
			kind       TEXT NOT NULL,
			value      TEXT NOT NULL,
			reason     TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (kind, value)
		)`
)
