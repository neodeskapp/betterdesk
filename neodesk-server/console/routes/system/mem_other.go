//go:build !linux

package system

// totalMemory is only asked for on Linux, where ps reports memory as a
// share of it.
func totalMemory() uint64 { return 0 }

// residentBytes is not known here; the caller falls back to what the Go
// runtime has taken from the system.
func residentBytes() uint64 { return 0 }
