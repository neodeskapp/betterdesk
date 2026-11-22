package console

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// oldFonts makes the fonts folder of a Node console install: a .gitkeep
// and one folder per font with its files.
func oldFonts(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	files := map[string]time.Time{
		".gitkeep":              time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),
		"Inter/inter-400.woff2": time.Date(2026, 1, 2, 3, 4, 5, 123456700, time.UTC),
		"Inter/inter-700.woff2": time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
		"Inter/meta.json":       time.Date(2026, 1, 2, 3, 4, 7, 999000000, time.UTC),
		"Inter/font.css":        time.Date(2026, 1, 2, 3, 4, 8, 0, time.UTC),
		"My Font/font.ttf":      time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	}
	for name, at := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("bytes of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// The fonts of a Node install are copied once into the new folder, with
// their times; the old folder stays as it was.
func TestOldFontsAreCopiedOnce(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "console", "public", "fonts")
	to := filepath.Join(root, "data", "fonts")
	files := oldFonts(t, from)
	if runtime.GOOS != "windows" {
		if err := os.Symlink("/etc/passwd", filepath.Join(from, "Inter", "link.woff2")); err != nil {
			t.Fatal(err)
		}
	}

	if err := copyOldFonts(from, to); err != nil {
		t.Fatal(err)
	}
	for name, at := range files {
		got, err := os.ReadFile(filepath.Join(to, filepath.FromSlash(name)))
		if err != nil || string(got) != "bytes of "+name {
			t.Errorf("%s in the new folder: %q %v", name, got, err)
			continue
		}
		info, _ := os.Stat(filepath.Join(to, filepath.FromSlash(name)))
		if !info.ModTime().Equal(at) {
			t.Errorf("%s has the time %v, want %v", name, info.ModTime(), at)
		}
		if old, err := os.ReadFile(filepath.Join(from, filepath.FromSlash(name))); err != nil || string(old) != "bytes of "+name {
			t.Errorf("%s left the old folder: %v", name, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(to, "Inter", "link.woff2")); runtime.GOOS != "windows" && err == nil {
		t.Error("a symbolic link was copied")
	}
	entries, _ := os.ReadDir(filepath.Dir(to))
	if len(entries) != 1 {
		t.Errorf("the data folder holds %d entries, want only fonts: %v", len(entries), entries)
	}

	// A font removed in the console stays removed, and a font added to the
	// old folder later is not taken over
	if err := os.RemoveAll(filepath.Join(to, "Inter")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, "late.ttf"), []byte("late"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyOldFonts(from, to); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"Inter", "late.ttf"} {
		if _, err := os.Stat(filepath.Join(to, gone)); err == nil {
			t.Errorf("%s is in the new folder after the second start", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(to, "My Font", "font.ttf")); err != nil {
		t.Error(err)
	}
}

// Without an old folder the new one is made empty, and an existing new
// folder is left alone.
func TestOldFontsWithoutTheOldFolder(t *testing.T) {
	root := t.TempDir()
	to := filepath.Join(root, "data", "fonts")
	if err := copyOldFonts(filepath.Join(root, "missing"), to); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(to); err != nil || len(entries) != 0 {
		t.Errorf("new folder without an old one: %v %v", entries, err)
	}
	if err := copyOldFonts("", filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "other")); err != nil || !info.IsDir() {
		t.Errorf("new folder without an old one named: %v", err)
	}

	from := filepath.Join(root, "old")
	oldFonts(t, from)
	if err := copyOldFonts(from, to); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(to); len(entries) != 0 {
		t.Errorf("fonts were copied into a folder that was there: %v", entries)
	}
	if err := copyOldFonts(from, ""); err != nil {
		t.Errorf("no new folder: %v", err)
	}
}

// A copy that fails leaves no new folder, so the next start copies again.
func TestOldFontsFailedCopyIsTriedAgain(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "old")
	oldFonts(t, from)
	blocked := filepath.Join(root, "data")
	if err := os.WriteFile(blocked, []byte("a file where the data folder goes"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(blocked, "fonts")
	if err := copyOldFonts(from, to); err == nil {
		t.Fatal("the copy into a folder that cannot be made went through")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if err := copyOldFonts(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "Inter", "meta.json")); err != nil {
		t.Errorf("second start: %v", err)
	}
	var leftovers []string
	_ = filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && filepath.Base(name) != "fonts" && filepath.Dir(name) == blocked {
			leftovers = append(leftovers, name)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Errorf("copies left beside the new folder: %v", leftovers)
	}
}

// The console copies the old fonts when it is built, before the branding
// lists them, and serves them at /fonts.
func TestConsoleTakesOverTheOldFonts(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	old := filepath.Join(root, "console", "public", "fonts")
	oldFonts(t, old)
	cfg := &config.Config{
		Host: "127.0.0.1", DataDir: data, UploadsDir: filepath.Join(data, "uploads"),
		FontsDir: filepath.Join(data, "fonts"), OldFontsDir: old,
		LangDir: filepath.Join("static", "files", "lang"), WebUIDir: t.TempDir(), DefaultLanguage: "en",
		Getenv: func(string) string { return testSecret },
	}
	s, err := New(context.Background(), Options{Config: cfg, Store: storetest.Migrated(t), API: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "fonts", "Inter", "inter-400.woff2")); err != nil {
		t.Errorf("the font was not copied: %v", err)
	}
	fonts, err := s.Deps().Branding.ListLocalFonts()
	if err != nil {
		t.Fatal(err)
	}
	if len(fonts) != 1 {
		t.Errorf("the branding lists %d installed fonts, want Inter", len(fonts))
	}
}
