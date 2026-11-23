package migrate

// sqlite2pg and pg2sqlite move a whole install: every table of the
// server and the console, every column, ids kept. The destination's tables
// are the ones the server and the console create themselves, in the start
// order the source's tables show.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// moveOptions are the flags of sqlite2pg and pg2sqlite.
type moveOptions struct {
	// overwrite replaces an existing SQLite file, or the rows of a
	// PostgreSQL database that has any.
	overwrite bool
	// skipLeftovers moves the rest when the source has tables or columns
	// the destination's schema lacks, instead of failing.
	skipLeftovers bool
}

// authImportMarker is the row of neodesk_migrations that says an old
// console's auth.db has been moved into the SQLite file (console/authimport,
// db.SQLiteAuthConsolidated).
const authImportMarker = "sqlite_auth_consolidation_v1"

// sqliteOnlyTables are tables a SQLite install has that have no place on
// PostgreSQL, with the reason.
var sqliteOnlyTables = map[string]string{
	"neodesk_migrations": "the marker of the auth.db import, which exists only next to SQLite",
}

// knownMissingColumns are source columns the destination's schema lacks by
// design, keyed by destination dialect ("postgres" or "sqlite") and
// "table.column", with the reason.
var knownMissingColumns = map[string]map[string]string{
	"postgres": {
		"peer.tags": "the console's peer table has no tags column on PostgreSQL (BUGS D4); the devices' tags are in peers.tags",
	},
}

// runSQLite2PG moves the SQLite install at srcPath into the PostgreSQL
// database dstDSN.
func runSQLite2PG(srcPath, dstDSN string, opts moveOptions) error {
	if srcPath == "" || dstDSN == "" || !isPG(dstDSN) {
		return errors.New("usage: neodesk-server migrate -mode sqlite2pg -src <sqlite-path> -dst <postgres://...> [-overwrite] [-skip-leftovers]")
	}
	if err := requireFile(srcPath); err != nil {
		return err
	}
	bp, err := createBackup(srcPath)
	if err != nil {
		return err
	}
	log.Printf("Backup created: %s", bp)

	ctx := context.Background()
	src, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)", srcPath))
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()
	src.SetMaxOpenConns(1)
	srcTx, err := src.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	defer srcTx.Rollback()
	srcCat, err := readCatalog(ctx, srcTx, false)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}

	dst, closeDst, err := openPostgres(ctx, dstDSN)
	if err != nil {
		return err
	}
	defer closeDst()
	full, err := tablesWithRows(ctx, dst)
	if err != nil {
		return err
	}
	if len(full) > 0 && !opts.overwrite {
		return fmt.Errorf("the PostgreSQL database holds data already (%s): pass -overwrite to replace everything in it", strings.Join(full, ", "))
	}

	consoleFirst := startedByConsole(srcCat)
	log.Printf("Creating the server's and the console's tables in PostgreSQL (%s)...", startOrderName(consoleFirst))
	if err := createSchema(ctx, dstDSN, consoleFirst); err != nil {
		return err
	}
	m := &mover{src: srcTx, srcCat: srcCat, opts: opts}
	if err := m.intoPostgres(ctx, dst); err != nil {
		if len(full) == 0 {
			// The database had no rows: take out the few the migrations
			// seeded, so that it is as empty as before and a second run
			// needs no -overwrite. The tables stay; the server creates
			// the same at its start.
			if cerr := truncateAll(ctx, dst); cerr != nil {
				log.Printf("WARN: the PostgreSQL database keeps the rows the server's migrations seeded: %v", cerr)
			}
		}
		return err
	}
	log.Printf("Migration complete: %d rows in %d tables moved to PostgreSQL", m.total, m.tablesCopied)
	return nil
}

