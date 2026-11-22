package servermgmt

import (
	"errors"
	"io/fs"
	"syscall"
)

// uvErrors are libuv's names and texts for the system errors, as Node put
// them into the messages of failed file operations.
var uvErrors = map[syscall.Errno][2]string{
	syscall.E2BIG:        {"E2BIG", "argument list too long"},
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EAGAIN:       {"EAGAIN", "resource temporarily unavailable"},
	syscall.EBADF:        {"EBADF", "bad file descriptor"},
	syscall.EBUSY:        {"EBUSY", "resource busy or locked"},
	syscall.EEXIST:       {"EEXIST", "file already exists"},
	syscall.EFAULT:       {"EFAULT", "bad address in system call argument"},
	syscall.EFBIG:        {"EFBIG", "file too large"},
	syscall.EINTR:        {"EINTR", "interrupted system call"},
	syscall.EINVAL:       {"EINVAL", "invalid argument"},
	syscall.EIO:          {"EIO", "i/o error"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.EMFILE:       {"EMFILE", "too many open files"},
	syscall.EMLINK:       {"EMLINK", "too many links"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
	syscall.ENFILE:       {"ENFILE", "file table overflow"},
	syscall.ENODEV:       {"ENODEV", "no such device"},
	syscall.ENOENT:       {"ENOENT", "no such file or directory"},
	syscall.ENOMEM:       {"ENOMEM", "not enough memory"},
	syscall.ENOSPC:       {"ENOSPC", "no space left on device"},
	syscall.ENOSYS:       {"ENOSYS", "function not implemented"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.ENOTEMPTY:    {"ENOTEMPTY", "directory not empty"},
	syscall.ENOTSUP:      {"ENOTSUP", "operation not supported on socket"},
	syscall.ENXIO:        {"ENXIO", "no such device or address"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.EPIPE:        {"EPIPE", "broken pipe"},
	syscall.EROFS:        {"EROFS", "read-only file system"},
	syscall.ESPIPE:       {"ESPIPE", "invalid seek"},
	syscall.ETXTBSY:      {"ETXTBSY", "text file is busy"},
	syscall.EXDEV:        {"EXDEV", "cross-device link not permitted"},
}

// fileError is a failed file operation worded as Node worded it:
// "ENOENT: no such file or directory, stat '/opt/x'", with a second path
// for a rename ("rename '/a' -> '/b'") and none for a read or write of an
// open file.
func fileError(err error, call, path string, dest ...string) error {
	code, text, ok := uvError(err)
	if !ok {
		return err
	}
	msg := code + ": " + text + ", " + call
	if path != "" {
		msg += " '" + path + "'"
	}
	if len(dest) > 0 {
		msg += " -> '" + dest[0] + "'"
	}
	return errors.New(msg)
}

// uvError finds libuv's name and text for the system error behind err.
// Errors of other kinds, and on Windows the system's own codes, are named
// by their class.
func uvError(err error) (code, text string, ok bool) {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if name, known := uvErrors[errno]; known {
			return name[0], name[1], true
		}
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "ENOENT", "no such file or directory", true
	case errors.Is(err, fs.ErrExist):
		return "EEXIST", "file already exists", true
	case errors.Is(err, fs.ErrPermission):
		return "EPERM", "operation not permitted", true
	}
	return "", "", false
}
