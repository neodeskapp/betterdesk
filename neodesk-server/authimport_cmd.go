package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unitronix/betterdesk-server/console/authimport"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/db"
)

// authImportOutcome is what -migrate-sqlite-auth prints: the import's
// report, or that the main file had the marker already.
type authImportOutcome struct {
	DryRun          bool `json:"dry_run"`
	AlreadyComplete bool `json:"already_complete"`
	*authimport.Report
}

// migrateSQLiteAuth is -migrate-sqlite-auth: it moves the old console's
// auth.db into the SQLite database with the import the console runs at
// its first start (console/authimport), snapshots in backupDir. The dry
// run imports into a copy of the database in a temporary folder and leaves
// both files as they are.
func migrateSQLiteAuth(ctx context.Context, dbPath, authDBPath, backupDir string, dryRun bool) (*authImportOutcome, error) {
	lower := strings.ToLower(dbPath)
	if strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://") {
		return nil, errors.New("the database is PostgreSQL: there is no auth.db to move into it")
	}
	if !isFile(authDBPath) {
		return nil, fmt.Errorf("no auth.db at %q", authDBPath)
	}
	target := dbPath
	if dryRun {
		dir, err := os.MkdirTemp("", "betterdesk-auth-import-")
		if err != nil {
			return nil, fmt.Errorf("dry run: %w", err)
		}
		defer os.RemoveAll(dir)
		target, backupDir = filepath.Join(dir, filepath.Base(dbPath)), filepath.Join(dir, "backups")
		if err := copySQLite(ctx, dbPath, target); err != nil {
			return nil, fmt.Errorf("dry run: copy %s: %w", dbPath, err)
		}
	}

	database, err := db.OpenSQLite(target)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		return nil, fmt.Errorf("server schema: %w", err)
	}
	sdb, err := store.OpenSQLite(target)
	if err != nil {
		return nil, fmt.Errorf("console database: %w", err)
	}
	defer sdb.Close()
	if err := sdb.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("console schema: %w", err)
	}
	report, err := authimport.Run(ctx, sdb, authimport.Options{AuthDBPath: authDBPath, BackupDir: backupDir})
	if err != nil {
		return nil, err
	}
	return &authImportOutcome{DryRun: dryRun, AlreadyComplete: report == nil, Report: report}, nil
}

// copySQLite writes a consistent copy of a SQLite file to a new path.
func copySQLite(ctx context.Context, from, to string) error {
	handle, err := sql.Open("sqlite", "file:"+from+"?mode=ro")
	if err != nil {
		return err
	}
	defer handle.Close()
	_, err = handle.ExecContext(ctx, `VACUUM INTO ?`, to)
	return err
}
