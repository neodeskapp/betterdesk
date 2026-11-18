package servermgmt

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/safepath"
)

// The file browser's limits.
const (
	// FileMaxBytes is the largest file the browser opens for editing and
	// the most a write may hold: 8 MiB.
	FileMaxBytes = 8 * 1024 * 1024
	// ReadPreviewBytes is how much of a file a read returns: 256 KiB.
	ReadPreviewBytes = 256 * 1024
)

// The refusals of the file browser besides those of the folder check
// (package safepath) and the file system's own.
var (
	ErrNotDirectory   = errors.New("Not a directory")
	ErrNotRegularFile = errors.New("Not a regular file")
	ErrFileTooLarge   = errors.New("File too large for inline edit (>8 MB)")
	ErrContentTooBig  = errors.New("Content exceeds 8 MB limit")
	ErrDeleteRoot     = errors.New("Refusing to delete filesystem root")
)

// within checks a path the browser got against folders, the way
// resolvePathWithinAnyRoot did: not empty, no NUL, made absolute against
// WorkDir, inside one of the folders, and when it exists its real path
// inside one too. The result is the real path of a path that exists and
// the absolute path of one that does not.
func (s *Service) within(p string, roots []string) (string, error) {
	if p == "" {
		return "", safepath.ErrPathRequired
	}
	if strings.ContainsRune(p, 0) {
		return "", safepath.ErrInvalidPath
	}
	abs := make([]string, len(roots))
	for i, r := range roots {
		abs[i] = s.resolve(r)
	}
	return safepath.ResolveWithinAnyRoot(s.resolve(p), abs)
}

// Entry is one entry of a folder list.
type Entry struct {
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	IsDirectory bool    `json:"isDirectory"`
	IsFile      bool    `json:"isFile"`
	IsSymlink   bool    `json:"isSymlink"`
	Size        int64   `json:"size"`
	Mtime       *string `json:"mtime"`
	Mode        *uint32 `json:"mode"`
	UID         *int64  `json:"uid"`
	GID         *int64  `json:"gid"`
}

// Listing is a folder and its entries (listDirectory).
type Listing struct {
	Path   string  `json:"path"`
	Parent string  `json:"parent"`
	Items  []Entry `json:"items"`
}

// ListDirectory lists a folder the browser may read: folders first, then
// by name as localeCompare orders them. Entries whose name is no plain
// name (a backslash in it, a name that starts with "..") or that are links
// out of the folder are left out. An entry that cannot be looked at is
// listed with what the folder said of it.
func (s *Service) ListDirectory(p string) (*Listing, error) {
	abs, err := s.within(p, s.ReadRoots())
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fileError(err, "stat", abs)
	}
	if !st.IsDir() {
		return nil, ErrNotDirectory
	}
	dir, err := os.Open(abs)
	if err != nil {
		return nil, fileError(err, "scandir", abs)
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return nil, fileError(err, "scandir", abs)
	}
	items := []Entry{}
	for _, e := range entries {
		full, err := safepath.ResolveChild(abs, e.Name())
		if err != nil {
			continue
		}
		item := Entry{Name: e.Name(), Path: full, IsDirectory: e.IsDir(), IsFile: e.Type().IsRegular()}
		if info, err := os.Lstat(full); err == nil {
			item.IsDirectory = info.IsDir()
			item.IsFile = info.Mode().IsRegular()
			item.IsSymlink = info.Mode()&os.ModeSymlink != 0
			item.Size = info.Size()
			mtime := isoTime(info.ModTime())
			mode := uint32(info.Mode().Perm())
			uid, gid := owner(info)
			item.Mtime, item.Mode, item.UID, item.GID = &mtime, &mode, &uid, &gid
		}
		items = append(items, item)
	}
	c := newCollator()
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].IsDirectory != items[j].IsDirectory {
			return items[i].IsDirectory
		}
		return c.compare(items[i].Name, items[j].Name) < 0
	})
	return &Listing{Path: abs, Parent: filepath.Dir(abs), Items: items}, nil
}

// Preview is the start of a file (readFilePreview).
type Preview struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mtime     string `json:"mtime"`
	Mode      uint32 `json:"mode"`
	IsBinary  bool   `json:"isBinary"`
	Truncated bool   `json:"truncated"`
	Content   string `json:"content"`
}

// ReadFilePreview reads the first ReadPreviewBytes of a regular file of
// at most FileMaxBytes that the browser may read. A file with a NUL byte
// in that part is binary and its content is "".
func (s *Service) ReadFilePreview(p string) (*Preview, error) {
	abs, err := s.within(p, s.ReadRoots())
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fileError(err, "stat", abs)
	}
	if !st.Mode().IsRegular() {
		return nil, ErrNotRegularFile
	}
	if st.Size() > FileMaxBytes {
		return nil, ErrFileTooLarge
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, fileError(err, "open", abs)
	}
	defer f.Close()
	buf := make([]byte, min(st.Size(), ReadPreviewBytes))
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return nil, fileError(err, "read", "")
	}
	preview := &Preview{
		Path: abs, Size: st.Size(), Mtime: isoTime(st.ModTime()), Mode: uint32(st.Mode().Perm()),
		IsBinary: bytes.IndexByte(buf, 0) >= 0, Truncated: st.Size() > int64(len(buf)),
	}
	if !preview.IsBinary {
		preview.Content = decodeUTF8(buf)
	}
	return preview, nil
}