// runPG2SQLite moves the PostgreSQL install srcDSN into a new SQLite file
// at dstPath.
func runPG2SQLite(srcDSN, dstPath string, opts moveOptions) error {
	if srcDSN == "" || !isPG(srcDSN) || dstPath == "" {
		return errors.New("usage: neodesk-server migrate -mode pg2sqlite -src <postgres://...> -dst <sqlite-path> [-overwrite] [-skip-leftovers]")
	}
	if err := checkSQLiteDst(dstPath, opts.overwrite); err != nil {
		return err
	}

	ctx := context.Background()
	src, closeSrc, err := openPostgres(ctx, srcDSN)
	if err != nil {
		return err
	}
	defer closeSrc()
	// One snapshot of the whole source.
	srcTx, err := src.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	defer srcTx.Rollback()
	srcCat, err := readCatalog(ctx, srcTx, true)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	consoleFirst := startedByConsole(srcCat)

	var m *mover
	_, err = writeSQLiteFile(dstPath, opts.overwrite, func(path string) (int, error) {
		log.Printf("Creating the server's and the console's tables in SQLite (%s)...", startOrderName(consoleFirst))
		if err := createSchema(ctx, path, consoleFirst); err != nil {
			return 0, err
		}
		dst, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", path))
		if err != nil {
			return 0, fmt.Errorf("open destination: %w", err)
		}
		dst.SetMaxOpenConns(1)
		n, err := func() (int, error) {
			tx, err := dst.BeginTx(ctx, nil)
			if err != nil {
				return 0, fmt.Errorf("begin: %w", err)
			}
			defer tx.Rollback()
			dstCat, err := readCatalog(ctx, tx, false)
			if err != nil {
				return 0, fmt.Errorf("destination: %w", err)
			}
			m = &mover{src: srcTx, dst: tx, srcCat: srcCat, dstCat: dstCat, opts: opts}
			if err := m.run(ctx); err != nil {
				return m.total, err
			}
			if err := writeAuthImportMarker(ctx, tx); err != nil {
				return m.total, err
			}
			if err := tx.Commit(); err != nil {
				return m.total, fmt.Errorf("commit: %w", err)
			}
			// One file without -wal next to it; the server turns WAL on
			// again when it opens the file.
			if _, err := dst.ExecContext(ctx, `PRAGMA journal_mode=DELETE`); err != nil {
				return m.total, fmt.Errorf("leave WAL mode: %w", err)
			}
			return m.total, nil
		}()
		if cerr := dst.Close(); err == nil && cerr != nil {
			err = fmt.Errorf("close destination: %w", cerr)
		}
		return n, err
	})
	if err != nil {
		return err
	}
	log.Printf("Migration complete: %d rows in %d tables moved to %s", m.total, m.tablesCopied, dstPath)
	return nil
}

