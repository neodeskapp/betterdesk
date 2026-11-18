package servermgmt

import (
	"io/fs"
	"os"
)

// owner is 0 and 0 on Windows, as Node's stat gave it there.
func owner(fs.FileInfo) (uid, gid int64) { return 0, 0 }

// unlink removes a file.
func unlink(path string) error { return os.Remove(path) }

// rename moves a file or folder, replacing a file at the new path.
func rename(from, to string) error { return os.Rename(from, to) }
