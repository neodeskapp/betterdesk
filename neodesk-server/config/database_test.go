package config

import "testing"

// L70: the server opens the database DATABASE_URL names, the type from its
// scheme; DB_URL and DB_TYPE are read for older installs.
func TestLoadEnv_DatabaseURLNamesTheDatabase(t *testing.T) {
	const pg = "postgres://bd@db:5432/neodesk"
	for _, c := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"DATABASE_URL over DB_URL", map[string]string{"DATABASE_URL": pg, "DB_URL": "/old/db_v2.sqlite3"}, pg},
		{"a SQLite address", map[string]string{"DATABASE_URL": "sqlite:///data/db_v2.sqlite3"}, "/data/db_v2.sqlite3"},
		{"a SQLite path", map[string]string{"DATABASE_URL": "/data/db_v2.sqlite3"}, "/data/db_v2.sqlite3"},
		{"an older install", map[string]string{"DB_URL": "/opt/rustdesk/db_v2.sqlite3", "DB_TYPE": "sqlite"}, "/opt/rustdesk/db_v2.sqlite3"},
		{"an older install with a stale DATABASE_URL", map[string]string{"DATABASE_URL": pg, "DB_TYPE": "sqlite", "DB_URL": "/opt/rustdesk/db_v2.sqlite3"}, "/opt/rustdesk/db_v2.sqlite3"},
		{"nothing: the -db flag", nil, "/flag/db_v2.sqlite3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, key := range []string{"DATABASE_URL", "DB_URL", "DB_TYPE"} {
				t.Setenv(key, c.env[key])
			}
			cfg := DefaultConfig()
			cfg.DBPath = "/flag/db_v2.sqlite3"
			cfg.LoadEnv()
			if cfg.DBPath != c.want {
				t.Errorf("DBPath = %q, want %q", cfg.DBPath, c.want)
			}
		})
	}
}
