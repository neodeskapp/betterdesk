package backup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store"
)

// ImportResult is what ImportAllTables did: the tables it replaced, the
// names it left alone (no plain identifier, no such table, no list of
// rows) and one warning per table that failed.
type ImportResult struct {
	Restored []string
	Skipped  []string
	Warnings []string
}

// ImportAllTables puts a dump back (importAllTables of the old adapter).
// Each table of the dump that exists in the database is emptied and filled
// with the dumped rows; a table that fails is left as it was and the
// others go on. Only the columns the table has are written. Values are
// bound as the old console's driver bound them, so the database accepts
// and refuses what it did, but for a list in a json or jsonb column, which
// goes in as its JSON text.
//
// SQLite: foreign keys are off for the whole run, each table is replaced
// in a transaction of its own and rows are written with INSERT OR REPLACE.
// PostgreSQL: see replacePostgresTables.
//
// The users table gets the sign-in token times of the accounts it replaced
// (endReplacedCredentials, with the time of the restore from now) in the
// same transaction as its rows; on PostgreSQL right before the commit.
//
// An error is returned when the run stops before the end; on SQLite the
// tables up to that point are replaced, on PostgreSQL none is.
func ImportAllTables(ctx context.Context, db *store.DB, dump any) (ImportResult, error) {
	return importAllTables(ctx, db, dump, time.Now)
}

