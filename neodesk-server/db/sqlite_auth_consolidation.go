package db

// The marker that says the old console's auth.db has been moved into the
// selected SQLite database, and the rollback to a snapshot taken before
// the move. The move itself is console/authimport, which the console runs
// at its first start and -migrate-sqlite-auth runs on demand.

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const sqliteAuthConsolidationName = "sqlite_auth_consolidation_v1"

// RollbackSQLiteAuth replaces DBPath with a known-good snapshot taken
// before the move of auth.db. It never deletes the failed active DB.
func RollbackSQLiteAuth(dbPath, backupPath string) error {
	if isPostgresDSN(dbPath) {
		return errors.New("sqlite auth rollback is not applicable to PostgreSQL")
	}
	targetPath, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	sourcePath, err := filepath.Abs(backupPath)
	if err != nil {
		return err
	}
	if err := validateSQLiteFile(sourcePath); err != nil {
		return fmt.Errorf("rollback snapshot validation failed: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	failedPath := targetPath + ".failed-auth-consolidation-" + stamp
	if err := os.Rename(targetPath, failedPath); err != nil {
		return fmt.Errorf("stage failed target database: %w", err)
	}
	if err := sqliteSnapshot(sourcePath, targetPath); err != nil {
		_ = os.Rename(failedPath, targetPath)
		return fmt.Errorf("restore snapshot: %w", err)
	}
	return nil
}

// SQLiteAuthConsolidated reports whether the selected SQLite database already
// contains the successful, versioned consolidation marker.
func SQLiteAuthConsolidated(dbPath string) (bool, error) {
	if isPostgresDSN(dbPath) {
		return true, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	db, err := openSQLiteReadOnly(dbPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var count int
	err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='betterdesk_migrations'`).Scan(&count)
	if err != nil || count == 0 {
		return false, nil
	}
	var marker int
	err = db.QueryRow(`SELECT COUNT(*) FROM betterdesk_migrations WHERE name = ? AND status = 'complete'`, sqliteAuthConsolidationName).Scan(&marker)
	if err != nil {
		return false, nil
	}
	return marker > 0, nil
}

func validateSQLiteFile(path string) error {
	db, err := openSQLiteReadOnly(path)
	if err != nil {
		return err
	}
	defer db.Close()
	return validateSQLiteDB(db)
}

func validateSQLiteDB(db *sql.DB) error {
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity_check: %s", integrity)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("foreign_key_check returned violations")
	}
	return rows.Err()
}

func sqliteSnapshot(sourcePath, destinationPath string) error {
	if err := os.RemoveAll(destinationPath); err != nil {
		return err
	}
	db, err := openSQLiteReadWrite(sourcePath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := db.Exec(`VACUUM INTO ?`, destinationPath); err != nil {
		return err
	}
	if err := validateSQLiteFile(destinationPath); err != nil {
		return err
	}
	return nil
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000&_foreign_keys=ON", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func openSQLiteReadWrite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func isPostgresDSN(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "postgres://") || strings.HasPrefix(value, "postgresql://")
}
