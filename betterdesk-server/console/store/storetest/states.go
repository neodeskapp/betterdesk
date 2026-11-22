package storetest

import (
	"context"
	_ "embed" // the Node console's schema dumps
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/db"
)

// The schema the Node console created on an empty database, dumped from a
// real run. They are how tests get a database "as Node left it", the
// state an install upgraded from the old console starts in.
var (
	//go:embed dumps/node-sqlite.sql
	nodeSQLiteDump string
	//go:embed dumps/node-postgres.sql
	nodePostgresDump string
)

// Origin says which program created the tables of a test database. The
// tables the console shares with the signal server differ by it.
type Origin int

// The three ways a database comes to have the console's tables.
const (
	// ConsoleFirst is an empty database the Go console migrated.
	ConsoleFirst Origin = iota
	// GoServerFirst is a database the signal server set up before the
	// console got to it: the shared tables have the signal server's columns.
	GoServerFirst
	// NodeFirst is a database the Node console created.
	NodeFirst
)

func (o Origin) String() string {
	switch o {
	case GoServerFirst:
		return "go-server-first"
	case NodeFirst:
		return "node-first"
	}
	return "console-first"
}

// Migrated returns a SQLite database with the console's schema.
func Migrated(t *testing.T) *store.DB {
	t.Helper()
	return Prepare(t, SQLite(t), ConsoleFirst)
}

// EachMigrated runs fn once per database, each with the console's schema:
// subtest "sqlite", and "postgres" when a server is configured.
func EachMigrated(t *testing.T, fn func(t *testing.T, db *store.DB)) {
	t.Helper()
	Each(t, func(t *testing.T, db *store.DB) { fn(t, Prepare(t, db, ConsoleFirst)) })
}

// EachOrigin runs fn once per database and origin, each with the console's
// schema on top of what the origin left: subtests "sqlite/console-first",
// "sqlite/go-server-first", "sqlite/node-first" and the same three under
// "postgres". Code that touches a table both programs create should be
// tested this way.
func EachOrigin(t *testing.T, fn func(t *testing.T, db *store.DB, origin Origin)) {
	t.Helper()
	origins := []Origin{ConsoleFirst, GoServerFirst, NodeFirst}
	t.Run("sqlite", func(t *testing.T) {
		for _, origin := range origins {
			t.Run(origin.String(), func(t *testing.T) { fn(t, Prepare(t, SQLite(t), origin), origin) })
		}
	})
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv(PostgresEnv) == "" {
			t.Skipf("%s is not set", PostgresEnv)
		}
		for _, origin := range origins {
			t.Run(origin.String(), func(t *testing.T) { fn(t, Prepare(t, Postgres(t), origin), origin) })
		}
	})
}

// Prepare takes an empty database through the origin's history and then
// runs the console's migration on it.
func Prepare(t *testing.T, sdb *store.DB, origin Origin) *store.DB {
	t.Helper()
	switch origin {
	case GoServerFirst:
		GoServerMigrate(t, sdb)
	case NodeFirst:
		NodeSchema(t, sdb)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := sdb.Migrate(ctx); err != nil {
		t.Fatalf("migrate the console schema: %v", err)
	}
	return sdb
}

// GoServerMigrate runs the signal server's own migration on the database,
// as the server does at every start.
func GoServerMigrate(t *testing.T, sdb *store.DB) {
	t.Helper()
	gdb := openGoServer(t, sdb)
	if err := gdb.Close(); err != nil {
		t.Fatalf("close the signal server's database handle: %v", err)
	}
}

// GoServer runs the signal server's migration on the database and returns
// the server's own handle on it, for tests that use both sides. It is
// closed when the test ends.
func GoServer(t *testing.T, sdb *store.DB) db.Database {
	t.Helper()
	gdb := openGoServer(t, sdb)
	t.Cleanup(func() { _ = gdb.Close() })
	return gdb
}

func openGoServer(t *testing.T, sdb *store.DB) db.Database {
	t.Helper()
	gdb, err := db.Open(goServerDSN(t, sdb))
	if err != nil {
		t.Fatalf("open the database as the signal server: %v", err)
	}
	if err := gdb.Migrate(); err != nil {
		_ = gdb.Close()
		t.Fatalf("signal server migration: %v", err)
	}
	if err := gdb.EnsureClientSessionsSchema(); err != nil {
		_ = gdb.Close()
		t.Fatalf("signal server client sessions schema: %v", err)
	}
	return gdb
}

// goServerDSN returns what the signal server needs to open the same
// database: the file path on SQLite, the server's address with the test's
// schema as search path on PostgreSQL.
func goServerDSN(t *testing.T, sdb *store.DB) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !sdb.IsPostgres() {
		var path string
		if err := sdb.QueryRow(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
			t.Fatalf("find the sqlite file: %v", err)
		}
		return path
	}
	var database, schema string
	if err := sdb.QueryRow(ctx, `SELECT current_database(), current_schema()`).Scan(&database, &schema); err != nil {
		t.Fatalf("find the test schema: %v", err)
	}
	u, err := url.Parse(os.Getenv(PostgresEnv))
	if err != nil || u.Scheme == "" {
		t.Fatalf("%s must be a postgres:// URL to open the database as the signal server", PostgresEnv)
	}
	u.Path = "/" + database
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// NodeSchema gives an empty database the tables, indexes and the two
// default groups the Node console created at its first start, panel_sessions
// included.
func NodeSchema(t *testing.T, sdb *store.DB) {
	t.Helper()
	dump := nodeSQLiteDump
	if sdb.IsPostgres() {
		dump = nodePostgresDump
	}
	ApplyDump(t, sdb, dump)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for table, note := range map[string]string{"user_groups": "Default user group", "device_groups": "Default device group"} {
		if _, err := sdb.Exec(ctx, `INSERT INTO `+table+` (guid, name, note) VALUES (?, ?, ?)`, uuid.NewString(), "Default", note); err != nil {
			t.Fatalf("seed %s: %v", table, err)
		}
	}
}

// ApplyDump runs a schema dump on the database: the output of
// `sqlite3 file .schema` on SQLite, of `pg_dump --schema-only` on
// PostgreSQL. A PostgreSQL dump names the schema public; the tables go into
// the test's schema instead.
func ApplyDump(t *testing.T, sdb *store.DB, dump string) {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(dump, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "--"), strings.HasPrefix(line, `\`):
		case strings.HasPrefix(line, "SET "), strings.HasPrefix(line, "SELECT pg_catalog.set_config("):
		case strings.HasPrefix(line, "CREATE TABLE sqlite_sequence("):
		default:
			if sdb.IsPostgres() {
				line = strings.ReplaceAll(line, "public.", "")
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	// One transaction: SQLite would otherwise sync the file after every
	// statement, and PostgreSQL runs a batch of statements as one anyway.
	script := b.String()
	if !sdb.IsPostgres() {
		script = "BEGIN;\n" + script + "COMMIT;\n"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := sdb.Exec(ctx, script); err != nil {
		t.Fatalf("apply the schema dump: %v", err)
	}
}