func importAllTables(ctx context.Context, db *store.DB, dump any, now func() time.Time) (ImportResult, error) {
	res := ImportResult{Restored: []string{}, Skipped: []string{}, Warnings: []string{}}
	var tables any = branding.NewObject()
	if truthy(dump) {
		if t := prop(dump, "tables"); truthy(t) {
			tables = t
		}
	}
	liveQuery := `SELECT name FROM sqlite_master WHERE type='table'`
	if db.IsPostgres() {
		liveQuery = `SELECT table_name FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`
	}
	liveNames, err := readStrings(ctx, db, liveQuery)
	if err != nil {
		return res, fmt.Errorf("list tables: %w", err)
	}
	live := map[string]bool{}
	for _, n := range liveNames {
		live[n] = true
	}

	var jobs []*tableJob
	for _, e := range entriesOf(tables) {
		rows, isList := e.value.([]any)
		if !isSafeIdent(e.key) || !live[e.key] || !isList {
			res.Skipped = append(res.Skipped, e.key)
			continue
		}
		cols, err := tableColumns(ctx, db, e.key)
		if err != nil {
			return res, fmt.Errorf("columns of %s: %w", e.key, err)
		}
		jobs = append(jobs, &tableJob{name: e.key, cols: cols, rows: rows})
	}

	if db.IsPostgres() {
		if err := replacePostgresTables(ctx, db, jobs, now); err != nil {
			return res, err
		}
	} else {
		if _, err := db.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return res, fmt.Errorf("switch foreign keys off: %w", err)
		}
		defer func() { _, _ = db.Exec(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`) }()
		for _, j := range jobs {
			j.err = db.InTx(ctx, func(tx *store.Tx) error {
				if err := j.readCredentials(ctx, tx); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(j.name)); err != nil {
					return err
				}
				if err := writeRows(ctx, tx, j); err != nil {
					return err
				}
				return j.endReplacedCredentials(ctx, tx, now)
			})
		}
	}
	for _, j := range jobs {
		if j.err != nil {
			res.Warnings = append(res.Warnings, "Table "+j.name+": "+driverMessage(j.err))
			continue
		}
		res.Restored = append(res.Restored, j.name)
	}
	return res, nil
}

// tableJob is a table of the dump to replace: its columns, the dumped
// rows and the error that left it as it was.
type tableJob struct {
	name string
	cols map[string]string
	rows []any
	err  error
	// credentials are the accounts before the restore, for the users
	// table with credentials_changed_at
	credentials map[string]credential
}

// tracksCredentials is true for the users table with
// credentials_changed_at.
func (j *tableJob) tracksCredentials() bool {
	_, has := j.cols[credentialsChangedColumn]
	return j.name == "users" && has
}

// readCredentials keeps the accounts' credentials before the table is
// emptied.
func (j *tableJob) readCredentials(ctx context.Context, tx *store.Tx) error {
	if !j.tracksCredentials() {
		return nil
	}
	before, err := readCredentials(ctx, tx.Query, true)
	j.credentials = before
	return err
}

// endReplacedCredentials sets the token times of the restored accounts.
func (j *tableJob) endReplacedCredentials(ctx context.Context, tx *store.Tx, now func() time.Time) error {
	if j.credentials == nil {
		return nil
	}
	return endReplacedCredentials(ctx, tx.Query, tx.Exec, j.credentials, now().Unix())
}

type entry struct {
	key   string
	value any
}

// entriesOf is Object.entries(v) for a value that is not null.
func entriesOf(v any) []entry {
	switch x := v.(type) {
	case *branding.Object:
		keys := x.Keys()
		out := make([]entry, 0, len(keys))
		for _, k := range keys {
			value, _ := x.Get(k)
			out = append(out, entry{k, value})
		}
		return out
	case []any:
		out := make([]entry, len(x))
		for i, item := range x {
			out[i] = entry{strconv.Itoa(i), item}
		}
		return out
	case string:
		keys, _ := objectKeys(x)
		out := make([]entry, len(keys))
		for i, k := range keys {
			out[i] = entry{k, nil}
		}
		return out
	}
	return nil
}

// tableColumns lists the columns of a table with their type on
// PostgreSQL (data_type of information_schema), "" on SQLite.
func tableColumns(ctx context.Context, db *store.DB, table string) (map[string]string, error) {
	cols := map[string]string{}
	if !db.IsPostgres() {
		names, err := sqliteColumns(ctx, db, table)
		for _, n := range names {
			cols[n] = ""
		}
		return cols, err
	}
	rows, err := db.Query(ctx, `SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = ?`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		cols[name] = typ
	}
	return cols, rows.Err()
}

func sqliteColumns(ctx context.Context, q store.Querier, table string) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// writeRows writes the dumped rows of a table that was emptied. A list
// for a json or jsonb column is sent as its JSON text, as an object is
// (pgArg would make it an array literal, which JSON refuses).
func writeRows(ctx context.Context, tx *store.Tx, j *tableJob) error {
	verb := "INSERT OR REPLACE INTO "
	if tx.Dialect() == store.Postgres {
		verb = "INSERT INTO "
	}
	for _, row := range j.rows {
		keys, err := objectKeys(row)
		if err != nil {
			return err
		}
		var names []string
		var values []any
		for _, k := range keys {
			typ, has := j.cols[k]
			if !has || !isSafeIdent(k) {
				continue
			}
			names = append(names, quoteIdent(k))
			v := prop(row, k)
			if text, ok := isBufferObject(v); ok {
				v = buffer(nodeBase64(text))
			}
			if list, isList := v.([]any); isList && (typ == "json" || typ == "jsonb") {
				v = branding.Stringify(list)
			}
			values = append(values, v)
		}
		if len(names) == 0 {
			continue
		}
		args, err := bindArgs(tx, len(names), values)
		if err != nil {
			return err
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
		query := verb + quoteIdent(j.name) + ` (` + strings.Join(names, ", ") + `) VALUES (` + marks + `)`
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}

// replacePostgresTables replaces the tables of jobs in one transaction.
// It asks for session_replication_role = replica, which switches foreign
// keys and their cascades off for a superuser and is refused (and
// ignored) for anyone else. So that the restore holds for anyone else
// too, the tables are emptied children first and filled parents first, in
// the order of their foreign keys: no cascade removes restored rows and no
// child row comes before its parent. Each step of a table has its own
// savepoint: a table that cannot be emptied keeps its rows, and one whose
// rows fail gets its rows from before back (kept in a temporary table).
// With its rows, every sequence of a serial column of the table is moved
// to the column's largest value, so the next row gets a free id; a table
// whose sequence cannot be moved counts as failed. The token times of the
// restored accounts are set last, so that the time of the restore is the
// time of the commit; when that fails, nothing is replaced.
func replacePostgresTables(ctx context.Context, db *store.DB, jobs []*tableJob, now func() time.Time) error {
	order, err := parentsFirst(ctx, db, jobs)
	if err != nil {
		return err
	}
	return db.InTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(ctx, `SAVEPOINT replica`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
			if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT replica`); err != nil {
				return err
			}
		}
		kept := func(i int) string { return "backup_kept_" + strconv.Itoa(i) }
		for i := len(order) - 1; i >= 0; i-- {
			j := order[i]
			j.err = inSavepoint(ctx, tx, func() error {
				if err := j.readCredentials(ctx, tx); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `CREATE TEMP TABLE `+kept(i)+` ON COMMIT DROP AS SELECT * FROM `+quoteIdent(j.name)); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(j.name))
				return err
			})
		}
		for i, j := range order {
			if j.err != nil {
				continue
			}
			j.err = inSavepoint(ctx, tx, func() error {
				if err := writeRows(ctx, tx, j); err != nil {
					return err
				}
				return moveSequences(ctx, tx, j.name)
			})
			if j.err != nil {
				_ = inSavepoint(ctx, tx, func() error {
					_, err := tx.Exec(ctx, `INSERT INTO `+quoteIdent(j.name)+` SELECT * FROM `+kept(i))
					return err
				})
			}
		}
		for _, j := range order {
			if j.err != nil {
				continue
			}
			if err := j.endReplacedCredentials(ctx, tx, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// inSavepoint runs fn in a savepoint, which is rolled back when fn fails,
// so that the transaction goes on.
func inSavepoint(ctx context.Context, tx *store.Tx, fn func() error) error {
	if _, err := tx.Exec(ctx, `SAVEPOINT backup_table`); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT backup_table`); rerr != nil {
			return errors.Join(err, rerr)
		}
		_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT backup_table`)
		return err
	}
	_, err := tx.Exec(ctx, `RELEASE SAVEPOINT backup_table`)
	return err
}

