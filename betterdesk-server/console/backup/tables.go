package backup

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
// with the dumped rows, in one transaction per table, so a table that
// fails is left as it was and the others go on. Only the columns the table
// has are written. Values are bound as the old console's driver bound
// them, so the database accepts and refuses what it did, but for a list in
// a json or jsonb column, which goes in as its JSON text.
//
// SQLite: foreign keys are off for the whole run and rows are written with
// INSERT OR REPLACE. PostgreSQL: each transaction asks for
// session_replication_role = replica, which switches foreign keys and
// their cascades off for a superuser and is refused (and ignored) for
// anyone else; rows are written with a plain INSERT.
//
// An error is returned when the run stops before the end, after the
// tables up to that point were replaced.
func ImportAllTables(ctx context.Context, db *store.DB, dump any) (ImportResult, error) {
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

	if !db.IsPostgres() {
		if _, err := db.Exec(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return res, fmt.Errorf("switch foreign keys off: %w", err)
		}
		defer func() { _, _ = db.Exec(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`) }()
	}

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
		if err := replaceTable(ctx, db, e.key, cols, rows); err != nil {
			res.Warnings = append(res.Warnings, "Table "+e.key+": "+driverMessage(err))
			continue
		}
		res.Restored = append(res.Restored, e.key)
	}
	return res, nil
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

// replaceTable empties a table and writes the rows in one transaction. A
// list for a json or jsonb column is sent as its JSON text, as an object
// is (pgArg would make it an array literal, which JSON refuses).
func replaceTable(ctx context.Context, db *store.DB, table string, cols map[string]string, rows []any) error {
	return db.InTx(ctx, func(tx *store.Tx) error {
		if db.IsPostgres() {
			if _, err := tx.Exec(ctx, `SAVEPOINT replica`); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err != nil {
				if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT replica`); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(table)); err != nil {
			return err
		}
		verb := "INSERT OR REPLACE INTO "
		if db.IsPostgres() {
			verb = "INSERT INTO "
		}
		for _, row := range rows {
			keys, err := objectKeys(row)
			if err != nil {
				return err
			}
			var names []string
			var values []any
			for _, k := range keys {
				typ, has := cols[k]
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
			args, err := bindArgs(db, len(names), values)
			if err != nil {
				return err
			}
			marks := strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")
			query := verb + quoteIdent(table) + ` (` + strings.Join(names, ", ") + `) VALUES (` + marks + `)`
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				return err
			}
		}
		return nil
	})
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
