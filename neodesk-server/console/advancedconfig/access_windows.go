//go:build windows

package advancedconfig

import "os"

// writable is fs.access(path, W_OK) as Node does it on Windows: a folder
// can always be written, a file unless it is read-only, and a path that
// is not there cannot.
func writable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().Perm()&0o200 != 0
}
