//go:build !windows

package updater

import (
	"os"
	"syscall"
)

// freeSpace is the space an unprivileged user may still use at path
// (bavail times the block size, as statfs reports it).
func freeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// writable is access(path, W_OK).
func writable(path string) error {
	return syscall.Access(path, 2)
}

// readable is access(path, R_OK).
func readable(path string) error {
	return syscall.Access(path, 4)
}

// syncDir flushes a folder's entries to disk, so a rename survives a
// power cut.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
