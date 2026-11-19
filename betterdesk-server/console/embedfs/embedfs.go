// Package embedfs holds folders that are built into the server binary
// (go:embed) together with the modification times their files had on
// disk, so a file server answers for an embedded file as it did for the
// file on disk: the same Last-Modified and the same ETag, which both come
// from the time.
//
// go:embed cannot reach outside the module and keeps no times, so the
// build copies a folder into the module first with Copy. Copy writes the
// time of every file into StampFile in the copy. The folder in the module
// holds Placeholder, a committed file that lets go:embed compile before
// anything was copied in; without a copy the folder is empty. A folder
// that is committed inside the module is embedded where it is: Stamp
// writes its StampFile, and without one its files have the fallback time.
//
// StampFile and Placeholder at the top of the folder are not part of what
// an FS shows. Every other name is, also names that start with "." or "_":
// embed such a folder with the "all:" prefix.
package embedfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

const (
	// StampFile is where Copy writes the modification times: a JSON
	// object of each file's slash-separated path and its time in
	// nanoseconds since 1970.
	StampFile = ".embedfs-times.json"
	// Placeholder is the committed file of an embedded folder. Copy
	// leaves it in place.
	Placeholder = ".gitkeep"
)

// FS is an embedded folder. It is an fs.FS, fs.StatFS, fs.ReadFileFS and
// fs.ReadDirFS, and the files it opens are io.Seekers when the files of
// the underlying file system are (embed.FS's are).
type FS struct {
	root     fs.FS
	times    map[string]time.Time
	fallback time.Time
}

// New makes an FS of the folder dir of fsys, with the times its StampFile
// names. A file the stamp does not name, and every folder, has the time
// fallback. A folder without a stamp is fine; a stamp that is not what
// Copy writes is an error.
func New(fsys fs.FS, dir string, fallback time.Time) (*FS, error) {
	root, err := fs.Sub(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("embedded folder %s: %w", dir, err)
	}
	f := &FS{root: root, times: map[string]time.Time{}, fallback: fallback}
	raw, err := fs.ReadFile(root, StampFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, nil
	case err != nil:
		return nil, fmt.Errorf("embedded folder %s: read %s: %w", dir, StampFile, err)
	}
	var stamp map[string]int64
	if err := json.Unmarshal(raw, &stamp); err != nil {
		return nil, fmt.Errorf("embedded folder %s: %s: %w", dir, StampFile, err)
	}
	for name, nanos := range stamp {
		f.times[name] = time.Unix(0, nanos)
	}
	return f, nil
}

// hidden reports the names of the folder that are not part of it.
func hidden(name string) bool { return name == StampFile || name == Placeholder }

func (f *FS) refuse(op, name string) error {
	if hidden(name) {
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return nil
}

func (f *FS) modTime(name string) time.Time {
	if t, ok := f.times[name]; ok {
		return t
	}
	return f.fallback
}

// Empty reports whether the folder has no files, which is the case before
// a build copied any in.
func (f *FS) Empty() bool {
	entries, err := f.ReadDir(".")
	return err != nil || len(entries) == 0
}

// Open opens a file or folder of the embedded folder.
func (f *FS) Open(name string) (fs.File, error) {
	if err := f.refuse("open", name); err != nil {
		return nil, err
	}
	file, err := f.root.Open(name)
	if err != nil {
		return nil, err
	}
	return &openFile{File: file, fsys: f, name: name}, nil
}

// Stat describes a file or folder.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	if err := f.refuse("stat", name); err != nil {
		return nil, err
	}
	info, err := fs.Stat(f.root, name)
	if err != nil {
		return nil, err
	}
	return fileInfo{FileInfo: info, mod: f.modTime(name)}, nil
}

// ReadFile reads a whole file.
func (f *FS) ReadFile(name string) ([]byte, error) {
	if err := f.refuse("read", name); err != nil {
		return nil, err
	}
	return fs.ReadFile(f.root, name)
}

// ReadDir lists a folder, sorted by name.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := f.refuse("readdir", name); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(f.root, name)
	if err != nil {
		return nil, err
	}
	return f.entries(name, entries), nil
}

func (f *FS) entries(dir string, entries []fs.DirEntry) []fs.DirEntry {
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if dir == "." && hidden(e.Name()) {
			continue
		}
		out = append(out, dirEntry{DirEntry: e, fsys: f, name: path.Join(dir, e.Name())})
	}
	return out
}

type fileInfo struct {
	fs.FileInfo
	mod time.Time
}

func (i fileInfo) ModTime() time.Time { return i.mod }

type dirEntry struct {
	fs.DirEntry
	fsys *FS
	name string
}

