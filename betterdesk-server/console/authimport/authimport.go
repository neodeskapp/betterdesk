// Package authimport moves the old console's separate SQLite file of
// accounts (auth.db) into the main database, once, at the start of the
// console (DECISIONS D1).
//
// Installs that began with the split layout keep the console's accounts,
// groups, folders, tickets, settings and audit log in auth.db next to the
// server's db_v2.sqlite3. The Go console reads only the main file, so at
// its first start it copies what auth.db holds into it, after taking a
// snapshot of both files, and then writes the marker the old console and
// the server already look for (betterdesk_migrations row
// sqlite_auth_consolidation_v1 with status complete). With the marker in
// place an old version started after a rollback uses the main file too.
// auth.db itself is only read.
//
// The import runs in one transaction on the main file: it applies
// completely or not at all. Accounts that exist in both files are matched
// by name and keep the main file's row and id, with the console's profile,
// two-factor settings and password hash (D5). Nothing is pruned (D6).
package authimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// MarkerName is the row of betterdesk_migrations that says auth.db has
// been moved into the main file.
const MarkerName = "sqlite_auth_consolidation_v1"

// Options say where the files are.
type Options struct {
	// AuthDBPath is the old console's auth.db: AUTH_DB_PATH, else auth.db
	// in DATA_DIR.
	AuthDBPath string
	// Mode is SQLITE_AUTH_DB_MODE: "consolidated" (any letter case) means
	// auth.db is not in use, and nothing is imported.
	Mode string
	// BackupDir is where the snapshots go: "backups" next to the main file
	// when empty.
	BackupDir string
	// Now is the clock of the snapshot names and the session ends;
	// time.Now when nil.
	Now func() time.Time
	// Logf writes the log lines; the console's log when nil.
	Logf func(format string, args ...any)
}

// FromEnv reads AUTH_DB_PATH and SQLITE_AUTH_DB_MODE the way the old
// console did, with dataDir as DATA_DIR.
func FromEnv(getenv func(string) string, dataDir string) Options {
	path := getenv("AUTH_DB_PATH")
	if path == "" {
		path = filepath.Join(dataDir, "auth.db")
	}
	return Options{AuthDBPath: path, Mode: getenv("SQLITE_AUTH_DB_MODE")}
}

// Report says what an import did. It holds no secret.
type Report struct {
	Source         string `json:"source"`
	Target         string `json:"target"`
	SourceSHA256   string `json:"source_sha256"`
	TargetBackup   string `json:"target_backup"`
	SourceBackup   string `json:"source_backup"`
	UsersMerged    int    `json:"users_merged"`
	UsersAdded     int    `json:"users_added"`
	PasswordsTaken int    `json:"passwords_taken"`
	// UserIDs maps every old console account id to its id in the main
	// file; the ones that changed are what a rollback by hand needs.
	UserIDs map[int64]int64 `json:"user_ids"`
	// GroupIDs maps old ids of user and device groups and strategies to
	// their ids in the main file, by table.
	GroupIDs map[string]map[int64]int64 `json:"group_ids"`
	// Rows counts the rows copied, by table.
	Rows map[string]int64 `json:"rows"`
	// Skipped counts rows left out because they point at an account or
	// row that no longer exists, by table.
	Skipped map[string]int64 `json:"skipped"`
	// Kept counts rows of auth.db whose key the main file already had: the
	// main file's row stays, except for groups and strategies, where the
	// console's row is written over it.
	Kept map[string]int64 `json:"kept"`
	// Unknown lists tables of auth.db the console's schema does not have;
	// they are not copied.
	Unknown []string `json:"unknown,omitempty"`
	// SessionsEnded is the number of signed-in sessions ended because
	// their account's id changed.
	SessionsEnded int64 `json:"sessions_ended"`
}

