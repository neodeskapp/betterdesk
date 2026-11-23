package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeTree writes files (slash paths relative to root) into root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// repoRoot is the repository the test runs in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "neodesk-server", "go.mod")); err != nil {
		t.Fatalf("no repository at %s: %v", root, err)
	}
	return root
}

// result is what a command printed and returned.
type result struct {
	code           int
	stdout, stderr string
}

func runCommand(fn func(string, []string, io.Writer, io.Writer) int, root string, args ...string) result {
	var out, errOut bytes.Buffer
	code := fn(root, args, &out, &errOut)
	return result{code, out.String(), errOut.String()}
}

// want fails the test when r differs from the expected result.
func (r result) want(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if r.code != code {
		t.Errorf("exit code %d, want %d", r.code, code)
	}
	if r.stdout != stdout {
		t.Errorf("stdout:\n%s\nwant:\n%s", r.stdout, stdout)
	}
	if r.stderr != stderr {
		t.Errorf("stderr:\n%s\nwant:\n%s", r.stderr, stderr)
	}
}