// openPostgres opens a database/sql handle on dsn whose sessions run in
// UTC, as the server's do, and the function that closes it.
func openPostgres(ctx context.Context, dsn string) (*sql.DB, func(), error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("PostgreSQL address: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	handle := stdlib.OpenDBFromPool(pool)
	return handle, func() { _ = handle.Close(); pool.Close() }, nil
}

// tablesWithRows lists the tables of the database's current schema that
// have a row.
func tablesWithRows(ctx context.Context, dst *sql.DB) ([]string, error) {
	cat, err := readCatalog(ctx, dst, true)
	if err != nil {
		return nil, fmt.Errorf("destination: %w", err)
	}
	var full []string
	for _, name := range cat.names() {
		var any bool
		if err := dst.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM `+quoteIdent(name)+`)`).Scan(&any); err != nil {
			return nil, fmt.Errorf("destination: look into %s: %w", name, err)
		}
		if any {
			full = append(full, name)
		}
	}
	return full, nil
}

// truncateAll removes every row of every table of the current schema.
func truncateAll(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	cat, err := readCatalog(ctx, q, true)
	if err != nil {
		return err
	}
	if len(cat.tables) == 0 {
		return nil
	}
	quoted := make([]string, 0, len(cat.tables))
	for _, n := range cat.names() {
		quoted = append(quoted, quoteIdent(n))
	}
	_, err = q.ExecContext(ctx, `TRUNCATE `+strings.Join(quoted, ", ")+` RESTART IDENTITY`)
	return err
}

// intoPostgres copies the source into the PostgreSQL database dst in one
// transaction and sets the sequences.
func (m *mover) intoPostgres(ctx context.Context, dst *sql.DB) error {
	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	if m.dstCat, err = readCatalog(ctx, tx, true); err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	m.dst = tx
	if err := m.run(ctx); err != nil {
		return err
	}
	if err := m.setSequences(ctx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// startedByConsole tells from the shared tables which program created the
// source's tables first: on SQLite the console keys address books by user
// id and gives users the default role admin, on PostgreSQL its user ids are
// integer where the signal server's are bigint.
func startedByConsole(cat *catalog) bool {
	users, hasUsers := cat.tables["users"]
	if cat.pg {
		return hasUsers && users.col("id").declared == "integer"
	}
	if ab, ok := cat.tables["address_books"]; ok {
		switch {
		case ab.has("user_id"):
			return true
		case ab.has("username"):
			return false
		}
	}
	return hasUsers && strings.Contains(users.col("role").dflt, "admin")
}

func startOrderName(consoleFirst bool) string {
	if consoleFirst {
		return "console first, as the source"
	}
	return "server first, as the source"
}

// createSchema creates the tables in the database at target (a SQLite path
// or a postgres:// address) the way the server does at its start: the
// signal server's migration and the console's, in the given order, then
// the console's session table.
func createSchema(ctx context.Context, target string, consoleFirst bool) error {
	server := func() error {
		gdb, err := db.Open(target)
		if err != nil {
			return fmt.Errorf("server schema: %w", err)
		}
		err = gdb.Migrate()
		if err == nil {
			err = gdb.EnsureClientSessionsSchema()
		}
		if cerr := gdb.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("server schema: %w", err)
		}
		return nil
	}
	console := func() error {
		var sdb *store.DB
		var closePool func()
		if isPG(target) {
			pg, err := db.OpenPostgres(target)
			if err != nil {
				return fmt.Errorf("console schema: %w", err)
			}
			sdb, closePool = store.OpenPostgres(pg.Pool()), func() { _ = pg.Close() }
		} else {
			var err error
			if sdb, err = store.OpenSQLite(target); err != nil {
				return fmt.Errorf("console schema: %w", err)
			}
			closePool = func() {}
		}
		defer closePool()
		err := sdb.Migrate(ctx)
		if err == nil {
			// The console's session manager creates panel_sessions.
			_, err = session.New(ctx, session.Options{DB: sdb, Secret: "migrate"})
		}
		if cerr := sdb.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("console schema: %w", err)
		}
		return nil
	}
	steps := []func() error{server, console}
	if consoleFirst {
		steps = []func() error{console, server}
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// writeAuthImportMarker records in a new SQLite file that there is no
// auth.db left to import: the accounts came from PostgreSQL. Without it the
// console would merge an old auth.db next to the file over them at its
// next start.
func writeAuthImportMarker(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS neodesk_migrations (
			name TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			source_sha256 TEXT NOT NULL DEFAULT '',
			completed_at TEXT NOT NULL DEFAULT (datetime('now')),
			details TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT OR REPLACE INTO neodesk_migrations (name, status, details)
			VALUES ('` + authImportMarker + `', 'complete', '{"source":"migrate pg2sqlite"}')`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("write the auth.db import marker: %w", err)
		}
	}
	return nil
}

// mover copies the rows of every source table into the destination, in one
// transaction on each side.
type mover struct {
	src, dst       *sql.Tx
	srcCat, dstCat *catalog
	opts           moveOptions

	total        int
	tablesCopied int
	// defaults caches the value of a destination column's default
	// expression, keyed by "table.column".
	defaults map[string]any
}

// source of one destination column: a source column, or an expression on
// the source table t.
type source struct {
	dst  column
	src  column
	expr string
}

