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
