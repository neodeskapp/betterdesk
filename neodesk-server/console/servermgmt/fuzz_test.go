//go:build unix

package servermgmt

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/safepath"
)

// FuzzFilePaths sends any path to the list and the read of a tree with
// links into and out of the folders. Whatever the path, a list or a read
// that succeeds names a path inside a folder the browser may read.
func FuzzFilePaths(f *testing.F) {
	root := f.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(p, 0o777); err != nil {
			f.Fatal(err)
		}
		return p
	}
	server, console, home, outside := mk("server/sub"), mk("console"), mk("home"), mk("outside")
	server = filepath.Dir(server)
	for _, file := range []string{"server/a.txt", "server/sub/b.txt", "outside/secret.txt", "home/h.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(file), 0o666); err != nil {
			f.Fatal(err)
		}
	}
	for link, target := range map[string]string{"server/in": "sub", "server/out": "../outside", "server/out-file": "../outside/secret.txt", "server/dangling": "nothing", "server/loop": "loop", "console/up": ".."} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			f.Fatal(err)
		}
	}
	env := map[string]string{"NEODESK_PATH": server, "NEODESK_CONSOLE_PATH": console, "HOME": home, "TMPDIR": home}
	s := New(Options{Getenv: func(k string) string { return env[k] }, WorkDir: console, GOOS: "linux", LogDir: home, TempDirs: []string{home}})
	inside := func(p string, roots []string) bool {
		for _, r := range roots {
			if safepath.IsInsideRoot(p, r) {
				return true
			}
		}
		return false
	}
	for _, seed := range []string{"", "/", ".", "..", "a.txt", "../server/a.txt", server + "/a.txt", server + "/out/secret.txt", server + "/out-file",
		server + "/in/b.txt", server + "/../outside/secret.txt", outside, "up/outside/secret.txt", server + "/dangling", server + "/loop", server + "/a.txt\x00",
		server + "//sub/./b.txt", server + "x", "~", server + "/new/../new.txt", "\\..\\outside"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if !utf8.ValidString(p) {
			return
		}
		read := s.ReadRoots()
		if l, err := s.ListDirectory(p); err == nil && !inside(l.Path, read) {
			t.Fatalf("listed %q (%q) outside the folders", l.Path, p)
		}
		if r, err := s.ReadFilePreview(p); err == nil && !inside(r.Path, read) {
			t.Fatalf("read %q (%q) outside the folders", r.Path, p)
		}
	})
}

// FuzzCommandOutput parses any output of df, wmic, /proc/meminfo,
// systemctl and Get-Service without failing; df's disks are at least
// 1 MiB, wmic's have a size, and listed services are on the allow list.
func FuzzCommandOutput(f *testing.F) {
	f.Add("Mounted on Type 1B-blocks Used Avail\n/ ext4 100000000000 2 3\n")
	f.Add("neodesk-server.service loaded active running x\n\"NeoDeskServer\",\"Running\",\"x\"")
	f.Add("SwapTotal: 5 kB\nSwapFree: 9 kB\nMemTotal: 3 kB\n")
	f.Fuzz(func(t *testing.T, text string) {
		// wmic's sizes pass as long as they are numbers other than 0, a
		// negative one too ("if (!size) return")
		for _, d := range parseWMIC(text) {
			if size := float64(d.Size); size == 0 || math.IsNaN(size) {
				t.Fatalf("disk of size %v from %q", size, text)
			}
		}
		for _, d := range parseDF(text) {
			if float64(d.Size) < 1024*1024 || pseudoFileSystems[d.FSType] {
				t.Fatalf("disk %+v from %q", d, text)
			}
		}
		_, _, swap := parseMeminfo(text)
		if math.IsNaN(float64(swap.Total)) {
			t.Fatalf("swap %+v", swap)
		}
		run := func(out string) func() []Unit {
			return func() []Unit {
				s := New(Options{GOOS: "linux", Run: (&fakeRun{stdout: out}).run})
				return s.ListServices(t.Context())
			}
		}
		for _, u := range run(text)() {
			if !AllowedServices[u.Name] {
				t.Fatalf("listed %q", u.Name)
			}
		}
		_ = parseGetService(text)
	})
}
