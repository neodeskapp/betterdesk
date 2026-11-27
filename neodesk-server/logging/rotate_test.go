package logging

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// The log moves to NAME.1 before a write would take it past the limit,
// and the next move replaces the older NAME.1: the log of the Windows
// service never holds more than two files.
func TestRotatingFileMovesTheLogAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "server.log")
	r, err := OpenRotatingFile(path, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, line := range []string{"first line\n", "second\n", "third line\n", "fourth line\n"} {
		if _, err := r.Write([]byte(line)); err != nil {
			t.Fatalf("write %q: %v", line, err)
		}
	}
	if got := readFile(t, path); got != "fourth line\n" {
		t.Errorf("server.log = %q, want the newest line only", got)
	}
	if got := readFile(t, path+".1"); got != "third line\n" {
		t.Errorf("server.log.1 = %q, want the line before", got)
	}
}

// A log that is already at the limit when the service starts moves aside
// at once, and a log under it is appended to.
func TestRotatingFileAtStart(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.log")
	os.WriteFile(full, []byte(strings.Repeat("x", 30)), 0o600)
	r, err := OpenRotatingFile(full, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("new\n"))
	r.Close()
	if got := readFile(t, full); got != "new\n" {
		t.Errorf("full.log = %q, want only the new line", got)
	}
	if got := readFile(t, full+".1"); got != strings.Repeat("x", 30) {
		t.Errorf("full.log.1 = %q, want the old log", got)
	}

	short := filepath.Join(dir, "short.log")
	os.WriteFile(short, []byte("old\n"), 0o600)
	r, err = OpenRotatingFile(short, 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("new\n"))
	r.Close()
	if got := readFile(t, short); got != "old\nnew\n" {
		t.Errorf("short.log = %q, want the new line appended", got)
	}
	if _, err := os.Stat(short + ".1"); err == nil {
		t.Errorf("a log under the limit was moved aside")
	}
}

// onOpen hears of every file the log opens and of each close, so the
// service can point its crash output at the current file.
func TestRotatingFileReportsEachFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	var opened []string
	r, err := OpenRotatingFile(path, 10, func(f *os.File) {
		if f == nil {
			opened = append(opened, "closed")
			return
		}
		opened = append(opened, "opened")
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("0123456789"))
	r.Write([]byte("next\n"))
	r.Close()
	if got := strings.Join(opened, " "); got != "opened closed opened closed" {
		t.Errorf("onOpen calls: %s", got)
	}
	if _, err := r.Write([]byte("late\n")); err == nil {
		t.Errorf("a write after Close succeeded")
	}
}

// Setup writes to the destination a Windows service sets, not to the
// standard error it does not have.
func TestSetupWritesToTheDestination(t *testing.T) {
	var out bytes.Buffer
	SetDestination(&out)
	t.Cleanup(func() {
		SetDestination(nil)
		Setup("text", "info")
	})
	Setup("text", "info")
	log.Printf("Service stop requested")
	if !strings.Contains(out.String(), "Service stop requested") {
		t.Errorf("the line did not reach the destination: %q", out.String())
	}
}
