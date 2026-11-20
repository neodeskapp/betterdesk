//go:build nodeschema

package store_test

// These tests run the Node console's own start code (web-nodejs) next to
// Migrate and compare the results. They need node and the console's
// node_modules, so they sit behind the build tag "nodeschema" and go away
// with the Node console. testdata/nodeschema/README.md says how to run them.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// nodeDatabase is an empty database the Node console can open: a directory
// for SQLite, a database of its own on PostgreSQL. The console looks for
// existing columns in every schema of a database, so a schema would not
// keep it apart from other tests.
type nodeDatabase struct {
	db  *store.DB
	env []string
}

func newNodeDatabase(t *testing.T, postgres bool) nodeDatabase {
	t.Helper()
	if !postgres {
		dir := t.TempDir()
		db, err := store.OpenSQLite(filepath.Join(dir, "db_v2.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return nodeDatabase{db: db, env: []string{"DB_TYPE=sqlite", "DATABASE_URL=", "NODESCHEMA_SQLITE_DIR=" + dir}}
	}

	dsn := os.Getenv(storetest.PostgresEnv)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "ns_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("create database: %v", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenPostgres(pool)
	t.Cleanup(func() {
		_ = db.Close()
		pool.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), time.Minute)
		defer dropCancel()
		_, _ = admin.Exec(dropCtx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close()
	})
	return nodeDatabase{db: db, env: []string{"DB_TYPE=postgres", "DATABASE_URL=" + u.String()}}
}

// eachNodeDatabase runs fn for SQLite and, when a server is configured, for
// PostgreSQL. fresh gives fn another empty database of the same kind.
func eachNodeDatabase(t *testing.T, fn func(t *testing.T, fresh func() nodeDatabase)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) {
		fn(t, func() nodeDatabase { return newNodeDatabase(t, false) })
	})
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv(storetest.PostgresEnv) == "" {
			t.Skipf("%s is not set", storetest.PostgresEnv)
		}
		fn(t, func() nodeDatabase { return newNodeDatabase(t, true) })
	})
}

