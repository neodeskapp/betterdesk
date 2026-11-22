//go:build windows

package generator

import (
	"syscall"
	"unsafe"
)

var getDiskFreeSpaceEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeBytes is the space below dir that the user may use.
func freeBytes(dir string) (int64, error) {
	path, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	r, _, callErr := getDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if r == 0 {
		return 0, callErr
	}
	return int64(available), nil
}
