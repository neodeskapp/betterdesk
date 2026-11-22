package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// kind is what a column holds, reduced to what changes how a value moves
// between SQLite and PostgreSQL.
type kind int

const (
	kOther        kind = iota
	kText              // text
	kInteger           // SQLite INTEGER; PostgreSQL smallint, integer, bigint
	kBool              // PostgreSQL boolean
	kReal              // SQLite REAL; PostgreSQL double precision
	kFloat4            // PostgreSQL real, a four-byte float
	kNumeric           // PostgreSQL numeric
	kBlob              // SQLite BLOB or no type; PostgreSQL bytea
	kTime              // PostgreSQL timestamptz, timestamp, date
	kDateTimeText      // SQLite text declared DATETIME, DATE or TIMESTAMP
	kJSON              // PostgreSQL json and jsonb
)

// column is one column as the database has it.
type column struct {
	name     string
	declared string
	kind     kind
	notNull  bool
	// dflt is the default expression, empty when there is none.
	dflt string
	// serial says the database assigns the value: a lone INTEGER PRIMARY
	// KEY on SQLite, a sequence default or identity column on PostgreSQL.
	serial   bool
	identity bool
	pk       int
}

// foreignKey is a single-column foreign key: column points at
// refTable.refColumn, and onDelete is what deleting the row it points at
// does: "CASCADE", "SET NULL" or another action.
type foreignKey struct {
	column, refTable, refColumn, onDelete string
}

// table is one table: its columns in table order, its primary key and its
// foreign keys.
type table struct {
	name   string
	cols   []column
	byName map[string]int
	pk     []string
	fks    []foreignKey
}

func (t *table) has(name string) bool {
	_, ok := t.byName[name]
	return ok
}

func (t *table) col(name string) column {
	if i, ok := t.byName[name]; ok {
		return t.cols[i]
	}
	return column{name: name}
}

// catalog is the tables of one database (the current schema on PostgreSQL).
type catalog struct {
	pg     bool
	tables map[string]*table
}

func (c *catalog) names() []string {
	out := make([]string, 0, len(c.tables))
	for name := range c.tables {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const sqliteColumnsQuery = `
	SELECT m.name, p.name, p.type, p."notnull", COALESCE(p.dflt_value, ''), p.pk,
	       (SELECT COUNT(*) FROM pragma_table_info(m.name) k WHERE k.pk > 0)
	FROM sqlite_master m JOIN pragma_table_info(m.name) p
	WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
	ORDER BY m.name, p.cid`

const sqliteRefsQuery = `
	SELECT m.name, f."from", f."table", COALESCE(f."to", ''), UPPER(f.on_delete),
	       (SELECT COUNT(*) FROM pragma_foreign_key_list(m.name) g WHERE g.id = f.id)
	FROM sqlite_master m JOIN pragma_foreign_key_list(m.name) f
	WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'`

const postgresColumnsQuery = `
	SELECT c.relname, a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
	       COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
	       COALESCE(array_position(pk.conkey, a.attnum), 0),
	       a.attidentity <> ''
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
	LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
	LEFT JOIN pg_constraint pk ON pk.conrelid = c.oid AND pk.contype = 'p'
	WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p')
	ORDER BY c.relname, a.attnum`

const postgresRefsQuery = `
	SELECT c.relname, a.attname, r.relname, ra.attname,
	       CASE k.confdeltype WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT'
	                          WHEN 'r' THEN 'RESTRICT' ELSE 'NO ACTION' END,
	       cardinality(k.conkey)
	FROM pg_constraint k
	JOIN pg_class c ON c.oid = k.conrelid
	JOIN pg_class r ON r.oid = k.confrelid
	JOIN pg_namespace n ON n.oid = c.relnamespace
	JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.conkey[1]
	JOIN pg_attribute ra ON ra.attrelid = r.oid AND ra.attnum = k.confkey[1]
	WHERE k.contype = 'f' AND n.nspname = current_schema()`

// readCatalog reads the tables, columns and foreign keys of a database.
func readCatalog(ctx context.Context, q queryer, pg bool) (*catalog, error) {
	cat := &catalog{pg: pg, tables: map[string]*table{}}
	query := sqliteColumnsQuery
	if pg {
		query = postgresColumnsQuery
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read the tables: %w", err)
	}
	for rows.Next() {
		var name string
		var c column
		var extra any
		if err := rows.Scan(&name, &c.name, &c.declared, &c.notNull, &c.dflt, &c.pk, &extra); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read the tables: %w", err)
		}
		if pg {
			c.kind = postgresKind(c.declared)
			c.identity, _ = extra.(bool)
			c.serial = c.identity || strings.HasPrefix(c.dflt, "nextval(")
		} else {
			c.kind = sqliteKind(c.declared)
			keyColumns, _ := extra.(int64)
			c.serial = c.pk > 0 && keyColumns == 1 && strings.EqualFold(c.declared, "INTEGER")
		}
		t := cat.tables[name]
		if t == nil {
			t = &table{name: name, byName: map[string]int{}}
			cat.tables[name] = t
		}
		t.byName[c.name] = len(t.cols)
		t.cols = append(t.cols, c)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("read the tables: %w", err)
	}
	for _, t := range cat.tables {
		keyed := make([]column, 0, 2)
		for _, c := range t.cols {
			if c.pk > 0 {
				keyed = append(keyed, c)
			}
		}
		sort.Slice(keyed, func(i, j int) bool { return keyed[i].pk < keyed[j].pk })
		for _, c := range keyed {
			t.pk = append(t.pk, c.name)
		}
	}

	query = sqliteRefsQuery
	if pg {
		query = postgresRefsQuery
	}
	rows, err = q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read the foreign keys: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var from string
		var fk foreignKey
		var columns int
		if err := rows.Scan(&from, &fk.column, &fk.refTable, &fk.refColumn, &fk.onDelete, &columns); err != nil {
			return nil, fmt.Errorf("read the foreign keys: %w", err)
		}
		t := cat.tables[from]
		if t == nil || columns != 1 {
			continue
		}
		if fk.refColumn == "" {
			// SQLite: a key that names no column points at the primary key.
			if rt := cat.tables[fk.refTable]; rt != nil && len(rt.pk) == 1 {
				fk.refColumn = rt.pk[0]
			}
		}
		t.fks = append(t.fks, fk)
	}
	return cat, rows.Err()
}

