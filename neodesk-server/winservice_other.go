//go:build !windows

package main

// runAsWindowsService does nothing outside Windows.
func runAsWindowsService(func(stop <-chan struct{}) int) (int, bool) {
	return 0, false
}
