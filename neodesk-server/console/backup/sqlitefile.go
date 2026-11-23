package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The SQLite file of an install (DB_PATH) is open while the console runs,
// with its last writes in the -wal file next to it. A backup copies it
// with VACUUM INTO, which gives one consistent file with what the WAL
// holds. A restore never writes over it: it checks the archive's file and
// leaves it next to it as DB_PATH + RestoredSuffix, and the next start
// puts that file in place before anything opens the database and keeps
// the old one (SwapInRestoredDatabase).

// RestoredSuffix is added to DB_PATH for the server database a restore
// left for the next start.
const RestoredSuffix = ".restored"

// BeforeRestoreSuffix is added to DB_PATH for the database the swap
// replaced, kept with its -wal and -shm files.
const BeforeRestoreSuffix = ".before-restore"

// copySQLite returns a copy of the open SQLite database, made with VACUUM
// INTO in a folder of its own below the system's temporary folder.
func copySQLite(ctx context.Context, db *store.DB) ([]byte, error) {
	dir, err := os.MkdirTemp("", "neodesk-backup-")
	if err != nil {
		return nil, fmt.Errorf("make a folder for the database copy: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "db.sqlite3")
	if _, err := db.Exec(ctx, `VACUUM INTO ?`, path); err != nil {
		return nil, fmt.Errorf("copy the database: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the database copy: %w", err)
	}
	return data, nil
}

// sqliteHeader starts every SQLite database file.
const sqliteHeader = "SQLite format 3\x00"

// stageServerDatabase leaves data as path (DB_PATH + RestoredSuffix) for
// the next start, once it passed the check: the file starts with SQLite's
// header, and PRAGMA integrity_check on it answers "ok". The data is
// written and checked in a folder of its own next to path and then
// renamed to path, so path is the whole checked file or what it was
// before.
func stageServerDatabase(ctx context.Context, path string, data []byte) error {
	if !bytes.HasPrefix(data, []byte(sqliteHeader)) {
		return errors.New("the archive's file is no SQLite database")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return NodeFSError(err, "mkdir", dir)
	}
	checkDir, err := os.MkdirTemp(dir, ".restore-check-")
	if err != nil {
		return fmt.Errorf("make a folder for the check: %w", err)
	}
	defer os.RemoveAll(checkDir)
	staged := filepath.Join(checkDir, "db.sqlite3")
	if err := os.WriteFile(staged, data, 0o600); err != nil {
		return NodeFSError(err, "open", staged)
	}
	if err := checkSQLite(ctx, staged); err != nil {
		return err
	}
	if err := os.Rename(staged, path); err != nil {
		return fmt.Errorf("put the checked file next to the database: %w", err)
	}
	return nil
}

// checkSQLite runs PRAGMA integrity_check on the SQLite file at path and
// returns an error unless it answers "ok".
func checkSQLite(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return fmt.Errorf("open the archive's database: %w", err)
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("check the archive's database: %w", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("check the archive's database: %w", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check the archive's database: %w", err)
	}
	if len(lines) != 1 || lines[0] != "ok" {
		if len(lines) > 3 {
			lines = lines[:3]
		}
		return fmt.Errorf("the archive's database fails the integrity check: %s", strings.Join(lines, "; "))
	}
	return nil
}

// SwapInRestoredDatabase puts the server database a restore left next to
// dbPath in place of dbPath. The old file and its -wal and -shm files are
// kept as dbPath + BeforeRestoreSuffix, in place of what an earlier swap
// kept there. It must run at the start, before anything opens dbPath.
// swapped is false when no restore left a file. When the swap fails,
// dbPath and its -wal and -shm files are as they were.
//
// Before the swap, the accounts of the restored file get their sign-in
// token times against the accounts of dbPath, with the time of the swap
// (endReplacedCredentialsInFile): a token issued before the start for
// credentials the restored file replaces does not work after it.
func SwapInRestoredDatabase(dbPath string) (swapped bool, err error) {
	restored := dbPath + RestoredSuffix
	info, err := os.Stat(restored)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("look for a restored database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is no file", filepath.Base(restored))
	}
	if err := endReplacedCredentialsInFile(context.Background(), restored, dbPath, time.Now().Unix()); err != nil {
		return false, err
	}
	keep := dbPath + BeforeRestoreSuffix
	// the -wal and -shm files of the file an earlier swap kept belong to
	// that file and would be read into the one kept now
	for _, side := range []string{keep + "-wal", keep + "-shm"} {
		if err := os.Remove(side); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("remove %s: %w", filepath.Base(side), err)
		}
	}
	type move struct{ from, to string }
	var done []move
	putBack := func() {
		for i := len(done) - 1; i >= 0; i-- {
			_ = os.Rename(done[i].to, done[i].from)
		}
	}
	for _, m := range []move{{dbPath + "-wal", keep + "-wal"}, {dbPath + "-shm", keep + "-shm"}, {dbPath, keep}} {
		err := os.Rename(m.from, m.to)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			putBack()
			return false, fmt.Errorf("keep %s as %s: %w", filepath.Base(m.from), filepath.Base(m.to), err)
		}
		done = append(done, m)
	}
	if err := os.Rename(restored, dbPath); err != nil {
		putBack()
		return false, fmt.Errorf("put the restored database in place: %w", err)
	}
	return true, nil
}

