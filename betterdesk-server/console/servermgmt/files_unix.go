//go:build unix

package servermgmt

import (
	"io/fs"
	"syscall"
)

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
