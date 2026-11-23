package envvar

import (
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// L70: DATABASE_URL names the database and its scheme the type; DB_URL and
// DB_TYPE are read for older installs.
func TestDatabaseFromTheEnvironment(t *testing.T) {
	const pg = "postgres://bd:secret@db:5432/neodesk"
	for _, c := range []struct {
		name     string
		env      map[string]string
		dsn      string
		postgres bool
		warned   bool
	}{
		{"nothing", nil, "", false, false},
		{"PostgreSQL in DATABASE_URL", map[string]string{"DATABASE_URL": pg}, pg, true, false},
		{"postgresql scheme", map[string]string{"DATABASE_URL": "PostgreSQL://h/db"}, "PostgreSQL://h/db", true, false},
		{"SQLite path in DATABASE_URL", map[string]string{"DATABASE_URL": "/opt/neodesk/db_v2.sqlite3"}, "/opt/neodesk/db_v2.sqlite3", false, false},
		{"sqlite:// in DATABASE_URL", map[string]string{"DATABASE_URL": "sqlite:///opt/neodesk/db_v2.sqlite3"}, "/opt/neodesk/db_v2.sqlite3", false, false},
		{"sqlite: in DATABASE_URL", map[string]string{"DATABASE_URL": `SQLite:C:\NeoDesk\db_v2.sqlite3`}, `C:\NeoDesk\db_v2.sqlite3`, false, false},
		{"Windows path", map[string]string{"DATABASE_URL": `C:\NeoDesk\db_v2.sqlite3`}, `C:\NeoDesk\db_v2.sqlite3`, false, false},
		{"DATABASE_URL over DB_URL", map[string]string{"DATABASE_URL": pg, "DB_URL": "/old/db_v2.sqlite3"}, pg, true, true},
		{"the old three in step", map[string]string{"DATABASE_URL": pg, "DB_URL": pg, "DB_TYPE": "postgres"}, pg, true, false},
		{"older install: DB_URL alone", map[string]string{"DB_URL": "/opt/rustdesk/db_v2.sqlite3"}, "/opt/rustdesk/db_v2.sqlite3", false, false},
		{"older install: DB_URL PostgreSQL", map[string]string{"DB_URL": pg, "DB_TYPE": "postgres"}, pg, true, false},
		{"older install on SQLite with a stale DATABASE_URL", map[string]string{"DATABASE_URL": pg, "DB_TYPE": "sqlite", "DB_URL": "/opt/rustdesk/db_v2.sqlite3"}, "/opt/rustdesk/db_v2.sqlite3", false, true},
		{"stale DATABASE_URL without DB_URL", map[string]string{"DATABASE_URL": pg, "DB_TYPE": "SQLite"}, "", false, true},
		{"DB_TYPE=postgres that the scheme contradicts", map[string]string{"DATABASE_URL": "not a url", "DB_TYPE": "postgres"}, "not a url", true, true},
		{"DB_TYPE=sqlite in step", map[string]string{"DATABASE_URL": "/x.sqlite3", "DB_TYPE": "sqlite"}, "/x.sqlite3", false, false},
		{"space around", map[string]string{"DATABASE_URL": "  " + pg + "\n"}, pg, true, false},
	} {
		db := DatabaseFrom(env(c.env))
		if db.DSN != c.dsn || db.Postgres != c.postgres || (len(db.Warnings) > 0) != c.warned {
			t.Errorf("%s: %+v, want DSN %q, PostgreSQL %v, warned %v", c.name, db, c.dsn, c.postgres, c.warned)
		}
		for _, w := range db.Warnings {
			if strings.Contains(w, "secret") {
				t.Errorf("%s: warning %q holds the password", c.name, w)
			}
		}
	}
}
