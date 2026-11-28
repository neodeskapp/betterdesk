package config

import (
	"path/filepath"
	"testing"
)

func TestServerDataDir(t *testing.T) {
	for _, tc := range []struct {
		name, db, key, data, want string
	}{
		{"sqlite keeps its folder", filepath.Join("opt", "rustdesk", "db.sqlite3"), "id_ed25519", filepath.Join("data"), filepath.Join("opt", "rustdesk")},
		{"postgres uses DATA_DIR", "postgres://u@db/neodesk", filepath.Join("secrets", "id_ed25519"), filepath.Join("data"), filepath.Join("data")},
		{"postgres without DATA_DIR uses the key folder", "postgresql://u@db/neodesk", filepath.Join("keys", "id_ed25519"), "", filepath.Join("keys")},
		{"nothing set", "", "", "", "."},
	} {
		c := &Config{DBPath: tc.db, KeyFile: tc.key, DataDir: tc.data}
		if got := c.ServerDataDir(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
