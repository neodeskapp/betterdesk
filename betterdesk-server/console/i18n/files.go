package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// The bundle reads its files from the folder Dir on disk, or from FS when
// FS is set. A path is Dir joined with a file name either way; in FS the
// file name is looked up at the top, where every language file is. The
// files in FS cannot be written: a write fails as it does on a read-only
// file system.

func (b *Bundle) dirExists() bool {
	var err error
	if b.FS != nil {
		_, err = fs.Stat(b.FS, ".")
	} else {
		_, err = os.Stat(b.Dir)
	}
	return err == nil
}

func (b *Bundle) stat(path string) error {
	var err error
	if b.FS != nil {
		_, err = fs.Stat(b.FS, filepath.Base(path))
	} else {
		_, err = os.Stat(path)
	}
	return err
}

func (b *Bundle) readFile(path string) ([]byte, error) {
	if b.FS != nil {
		return fs.ReadFile(b.FS, filepath.Base(path))
	}
	return os.ReadFile(path)
}

func (b *Bundle) readDir() ([]fs.DirEntry, error) {
	if b.FS != nil {
		return fs.ReadDir(b.FS, ".")
	}
	return os.ReadDir(b.Dir)
}

func (b *Bundle) writeFile(path string, data []byte) error {
	if b.FS != nil {
		return &fs.PathError{Op: "open", Path: path, Err: syscall.EROFS}
	}
	return os.WriteFile(path, data, 0o666)
}

// remove deletes a language file and refuses a directory, as unlink does.
func (b *Bundle) remove(path string) error {
	if b.FS != nil {
		return &fs.PathError{Op: "unlink", Path: path, Err: syscall.EROFS}
	}
	if info, err := os.Lstat(path); err == nil && info.IsDir() {
		return &fs.PathError{Op: "unlink", Path: path, Err: syscall.EISDIR}
	}
	return os.Remove(path)
}
