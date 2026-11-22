package envvar

import "strings"

// Database is the database the environment names for the server, the
// console and reset-password.
type Database struct {
	// DSN is a postgres:// or postgresql:// address for PostgreSQL and the
	// path of the file for SQLite; empty when the environment names none,
	// and the caller's SQLite file is used.
	DSN string
	// Postgres says DSN is a PostgreSQL address.
	Postgres bool
	// Warnings name the variables that are set but not used, for the log.
	// They hold no values, so no password reaches a log.
	Warnings []string
}

// DatabaseFrom reads the database from DATABASE_URL, whose scheme gives
// the type: a postgres:// or postgresql:// address is PostgreSQL;
// sqlite:// or sqlite: before a path, or a path alone, is a SQLite file.
// Older installs name the database with DB_URL (read the same way) and
// DB_TYPE, which are read when DATABASE_URL is empty. A DB_TYPE that the
// scheme contradicts is an older install's, and it wins: DB_TYPE=sqlite
// keeps the install on SQLite where DATABASE_URL still names a PostgreSQL
// database (such an install ran on SQLite), and DB_TYPE=postgres keeps
// DATABASE_URL a PostgreSQL address, which then fails to open instead of
// making a SQLite file of that name.
func DatabaseFrom(getenv func(string) string) Database {
	url := strings.TrimSpace(getenv("DATABASE_URL"))
	legacy := strings.TrimSpace(getenv("DB_URL"))
	dbType := strings.ToLower(strings.TrimSpace(getenv("DB_TYPE")))
	var db Database
	switch {
	case url != "" && isPostgresURL(url) && dbType == "sqlite":
		db.Warnings = append(db.Warnings, "DATABASE_URL names a PostgreSQL database but DB_TYPE=sqlite: the server stays on SQLite. Remove DB_TYPE to use DATABASE_URL")
		if legacy != "" && !isPostgresURL(legacy) {
			db.DSN = sqlitePath(legacy)
		}
	case url != "":
		db.DSN, db.Postgres = databaseDSN(url)
		if legacy != "" && legacy != url && legacy != db.DSN {
			db.Warnings = append(db.Warnings, "DB_URL is not used: DATABASE_URL names the database")
		}
		if !db.Postgres && (dbType == "postgres" || dbType == "postgresql") {
			db.DSN, db.Postgres = url, true
			db.Warnings = append(db.Warnings, "DB_TYPE=postgres but DATABASE_URL is no postgres:// address. Remove DB_TYPE, or give DATABASE_URL the PostgreSQL address")
		}
	case legacy != "":
		db.DSN, db.Postgres = databaseDSN(legacy)
	}
	return db
}

// databaseDSN is the DSN of a database address and whether it is
// PostgreSQL.
func databaseDSN(address string) (string, bool) {
	if isPostgresURL(address) {
		return address, true
	}
	return sqlitePath(address), false
}

func isPostgresURL(address string) bool {
	lower := strings.ToLower(address)
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

// sqlitePath is the path in a SQLite address: without sqlite:// or
// sqlite: before it.
func sqlitePath(address string) string {
	for _, scheme := range []string{"sqlite://", "sqlite:"} {
		if len(address) >= len(scheme) && strings.EqualFold(address[:len(scheme)], scheme) {
			return address[len(scheme):]
		}
	}
	return address
}