// node runs testdata/nodeschema/init.js against the database.
func (n nodeDatabase) node(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", append([]string{filepath.Join("testdata", "nodeschema", "init.js")}, args...)...)
	cmd.Env = append(os.Environ(), n.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("node init.js %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
}

type nodeSnapshot struct {
	Reads  map[string]json.RawMessage `json:"reads"`
	Tables map[string]json.RawMessage `json:"tables"`
}

// snapshot starts the Node console's database layer (which runs its
// migration) and returns what it reads: the answers of its list methods and
// every row of every table as its driver returns it.
func (n nodeDatabase) snapshot(t *testing.T) nodeSnapshot {
	t.Helper()
	file := filepath.Join(t.TempDir(), "snapshot.json")
	n.node(t, "snapshot", file)
	text, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s nodeSnapshot
	if err := json.Unmarshal(text, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func requireSameNodeSnapshot(t *testing.T, before, after nodeSnapshot) {
	t.Helper()
	compare := func(kind string, a, b map[string]json.RawMessage) {
		var names []string
		for name := range a {
			names = append(names, name)
		}
		for name := range b {
			if _, ok := a[name]; !ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if !bytes.Equal(a[name], b[name]) {
				t.Errorf("%s %s differs after Migrate\n  before: %.600s\n  after:  %.600s", kind, name, a[name], b[name])
			}
		}
	}
	compare("read", before.Reads, after.Reads)
	compare("table", before.Tables, after.Tables)
}

// A database Migrate sets up has the catalog of one the Node console's
// init() sets up: tables, columns with type, nullability and default,
// keys, constraints, indexes, sequences, and on SQLite the statement text.
func TestNodeSchemaFreshEqualsNode(t *testing.T) {
	eachNodeDatabase(t, func(t *testing.T, fresh func() nodeDatabase) {
		byNode := fresh()
		byNode.node(t, "init")
		want := readCatalog(t, byNode.db)
		if n := len(want.tables()); n != 55 {
			t.Fatalf("Node's init created %d tables, want 55", n)
		}

		byGo := fresh()
		mustMigrate(t, byGo.db)
		requireSameCatalog(t, "Migrate against Node's init", want, readCatalog(t, byGo.db))
		for _, table := range []string{"user_groups", "device_groups"} {
			a := queryStrings(t, byNode.db, `SELECT name, note, team_id FROM `+table)
			b := queryStrings(t, byGo.db, `SELECT name, note, team_id FROM `+table)
			if len(a) != 1 || len(b) != 1 || strings.Join(a[0], "|") != strings.Join(b[0], "|") {
				t.Errorf("%s: Node seeded %v, Migrate seeded %v", table, a, b)
			}
		}
	})
}

// The same with the signal server in the picture, in both start orders,
// against the real thing instead of the stored dumps.
func TestNodeSchemaSharedStartOrders(t *testing.T) {
	for _, goServerFirst := range []bool{true, false} {
		name := "console-first"
		if goServerFirst {
			name = "go-server-first"
		}
		t.Run(name, func(t *testing.T) {
			eachNodeDatabase(t, func(t *testing.T, fresh func() nodeDatabase) {
				withNode, withGo := fresh(), fresh()
				if goServerFirst {
					storetest.GoServerMigrate(t, withNode.db)
					withNode.node(t, "init")
					storetest.GoServerMigrate(t, withGo.db)
					mustMigrate(t, withGo.db)
				} else {
					withNode.node(t, "init")
					storetest.GoServerMigrate(t, withNode.db)
					mustMigrate(t, withGo.db)
					storetest.GoServerMigrate(t, withGo.db)
				}
				want := readCatalog(t, withNode.db)
				if n := len(want.tables()); n != 86 {
					t.Fatalf("%d tables with Node and the signal server, want 86", n)
				}
				requireSameCatalog(t, "Migrate with the signal server against Node with the signal server", want, readCatalog(t, withGo.db))
			})
		})
	}
}

// Rollback: a database the Node console made and filled is still the
// Node console's after Migrate ran on it. Its init() runs, its reads give
// the same rows, and the catalog is what it was.
func TestNodeSchemaRollbackAfterMigrate(t *testing.T) {
	eachNodeDatabase(t, func(t *testing.T, fresh func() nodeDatabase) {
		n := fresh()
		n.node(t, "init")
		tables := readCatalog(t, n.db).tables()
		fillTables(t, n.db, tables, 2)
		before := n.snapshot(t)
		catalogBefore := readCatalog(t, n.db)

		for name, read := range before.Reads {
			if bytes.Contains(read, []byte(`"error"`)) && bytes.HasPrefix(read, []byte(`{`)) && len(read) < 400 {
				t.Errorf("Node read %s failed on the fixture rows: %s", name, read)
			}
			if string(read) == "[]" || string(read) == "{}" {
				t.Errorf("Node read %s returned nothing; the fixture should give it rows", name)
			}
		}
		if len(before.Reads) < 25 || len(before.Tables) != 55 {
			t.Fatalf("snapshot has %d reads and %d tables", len(before.Reads), len(before.Tables))
		}

		mustMigrate(t, n.db)
		mustMigrate(t, n.db)

		requireSameCatalog(t, "catalog of Node's database after Migrate", catalogBefore, readCatalog(t, n.db))
		requireSameNodeSnapshot(t, before, n.snapshot(t))
		requireSameCatalog(t, "catalog after Node started again", catalogBefore, readCatalog(t, n.db))
	})
}

// Rollback from the other side: on a database only Migrate ever touched,
// the Node console starts, changes nothing, and reads the rows Go wrote.
func TestNodeStartsOnMigratedDatabase(t *testing.T) {
	eachNodeDatabase(t, func(t *testing.T, fresh func() nodeDatabase) {
		n := fresh()
		mustMigrate(t, n.db)
		tables := readCatalog(t, n.db).tables()
		fillTables(t, n.db, tables, 2)
		catalogBefore := readCatalog(t, n.db)
		rowsBefore := readSnapshot(t, n.db, tables)

		first := n.snapshot(t)
		for name, read := range first.Reads {
			if bytes.Contains(read, []byte(`"error"`)) && bytes.HasPrefix(read, []byte(`{`)) && len(read) < 400 {
				t.Errorf("Node read %s failed on a database Migrate made: %s", name, read)
			}
		}
		requireSameCatalog(t, "catalog after the Node console started on Migrate's database", catalogBefore, readCatalog(t, n.db))
		requireSameSnapshot(t, "rows after the Node console started", rowsBefore, readSnapshot(t, n.db, tables))
		requireSameNodeSnapshot(t, first, n.snapshot(t))
	})
}

// An old install: the schema of an older release, upgraded once by Migrate
// and once by the current Node console, ends up the same.
func TestNodeSchemaOldInstallsUpgradeAlike(t *testing.T) {
	for _, version := range oldVersions {
		t.Run(version, func(t *testing.T) {
			eachNodeDatabase(t, func(t *testing.T, fresh func() nodeDatabase) {
				byNode, byGo := fresh(), fresh()
				dump := readTestdata(t, "old/"+version+"-"+byNode.db.Dialect().String()+".sql")
				for _, d := range []nodeDatabase{byNode, byGo} {
					storetest.ApplyDump(t, d.db, dump)
					fillTables(t, d.db, readCatalog(t, d.db).tables(), 3)
				}
				byNode.node(t, "init")
				mustMigrate(t, byGo.db)
				requireSameCatalog(t, version+" upgraded by Migrate against upgraded by Node", readCatalog(t, byNode.db), readCatalog(t, byGo.db))

				tables := readCatalog(t, byNode.db).tables()
				a, b := readSnapshot(t, byNode.db, tables), readSnapshot(t, byGo.db, tables)
				// Rows the two wrote on their own differ in random ids and times.
				for _, table := range []string{"user_groups", "device_groups"} {
					delete(a, table)
					delete(b, table)
				}
				requireSameSnapshot(t, "rows of the "+version+" install after Node's upgrade and after Migrate", a, b)
			})
		})
	}
}

// A SQLite settings table from before updated_at, with rows in it: SQLite
// cannot add a column with an expression as default to a table that has
// rows. The Node console logged the error and went on without the column.
// Migrate adds it without the default (BUGS.md D17, the one intended
// difference); the tables are otherwise the same.
func TestNodeSchemaUpdatedAtOnOldSQLiteTables(t *testing.T) {
	byNode, byGo := newNodeDatabase(t, false), newNodeDatabase(t, false)
	for _, d := range []nodeDatabase{byNode, byGo} {
		mustExec(t, d.db, `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		mustExec(t, d.db, `CREATE TABLE branding_config (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		mustExec(t, d.db, `INSERT INTO settings (key, value) VALUES ('k', 'v')`)
		mustExec(t, d.db, `INSERT INTO branding_config (key, value) VALUES ('k', 'v')`)
	}
	byNode.node(t, "init")
	mustMigrate(t, byGo.db)
	want, got := readCatalog(t, byNode.db), readCatalog(t, byGo.db)
	for _, table := range []string{"settings", "branding_config"} {
		key := "column:" + table + ".updated_at"
		if _, has := want[key]; has {
			t.Errorf("Node added %s.updated_at; BUGS.md D17 and schema.md say it cannot", table)
		}
		if _, has := got[key]; !has {
			t.Errorf("Migrate did not add %s.updated_at (BUGS.md D17)", table)
		}
		delete(got, key)
	}
	requireSameCatalog(t, "old settings tables after Migrate against after Node's init", want, got)
}
