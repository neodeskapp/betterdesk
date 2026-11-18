//go:build windows

package generator

import "syscall"

var errnoNames = map[syscall.Errno]string{
	syscall.ENOENT:  "ENOENT",
	syscall.EACCES:  "EACCES",
	syscall.EEXIST:  "EEXIST",
	syscall.ENOTDIR: "ENOTDIR",
	syscall.EISDIR:  "EISDIR",
	syscall.EPERM:   "EPERM",
	syscall.EXDEV:   "EXDEV",
	syscall.ENOSPC:  "ENOSPC",
	syscall.EBUSY:   "EBUSY",
	syscall.ELOOP:   "ELOOP",
}
