package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/embedfs"
)

func TestRunCopiesABuildAndStampsTheConsoleFiles(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "web-ui", "build")
	to := filepath.Join(root, "neodesk-server", "console", "webui", "dist")
	static := filepath.Join(root, "neodesk-server", "console", "static", "files")
	for _, dir := range []string{filepath.Join(from, "_ui"), filepath.Dir(to), filepath.Join(static, "lang")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"index.html": "<html>", "_ui/a.js": "a"} {
		if err := os.WriteFile(filepath.Join(from, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	en := filepath.Join(static, "lang", "en.json")
	at := time.Date(2025, 11, 10, 1, 59, 3, 123456700, time.UTC)
	if err := os.WriteFile(en, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(en, at, at); err != nil {
		t.Fatal(err)
	}
	if err := run(from, to, static); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "_ui/a.js", embedfs.StampFile} {
		if _, err := os.Stat(filepath.Join(to, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(static, embedfs.StampFile))
	if err != nil {
		t.Fatal(err)
	}
	var stamp map[string]int64
	if err := json.Unmarshal(raw, &stamp); err != nil {
		t.Fatal(err)
	}
	if len(stamp) != 1 || stamp["lang/en.json"] != at.UnixNano() {
		t.Errorf("stamp of the console files: %v", stamp)
	}
}

func TestRunRefuses(t *testing.T) {
	root := t.TempDir()
	noBuild := filepath.Join(root, "empty")
	static := filepath.Join(root, "static")
	for _, dir := range []string{noBuild, static} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(noBuild, filepath.Join(root, "dist"), static); err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("a folder without index.html: %v", err)
	}
	if err := os.WriteFile(filepath.Join(noBuild, "index.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(noBuild, filepath.Join(root, "no", "such", "dist"), static); err == nil || !strings.Contains(err.Error(), "run this in neodesk-server") {
		t.Errorf("outside the module: %v", err)
	}
	if err := run(noBuild, filepath.Join(root, "dist"), filepath.Join(root, "no-static")); err == nil || !strings.Contains(err.Error(), "run this in neodesk-server") {
		t.Errorf("without the console files: %v", err)
	}
	for _, dir := range []string{"no", "dist"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err == nil {
			t.Errorf("the step made %s before it checked its folders", dir)
		}
	}
}