// sqliteKind follows SQLite's rules for the affinity of a declared type,
// with the declarations the driver turns into time values kept apart.
func sqliteKind(declared string) kind {
	d := strings.ToUpper(strings.TrimSpace(declared))
	switch d {
	case "DATETIME", "DATE", "TIMESTAMP":
		return kDateTimeText
	case "":
		return kBlob
	}
	switch {
	case strings.Contains(d, "INT"):
		return kInteger
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return kText
	case strings.Contains(d, "BLOB"):
		return kBlob
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return kReal
	}
	return kOther
}

func postgresKind(declared string) kind {
	switch declared {
	case "integer", "smallint", "bigint":
		return kInteger
	case "boolean":
		return kBool
	case "text":
		return kText
	case "real":
		return kFloat4
	case "double precision":
		return kReal
	case "bytea":
		return kBlob
	case "date":
		return kTime
	case "json", "jsonb":
		return kJSON
	}
	switch {
	case strings.HasPrefix(declared, "character"):
		return kText
	case strings.HasPrefix(declared, "numeric"):
		return kNumeric
	case strings.HasPrefix(declared, "timestamp"):
		return kTime
	}
	return kOther
}

// copyOrder sorts the tables so that a table comes after the tables its
// foreign keys point at, by name where that leaves a choice. Tables in a
// cycle keep their order by name.
func copyOrder(cat *catalog) []string {
	names := cat.names()
	placed := map[string]bool{}
	var out []string
	for len(out) < len(names) {
		progress := false
		for _, name := range names {
			if placed[name] {
				continue
			}
			ready := true
			for _, fk := range cat.tables[name].fks {
				if ref := fk.refTable; ref != name && cat.tables[ref] != nil && !placed[ref] {
					ready = false
					break
				}
			}
			if ready {
				placed[name] = true
				out = append(out, name)
				progress = true
			}
		}
		if !progress {
			for _, name := range names {
				if !placed[name] {
					placed[name] = true
					out = append(out, name)
				}
			}
		}
	}
	return out
}

// quoteIdent quotes a table or column name for both databases.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
