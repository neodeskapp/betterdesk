package serverinfo

import (
	"io/fs"
	"os"
)

// FS is the file access the services use. OSFS is the real one; tests wrap
// it to make a call fail.
type FS interface {
	// Exists reports whether path can be reached: a file, a directory or
	// anything else.
	Exists(path string) bool
	ReadFile(path string) ([]byte, error)
	// WriteFile replaces the content of path. perm is the mode of a file
	// it creates; an existing file keeps its mode.
	WriteFile(path string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Rename(oldPath, newPath string) error
	Chmod(path string, mode fs.FileMode) error
}

// OSFS is FS on the machine's file system.
type OSFS struct{}

// Exists implements FS.
func (OSFS) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ReadFile implements FS.
func (OSFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// WriteFile implements FS.
func (OSFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// MkdirAll implements FS.
func (OSFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Rename implements FS.
func (OSFS) Rename(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// Chmod implements FS.
func (OSFS) Chmod(path string, mode fs.FileMode) error { return os.Chmod(path, mode) }

// readText reads a file as UTF-8 text. See decodeUTF8 for bytes that are
// not.
func readText(fsys FS, path string) (string, error) {
	raw, err := fsys.ReadFile(path)
	if err != nil {
		return "", err
	}
	return decodeUTF8(raw), nil
}
