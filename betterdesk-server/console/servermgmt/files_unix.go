//go:build unix

package servermgmt

import (
	"io/fs"
	"os"
	"syscall"
)

// openForWrite opens a file for a write that replaces what it holds,
// creating it. A path that became a link after the checks fails (ELOOP)
// instead of being followed, and one that became a FIFO fails (ENXIO)
// instead of waiting for a reader.
func openForWrite(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o666)
}

// owner is the user and group id of a file.
func owner(info fs.FileInfo) (uid, gid int64) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Uid), int64(st.Gid)
	}
	return 0, 0
}

// unlink removes a name that is not a folder (fs.unlinkSync).
func unlink(path string) error { return syscall.Unlink(path) }

// rename is rename(2), which replaces an empty folder or a file at the
// new path, unlike os.Rename, which refuses any folder there.
func rename(from, to string) error { return syscall.Rename(from, to) }
