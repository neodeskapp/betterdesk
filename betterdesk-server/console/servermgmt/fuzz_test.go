//go:build unix

package servermgmt

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/safepath"
)

var errFuzzStop = errors.New("fuzz: no change")

// FuzzFilePaths sends any path to every file operation of a tree with
// links into and out of the folders. Whatever the path, a list or a read
// that succeeds names a path inside a folder the browser may read, and a
// change reaches the file system (the guard, which stops it here) only
// for paths inside a folder it may change. A path outside never gets
// that far.
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
	env := map[string]string{"BETTERDESK_PATH": server, "BETTERDESK_CONSOLE_PATH": console, "HOME": home, "TMPDIR": home}
	var changed []string
	s := New(Options{
		Getenv: func(k string) string { return env[k] }, WorkDir: console, GOOS: "linux", LogDir: home, TempDirs: []string{home},
		beforeChange: func(paths ...string) error {
			changed = append(changed, paths...)
			return errFuzzStop
		},
	})
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
		f.Add(seed, seed)
	}
	f.Fuzz(func(t *testing.T, p, q string) {
		if !utf8.ValidString(p) || !utf8.ValidString(q) {
			return
		}
		read, write := s.ReadRoots(), s.WriteRoots()
		if l, err := s.ListDirectory(p); err == nil && !inside(l.Path, read) {
			t.Fatalf("listed %q (%q) outside the folders", l.Path, p)
		}
		if r, err := s.ReadFilePreview(p); err == nil && !inside(r.Path, read) {
			t.Fatalf("read %q (%q) outside the folders", r.Path, p)
		}
		changed = nil
		_, _ = s.WriteFile(p, "x")
		_, _ = s.MakeDirectory(p)
		_, _ = s.DeletePath(p)
		_, _ = s.RenamePath(p, q)
		for _, c := range changed {
			if c == "/" || !inside(c, write) {
				t.Fatalf("a change of %q (%q, %q) got through the checks", c, p, q)
			}
		}
	})
}

// FuzzTerminalMessage reads any message as the terminal reads it: a
// message is an object or nothing, and the terminal's size always ends up
// within its limits, whatever the members hold.
func FuzzTerminalMessage(f *testing.F) {
	for _, seed := range []string{`{"type":"init","cols":1000,"rows":"x"}`, `[1]`, `null`, `{`, `{} x`, `{"type":"input","data":5}`,
		`{"cols":[[1,2],[3]],"rows":{"a":1}}`, `{"cols":1e400,"rows":-1e400}`, `{"cols":"  42abc","rows":"-0"}`, "\xff{}", `{"__proto__":{"type":"init"}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, ok := parseMessage(data)
		text := strings.TrimLeft(decodeUTF8(data), " \t\r\n")
		if isObject := json.Valid([]byte(text)) && strings.HasPrefix(text, "{"); ok != isObject {
			t.Fatalf("object %v, parsed %v: %q", isObject, ok, data)
		}
		if !ok {
			return
		}
		cols, rows := dimension(msg["cols"], 20, 500, 80), dimension(msg["rows"], 5, 200, 24)
		if cols < 20 || cols > 500 || rows < 5 || rows > 200 {
			t.Fatalf("size %dx%d from %q", cols, rows, data)
		}
		_ = jsString(msg["type"])
	})
}

// FuzzDecodeStream feeds output in two pieces: what the terminal sends of
// them is what one piece would have given, and every text is UTF-8.
func FuzzDecodeStream(f *testing.F) {
	f.Add([]byte("h\xc3\xa9llo \xf0\x9f\x99\x82"), 2)
	f.Add([]byte("\xe2\x82\x41\xc3"), 1)
	f.Fuzz(func(t *testing.T, data []byte, cut int) {
		if cut < 0 || cut > len(data) {
			return
		}
		first, carry := decodeStream(data[:cut])
		rest, tail := decodeStream(append(carry, data[cut:]...))
		got := first + rest + decodeUTF8(tail)
		if want := decodeUTF8(data); got != want {
			t.Fatalf("cut at %d: %q, want %q", cut, got, want)
		}
		if !utf8.ValidString(first) || !utf8.ValidString(rest) || len(carry) > 3 || len(tail) > 3 {
			t.Fatalf("pieces %q %q, kept %q %q", first, rest, carry, tail)
		}
	})
}

// FuzzCommandOutput parses any output of df, wmic, /proc/meminfo,
// systemctl and Get-Service without failing; df's disks are at least
// 1 MiB, wmic's have a size, and listed services are on the allow list.
func FuzzCommandOutput(f *testing.F) {
	f.Add("Mounted on Type 1B-blocks Used Avail\n/ ext4 100000000000 2 3\n")
	f.Add("betterdesk-server.service loaded active running x\n\"BetterDeskServer\",\"Running\",\"x\"")
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

// FuzzPasswd picks the terminal's account from any /etc/passwd: a console
// running as root never picks root by itself, and an account it picks
// has a user id from 1000 to 59999, a login shell and a home other than /.
func FuzzPasswd(f *testing.F) {
	f.Add("root:x:0:0:root:/root:/bin/bash\nnode:x:1000:1000::/home/node:/bin/bash\n", "")
	f.Add("a:x:1000:1000::/:/bin/sh\nb:x:1001abc:5::/home/b:/sbin/nologin\nnobody:x:1002:1::/home/n:/bin/sh\nc:x:59999:1::/h:/bin/FALSE", "b")
	f.Fuzz(func(t *testing.T, passwd, configured string) {
		s := New(Options{GOOS: "linux", Getenv: func(k string) string {
			if k == "BETTERDESK_TERMINAL_USER" {
				return configured
			}
			return ""
		}, Terminal: TerminalOptions{
			Passwd:      func() (string, error) { return passwd, nil },
			IsRoot:      func() bool { return true },
			CurrentUser: func() Account { return Account{Username: "console", UID: 0, GID: 0, Home: "/root"} },
			Executable:  func(p string) bool { return p == "/bin/sh" },
		}})
		a := s.PickTerminalUser()
		if a.Shell != "/bin/sh" {
			t.Fatalf("shell %q", a.Shell)
		}
		if a.Username == "console" || configured != "" {
			return
		}
		if a.UID < 1000 || a.UID >= 60000 || a.Username == "nobody" || a.Home == "/" || a.Home == "" {
			t.Fatalf("picked %+v from %q", a, passwd)
		}
	})
}
