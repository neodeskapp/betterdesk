package console

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// copyOldFonts fills the folder of installed fonts (DATA_DIR/fonts) once
// from the folder the Node console installed them in
// (CONSOLE_DIR/public/fonts), so an upgraded install keeps its fonts
// (DECISIONS A25). It copies: the old folder stays as it is, so the Node
// console of a rollback still has its fonts. Once the new folder exists
// nothing is copied again, so a font removed in the console stays
// removed. Without an old folder the new one is made empty. The copy is
// made beside the new folder and renamed into place, so a copy that
// fails halfway leaves no new folder and the next start tries again.
// Files keep their modification times, so they are served with the same
// ETag as before; what is not a folder or a regular file is left out.
func copyOldFonts(from, to string) error {
	if to == "" {
		return nil
	}
	if _, err := os.Stat(to); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return fmt.Errorf("create the folder of %s: %w", to, err)
	}
	if info, err := os.Stat(from); from == "" || err != nil || !info.IsDir() {
		if err := os.Mkdir(to, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create %s: %w", to, err)
		}
		return nil
	}
	tmp, err := os.MkdirTemp(filepath.Dir(to), "."+filepath.Base(to)+"-copy-")
	if err != nil {
		return fmt.Errorf("copy the fonts of %s: %w", from, err)
	}
	if err := copyTree(from, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copy the fonts of %s: %w", from, err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copy the fonts of %s: %w", from, err)
	}
	if err := os.Rename(tmp, to); err != nil {
		_ = os.RemoveAll(tmp)
		return fmt.Errorf("copy the fonts of %s to %s: %w", from, to, err)
	}
	return nil
}

// copyTree copies the folders and regular files below from into the
// folder to, with their modes, and the files with their modification
// times.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, name)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(target, info.Mode().Perm()|0o700)
		case d.Type().IsRegular():
			if err := copyFile(name, target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		}
		return nil
	})
}

func copyFile(from, to string, mode fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
