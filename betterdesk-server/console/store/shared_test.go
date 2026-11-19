package store_test

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	serverdb "github.com/unitronix/betterdesk-server/db"
)

// The console and the signal server share one database and each creates
// the tables both use when it starts first. In both start orders the
// result is the database the Node console and the signal server left in
// that order, and both sides can still work with the shared tables.
func TestSharedDatabaseStartOrders(t *testing.T) {
	orders := []struct {
		name          string
		goServerFirst bool
		dump          string
	}{
		{"go-server-first", true, "go-first"},
		{"console-first", false, "node-first"},
	}
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			storetest.Each(t, func(t *testing.T, db *store.DB) {
				if order.goServerFirst {
					storetest.GoServerMigrate(t, db)
					mustMigrate(t, db)
				} else {
					mustMigrate(t, db)
					storetest.GoServerMigrate(t, db)
				}
				got := readCatalog(t, db)

				reference := storetest.SQLite(t)
				if db.IsPostgres() {
					reference = storetest.Postgres(t)
				}
				storetest.ApplyDump(t, reference, readTestdata(t, "dumps/shared-"+db.Dialect().String()+"-"+order.dump+".sql"))
				// On PostgreSQL alert_rules.condition_value is double
				// precision (BUGS AE4)
				want := readCatalog(t, reference).without(sessionTable).wideConditionValue()
				if n := len(want.tables()); n != 87 {
					t.Fatalf("the shared dump has %d tables besides %s, want 87", n, sessionTable)
				}
				requireSameCatalog(t, "shared database against the dump", want, got)

				// Both migrations again, in both orders: nothing moves.
				mustMigrate(t, db)
				storetest.GoServerMigrate(t, db)
				mustMigrate(t, db)
				requireSameCatalog(t, "shared database after another round of migrations", got, readCatalog(t, db))

				sharedTableOperations(t, db, order.goServerFirst)
			})
		})
	}
}