func (e dirEntry) Info() (fs.FileInfo, error) {
	info, err := e.DirEntry.Info()
	if err != nil {
		return nil, err
	}
	return fileInfo{FileInfo: info, mod: e.fsys.modTime(e.name)}, nil
}

// openFile is an open file of an FS: its Stat gives the recorded time,
// Seek and ReadAt pass through, and a folder lists what the FS shows.
type openFile struct {
	fs.File
	fsys *FS
	name string
}

func (o *openFile) Stat() (fs.FileInfo, error) {
	info, err := o.File.Stat()
	if err != nil {
		return nil, err
	}
	return fileInfo{FileInfo: info, mod: o.fsys.modTime(o.name)}, nil
}

func (o *openFile) Seek(offset int64, whence int) (int64, error) {
	seeker, ok := o.File.(io.Seeker)
	if !ok {
		return 0, &fs.PathError{Op: "seek", Path: o.name, Err: errors.ErrUnsupported}
	}
	return seeker.Seek(offset, whence)
}

func (o *openFile) ReadAt(p []byte, offset int64) (int, error) {
	reader, ok := o.File.(io.ReaderAt)
	if !ok {
		return 0, &fs.PathError{Op: "read", Path: o.name, Err: errors.ErrUnsupported}
	}
	return reader.ReadAt(p, offset)
}

func (o *openFile) ReadDir(n int) ([]fs.DirEntry, error) {
	dir, ok := o.File.(fs.ReadDirFile)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: o.name, Err: errors.New("not a directory")}
	}
	for {
		entries, err := dir.ReadDir(n)
		shown := o.fsys.entries(o.name, entries)
		// A part that held only hidden names is not the end of the folder
		if len(shown) > 0 || err != nil || n <= 0 || len(entries) == 0 {
			return shown, err
		}
	}
}

// Copy makes the folder to a copy of the folder from for embedding: it
// removes what to holds except Placeholder, copies every folder and
// regular file of from with its modification time, and writes the times
// into StampFile. It answers the number of files and their bytes. A
// symbolic link or another irregular file in from is an error, and so is a
// file at the top of from named like StampFile or Placeholder.
func Copy(from, to string) (files int, size int64, err error) {
	src, err := filepath.Abs(from)
	if err != nil {
		return 0, 0, err
	}
	dst, err := filepath.Abs(to)
	if err != nil {
		return 0, 0, err
	}
	if src == dst {
		return 0, 0, fmt.Errorf("copy %s into itself", from)
	}
	if info, err := os.Stat(src); err != nil {
		return 0, 0, err
	} else if !info.IsDir() {
		return 0, 0, fmt.Errorf("%s is not a folder", from)
	}
	if err := emptyFolder(dst); err != nil {
		return 0, 0, err
	}
	stamp := map[string]int64{}
	err = filepath.WalkDir(src, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", name)
		case hidden(slash):
			return fmt.Errorf("%s has the name the embedded folder keeps for itself", name)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := copyFile(name, target, info.ModTime()); err != nil {
			return err
		}
		stamp[slash] = info.ModTime().UnixNano()
		files++
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("copy %s to %s: %w", from, to, err)
	}
	if err := writeStamp(dst, stamp); err != nil {
		return 0, 0, fmt.Errorf("write the times of %s: %w", to, err)
	}
	return files, size, nil
}

// Stamp records the times of a folder that is embedded where it is,
// without a copy: it writes StampFile into dir with the modification time
// of every regular file below it. StampFile and Placeholder at the top
// are left out. It answers the number of files and their bytes. A
// symbolic link or another irregular file is an error.
func Stamp(dir string) (files int, size int64, err error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return 0, 0, err
	}
	if info, err := os.Stat(root); err != nil {
		return 0, 0, err
	} else if !info.IsDir() {
		return 0, 0, fmt.Errorf("%s is not a folder", dir)
	}
	stamp := map[string]int64{}
	err = filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		switch {
		case d.IsDir() || hidden(slash):
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", name)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		stamp[slash] = info.ModTime().UnixNano()
		files++
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("stamp %s: %w", dir, err)
	}
	if err := writeStamp(root, stamp); err != nil {
		return 0, 0, fmt.Errorf("write the times of %s: %w", dir, err)
	}
	return files, size, nil
}

// writeStamp writes the times of a folder's files into its StampFile.
func writeStamp(dir string, stamp map[string]int64) error {
	raw, err := json.Marshal(stamp)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, StampFile), raw, 0o644)
}

// emptyFolder empties a folder, or makes it, keeping Placeholder.
func emptyFolder(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name() != Placeholder {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("empty %s: %w", dir, err)
		}
	}
	return nil
}

func copyFile(from, to string, modified time.Time) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(to, modified, modified)
}
