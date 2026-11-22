//go:build windows

package updater

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeSpace is the space the caller may still use at path.
func freeSpace(path string) (int64, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	ok, _, callErr := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return 0, callErr
	}
	return int64(available), nil
}

// writable checks write access the way Windows allows it: a folder by
// creating and removing a file in it, a file by opening it for writing.
func writable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	}
	f, err := os.CreateTemp(path, ".bd-write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	if filepath.Dir(name) != filepath.Clean(path) {
		return errors.New("unexpected temporary file")
	}
	return os.Remove(name)
}

// readable checks that path can be opened.
func readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// syncDir does nothing: Windows has no flush of a folder.
func syncDir(string) error { return nil }
