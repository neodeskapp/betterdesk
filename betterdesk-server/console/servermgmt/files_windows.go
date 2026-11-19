package servermgmt

import (
	"io/fs"
	"os"
)

// openForWrite opens a file for a write that replaces what it holds,
// creating it.
func openForWrite(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
}

// owner is 0 and 0 on Windows, as Node's stat gave it there.
func owner(fs.FileInfo) (uid, gid int64) { return 0, 0 }

// unlink removes a file.
func unlink(path string) error { return os.Remove(path) }

// rename moves a file or folder, replacing a file at the new path.
func rename(from, to string) error { return os.Rename(from, to) }
