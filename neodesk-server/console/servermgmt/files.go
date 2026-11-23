package servermgmt

import (
	"bytes"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/safepath"
)

// The file browser's limits.
const (
	// FileMaxBytes is the largest file the browser opens: 8 MiB.
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