// Run imports auth.db into the database when it has to: on SQLite, when
// the file exists, the mode is not "consolidated" and the main file has
// no completion marker. It returns nil and no error when there was
// nothing to do. db must be migrated (store.Migrate) and nothing else may
// write the console's tables while it runs.
func Run(ctx context.Context, db *store.DB, opts Options) (*Report, error) {
	if db.IsPostgres() || strings.ToLower(opts.Mode) == "consolidated" || opts.AuthDBPath == "" {
		return nil, nil
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = func(format string, args ...any) { logging.Logf("console", format, args...) }
	}
	info, err := os.Stat(opts.AuthDBPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look at %s: %w", opts.AuthDBPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a folder, not the old console's database", opts.AuthDBPath)
	}
	var mainPath string
	if err := db.QueryRow(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&mainPath); err != nil {
		return nil, fmt.Errorf("find the main database file: %w", err)
	}
	source, err := filepath.Abs(opts.AuthDBPath)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", opts.AuthDBPath, err)
	}
	if samePath(source, mainPath) {
		return nil, nil
	}
	done, err := markerComplete(ctx, db)
	if err != nil {
		return nil, err
	}
	if done {
		return nil, nil
	}

	report := &Report{
		Source:   source,
		Target:   mainPath,
		UserIDs:  map[int64]int64{},
		GroupIDs: map[string]map[int64]int64{},
		Rows:     map[string]int64{},
		Skipped:  map[string]int64{},
		Kept:     map[string]int64{},
	}
	if report.SourceSHA256, err = fileSHA256(source); err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	if err := snapshot(ctx, opts, report); err != nil {
		return nil, err
	}
	if err := merge(ctx, opts, report); err != nil {
		return nil, fmt.Errorf("import %s into %s (nothing changed; snapshots in %s and %s): %w",
			source, mainPath, report.TargetBackup, report.SourceBackup, err)
	}
	opts.Logf("auth.db import: %s is now in %s: %d accounts merged, %d added, %d tables copied, %d sessions ended; snapshots %s and %s",
		source, mainPath, report.UsersMerged, report.UsersAdded, len(report.Rows), report.SessionsEnded, report.TargetBackup, report.SourceBackup)
	for _, table := range sortedTables(report.Skipped) {
		if n := report.Skipped[table]; n > 0 {
			opts.Logf("WARN: auth.db import: %d rows of %s left out: they point at an account or row that no longer exists", n, table)
		}
	}
	if len(report.Unknown) > 0 {
		opts.Logf("WARN: auth.db import: tables not copied, the console does not use them: %s", strings.Join(report.Unknown, ", "))
	}
	return report, nil
}

// markerComplete reports whether the main file says auth.db was moved.
func markerComplete(ctx context.Context, db *store.DB) (bool, error) {
	var n int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'betterdesk_migrations'`).Scan(&n); err != nil {
		return false, fmt.Errorf("look for the consolidation marker: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM betterdesk_migrations WHERE name = ? AND status = 'complete'`, MarkerName).Scan(&n); err != nil {
		return false, fmt.Errorf("read the consolidation marker: %w", err)
	}
	return n > 0, nil
}

// snapshot copies both files with VACUUM INTO, under the names the
// server's -migrate-sqlite-auth used.
func snapshot(ctx context.Context, opts Options, report *Report) error {
	dir := opts.BackupDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(report.Target), "backups")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the backup folder %s: %w", dir, err)
	}
	stamp := opts.Now().UTC().Format("20060102T150405Z")
	report.TargetBackup = filepath.Join(dir, filepath.Base(report.Target)+".pre-auth-consolidation-"+stamp)
	report.SourceBackup = filepath.Join(dir, filepath.Base(report.Source)+".legacy-auth-"+stamp)
	for _, c := range []struct{ from, to string }{{report.Target, report.TargetBackup}, {report.Source, report.SourceBackup}} {
		if err := vacuumInto(ctx, c.from, c.to); err != nil {
			return fmt.Errorf("snapshot %s: %w", c.from, err)
		}
	}
	return nil
}

func vacuumInto(ctx context.Context, from, to string) error {
	if _, err := os.Stat(to); err == nil {
		return fmt.Errorf("%s exists already", to)
	}
	handle, err := sql.Open("sqlite", "file:"+from+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer handle.Close()
	if _, err := handle.ExecContext(ctx, `VACUUM INTO ?`, to); err != nil {
		return err
	}
	var result string
	check, err := sql.Open("sqlite", "file:"+to+"?mode=ro")
	if err != nil {
		return err
	}
	defer check.Close()
	if err := check.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&result); err != nil {
		return fmt.Errorf("check the snapshot: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("the snapshot %s fails its check: %s", to, result)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