// plan says how one table moves.
type plan struct {
	name    string
	sources []source
}

func (m *mover) dialect() string {
	if m.dstCat.pg {
		return "postgres"
	}
	return "sqlite"
}

// planTables matches the source's tables and columns with the
// destination's. Source tables and columns the destination lacks are
// named; unless they are known differences they fail the run without
// -skip-leftovers.
func (m *mover) planTables(ctx context.Context) ([]plan, error) {
	var plans []plan
	var leftovers, skipped []string
	for _, name := range copyOrder(m.dstCat) {
		st, ok := m.srcCat.tables[name]
		if !ok {
			continue
		}
		dt := m.dstCat.tables[name]
		p := plan{name: name}
		used := map[string]bool{}
		for _, dc := range dt.cols {
			if sc, ok := st.byName[dc.name]; ok {
				p.sources = append(p.sources, source{dst: dc, src: st.cols[sc]})
				used[dc.name] = true
			}
		}
		if name == "address_books" {
			p.sources = append(p.sources, addressBookOwner(st, dt, used)...)
		}
		for _, sc := range st.cols {
			if used[sc.name] {
				continue
			}
			field := name + "." + sc.name
			if why, ok := knownMissingColumns[m.dialect()][field]; ok {
				n, err := m.countFilled(ctx, name, sc.name)
				if err != nil {
					return nil, err
				}
				skipped = append(skipped, fmt.Sprintf("column %s (%d rows with a value): %s", field, n, why))
				continue
			}
			leftovers = append(leftovers, "column "+field)
		}
		plans = append(plans, p)
	}
	for _, name := range m.srcCat.names() {
		if _, ok := m.dstCat.tables[name]; ok {
			continue
		}
		n, err := m.countRows(ctx, name)
		if err != nil {
			return nil, err
		}
		if why, ok := sqliteOnlyTables[name]; ok && m.dstCat.pg {
			skipped = append(skipped, fmt.Sprintf("table %s (%d rows): %s", name, n, why))
			continue
		}
		leftovers = append(leftovers, fmt.Sprintf("table %s (%d rows)", name, n))
	}
	for _, s := range skipped {
		log.Printf("Not copied: %s", s)
	}
	if len(leftovers) > 0 {
		for _, l := range leftovers {
			log.Printf("Not in the destination's schema: %s", l)
		}
		if !m.opts.skipLeftovers {
			return nil, fmt.Errorf("the source has %d tables or columns the server's schema does not have (listed above): pass -skip-leftovers to move the rest without them", len(leftovers))
		}
		log.Printf("Leaving them out (-skip-leftovers)")
	}
	return plans, nil
}

// addressBookOwner maps the owner of an address book between the two
// shapes: the console keys them by user id on SQLite, PostgreSQL keys them
// by user name. The SQLite table's own row id has no place on PostgreSQL;
// on SQLite it is assigned anew.
func addressBookOwner(st, dt *table, used map[string]bool) []source {
	switch {
	case st.has("user_id") && !st.has("username") && dt.has("username") && !dt.has("user_id"):
		used["user_id"], used["id"] = true, true
		return []source{{dst: dt.col("username"), src: column{name: "username", kind: kText},
			expr: `(SELECT u.username FROM users u WHERE u.id = t.user_id)`}}
	case st.has("username") && !st.has("user_id") && dt.has("user_id") && !dt.has("username"):
		used["username"] = true
		return []source{{dst: dt.col("user_id"), src: column{name: "user_id", kind: kInteger},
			expr: `(SELECT u.id FROM users u WHERE u.username = t.username)`}}
	}
	return nil
}

func (m *mover) countRows(ctx context.Context, name string) (int, error) {
	var n int
	if err := m.src.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(name)).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s: %w", name, err)
	}
	return n, nil
}

