package db

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// MoveAsideRustDeskDatabase moves the SQLite file at path, with its -wal
// and -shm files, to path.rustdesk-<time> when RustDesk's hbbs made it: it
// has hbbs's table peer (with the column guid) and no table peers, so this
// server never opened it. The console keeps its own table peer, which a
// table of that name in hbbs's shape would stand in for. It returns the
// new path, or "" when the file stays: another database, an empty or
// missing file, or a PostgreSQL address.
func MoveAsideRustDeskDatabase(path string, now time.Time) (string, error) {
	if isPostgresDSN(path) {
		return "", nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("look at the database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil
	}
	hbbs, err := isRustDeskDatabase(path)
	if err != nil || !hbbs {
		return "", err
	}

	aside := path + ".rustdesk-" + now.UTC().Format("20060102-150405")
	type move struct{ from, to string }
	var done []move
	for _, m := range []move{{path + "-wal", aside + "-wal"}, {path + "-shm", aside + "-shm"}, {path, aside}} {
		err := os.Rename(m.from, m.to)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			for i := len(done) - 1; i >= 0; i-- {
				_ = os.Rename(done[i].to, done[i].from)
			}
			return "", fmt.Errorf("move %s to %s: %w", filepath.Base(m.from), filepath.Base(m.to), err)
		}
		done = append(done, m)
	}
	return aside, nil
}

// isRustDeskDatabase reports whether the SQLite file has hbbs's peer table
// and not the server's peers table.
func isRustDeskDatabase(path string) (bool, error) {
	conn, err := openSQLiteReadOnly(path)
	if err != nil {
		return false, fmt.Errorf("open the database to look at it: %w", err)
	}
	defer conn.Close()
	var peers, guid int
	err = conn.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'peers'),
		(SELECT COUNT(*) FROM pragma_table_info('peer') WHERE name = 'guid')`).Scan(&peers, &guid)
	if err != nil {
		return false, fmt.Errorf("read the tables of the database: %w", err)
	}
	return peers == 0 && guid == 1, nil
}
