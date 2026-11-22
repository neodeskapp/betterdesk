package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/unitronix/betterdesk-server/console/backup"
	consoleconfig "github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/settingsbackup"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/internal/envvar"
)

const backupUsage = "backup [--out DIR] [--db DSN]                 write the console's full backup, also while the server runs"

// backupTimeout bounds a backup command: the dump, the SQLite copy and
// the files.
const backupTimeout = 30 * time.Minute

// runBackup is betterdesk-server backup: the archive the console's full
// backup makes (settings, Backup), written into --out (default
// scheduled-backups in DATA_DIR) with mode 0600, and its path printed on
// standard output. The database is --db, else DATABASE_URL (DB_URL and
// DB_TYPE as the server reads them), else DB_PATH; the other paths come
// from the environment as the console reads it. Exit status 0 when the
// archive is written, 1 when it is not, 2 for wrong arguments.
func runBackup(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "the folder the archive goes into (default: scheduled-backups in DATA_DIR)")
	dsn := flags.String("db", "", "the server's database, a SQLite file or a postgres:// URL (default: DATABASE_URL, else DB_PATH)")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 {
		fmt.Fprintln(stderr, "Usage: betterdesk-server "+backupUsage)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()
	path, err := writeBackup(ctx, os.Getenv, *dsn, *out)
	if err != nil {
		fmt.Fprintf(stderr, "betterdesk-server backup: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

// writeBackup makes the console's full backup of the install getenv
// describes and writes it into out. A running server keeps running: the
// dump reads every table in one snapshot and the SQLite file is copied
// with VACUUM INTO, both consistent while the server writes.
func writeBackup(ctx context.Context, getenv func(string) string, dsn, out string) (string, error) {
	cfg, err := consoleconfig.Load(getenv)
	if err != nil {
		return "", fmt.Errorf("configuration: %w", err)
	}
	if dsn == "" {
		dsn = envvar.DatabaseFrom(getenv).DSN
	}
	if dsn == "" {
		dsn = cfg.DBPath
	}
	sdb, closeDB, err := openBackupStore(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer closeDB()

	opts := settingsbackup.FileOptions(cfg, dsn)
	opts.Store = sdb
	archive, name, err := backup.New(opts).CreateFullBackup(ctx)
	if err != nil {
		return "", fmt.Errorf("make the archive: %w", err)
	}
	if out == "" {
		out = filepath.Join(cfg.DataDir, "scheduled-backups")
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return "", fmt.Errorf("make the folder %s: %w", out, err)
	}
	path := filepath.Join(out, name)
	tmp, err := os.CreateTemp(out, ".backup-*")
	if err != nil {
		return "", fmt.Errorf("write the archive: %w", err)
	}
	_, err = tmp.Write(archive)
	err = errors.Join(err, tmp.Chmod(0o600), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("write the archive %s: %w", path, err)
	}
	return path, nil
}

// openBackupStore opens the server's database for the console's store:
// PostgreSQL for a postgres:// URL, else the SQLite file, which must exist.
func openBackupStore(ctx context.Context, dsn string) (*store.DB, func(), error) {
	lower := strings.ToLower(dsn)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err != nil {
			if pool != nil {
				pool.Close()
			}
			return nil, nil, fmt.Errorf("open PostgreSQL: %w", err)
		}
		sdb := store.OpenPostgres(pool)
		return sdb, func() { _ = sdb.Close(); pool.Close() }, nil
	}
	if info, err := os.Stat(dsn); err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("no SQLite database at %s", dsn)
	}
	sdb, err := store.OpenSQLite(dsn)
	if err != nil {
		return nil, nil, err
	}
	return sdb, func() { _ = sdb.Close() }, nil
}