// endReplacedCredentialsInFile sets credentials_changed_at of the accounts
// in the SQLite file restored against those of the file current
// (endReplacedCredentials). current is read from a copy and stays as it
// is; when it cannot be read, every account of restored counts as
// replaced. A users table without the column gets it
// as the server's migration adds it. A file that is no SQLite database
// or has no users table is left alone.
func endReplacedCredentialsInFile(ctx context.Context, restored, current string, now int64) error {
	f, err := os.Open(restored)
	if err != nil {
		return fmt.Errorf("open the restored database: %w", err)
	}
	header := make([]byte, len(sqliteHeader))
	_, err = io.ReadFull(f, header)
	f.Close()
	if err != nil || string(header) != sqliteHeader {
		return nil
	}
	before := credentialsOfFile(ctx, current)

	db, err := sql.Open("sqlite", "file:"+restored)
	if err != nil {
		return fmt.Errorf("open the restored database: %w", err)
	}
	err = func() error {
		hasUsers, hasTime, err := usersTableColumns(ctx, db)
		if err != nil || !hasUsers {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if !hasTime {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE users ADD COLUMN `+credentialsChangedColumn+` INTEGER DEFAULT NULL`); err != nil {
				return err
			}
		}
		if err := endReplacedCredentials(ctx, tx.QueryContext, tx.ExecContext, before, now); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
		return err
	}()
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("the restored database's accounts: %w", err)
	}
	// what a -wal file still held would not go with the file
	if info, err := os.Stat(restored + "-wal"); err == nil && info.Size() > 0 {
		return fmt.Errorf("the restored database's accounts: %s keeps writes", filepath.Base(restored)+"-wal")
	}
	for _, side := range []string{restored + "-wal", restored + "-shm"} {
		if err := os.Remove(side); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", filepath.Base(side), err)
		}
	}
	return nil
}

// credentialsOfFile reads the accounts of the SQLite file at path from a
// copy of it and its -wal file, so that the file and its -wal and -shm
// files stay as they are; none when it has no users table or cannot be
// read.
func credentialsOfFile(ctx context.Context, path string) map[string]credential {
	none := map[string]credential{}
	if _, err := os.Stat(path); err != nil {
		return none
	}
	dir, err := os.MkdirTemp(filepath.Dir(path), ".restore-accounts-")
	if err != nil {
		return none
	}
	defer os.RemoveAll(dir)
	copyPath := filepath.Join(dir, "db.sqlite3")
	for _, side := range []string{"", "-wal"} {
		if err := copyFile(path+side, copyPath+side); err != nil && (side == "" || !errors.Is(err, fs.ErrNotExist)) {
			return none
		}
	}
	db, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		return none
	}
	defer db.Close()
	hasUsers, hasTime, err := usersTableColumns(ctx, db)
	if err != nil || !hasUsers {
		return none
	}
	before, err := readCredentials(ctx, db.QueryContext, hasTime)
	if err != nil {
		return none
	}
	return before
}

// copyFile copies the file from to the new file to.
func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// usersTableColumns says whether the SQLite database has a users table
// and whether it has credentials_changed_at.
func usersTableColumns(ctx context.Context, db *sql.DB) (hasUsers, hasTime bool, err error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info('users')`)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, false, err
		}
		hasUsers = true
		hasTime = hasTime || name == credentialsChangedColumn
	}
	return hasUsers, hasTime, rows.Err()
}
