package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testAllowList = `# Header comment

# Runs in another engine
a/engine.js

# Build fixtures
fixtures/**
web/*/site/*.js
`

func TestJSFindings(t *testing.T) {
	files := []string{
		"a/engine.js",
		"a/engine.ts",
		"fixtures/x.js",
		"fixtures/deep/er/y.mjs",
		"web/app/site/main.js",
		"web/app/site/sub/main.js",
		"scripts/tool.js",
		"scripts/tool.cjs",
		"scripts/OLD.JS",
		"scripts/old.Mjs",
		"fixtures/UPPER.JS",
		"src/types.d.mts",
		"README.md",
	}
	allowed, failures := jsFindings(testAllowList, files)
	if allowed != 5 {
		t.Errorf("allowed %d, want 5", allowed)
	}
	want := []string{
		"web/app/site/sub/main.js is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go",
		"scripts/tool.js is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go",
		"scripts/tool.cjs is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go",
		"scripts/OLD.JS is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go",
		"scripts/old.Mjs is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go",
	}
	if !reflect.DeepEqual(failures, want) {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(failures, "\n"), strings.Join(want, "\n"))
	}
}

func TestJSAllowListRules(t *testing.T) {
	list := `# Explained
a.js
b.js

c.js
# Explained, but nothing matches
gone/**
d[.js
`
	_, failures := jsFindings(list, []string{"a.js", "b.js", "c.js"})
	want := []string{
		".github/javascript-allowlist.txt line 5: c.js has no comment above it that says why it stays JavaScript",
		// a comment covers the entries right below it
		".github/javascript-allowlist.txt line 8: d[.js is not a valid glob",
		".github/javascript-allowlist.txt line 7: gone/** matches no tracked JavaScript file; remove it",
	}
	if !reflect.DeepEqual(failures, want) {
		t.Errorf("failures:\n%s\nwant:\n%s", strings.Join(failures, "\n"), strings.Join(want, "\n"))
	}
}

func TestGlobMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"a/b.js", "a/b.js", true},
		{"a/b.js", "a/b.jsx", false},
		{"a/*.js", "a/b.js", true},
		{"a/*.js", "a/c/b.js", false},
		{"a/**", "a/b.js", true},
		{"a/**", "a/c/d/b.js", true},
		{"a/**", "ab/c.js", false},
		{"a/**/b.js", "a/b.js", true},
		{"a/**/b.js", "a/x/y/b.js", true},
		{"a/**/b.js", "a/x/y/c.js", false},
		{"**/*.js", "x.js", true},
		{"a/?.js", "a/bc.js", false},
	} {
		if got := globMatch(c.pattern, c.name); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestCheckJSInARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is needed: %v", err)
	}
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".github/javascript-allowlist.txt": "# Another engine\nkeep/engine.js\n",
		"keep/engine.js":                   "",
		"src/app.ts":                       "",
		"untracked.js":                     "",
	})
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".github", "keep", "src")
	runCommand(checkJS, root).want(t, 0, "JavaScript check OK (1 allowed files)\n", "")

	writeTree(t, root, map[string]string{"tools/odd name.mjs": ""})
	git("add", "tools")
	runCommand(checkJS, root).want(t, 1, "", "JavaScript check failed:\n"+
		"  - tools/odd name.mjs is JavaScript and not on the allow list (.github/javascript-allowlist.txt); write it in TypeScript or Go\n")

	if err := os.Remove(filepath.Join(root, ".github", "javascript-allowlist.txt")); err != nil {
		t.Fatal(err)
	}
	if r := runCommand(checkJS, root); r.code != 1 || !strings.HasPrefix(r.stderr, "JavaScript check failed: ") {
		t.Errorf("without the list: %+v", r)
	}
}

func TestJSAllowListOfTheRepository(t *testing.T) {
	// The repository's list is well formed; which files it allows is
	// what CI checks with git
	text, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "javascript-allowlist.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, failures := parseAllowList(string(text)); len(failures) > 0 {
		t.Errorf("allow list: %v", failures)
	}
}
