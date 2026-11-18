package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/unitronix/betterdesk-server/console/safepath"
)

// uploadNameAllowed checks a name below console/uploads/ of an archive
// before it is written: not absolute, no "." part, and the path must
// stay in dir after the links that exist on the way are followed (safepath
// also refuses a NUL). The old console only refused names with "..".
func uploadNameAllowed(dir, rel string) bool {
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return false
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == filepath.Separator }) {
		if part == "." {
			return false
		}
	}
	_, err := safepath.ResolveWithinRoot(filepath.Join(dir, rel), dir)
	return err == nil
}

// nodeErrno names the errno codes the way libuv did in Node's messages.
var nodeErrno = map[syscall.Errno][2]string{
	syscall.EACCES:       {"EACCES", "permission denied"},
	syscall.EPERM:        {"EPERM", "operation not permitted"},
	syscall.ENOENT:       {"ENOENT", "no such file or directory"},
	syscall.ENOTDIR:      {"ENOTDIR", "not a directory"},
	syscall.EISDIR:       {"EISDIR", "illegal operation on a directory"},
	syscall.EEXIST:       {"EEXIST", "file already exists"},
	syscall.ENOSPC:       {"ENOSPC", "no space left on device"},
	syscall.EROFS:        {"EROFS", "read-only file system"},
	syscall.ENAMETOOLONG: {"ENAMETOOLONG", "name too long"},
	syscall.ELOOP:        {"ELOOP", "too many symbolic links encountered"},
	syscall.EMFILE:       {"EMFILE", "too many open files"},
	syscall.EBUSY:        {"EBUSY", "resource busy or locked"},
	syscall.EIO:          {"EIO", "i/o error"},
	syscall.EINVAL:       {"EINVAL", "invalid argument"},
}

// NodeFSError gives a file system error the text Node gave it:
// "ENOENT: no such file or directory, open '/app/data/x'". A write that
// fails after the file opened names no path ("ENOSPC: no space left on
// device, write").
func NodeFSError(err error, syscallName, path string) error {
	var pe *fs.PathError
	op := syscallName
	if errors.As(err, &pe) && pe.Op == "write" {
		op, path = "write", ""
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	if syscallName == "mkdir" && errno == syscall.ENOTDIR {
		// MkdirAll reports a file where the folder should be as ENOTDIR;
		// Node's recursive mkdir called it EEXIST.
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			errno = syscall.EEXIST
		}
	}
	name, ok := nodeErrno[errno]
	if !ok {
		return err
	}
	msg := name[0] + ": " + name[1] + ", " + op
	if path != "" {
		msg += " '" + path + "'"
	}
	return errors.New(msg)
}

// ErrnoCode is the code Node gave the errno behind err ("EACCES"), and
// false when err carries none Node named.
func ErrnoCode(err error) (string, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	name, ok := nodeErrno[errno]
	return name[0], ok
}
