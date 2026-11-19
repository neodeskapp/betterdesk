package backup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The SQLite file of an install (DB_PATH) is open while the console runs,
// with its last writes in the -wal file next to it. A backup copies it
// with VACUUM INTO, which gives one consistent file with what the WAL
// holds. A restore never writes over it: it leaves the archive's file
// next to it as DB_PATH + RestoredSuffix, and the next start puts that
// file in place before anything opens the database
// (SwapInRestoredDatabase).

// RestoredSuffix is added to DB_PATH for the server database a restore
// left for the next start.
const RestoredSuffix = ".restored"

// copySQLite returns a copy of the open SQLite database, made with VACUUM
// INTO in a folder of its own below the system's temporary folder.
func copySQLite(ctx context.Context, db *store.DB) ([]byte, error) {
	dir, err := os.MkdirTemp("", "betterdesk-backup-")
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

// SwapInRestoredDatabase puts the server database a restore left next to
// dbPath in place of dbPath, and takes away dbPath's -wal and -shm files,
// whose content belongs to the old file. It must run at the start, before
// anything opens dbPath. swapped is false when no restore left a file.
// When the swap fails, dbPath and its -wal and -shm files are as they
// were.
func SwapInRestoredDatabase(dbPath string) (swapped bool, err error) {
	restored := dbPath + RestoredSuffix
	if _, err := os.Stat(restored); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("look for a restored database: %w", err)
	}
	var moved []string
	putBack := func() {
		for _, side := range moved {
			_ = os.Rename(side+RestoredSuffix+".old", side)
		}
	}
	for _, side := range []string{dbPath + "-wal", dbPath + "-shm"} {
		err := os.Rename(side, side+RestoredSuffix+".old")
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			putBack()
			return false, fmt.Errorf("move %s aside: %w", filepath.Base(side), err)
		}
		moved = append(moved, side)
	}
	if err := os.Rename(restored, dbPath); err != nil {
		putBack()
		return false, fmt.Errorf("put the restored database in place: %w", err)
	}
	for _, side := range moved {
		_ = os.Remove(side + RestoredSuffix + ".old")
	}
	return true, nil
}