// sharedTableOperations writes and reads a user, a device group, a
// strategy, an address book and an audit row from both sides: through the
// signal server's db package and with the SQL the console uses.
func sharedTableOperations(t *testing.T, sdb *store.DB, goServerFirst bool) {
	t.Helper()
	ctx := testContext(t)
	gdb := storetest.GoServer(t, sdb)
	shape, err := sdb.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Users
	if err := gdb.CreateUser(&serverdb.User{Username: "from-go", PasswordHash: "hash", Role: "operator"}); err != nil {
		t.Fatalf("signal server: create user: %v", err)
	}
	consoleUserID, err := sdb.Insert(ctx, `INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, ?, ?, ?)`,
		"from-console", "hash", "admin", "local")
	if err != nil {
		t.Fatalf("console: create user: %v", err)
	}
	for _, name := range []string{"from-go", "from-console"} {
		u, err := gdb.GetUser(name)
		if err != nil || u == nil || u.Username != name {
			t.Errorf("signal server: read user %s = %+v, %v", name, u, err)
		}
		var role string
		if err := sdb.QueryRow(ctx, `SELECT role FROM users WHERE username = ?`, name).Scan(&role); err != nil || role == "" {
			t.Errorf("console: read user %s = %q, %v", name, role, err)
		}
	}

	// Device groups
	if err := gdb.CreateDeviceGroup(&serverdb.DeviceGroup{GUID: "dg-go", Name: "from go"}); err != nil {
		t.Fatalf("signal server: create device group: %v", err)
	}
	mustExec(t, sdb, `INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES (?, ?, ?, ?, ?, ?)`,
		"dg-console", "from console", "", "", "manual", "")
	groups, err := gdb.ListDeviceGroups()
	if err != nil {
		t.Fatalf("signal server: list device groups: %v", err)
	}
	seen := map[string]bool{}
	for _, g := range groups {
		seen[g.GUID] = true
	}
	if !seen["dg-go"] || !seen["dg-console"] {
		t.Errorf("signal server sees device groups %v, want dg-go and dg-console", seen)
	}
	if rows := queryStrings(t, sdb, `SELECT guid FROM device_groups WHERE guid IN ('dg-go', 'dg-console') ORDER BY guid`); len(rows) != 2 {
		t.Errorf("console sees device groups %v, want dg-console and dg-go", rows)
	}

	// Strategies
	if err := gdb.CreateStrategy(&serverdb.Strategy{GUID: "st-go", Name: "from go", Enabled: true, Permissions: `{"a":1}`}); err != nil {
		t.Fatalf("signal server: create strategy: %v", err)
	}
	mustExec(t, sdb, `INSERT INTO strategies (guid, name, user_group_guid, device_group_guid, enabled, permissions) VALUES (?, ?, ?, ?, ?, ?)`,
		"st-console", "from console", "", "", store.BoolArg(shape.Table("strategies").Type("enabled"), true), `{"b":2}`)
	strategies, err := gdb.ListStrategies()
	if err != nil {
		t.Fatalf("signal server: list strategies: %v", err)
	}
	seen = map[string]bool{}
	for _, s := range strategies {
		seen[s.GUID] = s.Enabled
	}
	if !seen["st-go"] || !seen["st-console"] {
		t.Errorf("signal server sees enabled strategies %v, want st-go and st-console", seen)
	}
	if rows := queryStrings(t, sdb, `SELECT guid FROM strategies ORDER BY guid`); len(rows) != 2 {
		t.Errorf("console sees strategies %v, want two", rows)
	}

	// Address books. The console keys them by user id on a SQLite table it
	// created itself and by user name everywhere else.
	byName := shape.Table("address_books").Has("username")
	if want := sdb.IsPostgres() || goServerFirst; byName != want {
		t.Errorf("address_books keyed by user name = %v, want %v", byName, want)
	}
	if byName {
		mustExec(t, sdb, `INSERT INTO address_books (username, ab_type, data, updated_at) VALUES (?, ?, ?, `+store.NowSQL(sdb.Dialect())+`)`,
			"from-console", "legacy", `{"peers":[]}`)
	} else {
		mustExec(t, sdb, `INSERT INTO address_books (user_id, ab_type, data, updated_at) VALUES (?, ?, ?, datetime('now'))`,
			consoleUserID, "legacy", `{"peers":[]}`)
	}
	if rows := queryStrings(t, sdb, `SELECT data FROM address_books`); len(rows) != 1 || rows[0][0] != `{"peers":[]}` {
		t.Errorf("console reads address books %v", rows)
	}
	saveErr := gdb.SaveAddressBook("from-go", "legacy", `{"peers":["1"]}`)
	if byName {
		if saveErr != nil {
			t.Errorf("signal server: save address book: %v", saveErr)
		}
		if data, err := gdb.GetAddressBook("from-go", "legacy"); err != nil || data != `{"peers":["1"]}` {
			t.Errorf("signal server: read its address book = %q, %v", data, err)
		}
		if data, err := gdb.GetAddressBook("from-console", "legacy"); err != nil || data != `{"peers":[]}` {
			t.Errorf("signal server: read the console's address book = %q, %v", data, err)
		}
	} else if saveErr == nil || !strings.Contains(saveErr.Error(), "username") {
		// BUGS.md D5: the signal server cannot use the table in the
		// console's SQLite shape. When that is fixed, this branch goes.
		t.Errorf("signal server on the console's SQLite address_books: error = %v, want the known failure on the username column", saveErr)
	}

	// Client audit rows
	if err := gdb.InsertAuditConnection(&serverdb.AuditConnection{HostID: "100", PeerID: "200", Action: "new", IP: "10.0.0.1"}); err != nil {
		t.Fatalf("signal server: insert audit row: %v", err)
	}
	mustExec(t, sdb, `INSERT INTO audit_connections (host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"300", "", "400", "", "close", 0, "", "10.0.0.2")
	audits, err := gdb.ListAuditConnections(serverdb.AuditFilter{Limit: 10})
	if err != nil || len(audits) != 2 {
		t.Errorf("signal server lists %d audit rows, %v, want 2", len(audits), err)
	}
	if rows := queryStrings(t, sdb, `SELECT host_id FROM audit_connections ORDER BY host_id`); len(rows) != 2 || rows[0][0] != "100" || rows[1][0] != "300" {
		t.Errorf("console reads audit rows %v", rows)
	}
}
