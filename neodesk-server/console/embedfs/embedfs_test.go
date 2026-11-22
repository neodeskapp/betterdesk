package embedfs

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// testdata/tree is an embedded folder as the build leaves one: the
// placeholder, a stamp, and files whose names start with "." and "_".
// The stamp names a file that is not there (gone.txt) and leaves d.txt
// out.
//
//go:embed all:testdata/tree
var tree embed.FS

var fallback = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func open(t *testing.T) *FS {
	t.Helper()
	f, err := New(tree, "testdata/tree", fallback)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFilesHaveTheRecordedTimes(t *testing.T) {
	f := open(t)
	want := map[string]time.Time{
		"a.txt":     time.Unix(0, 1767323045678000000),
		"_x/b.js":   time.Unix(0, 1700000000123456789),
		".hidden/c": time.Unix(0, 1000),
		"d.txt":     fallback,
		"_x":        fallback,
		".hidden":   fallback,
		".":         fallback,
	}
	for name, at := range want {
		info, err := f.Stat(name)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !info.ModTime().Equal(at) {
			t.Errorf("stat %s: %v, want %v", name, info.ModTime(), at)
		}
		file, err := f.Open(name)
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		opened, err := file.Stat()
		_ = file.Close()
		if err != nil || !opened.ModTime().Equal(at) {
			t.Errorf("open %s, stat: %v %v, want %v", name, opened, err, at)
		}
	}
	entries, err := f.ReadDir("_x")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if e.Name() == "b.js" && !info.ModTime().Equal(want["_x/b.js"]) {
			t.Errorf("entry b.js: %v", info.ModTime())
		}
	}
}

