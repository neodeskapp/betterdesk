package serverinfo

import (
	"errors"
	"io/fs"
	"syscall"
)

// FileError is a failed file operation with a message in the form Node
// uses, such as "ENOENT: no such file or directory, open '/etc/x'". Routes
// put the message of an error into their answer, and clients have seen
// this form. The cause is still there for errors.Is.
type FileError struct {
	// Code is Node's name for the cause, "ENOENT" for example. It is empty
	// when the cause has no such name; the message is then the cause's.
	Code string
	msg  string
	err  error
}

func (e *FileError) Error() string { return e.msg }
func (e *FileError) Unwrap() error { return e.err }

// nodeError wraps the error of a file operation. call is the system call
// Node named, and target what Node put after it: a path, or "from' -> 'to"
// for a rename.
func nodeError(err error, call, target string) error {
	if err == nil {
		return nil
	}
	var already *FileError
	if errors.As(err, &already) {
		return err
	}
	code, text := "", ""
	var errno syscall.Errno
	switch {
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.As(err, &errno) && errno == syscall.EPERM:
		code, text = "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	case errors.Is(err, syscall.EISDIR):
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.ENOTDIR):
		code, text = "ENOTDIR", "not a directory"
	case errors.Is(err, syscall.EROFS):
		code, text = "EROFS", "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		code, text = "ENOSPC", "no space left on device"
	default:
		return &FileError{msg: err.Error(), err: err}
	}
	return &FileError{Code: code, msg: code + ": " + text + ", " + call + " '" + target + "'", err: err}
}

// nodeReadError wraps the error of reading a file. Node opens a directory
// without complaint and fails on reading it, with a message that has no
// path.
func nodeReadError(err error, path string) error {
	if err != nil && errors.Is(err, syscall.EISDIR) {
		return &FileError{Code: "EISDIR", msg: "EISDIR: illegal operation on a directory, read", err: err}
	}
	return nodeError(err, "open", path)
}
