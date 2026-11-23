//go:build !windows

package ndcli

import (
	"fmt"
	"os"
	"syscall"
)

// checkPrivate refuses a file that group or others may read or write, or
// that another user owns.
func checkPrivate(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %o; run: chmod 600 %s", ErrConfigOpen, path, info.Mode().Perm(), path)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s belongs to another user", ErrConfigOpen, path)
	}
	return nil
}
