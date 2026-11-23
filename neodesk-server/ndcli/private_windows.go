//go:build windows

package ndcli

import "os"

// checkPrivate on Windows: the file lives in the user's own profile
// folder (%AppData%), which other users cannot read by default; the mode
// bits mean nothing there.
func checkPrivate(string, os.FileInfo) error { return nil }
