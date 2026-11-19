//go:build linux

package servermgmt

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// writableDir is a service whose server folder is a folder of the test,
// with the change guard of the replay.
func writableDir(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{"BETTERDESK_PATH": dir, "BETTERDESK_CONSOLE_PATH": dir}
	return New(Options{GOOS: "linux", Getenv: func(k string) string { return env[k] }, WorkDir: dir, beforeChange: onlyBelow(t, dir)}), dir
}

// A write refuses what is no regular file before it opens anything: a
// FIFO, whose open would wait for a reader and leave the request hanging,
// a socket, and a link, which the open would follow, also out of the
// folders when the link points nowhere yet.
func TestWriteRefusesWhatIsNoRegularFile(t *testing.T) {
	s, dir := writableDir(t)
	outside := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o666); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	for name, target := range map[string]string{"dangling": "nothing", "dangling-out": filepath.Join(outside, "planted.txt"), "loop": "loop"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"pipe", "sock", "dangling", "dangling-out", "loop"} {
		done := make(chan error, 1)
		go func() {
			_, err := s.WriteFile(filepath.Join(dir, name), "x")
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegularFile) {
				t.Errorf("write to %s: %v, want %v", name, err, ErrNotRegularFile)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("a write to %s still waits", name)
		}
	}
	for _, made := range []string{filepath.Join(dir, "nothing"), filepath.Join(outside, "planted.txt")} {
		if _, err := os.Lstat(made); err == nil {
			t.Errorf("a write through a link made %s", made)
		}
	}
}

// Delete and rename leave the folders the browser may change in place: a
// root itself, or a folder that holds one, is refused, also as the new
// name of a rename. A link is removed or moved itself, never what it
// points to, and a link outside the folders that points into one is
// refused.
func TestDeleteAndRenameKeepTheRoots(t *testing.T) {
	base := t.TempDir()
	server, console, data := filepath.Join(base, "server"), filepath.Join(base, "console"), filepath.Join(base, "server", "keep", "data")
	for _, d := range []string{server, console, data, filepath.Join(server, "sub"), filepath.Join(server, "empty")} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		filepath.Join(server, "link-in"): "sub", filepath.Join(server, "link-console"): console, filepath.Join(base, "serverlink"): "server",
	} {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(server, "sub", "b.txt"), []byte("bee"), 0o666); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"BETTERDESK_PATH": server, "BETTERDESK_CONSOLE_PATH": console, "BETTERDESK_MUTABLE_ROOTS": data}
	s := New(Options{GOOS: "linux", Getenv: func(k string) string { return env[k] }, WorkDir: console, beforeChange: onlyBelow(t, base)})

	for _, p := range []string{server, server + "/", console, data, filepath.Join(server, "keep"), filepath.Join(server, "sub", "..")} {
		if _, err := s.DeletePath(p); !errors.Is(err, ErrChangeRoot) {
			t.Errorf("delete %s: %v, want %v", p, err, ErrChangeRoot)
		}
	}
	for _, r := range [][2]string{
		{server, filepath.Join(console, "moved")}, {filepath.Join(server, "keep"), filepath.Join(server, "moved")},
		{filepath.Join(server, "empty"), data}, {filepath.Join(server, "empty"), console},
	} {
		if _, err := s.RenamePath(r[0], r[1]); !errors.Is(err, ErrChangeRoot) {
			t.Errorf("rename %s to %s: %v, want %v", r[0], r[1], err, ErrChangeRoot)
		}
	}
	if _, err := s.DeletePath(filepath.Join(base, "serverlink")); err == nil {
		t.Error("deleted through a link outside the folders")
	}
	for _, d := range []string{server, console, data, filepath.Join(server, "empty")} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("%s is gone: %v", d, err)
		}
	}

	if got, err := s.RenamePath(filepath.Join(server, "link-in"), filepath.Join(server, "link-moved")); err != nil || got.From != filepath.Join(server, "link-in") {
		t.Errorf("rename of a link: %+v %v", got, err)
	}
	if target, err := os.Readlink(filepath.Join(server, "link-moved")); err != nil || target != "sub" {
		t.Errorf("the moved link points to %q: %v", target, err)
	}
	for _, link := range []string{filepath.Join(server, "link-moved"), filepath.Join(server, "link-console")} {
		if got, err := s.DeletePath(link); err != nil || got.Path != link {
			t.Errorf("delete of the link %s: %+v %v", link, got, err)
		}
		if _, err := os.Lstat(link); err == nil {
			t.Errorf("the link %s is still there", link)
		}
	}
	if b, err := os.ReadFile(filepath.Join(server, "sub", "b.txt")); err != nil || string(b) != "bee" {
		t.Errorf("the folder behind the link: %q %v", b, err)
	}
	if _, err := os.Stat(console); err != nil {
		t.Errorf("the console's folder behind the link: %v", err)
	}
}

// The open behind a write holds when the path changed after the checks:
// it follows no link in the last part and does not wait for a FIFO's
// reader.
func TestOpenForWrite(t *testing.T) {
	dir := t.TempDir()
	target, link, pipe := filepath.Join(dir, "target"), filepath.Join(dir, "link"), filepath.Join(dir, "pipe")
	if err := os.WriteFile(target, []byte("keep"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(pipe, 0o666); err != nil {
		t.Fatal(err)
	}
	if f, err := openForWrite(link); err == nil {
		f.Close()
		t.Error("opened through a link")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("the link's target holds %q", b)
	}
	done := make(chan error, 1)
	go func() {
		f, err := openForWrite(pipe)
		if err == nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("opened a FIFO without a reader")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the open of a FIFO waits for a reader")
	}
	f, err := openForWrite(filepath.Join(dir, "new"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("x"); err != nil {
		t.Error(err)
	}
	f.Close()
}
