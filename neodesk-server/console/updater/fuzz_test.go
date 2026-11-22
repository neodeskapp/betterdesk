package updater

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest that parses has only names and versions that are safe in a
// path: they stay one element below the folder they are joined to.
func FuzzParseManifest(f *testing.F) {
	f.Add(manifestJSON(nil))
	f.Add(manifestJSON(func(m map[string]any) { m["version"] = "3.6.0-rc.1" }))
	f.Add([]byte(`{"schema_version":1,"files":[{"path":"..","sha256":""}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data, rules)
		if err != nil {
			return
		}
		root := filepath.Join(string(filepath.Separator), "data", "updates")
		dir := filepath.Join(root, m.Version)
		if filepath.Dir(dir) != root || strings.ContainsAny(m.Version, `/\`) || m.Version == ".." {
			t.Fatalf("version %q leaves the folder", m.Version)
		}
		for _, file := range m.Files {
			if filepath.Dir(filepath.Join(dir, file.Path)) != dir || strings.ContainsAny(file.Path, `/\:`) {
				t.Fatalf("file %q leaves the folder", file.Path)
			}
			if !reSHA256.MatchString(file.SHA256) {
				t.Fatalf("sha256 %q", file.SHA256)
			}
		}
		if !reCommit.MatchString(m.SHA) || m.Branch != "main" {
			t.Fatalf("sha %q branch %q", m.SHA, m.Branch)
		}
		_ = CanonicalPayload(m.Signed())
	})
}

// Whatever the executable's name, the paths the updater writes stay in
// its folder.
func FuzzBinaryPaths(f *testing.F) {
	for _, name := range []string{"betterdesk-server", "betterdesk-server.exe", "x y", "..", ".hidden", "a\nb"} {
		f.Add(name)
	}
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, name string) {
		if name == "" || strings.ContainsAny(name, "/\x00") || len(name) > 200 {
			return
		}
		exe := filepath.Join(dir, name)
		if filepath.Dir(exe) != dir {
			return
		}
		if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
			return
		}
		defer os.Remove(exe)
		p, err := ResolveBinaryPaths(func() (string, error) { return exe, nil })
		if err != nil {
			return
		}
		for _, derived := range []string{p.Prev, p.Pending, p.staging(7), p.aside(7, 9)} {
			if filepath.Dir(derived) != p.Dir || derived == p.Binary {
				t.Fatalf("%q from %q leaves the folder or is the binary", derived, name)
			}
		}
		for _, leftover := range p.leftovers() {
			if leftover == p.Binary || leftover == p.Prev || leftover == p.Pending {
				t.Fatalf("leftovers would remove %s", leftover)
			}
		}
	})
}

// A backup name the routes accept stays one folder below the backups.
func FuzzBackupName(f *testing.F) {
	for _, name := range []string{"pre-update-2026-01-01T10-00-00", "../x", "pre-update-..", "pre-update-1/2"} {
		f.Add(name)
	}
	root := f.TempDir()
	s := New(Options{DataDir: root, Logger: quietLogger{}})
	f.Fuzz(func(t *testing.T, name string) {
		if !ValidBackupName(name) {
			if _, err := s.backupDir(name); err == nil {
				t.Fatalf("%q passed without a valid name", name)
			}
			return
		}
		if filepath.Dir(filepath.Join(s.backupRoot(), name)) != s.backupRoot() {
			t.Fatalf("%q leaves the backups", name)
		}
	})
}

// Only addresses of the release's file on github.com pass the check.
func FuzzReleaseFileAddress(f *testing.F) {
	f.Add("https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json")
	f.Add("https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0/x/../manifest.json")
	f.Add("https://github.com/UNITRONIX/BetterDesk/releases/download/v1.0.0%2F..%2Fmanifest.json")
	want := releaseFile{owner: "UNITRONIX", repo: "BetterDesk", tag: "v1.0.0", name: "manifest.json"}
	f.Fuzz(func(t *testing.T, address string) {
		u, err := url.Parse(address)
		if err != nil || !plainHTTPS(u) || !strings.EqualFold(u.Host, githubSite) || u.RawQuery != "" || u.Fragment != "" || !want.matches(u.EscapedPath()) {
			return
		}
		path, err := url.PathUnescape(u.EscapedPath())
		if err != nil || !strings.EqualFold(path, "/UNITRONIX/BetterDesk/releases/download/v1.0.0/manifest.json") {
			t.Fatalf("%q passed with the path %q", address, path)
		}
	})
}
