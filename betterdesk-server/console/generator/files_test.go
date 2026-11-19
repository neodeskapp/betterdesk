package generator

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestCopyTreeKeepsRelativeLinks copies a symbolic link with its target
// as written, so a relative link still points inside the copy and none
// points into the folder it was copied from (BUGS GN7: the target was
// made absolute against the source folder).
func TestCopyTreeKeepsRelativeLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need privileges on Windows")
	}
	src := filepath.Join(t.TempDir(), "src")
	must(t, os.MkdirAll(filepath.Join(src, "lib"), 0o755))
	must(t, os.WriteFile(filepath.Join(src, "lib", "libx.so.1"), []byte("lib"), 0o644))
	must(t, os.Symlink("libx.so.1", filepath.Join(src, "lib", "libx.so")))
	must(t, os.Symlink("../lib/libx.so.1", filepath.Join(src, "lib", "up")))
	must(t, os.Symlink("/usr/share/doc", filepath.Join(src, "doc")))
	dst := filepath.Join(t.TempDir(), "dst")
	must(t, copyTree(src, dst))
	for link, want := range map[string]string{
		filepath.Join("lib", "libx.so"): "libx.so.1",
		filepath.Join("lib", "up"):      "../lib/libx.so.1",
		"doc":                           "/usr/share/doc",
	} {
		got, err := os.Readlink(filepath.Join(dst, link))
		if err != nil || got != want {
			t.Errorf("%s points at %q (%v), want %q", link, got, err, want)
		}
	}
	if data, err := os.ReadFile(filepath.Join(dst, "lib", "libx.so")); err != nil || string(data) != "lib" {
		t.Errorf("the copied link reads %q, %v", data, err)
	}
}

// TestBuildSettingsThatAreNoNumber reads a build setting that is text
// but no number as its default, as an empty one (BUGS GN8: the timeout
// fired at once and the disk space check was off).
func TestBuildSettingsThatAreNoNumber(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"":        time.Hour,
		"abc":     time.Hour,
		"NaN":     time.Hour,
		"10":      time.Minute,
		" 120000": 2 * time.Minute,
		"90000ms": 90 * time.Second,
	} {
		if got := buildTimeoutOf(raw); got != want {
			t.Errorf("BETTERDESK_CLIENT_BUILD_TIMEOUT_MS=%q: %v, want %v", raw, got, want)
		}
	}
	for raw, want := range map[string]int64{
		"":       3072,
		"abc":    3072,
		"-":      3072,
		"100":    512,
		"4096":   4096,
		"2048MB": 2048,
	} {
		if got := minFreeMB(raw); got != want {
			t.Errorf("BETTERDESK_CLIENT_BUILD_MIN_FREE_MB=%q: %d, want %d", raw, got, want)
		}
	}
}
