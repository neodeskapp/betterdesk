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