func (m *mover) countFilled(ctx context.Context, name, col string) (int, error) {
	var n int
	q := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND CAST(%s AS TEXT) <> ''`, quoteIdent(name), quoteIdent(col), quoteIdent(col))
	if err := m.src.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count %s.%s: %w", name, col, err)
	}
	return n, nil
}

// run empties the destination's tables (the migrations seed a few rows)
// and copies every planned table.
func (m *mover) run(ctx context.Context) error {
	plans, err := m.planTables(ctx)
	if err != nil {
		return err
	}
	if err := m.emptyDestination(ctx); err != nil {
		return err
	}
	m.defaults = map[string]any{}
	planned := map[string]bool{}
	for _, p := range plans {
		planned[p.name] = true
		res, err := m.copyTable(ctx, p)
		if err != nil {
			return fmt.Errorf("copy %s: %w", p.name, err)
		}
		var have int
		if err := m.dst.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(p.name)).Scan(&have); err != nil {
			return fmt.Errorf("count %s in the destination: %w", p.name, err)
		}
		dropped := 0
		for _, n := range res.dropped {
			dropped += n
		}
		if dropped > 0 {
			log.Printf("  %s: %d read, %d written, %d left out", p.name, res.read, have, dropped)
		} else {
			log.Printf("  %s: %d read, %d written", p.name, res.read, have)
		}
		if res.read != res.written+dropped || res.written != have {
			return fmt.Errorf("copy %s: %d rows read, %d written, %d left out, %d in the destination", p.name, res.read, res.written, dropped, have)
		}
		m.total += have
		m.tablesCopied++
	}
	var empty []string
	for _, name := range m.dstCat.names() {
		if !planned[name] {
			empty = append(empty, name)
		}
	}
	if len(empty) > 0 {
		log.Printf("Not in the source, left empty: %s", strings.Join(empty, ", "))
	}
	return nil
}

// emptyDestination removes the rows the migrations seeded (default groups
// and the like), so the destination holds the source's rows only.
func (m *mover) emptyDestination(ctx context.Context) error {
	names := m.dstCat.names()
	if m.dstCat.pg {
		if err := truncateAll(ctx, m.dst); err != nil {
			return fmt.Errorf("empty the destination: %w", err)
		}
		return nil
	}
	for _, n := range names {
		if _, err := m.dst.ExecContext(ctx, `DELETE FROM `+quoteIdent(n)); err != nil {
			return fmt.Errorf("empty %s in the destination: %w", n, err)
		}
	}
	var seq int
	if err := m.dst.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'sqlite_sequence'`).Scan(&seq); err != nil {
		return err
	}
	if seq > 0 {
		if _, err := m.dst.ExecContext(ctx, `DELETE FROM sqlite_sequence`); err != nil {
			return fmt.Errorf("reset the row ids: %w", err)
		}
	}
	return nil
}

// copied is what copyTable did with the rows of one table.
type copied struct {
	read, written int
	// dropped counts rows left out because they point at a row that is
	// not there, where the destination deletes such rows (ON DELETE
	// CASCADE), by the table they point at.
	dropped map[string]int
}

// orphanCheck is a foreign key of the destination with the expression that
// is true on the source when the row's value points at an existing row.
type orphanCheck struct {
	fk     foreignKey
	source int
	expr   string
}

// orphanChecks returns a check for every foreign key of the destination
// table whose column comes from a source column. SQLite does not hold every
// install to its foreign keys, so a source can have rows that point at
// rows deleted long ago.
func (m *mover) orphanChecks(p plan) []orphanCheck {
	var out []orphanCheck
	for _, fk := range m.dstCat.tables[p.name].fks {
		for i, s := range p.sources {
			if s.expr != "" || s.dst.name != fk.column {
				continue
			}
			col := "t." + quoteIdent(s.src.name)
			expr := "(" + col + " IS NULL)"
			if pt, ok := m.srcCat.tables[fk.refTable]; ok && pt.has(fk.refColumn) {
				expr = fmt.Sprintf("(%s IS NULL OR EXISTS (SELECT 1 FROM %s p WHERE p.%s = %s))",
					col, quoteIdent(fk.refTable), quoteIdent(fk.refColumn), col)
			}
			out = append(out, orphanCheck{fk: fk, source: i, expr: expr})
		}
	}
	return out
}

