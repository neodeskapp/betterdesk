package trends

import (
	"context"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestSessionsSinceReadsEveryDeviceInOneQuery: the sessions of every
// device that started at or after a time, from the signal server's table
// as the server makes it on each database, also of more than 500 devices
// and more than 500 sessions of one device; none while the table is
// missing (BUGS L103).
func TestSessionsSinceReadsEveryDeviceInOneQuery(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		read := sessionsSince(db)
		since := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
		if rows, err := read(ctx, since.UnixMilli()); err != nil || len(rows) != 0 {
			t.Fatalf("without the table: %v, %v", rows, err)
		}
		create := `CREATE TABLE peer_connection_sessions (id INTEGER PRIMARY KEY AUTOINCREMENT, host_id TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')), ended_at TEXT)`
		if db.IsPostgres() {
			create = `CREATE TABLE peer_connection_sessions (id BIGSERIAL PRIMARY KEY, host_id TEXT NOT NULL,
				started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), ended_at TIMESTAMPTZ)`
		}
		if _, err := db.Exec(ctx, create); err != nil {
			t.Fatal(err)
		}
		insert := func(host string, start time.Time, end *time.Time) {
			t.Helper()
			var ended any
			if end != nil {
				ended = end.UTC().Format(time.DateTime)
			}
			if _, err := db.Exec(ctx, `INSERT INTO peer_connection_sessions (host_id, started_at, ended_at) VALUES (?, ?, ?)`,
				host, start.UTC().Format(time.DateTime), ended); err != nil {
				t.Fatal(err)
			}
		}
		end := since.Add(26 * time.Hour)
		insert("first", since.Add(25*time.Hour), &end)
		insert("first", since.Add(-time.Second), nil)
		for i := 0; i < 600; i++ {
			insert("busy", since.Add(time.Duration(i)*time.Minute), nil)
		}
		for i := 0; i < 520; i++ {
			insert("d"+time.Duration(i).String(), since.Add(time.Hour), nil)
		}
		rows, err := read(ctx, since.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		hosts := map[string]int{}
		for _, r := range rows {
			hosts[r.HostID]++
			if r.Start < since.UnixMilli() {
				t.Errorf("a session that started before: %+v", r)
			}
		}
		if len(rows) != 1+600+520 || hosts["busy"] != 600 || hosts["first"] != 1 || len(hosts) != 522 {
			t.Errorf("%d sessions of %d devices, busy %d, first %d", len(rows), len(hosts), hosts["busy"], hosts["first"])
		}
		for _, r := range rows {
			if r.HostID == "first" && (r.Start != since.Add(25*time.Hour).UnixMilli() || r.End == nil || *r.End != end.UnixMilli()) {
				t.Errorf("the session of first: %+v", r)
			}
		}
	})
}
