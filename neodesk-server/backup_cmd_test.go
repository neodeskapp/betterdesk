package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/backup"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// betterdesk-server backup writes the archive the console's full backup
// makes, from the server's database on SQLite and PostgreSQL, with the
// console's files and the server's keys, into the folder --out names, and
// prints its path (L73).
func TestBackupCommand(t *testing.T) {
	storetest.Each(t, func(t *testing.T, sdb *store.DB) {
		sdb = storetest.Prepare(t, sdb, storetest.GoServerFirst)
		ctx := context.Background()
		if _, err := sdb.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('backup_probe', 'kept')`); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		keys, data, console := filepath.Join(dir, "keys"), filepath.Join(dir, "data"), filepath.Join(dir, "console")
		for _, d := range []string{keys, data, console} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		for path, text := range map[string]string{
			filepath.Join(keys, "id_ed25519.pub"): "PUBKEY",
			filepath.Join(console, ".env"):        "PORT=5000\n",
		} {
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for key, value := range map[string]string{
			"KEYS_PATH": keys, "DATA_DIR": data, "CONSOLE_DIR": console,
			"DATABASE_URL": "", "DB_URL": "", "DB_TYPE": "", "DB_PATH": "",
		} {
			t.Setenv(key, value)
		}
		if dsn := storetest.DSN(t, sdb); sdb.IsPostgres() {
			t.Setenv("DATABASE_URL", dsn)
		} else {
			t.Setenv("DB_PATH", dsn)
		}

		out := filepath.Join(dir, "out")
		var stdout, stderr strings.Builder
		if code := subcommands["backup"].run([]string{"--out", out}, strings.NewReader(""), &stdout, &stderr); code != 0 {
			t.Fatalf("backup: %d %q %q", code, stdout.String(), stderr.String())
		}
		path := strings.TrimSpace(stdout.String())
		if filepath.Dir(path) != out || !strings.HasPrefix(filepath.Base(path), "betterdesk-backup-") || !strings.HasSuffix(path, ".tar.gz") {
			t.Fatalf("archive path %q", path)
		}
		if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
			t.Fatalf("archive file: %v %v", info, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		files, err := backup.ExtractTarGz(raw, backup.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		manifest, _ := files.Get("manifest.json")
		dump, _ := files.Get("console/data.json")
		env, _ := files.Get("console/.env")
		pub, _ := files.Get("goserver/id_ed25519.pub")
		if !bytes.Contains(manifest, []byte(`"_format": "betterdesk-full-backup"`)) || !bytes.Contains(dump, []byte("backup_probe")) ||
			string(env) != "PORT=5000\n" || string(pub) != "PUBKEY" {
			t.Errorf("archive holds %v; manifest %s", files.Names(), manifest)
		}
		file, ok := files.Get("goserver/db_v2.sqlite3")
		if sdb.IsPostgres() == ok || (ok && !bytes.HasPrefix(file, []byte("SQLite format 3\x00"))) {
			t.Errorf("the server's SQLite file in the archive: %v (%d bytes)", ok, len(file))
		}

		if code := subcommands["backup"].run([]string{"--out", out, "extra"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
			t.Errorf("backup with an extra argument: %d, want 2", code)
		}
	})
}