// parentsFirst orders the tables of jobs so that a table comes after the
// tables its foreign keys point to (pg_constraint), and otherwise in the
// order of the dump. When the tables left all wait for each other (keys
// in a circle), the first of them comes next.
func parentsFirst(ctx context.Context, db *store.DB, jobs []*tableJob) ([]*tableJob, error) {
	rows, err := db.Query(ctx, `SELECT c.relname, p.relname FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid
		JOIN pg_class p ON p.oid = k.confrelid
		WHERE k.contype = 'f' AND c.relnamespace = current_schema()::regnamespace`)
	if err != nil {
		return nil, fmt.Errorf("read the foreign keys: %w", err)
	}
	defer rows.Close()
	parents := map[string][]string{}
	for rows.Next() {
		var child, parent string
		if err := rows.Scan(&child, &parent); err != nil {
			return nil, err
		}
		if child != parent {
			parents[child] = append(parents[child], parent)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the foreign keys: %w", err)
	}
	waiting := map[string]bool{}
	for _, j := range jobs {
		waiting[j.name] = true
	}
	ready := func(j *tableJob) bool {
		for _, p := range parents[j.name] {
			if waiting[p] {
				return false
			}
		}
		return true
	}
	order := make([]*tableJob, 0, len(jobs))
	for len(order) < len(jobs) {
		var next *tableJob
		for _, j := range jobs {
			if !waiting[j.name] {
				continue
			}
			if next == nil {
				next = j
			}
			if ready(j) {
				next = j
				break
			}
		}
		order = append(order, next)
		waiting[next.name] = false
	}
	return order, nil
}

// moveSequences sets the sequence of each serial column of a table to the
// column's largest value. A table without rows leaves it as it is.
func moveSequences(ctx context.Context, tx *store.Tx, table string) error {
	rows, err := tx.Query(ctx, `SELECT a.attname, pg_get_serial_sequence(quote_ident(c.relname), a.attname)
		FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = ? AND c.relnamespace = current_schema()::regnamespace
			AND a.attnum > 0 AND NOT a.attisdropped
			AND pg_get_serial_sequence(quote_ident(c.relname), a.attname) IS NOT NULL`, table)
	if err != nil {
		return fmt.Errorf("find the sequences of %s: %w", table, err)
	}
	type serial struct{ column, sequence string }
	var serials []serial
	for rows.Next() {
		var s serial
		if err := rows.Scan(&s.column, &s.sequence); err != nil {
			rows.Close()
			return err
		}
		serials = append(serials, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range serials {
		col := quoteIdent(s.column)
		// setval of NULL, the MAX of an empty table, changes nothing
		if _, err := tx.Exec(ctx, `SELECT setval(?::regclass, MAX(`+col+`)) FROM `+quoteIdent(table), s.sequence); err != nil {
			return fmt.Errorf("move the sequence of %s.%s: %w", table, s.column, err)
		}
	}
	return nil
}

// bindArgs binds values for a statement with n placeholders the way the
// driver of the database did.
func bindArgs(db store.Querier, n int, values []any) ([]any, error) {
	if db.Dialect() == store.Postgres {
		args := make([]any, len(values))
		for i, v := range values {
			args[i] = pgArg(v)
		}
		return args, nil
	}
	return sqliteArgs(n, values...)
}

// driverMessage is err.message of a driver error as the old console saw
// it: the server's message on PostgreSQL, sqlite3_errmsg on SQLite, the
// text of a JavaScript error otherwise.
func driverMessage(err error) string {
	var se *sqlite.Error
	if errors.As(err, &se) {
		msg := strings.TrimSuffix(se.Error(), " (SQLITE_BUSY)")
		msg = strings.TrimSuffix(msg, " ("+strconv.Itoa(se.Code())+")")
		if i := strings.Index(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		return msg
	}
	return errorText(err)
}
