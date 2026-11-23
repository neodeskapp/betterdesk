// Package store is the console's access to its tables on SQLite and
// PostgreSQL. Queries are written once, with ? placeholders, and run on
// both databases.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite" // the SQLite driver
)

// Dialect is the database a DB talks to.
type Dialect int

// The supported databases.
const (
	SQLite Dialect = iota
	Postgres
)

func (d Dialect) String() string {
	if d == Postgres {
		return "postgres"
	}
	return "sqlite"
}

// DB runs the console's queries. It is safe for concurrent use.
//
// On SQLite it holds two handles on the same file: one connection for
// writes, so writers queue up in order, and a few read-only connections
// that run beside it. Statements that change data go through Exec, Insert
// or InTx; Query and QueryRow are for reading.
type DB struct {
	write   *sql.DB
	read    *sql.DB
	dialect Dialect
	// shape is what the tables looked like at the last Migrate or Inspect
	shape atomic.Pointer[Shape]
	// newInstall says the last Migrate created the settings table
	newInstall atomic.Bool
}

// sqliteReaders is the number of read-only connections on SQLite.
const sqliteReaders = 4

// OpenSQLite opens the SQLite file at path, next to the connection the
// signal and API servers use. WAL mode lets the readers run beside the
// writer; a statement that finds the file locked by the other process
// waits up to five seconds. Commits are synced to disk (synchronous FULL,
// SQLite's default), as the console's writes always were.
func OpenSQLite(path string) (*DB, error) {
	base := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)
	write, err := openSQLiteHandle(base+"&_txlock=immediate", 1)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	read, err := openSQLiteHandle(base+"&_pragma=query_only(1)", sqliteReaders)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open sqlite %q for reading: %w", path, err)
	}
	return &DB{write: write, read: read, dialect: SQLite}, nil
}

func openSQLiteHandle(dsn string, conns int) (*sql.DB, error) {
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	handle.SetMaxOpenConns(conns)
	handle.SetMaxIdleConns(conns)
	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return handle, nil
}

// OpenPostgres wraps the server's PostgreSQL pool. Closing the DB does not
// close the pool.
func OpenPostgres(pool *pgxpool.Pool) *DB {
	handle := stdlib.OpenDBFromPool(pool)
	return &DB{write: handle, read: handle, dialect: Postgres}
}

// Close releases the connections this DB opened.
func (d *DB) Close() error {
	err := d.write.Close()
	if d.read != d.write {
		err = errors.Join(err, d.read.Close())
	}
	return err
}

// Dialect reports which database this is.
func (d *DB) Dialect() Dialect { return d.dialect }

// IsPostgres reports whether the database is PostgreSQL.
func (d *DB) IsPostgres() bool { return d.dialect == Postgres }

// Pick returns the text for the current database, for the few places where
// the two need different SQL.
func (d *DB) Pick(sqlite, postgres string) string {
	if d.dialect == Postgres {
		return postgres
	}
	return sqlite
}

// Querier is what DB and Tx share, so query functions work inside and
// outside a transaction.
type Querier interface {
	Exec(ctx context.Context, query string, args ...any) (sql.Result, error)
	Query(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sql.Row
	Insert(ctx context.Context, query string, args ...any) (int64, error)
	Dialect() Dialect
	Pick(sqlite, postgres string) string
}

// Exec runs a statement that returns no rows.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.write.ExecContext(ctx, d.rebind(query), args...)
}

// Query reads rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.read.QueryContext(ctx, d.rebind(query), args...)
}

// QueryRow reads at most one row.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.read.QueryRowContext(ctx, d.rebind(query), args...)
}

// Insert runs an INSERT into a table with an integer id column and returns
// the new id.
func (d *DB) Insert(ctx context.Context, query string, args ...any) (int64, error) {
	return insert(ctx, d.write, d.dialect, query, args)
}

func (d *DB) rebind(query string) string {
	if d.dialect == Postgres {
		return Rebind(query)
	}
	return query
}

// Tx is a transaction.
type Tx struct {
	tx      *sql.Tx
	dialect Dialect
}

// InSnapshot runs fn in a read-only transaction that sees one state of the
// database from its first read on, whatever other connections write
// meanwhile: REPEATABLE READ on PostgreSQL, a read transaction on one of
// the read-only connections on SQLite (WAL keeps its snapshot). fn must
// not write. A backup reads every table in it.
func (d *DB) InSnapshot(ctx context.Context, fn func(tx *Tx) error) error {
	opts := &sql.TxOptions{}
	if d.dialect == Postgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	sqlTx, err := d.read.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin a snapshot: %w", err)
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(&Tx{tx: sqlTx, dialect: d.dialect})
}

// InTx runs fn in a transaction: committed when fn returns nil, rolled back
// when it returns an error or panics.
func (d *DB) InTx(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
		if err != nil {
			if rbErr := sqlTx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = fmt.Errorf("%w (rollback: %v)", err, rbErr)
			}
			return
		}
		if cErr := sqlTx.Commit(); cErr != nil {
			err = fmt.Errorf("commit transaction: %w", cErr)
		}
	}()
	return fn(&Tx{tx: sqlTx, dialect: d.dialect})
}

// Exec runs a statement that returns no rows.
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.rebind(query), args...)
}

// Query runs a statement that returns rows.
func (t *Tx) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.rebind(query), args...)
}

// QueryRow runs a statement that returns at most one row.
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.rebind(query), args...)
}

// Insert runs an INSERT into a table with an integer id column and returns
// the new id.
func (t *Tx) Insert(ctx context.Context, query string, args ...any) (int64, error) {
	return insert(ctx, t.tx, t.dialect, query, args)
}

// Dialect reports which database this is.
func (t *Tx) Dialect() Dialect { return t.dialect }

// Pick returns the text for the current database.
func (t *Tx) Pick(sqlite, postgres string) string {
	if t.dialect == Postgres {
		return postgres
	}
	return sqlite
}

func (t *Tx) rebind(query string) string {
	if t.dialect == Postgres {
		return Rebind(query)
	}
	return query
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// insert returns the id of the inserted row: PostgreSQL through RETURNING
// id, SQLite through the connection's last insert id.
func insert(ctx context.Context, q execQuerier, dialect Dialect, query string, args []any) (int64, error) {
	if dialect == Postgres {
		var id int64
		if !strings.Contains(strings.ToUpper(query), "RETURNING") {
			query += " RETURNING id"
		}
		if err := q.QueryRowContext(ctx, Rebind(query), args...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Rebind turns ? placeholders into PostgreSQL's $1, $2, ... Question marks
// inside quoted strings, quoted identifiers and comments stay as they are.
func Rebind(query string) string {
	if !strings.Contains(query, "?") {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'' || c == '"':
			end := closingQuote(query, i)
			b.WriteString(query[i:end])
			i = end - 1
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				end = len(query) - i
			}
			b.WriteString(query[i : i+end])
			i += end - 1
		case c == '?':
			n++
			fmt.Fprintf(&b, "$%d", n)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// closingQuote returns the index just past the quoted run that starts at i.
// A doubled quote inside the run is an escaped quote.
func closingQuote(query string, i int) int {
	quote := query[i]
	for j := i + 1; j < len(query); j++ {
		if query[j] != quote {
			continue
		}
		if j+1 < len(query) && query[j+1] == quote {
			j++
			continue
		}
		return j + 1
	}
	return len(query)
}

// EscapeLike escapes %, _ and \ in user input for a LIKE pattern. Use it
// with ESCAPE '\'.
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
