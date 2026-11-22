//go:build !windows

package advancedconfig

import "syscall"

// writable is fs.access(path, W_OK): whether this process may write the
// file or folder.
func writable(path string) bool {
	const wOK = 2
	return syscall.Access(path, wOK) == nil
}
