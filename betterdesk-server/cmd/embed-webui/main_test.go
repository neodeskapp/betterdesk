package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/embedfs"
)

func TestRunCopiesABuild(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "web-ui", "build")
	to := filepath.Join(root, "betterdesk-server", "console", "webui", "dist")
	if err := os.MkdirAll(filepath.Join(from, "_ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"index.html": "<html>", "_ui/a.js": "a"} {
		if err := os.WriteFile(filepath.Join(from, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(from, to); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "_ui/a.js", embedfs.StampFile} {
		if _, err := os.Stat(filepath.Join(to, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRunRefuses(t *testing.T) {
	root := t.TempDir()
	noBuild := filepath.Join(root, "empty")
	if err := os.MkdirAll(noBuild, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(noBuild, filepath.Join(root, "dist")); err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("a folder without index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noBuild, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(noBuild, filepath.Join(root, "no", "such", "dist")); err == nil || !strings.Contains(err.Error(), "run this in betterdesk-server") {
		t.Errorf("outside the module: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "no")); err == nil {
		t.Error("the step made folders outside the module")
	}
}