// Written is what a write wrote: the path and the size in bytes.
type Written struct {
	Path string `json:"path"`
	Size int    `json:"size"`
}

// WriteFile writes content as UTF-8 to a file the browser may change,
// creating it or replacing what it held.
func (s *Service) WriteFile(p, content string) (*Written, error) {
	abs, err := s.within(p, s.WriteRoots())
	if err != nil {
		return nil, err
	}
	if len(content) > FileMaxBytes {
		return nil, ErrContentTooBig
	}
	if err := s.guard(abs); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return nil, fileError(err, "open", abs)
	}
	_, err = f.WriteString(content)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fileError(err, "write", "")
	}
	return &Written{Path: abs, Size: len(content)}, nil
}

// PathResult names the path an operation acted on.
type PathResult struct {
	Path string `json:"path"`
}

// driveRoot is the root of a Windows drive, which a delete refuses.
var driveRoot = regexp.MustCompile(`^[A-Za-z]:\\?$`)

// DeletePath removes a file, or a folder with everything in it, that the
// browser may change. The root of the file system is refused. A path that
// is a link to a folder removes the folder it points to.
func (s *Service) DeletePath(p string) (*PathResult, error) {
	roots := s.WriteRoots()
	abs, err := s.within(p, roots)
	if err != nil {
		return nil, err
	}
	if abs == "/" || driveRoot.MatchString(abs) {
		return nil, ErrDeleteRoot
	}
	st, err := os.Lstat(abs)
	if err != nil {
		return nil, fileError(err, "lstat", abs)
	}
	// The path is checked again right before the removal.
	target, err := s.within(p, roots)
	if err != nil {
		return nil, err
	}
	if err := s.guard(target); err != nil {
		return nil, err
	}
	if st.IsDir() {
		if err := removeAll(target); err != nil {
			return nil, err
		}
	} else if err := unlink(target); err != nil {
		return nil, fileError(err, "unlink", target)
	}
	return &PathResult{Path: abs}, nil
}

// MakeDirectory makes one folder the browser may change; its parent must
// exist.
func (s *Service) MakeDirectory(p string) (*PathResult, error) {
	abs, err := s.within(p, s.WriteRoots())
	if err != nil {
		return nil, err
	}
	if err := s.guard(abs); err != nil {
		return nil, err
	}
	if err := os.Mkdir(abs, 0o777); err != nil {
		return nil, fileError(err, "mkdir", abs)
	}
	return &PathResult{Path: abs}, nil
}

// Renamed is what a rename moved.
type Renamed struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RenamePath moves a file or folder; both paths must be ones the browser
// may change. What is at the new path is replaced as rename(2) does.
func (s *Service) RenamePath(from, to string) (*Renamed, error) {
	roots := s.WriteRoots()
	a, err := s.within(from, roots)
	if err != nil {
		return nil, err
	}
	b, err := s.within(to, roots)
	if err != nil {
		return nil, err
	}
	if err := s.guard(a, b); err != nil {
		return nil, err
	}
	if err := rename(a, b); err != nil {
		return nil, fileError(err, "rename", a, b)
	}
	return &Renamed{From: a, To: b}, nil
}

// guard passes the paths of a change to the test hook, when there is one.
func (s *Service) guard(paths ...string) error {
	if s.opts.beforeChange == nil {
		return nil
	}
	return s.opts.beforeChange(paths...)
}

// removeAll is fs.rmSync(path, {recursive: true}): the folder and all in
// it. A failure names the call and the entry it failed on.
func removeAll(path string) error {
	err := os.RemoveAll(path)
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		call := "rm"
		switch pe.Op {
		case "unlinkat", "unlink", "remove":
			call = "unlink"
		case "rmdir":
			call = "rmdir"
		case "open", "openfdat":
			call = "scandir"
		}
		return fileError(pe.Err, call, pe.Path)
	}
	return err
}

// isoTime is the toISOString() of a file time as Node gave it: the
// milliseconds of seconds and nanoseconds in floating point, rounded to
// the nearest, in UTC.
func isoTime(t time.Time) string {
	ms := float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6
	return time.UnixMilli(int64(jsRound(ms))).UTC().Format("2006-01-02T15:04:05.000Z")
}

// jsRound is Math.round: the nearest integer, halves up.
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r := math.Floor(x)
	if x-r >= 0.5 {
		r++
	}
	return r
}