func TestFilesReadSeekAndReadAt(t *testing.T) {
	f := open(t)
	raw, err := f.ReadFile("a.txt")
	if err != nil || string(raw) != "alpha\n" {
		t.Fatalf("read a.txt: %q %v", raw, err)
	}
	file, err := f.Open("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	seeker, ok := file.(io.ReadSeeker)
	if !ok {
		t.Fatal("an embedded file is no io.Seeker")
	}
	if at, err := seeker.Seek(2, io.SeekStart); err != nil || at != 2 {
		t.Fatalf("seek: %d %v", at, err)
	}
	rest, _ := io.ReadAll(seeker)
	if string(rest) != "pha\n" {
		t.Errorf("after the seek: %q", rest)
	}
	buf := make([]byte, 3)
	if n, err := file.(io.ReaderAt).ReadAt(buf, 1); err != nil || string(buf[:n]) != "lph" {
		t.Errorf("read at 1: %q %v", buf[:n], err)
	}
}

// The stamp and the placeholder at the top are not part of the folder;
// a placeholder further down is a file like any other.
func TestStampAndPlaceholderAreNotShown(t *testing.T) {
	f := open(t)
	for _, name := range []string{StampFile, Placeholder} {
		if _, err := f.Open(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("open %s: %v", name, err)
		}
		if _, err := f.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s: %v", name, err)
		}
		if _, err := f.ReadFile(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("read %s: %v", name, err)
		}
	}
	var files []string
	err := fs.WalkDir(f, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".hidden/c", "_x/.gitkeep", "_x/b.js", "a.txt", "d.txt"}
	if !slices.Equal(files, want) {
		t.Errorf("files %v, want %v", files, want)
	}
	dir, err := f.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	listed, err := dir.(fs.ReadDirFile).ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range listed {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != ".hidden _x a.txt d.txt" {
		t.Errorf("open(.).ReadDir: %v", names)
	}
	// One entry at a time: the hidden ones come first in the folder, and a
	// part that held only them is not the end
	dir1, err := f.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer dir1.Close()
	var one []string
	for {
		part, err := dir1.(fs.ReadDirFile).ReadDir(1)
		if len(part) == 0 {
			if err != io.EOF {
				t.Fatalf("ReadDir(1) gave nothing and %v", err)
			}
			break
		}
		one = append(one, part[0].Name())
	}
	if strings.Join(one, " ") != ".hidden _x a.txt d.txt" {
		t.Errorf("ReadDir(1) one by one: %v", one)
	}
	if err := fstest.TestFS(f, "a.txt", "d.txt", "_x/b.js", "_x/.gitkeep", ".hidden/c"); err != nil {
		t.Error(err)
	}
}

func TestNamesOutsideTheFolderAreInvalid(t *testing.T) {
	f := open(t)
	for _, name := range []string{"../embedfs.go", "/a.txt", "a.txt/", "./a.txt", "", "_x/../a.txt"} {
		if _, err := f.Open(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("open %q: %v", name, err)
		}
		if _, err := f.Stat(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("stat %q: %v", name, err)
		}
	}
	if _, err := f.Open("missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("open missing.txt: %v", err)
	}
}

func TestEmpty(t *testing.T) {
	placeholderOnly := fstest.MapFS{"dist/" + Placeholder: {}}
	f, err := New(placeholderOnly, "dist", fallback)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Empty() {
		t.Error("a folder with only the placeholder is not empty")
	}
	if open(t).Empty() {
		t.Error("the test tree counts as empty")
	}
}

func TestStampThatIsNotCopysIsAnError(t *testing.T) {
	for _, stamp := range []string{"not json", `{"a.txt":"x"}`, `["a.txt"]`, `{"a.txt":1.5}`} {
		fsys := fstest.MapFS{"d/" + StampFile: {Data: []byte(stamp)}, "d/a.txt": {Data: []byte("a")}}
		if _, err := New(fsys, "d", fallback); err == nil {
			t.Errorf("stamp %s was taken", stamp)
		}
	}
	fsys := fstest.MapFS{"d/a.txt": {Data: []byte("a"), ModTime: time.Unix(99, 0)}}
	f, err := New(fsys, "d", fallback)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := f.Stat("a.txt"); err != nil || !info.ModTime().Equal(fallback) {
		t.Errorf("without a stamp: %v %v", info.ModTime(), err)
	}
}

// makeTree writes files with their own times and answers the folder.
func makeTree(t *testing.T, files map[string]time.Time) string {
	t.Helper()
	dir := t.TempDir()
	for name, at := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("content of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCopyKeepsTimesAndThePlaceholder(t *testing.T) {
	source := map[string]time.Time{
		"index.html":                      time.Date(2025, 11, 10, 1, 59, 3, 123456700, time.UTC),
		"_ui/immutable/entry/start.js":    time.Date(2025, 11, 10, 1, 59, 4, 0, time.UTC),
		"_ui/immutable/entry/start.js.br": time.Date(2025, 11, 10, 1, 59, 5, 999000000, time.UTC),
		".vite/manifest.json":             time.Date(2024, 1, 2, 3, 4, 5, 6000, time.UTC),
		"fonts/a.woff2":                   time.Date(2023, 6, 7, 8, 9, 10, 0, time.UTC),
		"_x/.gitkeep":                     time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	from := makeTree(t, source)
	to := filepath.Join(t.TempDir(), "dist")
	if err := os.MkdirAll(filepath.Join(to, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(to, "old", "stale.js"), "stale")
	writeFile(t, filepath.Join(to, "stale.txt"), "stale")
	writeFile(t, filepath.Join(to, Placeholder), "kept")

	files, size, err := Copy(from, to)
	if err != nil {
		t.Fatal(err)
	}
	var wantSize int64
	for name := range source {
		wantSize += int64(len("content of " + name))
	}
	if files != len(source) || size != wantSize {
		t.Errorf("copied %d files of %d bytes, want %d of %d", files, size, len(source), wantSize)
	}
	for _, gone := range []string{"old", "stale.txt"} {
		if _, err := os.Stat(filepath.Join(to, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there: %v", gone, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(to, Placeholder)); err != nil || string(raw) != "kept" {
		t.Errorf("placeholder: %q %v", raw, err)
	}
	var stamp map[string]int64
	raw, err := os.ReadFile(filepath.Join(to, StampFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	if len(stamp) != len(source) {
		t.Errorf("stamp has %d files: %v", len(stamp), stamp)
	}
	f, err := New(os.DirFS(filepath.Dir(to)), "dist", fallback)
	if err != nil {
		t.Fatal(err)
	}
	for name, at := range source {
		// The time on disk of the source, which is what a file server
		// read before
		onDisk, err := os.Stat(filepath.Join(from, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if stamp[name] != onDisk.ModTime().UnixNano() {
			t.Errorf("stamp of %s: %d, the file has %d", name, stamp[name], onDisk.ModTime().UnixNano())
		}
		// The copy has the time too, so the folder serves alike from disk
		copied := filepath.Join(to, filepath.FromSlash(name))
		if info, err := os.Stat(copied); err != nil || !info.ModTime().Equal(onDisk.ModTime()) {
			t.Errorf("the copy of %s has the time %v, the file %v", name, info.ModTime(), onDisk.ModTime())
		}
		// Then the copy on disk gets other times; the embedded folder still
		// answers the recorded ones
		_ = os.Chtimes(copied, at.Add(time.Hour), at.Add(time.Hour))
		info, err := f.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(onDisk.ModTime()) {
			t.Errorf("%s: %v, want %v", name, info.ModTime(), onDisk.ModTime())
		}
		got, _ := f.ReadFile(name)
		if string(got) != "content of "+name {
			t.Errorf("%s: %q", name, got)
		}
	}

	// A second copy of a smaller build leaves nothing of the first
	delete(source, "fonts/a.woff2")
	if err := os.RemoveAll(filepath.Join(from, "fonts")); err != nil {
		t.Fatal(err)
	}
	if files, _, err := Copy(from, to); err != nil || files != len(source) {
		t.Fatalf("second copy: %d %v", files, err)
	}
	if _, err := os.Stat(filepath.Join(to, "fonts")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fonts from the first copy: %v", err)
	}
}

func TestCopyRefuses(t *testing.T) {
	at := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{StampFile, Placeholder} {
		from := makeTree(t, map[string]time.Time{"index.html": at, name: at})
		if _, _, err := Copy(from, filepath.Join(t.TempDir(), "dist")); err == nil {
			t.Errorf("a build with %s at its top was copied", name)
		}
	}
	from := makeTree(t, map[string]time.Time{"index.html": at})
	if _, _, err := Copy(from, from); err == nil {
		t.Error("a folder was copied into itself")
	}
	if _, _, err := Copy(filepath.Join(from, "index.html"), filepath.Join(t.TempDir(), "dist")); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Error("a file was copied as a folder")
	}
	if _, _, err := Copy(filepath.Join(from, "missing"), filepath.Join(t.TempDir(), "dist")); err == nil {
		t.Error("a missing folder was copied")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(from, "index.html"), filepath.Join(from, "link.html")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Copy(from, filepath.Join(t.TempDir(), "dist")); err == nil {
			t.Error("a symbolic link was copied")
		}
	}
}

func TestStampRecordsTheTimesInPlace(t *testing.T) {
	source := map[string]time.Time{
		"lang/en.json":           time.Date(2025, 11, 10, 1, 59, 3, 123456700, time.UTC),
		"public/img/a.png":       time.Date(2024, 1, 2, 3, 4, 5, 6000, time.UTC),
		"public/fonts/.gitkeep":  time.Date(2023, 6, 7, 8, 9, 10, 0, time.UTC),
		"_x/b.txt":               time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
		"config/theme.json":      time.Date(2021, 5, 6, 7, 8, 9, 999000000, time.UTC),
		"themes/sub/insolve.txt": time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC),
	}
	dir := makeTree(t, source)
	writeFile(t, filepath.Join(dir, Placeholder), "")
	writeFile(t, filepath.Join(dir, StampFile), `{"stale.txt":1}`)

	files, size, err := Stamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wantSize int64
	for name := range source {
		wantSize += int64(len("content of " + name))
	}
	if files != len(source) || size != wantSize {
		t.Errorf("stamped %d files of %d bytes, want %d of %d", files, size, len(source), wantSize)
	}
	raw, err := os.ReadFile(filepath.Join(dir, StampFile))
	if err != nil {
		t.Fatal(err)
	}
	var stamp map[string]int64
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	if len(stamp) != len(source) {
		t.Errorf("stamp names %d files: %v", len(stamp), stamp)
	}
	f, err := New(os.DirFS(filepath.Dir(dir)), filepath.Base(dir), fallback)
	if err != nil {
		t.Fatal(err)
	}
	for name, at := range source {
		if stamp[name] != at.UnixNano() {
			t.Errorf("stamp of %s: %d, want %d", name, stamp[name], at.UnixNano())
		}
		info, err := f.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(at) {
			t.Errorf("%s: %v, want %v", name, info.ModTime(), at)
		}
	}
	// The files stay where they are: nothing is copied or removed
	for name := range source {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, Placeholder)); err != nil {
		t.Error(err)
	}

	if _, _, err := Stamp(filepath.Join(dir, "lang", "en.json")); err == nil || !strings.Contains(err.Error(), "is not a folder") {
		t.Errorf("a file was stamped as a folder: %v", err)
	}
	if _, _, err := Stamp(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing folder was stamped")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, "lang", "en.json"), filepath.Join(dir, "lang", "link.json")); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Stamp(dir); err == nil {
			t.Error("a symbolic link was stamped")
		}
	}
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