// copyTable copies the rows of one table.
func (m *mover) copyTable(ctx context.Context, p plan) (copied, error) {
	res := copied{dropped: map[string]int{}}
	if len(p.sources) == 0 {
		return res, nil
	}
	st := m.srcCat.tables[p.name]
	selects := make([]string, len(p.sources))
	names := make([]string, len(p.sources))
	marks := make([]string, len(p.sources))
	for i, s := range p.sources {
		selects[i] = "t." + quoteIdent(s.src.name)
		if s.expr != "" {
			selects[i] = s.expr
		}
		names[i] = quoteIdent(s.dst.name)
		marks[i] = "?"
		if m.dstCat.pg {
			marks[i] = fmt.Sprintf("$%d", i+1)
		}
	}
	checks := m.orphanChecks(p)
	for _, c := range checks {
		selects = append(selects, c.expr)
	}
	query := "SELECT " + strings.Join(selects, ", ") + " FROM " + quoteIdent(p.name) + " t"
	switch {
	case len(st.pk) > 0:
		keys := make([]string, len(st.pk))
		for i, k := range st.pk {
			keys[i] = "t." + quoteIdent(k)
		}
		query += " ORDER BY " + strings.Join(keys, ", ")
	case !m.srcCat.pg:
		query += " ORDER BY t.rowid"
	}
	insert := "INSERT INTO " + quoteIdent(p.name) + " (" + strings.Join(names, ", ") + ")"
	if m.dstCat.pg && hasIdentity(m.dstCat.tables[p.name]) {
		insert += " OVERRIDING SYSTEM VALUE"
	}
	insert += " VALUES (" + strings.Join(marks, ", ") + ")"

	rows, err := m.src.QueryContext(ctx, query)
	if err != nil {
		return res, fmt.Errorf("read: %w", err)
	}
	defer rows.Close()
	stmt, err := m.dst.PrepareContext(ctx, insert)
	if err != nil {
		return res, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	values := make([]any, len(p.sources)+len(checks))
	ptrs := make([]any, len(values))
	for i := range values {
		ptrs[i] = &values[i]
	}
	args := make([]any, len(p.sources))
	substituted := map[string]int{}
	nulled := map[string]int{}
rowLoop:
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return res, fmt.Errorf("read row %d: %w", res.read+1, err)
		}
		res.read++
		key := func() string { return rowKey(st, p, values) }
		for i, s := range p.sources {
			field := p.name + "." + s.dst.name
			v, err := convert(values[i], s.src, s.dst, m.dstCat.pg, field)
			if err != nil {
				return res, fmt.Errorf("row %d (%s): %s: %w", res.read, key(), s.dst.name, err)
			}
			if v == nil {
				var byDefault bool
				if v, byDefault, err = m.nullValue(ctx, p.name, s); err != nil {
					return res, fmt.Errorf("row %d (%s): %s: %w", res.read, key(), s.dst.name, err)
				}
				if byDefault {
					substituted[s.dst.name]++
				}
			}
			args[i] = v
		}
		for j, c := range checks {
			if found, _ := convert(values[len(p.sources)+j], column{}, column{kind: kBool}, true, ""); found == true {
				continue
			}
			switch c.fk.onDelete {
			case "SET NULL":
				args[c.source] = nil
				nulled[c.fk.column]++
			case "CASCADE":
				res.dropped[c.fk.refTable]++
				continue rowLoop
			default:
				return res, fmt.Errorf("row %d (%s): %s %v points at no row of %s",
					res.read, key(), c.fk.column, values[c.source], c.fk.refTable)
			}
		}
		r, err := stmt.ExecContext(ctx, args...)
		if err != nil {
			return res, fmt.Errorf("write row %d (%s): %w", res.read, key(), err)
		}
		n, err := r.RowsAffected()
		if err != nil {
			return res, fmt.Errorf("write row %d: %w", res.read, err)
		}
		res.written += int(n)
	}
	if err := rows.Err(); err != nil {
		return res, fmt.Errorf("read: %w", err)
	}
	for _, c := range sortedKeys(substituted) {
		log.Printf("  %s.%s: %d empty values written as the column's default %s, as the destination does not take NULL there",
			p.name, c, substituted[c], m.dstCat.tables[p.name].col(c).dflt)
	}
	for _, c := range sortedKeys(nulled) {
		log.Printf("  %s.%s: %d values pointed at rows that are gone, written as NULL (the destination's ON DELETE SET NULL)",
			p.name, c, nulled[c])
	}
	for _, t := range sortedKeys(res.dropped) {
		log.Printf("  %s: %d rows pointed at rows of %s that are gone, left out (the destination's ON DELETE CASCADE)",
			p.name, res.dropped[t], t)
	}
	return res, nil
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// nullValue is what goes into a destination column for an empty source
// value: on SQLite the empty text a time column's default gives where the
// server writes it; in a column that does not take NULL its default, as a
// row written without the value gets it (byDefault reports this case);
// else NULL.
func (m *mover) nullValue(ctx context.Context, tableName string, s source) (v any, byDefault bool, err error) {
	if !m.dstCat.pg && s.src.kind == kTime && s.dst.dflt == "''" {
		return "", false, nil
	}
	if !s.dst.notNull || s.dst.dflt == "" || s.dst.serial || strings.EqualFold(s.dst.dflt, "NULL") {
		return nil, false, nil
	}
	key := tableName + "." + s.dst.name
	if v, ok := m.defaults[key]; ok {
		return v, true, nil
	}
	if err := m.dst.QueryRowContext(ctx, "SELECT "+s.dst.dflt).Scan(&v); err != nil {
		return nil, false, fmt.Errorf("default %s: %w", s.dst.dflt, err)
	}
	if b, ok := v.([]byte); ok && s.dst.kind != kBlob {
		v = string(b)
	}
	if t, ok := v.(time.Time); ok && !m.dstCat.pg {
		v = t.UTC().Format(sqliteTimeForm)
	}
	m.defaults[key] = v
	return v, true, nil
}

