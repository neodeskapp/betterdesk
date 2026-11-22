package branding

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// resolveChildPath returns the path of one entry of a folder. The name
// must be a single path element, and the result must lie in the folder,
// also after the symbolic links of what exists are resolved.
func resolveChildPath(root, name string) string {
	if name == "" || strings.ContainsAny(name, "\x00/\\") || name == "." || name == ".." {
		throw("Invalid path segment")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	target := filepath.Join(abs, name)
	rel, err := filepath.Rel(comparablePath(abs), comparablePath(target))
	if err != nil || (rel != "." && (strings.HasPrefix(rel, "..") || filepath.IsAbs(rel))) {
		throw("Path outside allowed directory")
	}
	return target
}

// comparablePath resolves the symbolic links of the part of a path that
// exists and keeps the rest as written.
func comparablePath(p string) string {
	var rest []string
	existing := p
	for !exists(existing) {
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		rest = append([]string{filepath.Base(existing)}, rest...)
		existing = parent
	}
	if exists(existing) {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			p = filepath.Join(append([]string{real}, rest...)...)
		}
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(strings.TrimPrefix(p, `\\?\`))
	}
	return p
}

// exists is fs.existsSync: whether the path can be reached, following
// symbolic links.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// fsError words a file error the way Node does, because some answers
// carry the text: "ENOTDIR: not a directory, scandir '/x'".
func fsError(err error, call, path string) error {
	if err == nil {
		return nil
	}
	code, text := "", ""
	switch {
	case errors.Is(err, syscall.EISDIR):
		code, text = "EISDIR", "illegal operation on a directory"
	case errors.Is(err, syscall.ENOTDIR):
		code, text = "ENOTDIR", "not a directory"
	case errors.Is(err, fs.ErrNotExist):
		code, text = "ENOENT", "no such file or directory"
	case errors.Is(err, syscall.EROFS):
		code, text = "EROFS", "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		code, text = "ENOSPC", "no space left on device"
	case errors.Is(err, syscall.ENOTEMPTY):
		code, text = "ENOTEMPTY", "directory not empty"
	case errors.Is(err, syscall.EPERM):
		code, text = "EPERM", "operation not permitted"
	case errors.Is(err, fs.ErrPermission):
		code, text = "EACCES", "permission denied"
	case errors.Is(err, fs.ErrExist):
		code, text = "EEXIST", "file already exists"
	default:
		return &Error{Name: "Error", Message: err.Error(), Err: err}
	}
	return &Error{Name: "Error", Message: code + ": " + text + ", " + call + " '" + path + "'", Err: err}
}
