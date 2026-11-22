//go:build !windows

package generator

import "syscall"

// freeBytes is the space below dir that an unprivileged user may use
// (statfs bavail times bsize).
func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