func hasIdentity(t *table) bool {
	for _, c := range t.cols {
		if c.identity {
			return true
		}
	}
	return false
}

// rowKey names a row in an error message by its primary key.
func rowKey(st *table, p plan, values []any) string {
	var parts []string
	for _, k := range st.pk {
		for i, s := range p.sources {
			if s.expr == "" && s.src.name == k {
				parts = append(parts, fmt.Sprintf("%s=%v", k, values[i]))
			}
		}
	}
	if len(parts) == 0 {
		return "no key"
	}
	return strings.Join(parts, ", ")
}

// setSequences sets every PostgreSQL sequence behind a column the
// database assigns past the highest id copied, so new rows get fresh ids.
func (m *mover) setSequences(ctx context.Context) error {
	for _, name := range m.dstCat.names() {
		for _, c := range m.dstCat.tables[name].cols {
			if !c.serial {
				continue
			}
			var seq sql.NullString
			if err := m.dst.QueryRowContext(ctx, `SELECT pg_get_serial_sequence($1, $2)`, quoteIdent(name), c.name).Scan(&seq); err != nil {
				return fmt.Errorf("find the sequence of %s.%s: %w", name, c.name, err)
			}
			if !seq.Valid {
				continue
			}
			q := fmt.Sprintf(`SELECT setval($1::regclass, COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false)`, quoteIdent(c.name), quoteIdent(name))
			if _, err := m.dst.ExecContext(ctx, q, seq.String); err != nil {
				return fmt.Errorf("set the sequence of %s.%s: %w", name, c.name, err)
			}
		}
	}
	return nil
}
