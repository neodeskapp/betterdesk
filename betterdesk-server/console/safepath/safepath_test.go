package safepath

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tree builds the folder of testdata/vectors.json in a temp folder and
// makes it the working directory: folders, files and symbolic links
// inside root, out of it, to nowhere and to themselves.
func tree(t testing.TB, entries [][]string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the tree needs symbolic links and POSIX paths")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		full := filepath.Join(dir, filepath.FromSlash(entry[1]))
		switch entry[0] {
		case "dir":
			err = os.MkdirAll(full, 0o755)
		case "file":
			err = os.WriteFile(full, []byte(entry[1]), 0o644)
		case "link":
			err = os.Symlink(entry[2], full)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

var smallTree = [][]string{
	{"dir", "root"}, {"dir", "root/sub"}, {"dir", "outside"},
	{"file", "root/file.txt"}, {"file", "root/sub/inner.txt"}, {"file", "outside/secret.txt"},
	{"link", "root/link-out", "../outside"}, {"link", "root/link-in", "sub"},
}

type vector struct {
	Fn     string            `json:"fn"`
	Args   []json.RawMessage `json:"args"`
	Result json.RawMessage   `json:"result"`
	Error  string            `json:"error"`
}

// TestNodeVectors replays what lib/safePath.js answered for about 1,400
// calls on the same tree (testdata/vectors.json): the same
// result, or the same error.
func TestNodeVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Tree    [][]string `json:"tree"`
		Vectors []vector   `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded.Vectors) < 1000 {
		t.Fatalf("vectors.json holds %d vectors", len(recorded.Vectors))
	}
	dir := tree(t, recorded.Tree)
	text := func(raw json.RawMessage) string {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("argument %s: %v", raw, err)
		}
		return strings.ReplaceAll(s, "<T>", dir)
	}
	for _, v := range recorded.Vectors {
		var got string
		var gotBool, isBool bool
		var err error
		switch v.Fn {
		case "resolveChildPath":
			got, err = ResolveChild(text(v.Args[0]), text(v.Args[1]))
		case "resolvePathWithinRoot":
			got, err = ResolveWithinRoot(text(v.Args[0]), text(v.Args[1]))
		case "resolvePathUnderRoot":
			got, err = ResolveUnderRoot(text(v.Args[0]), text(v.Args[1]))
		case "isPathInsideRoot":
			gotBool, isBool = IsInsideRoot(text(v.Args[0]), text(v.Args[1])), true
		case "resolvePathWithinAnyRoot":
			var roots []string
			if err := json.Unmarshal(v.Args[1], &roots); err != nil {
				t.Fatal(err)
			}
			for i := range roots {
				roots[i] = strings.ReplaceAll(roots[i], "<T>", dir)
			}
			got, err = ResolveWithinAnyRoot(text(v.Args[0]), roots)
		default:
			t.Fatalf("unknown function %s", v.Fn)
		}
		call := v.Fn + "(" + strings.Join(func() []string {
			parts := make([]string, len(v.Args))
			for i, a := range v.Args {
				parts[i] = string(a)
			}
			return parts
		}(), ", ") + ")"
		switch {
		case isBool:
			if want := string(v.Result) == "true"; gotBool != want {
				t.Errorf("%s = %v, want %v", call, gotBool, want)
			}
		case strings.HasPrefix(v.Error, "fs:"):
			// The file system refused (a link that points at itself)
			if err == nil || isOwn(err) {
				t.Errorf("%s = %q, %v; want the file system's error %s", call, got, err, v.Error)
			}
		case v.Error != "":
			if err == nil || err.Error() != v.Error {
				t.Errorf("%s = %q, %v; want the error %q", call, got, err, v.Error)
			}
		default:
			want := text(v.Result)
			if err != nil || got != want {
				t.Errorf("%s = %q, %v; want %q", call, strings.ReplaceAll(got, dir, "<T>"), err, strings.ReplaceAll(want, dir, "<T>"))
			}
		}
	}
}

func isOwn(err error) bool {
	for _, own := range []error{ErrInvalidSegment, ErrOutside, ErrResolvesOutside, ErrOutsideRoots, ErrResolvesOutsideRoots, ErrPathRequired, ErrInvalidPath, ErrRelativeRequired, ErrInvalidRelative} {
		if errors.Is(err, own) {
			return true
		}
	}
	return false
}

// The tests below carry web-nodejs/tests/safePath.test.js and the path
// checks of tests/security/08-path-redirect.test.js.

func TestIsInsideRoot(t *testing.T) {
	dir := tree(t, smallTree)
	root := filepath.Join(dir, "root")
	if !IsInsideRoot(filepath.Join(root, "a", "b.txt"), root) {
		t.Error("a descendant is not inside its root")
	}
	if !IsInsideRoot(root, root) {
		t.Error("the root is not inside itself")
	}
	if IsInsideRoot(filepath.Join(root, "..", "etc", "passwd"), root) {
		t.Error("a path through .. is inside the root")
	}
	if IsInsideRoot(filepath.Join(dir, "rootx"), root) {
		t.Error("a folder whose name starts like the root's is inside the root")
	}
	if IsInsideRoot(filepath.Join(root, "link-out", "secret.txt"), root) {
		t.Error("a path through a link out of the root is inside the root")
	}
	if !IsInsideRoot(filepath.Join(root, "link-in", "inner.txt"), root) {
		t.Error("a path through a link within the root is not inside the root")
	}
}

func TestResolveChild(t *testing.T) {
	dir := tree(t, smallTree)
	root := filepath.Join(dir, "root")
	got, err := ResolveChild(root, "backup-1")
	if err != nil || got != filepath.Join(root, "backup-1") {
		t.Errorf("ResolveChild(root, backup-1) = %q, %v", got, err)
	}
	for _, name := range []string{"", ".", "..", "../etc/passwd", "foo/bar", `foo\bar`, "/etc/passwd", "a\x00b", "../secrets.json"} {
		if got, err := ResolveChild(root, name); !errors.Is(err, ErrInvalidSegment) {
			t.Errorf("ResolveChild(root, %q) = %q, %v; want ErrInvalidSegment", name, got, err)
		}
	}
	if got, err := ResolveChild(root, "link-out"); !errors.Is(err, ErrOutside) {
		t.Errorf("a link out of the root: %q, %v; want ErrOutside", got, err)
	}
	// A language file is a child of the language folder
	if got, err := ResolveChild(root, "en.json"); err != nil || got != filepath.Join(root, "en.json") {
		t.Errorf("ResolveChild(root, en.json) = %q, %v", got, err)
	}
}

func TestResolveWithinRoot(t *testing.T) {
	dir := tree(t, smallTree)
	root := filepath.Join(dir, "root")
	// The real path of a file that exists
	got, err := ResolveWithinRoot(filepath.Join(root, "link-in", "inner.txt"), root)
	if err != nil || got != filepath.Join(root, "sub", "inner.txt") {
		t.Errorf("through a link within the root: %q, %v", got, err)
	}
	// The path itself for a file that does not exist yet
	got, err = ResolveWithinRoot(filepath.Join(root, "new", "x.txt"), root)
	if err != nil || got != filepath.Join(root, "new", "x.txt") {
		t.Errorf("a new file: %q, %v", got, err)
	}
	for path, want := range map[string]error{
		"":                                       ErrPathRequired,
		"a\x00b":                                 ErrInvalidPath,
		filepath.Join(dir, "outside/secret.txt"): ErrOutside,
		filepath.Join(root, "../outside"):        ErrOutside,
		filepath.Join(root, "link-out/secret.txt"): ErrOutside,
		"/etc/passwd": ErrOutside,
	} {
		if got, err := ResolveWithinRoot(path, root); !errors.Is(err, want) {
			t.Errorf("ResolveWithinRoot(%q) = %q, %v; want %v", path, got, err, want)
		}
	}
}

func TestResolveWithinAnyRoot(t *testing.T) {
	dir := tree(t, smallTree)
	root := filepath.Join(dir, "root")
	got, err := ResolveWithinAnyRoot(filepath.Join(root, "x"), []string{root, "/var/log"})
	if err != nil || got != filepath.Join(root, "x") {
		t.Errorf("a path in the first root: %q, %v", got, err)
	}
	got, err = ResolveWithinAnyRoot(filepath.Join(dir, "outside", "secret.txt"), []string{root, filepath.Join(dir, "outside")})
	if err != nil || got != filepath.Join(dir, "outside", "secret.txt") {
		t.Errorf("a path in the second root: %q, %v", got, err)
	}
	if _, err := ResolveWithinAnyRoot(filepath.Join(dir, "outside", "secret.txt"), []string{root}); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("a path in no root: %v", err)
	}
	if _, err := ResolveWithinAnyRoot("", []string{root}); !errors.Is(err, ErrPathRequired) {
		t.Errorf("no path: %v", err)
	}
	if _, err := ResolveWithinAnyRoot(root, nil); !errors.Is(err, ErrOutsideRoots) {
		t.Errorf("no roots: %v", err)
	}
}

func TestResolveUnderRoot(t *testing.T) {
	dir := tree(t, smallTree)
	root := filepath.Join(dir, "root")
	got, err := ResolveUnderRoot(root, "sub/b/file.txt")
	if err != nil || got != filepath.Join(root, "sub", "b", "file.txt") {
		t.Errorf("a nested path: %q, %v", got, err)
	}
	got, err = ResolveUnderRoot(root, `sub\\inner.txt`)
	if err != nil || got != filepath.Join(root, "sub", "inner.txt") {
		t.Errorf("a path with backslashes: %q, %v", got, err)
	}
	for rel, want := range map[string]error{
		"":                    ErrRelativeRequired,
		"a\x00":               ErrInvalidRelative,
		"/etc/passwd":         ErrInvalidRelative,
		`\etc\passwd`:         ErrInvalidRelative,
		"../etc/passwd":       ErrInvalidSegment,
		"a/../../etc/passwd":  ErrInvalidSegment,
		"sub/./inner.txt":     ErrInvalidSegment,
		"link-out/secret.txt": ErrOutside,
	} {
		if got, err := ResolveUnderRoot(root, rel); !errors.Is(err, want) {
			t.Errorf("ResolveUnderRoot(%q) = %q, %v; want %v", rel, got, err, want)
		}
	}
}

// What a Resolve function returns never lies outside the root, whatever
// the name is made of.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{"file.txt", "..", "../x", "sub/inner.txt", `sub\inner.txt`, "link-out/secret.txt", "link-in/../..", "/abs", "a\x00b", "..dots", "", ".", "sub//deep", "link-out", "./x", "~", "sub/../../outside/secret.txt"} {
		f.Add(seed)
	}
	dir := tree(f, smallTree)
	root := filepath.Join(dir, "root")
	realRoot := root
	inside := func(t *testing.T, what, input, got string) {
		real := got
		if evaluated, err := filepath.EvalSymlinks(got); err == nil {
			real = evaluated
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			t.Errorf("%s(%q) = %q, which is outside the root (real path %q)", what, input, got, real)
		}
	}
	f.Fuzz(func(t *testing.T, name string) {
		if got, err := ResolveChild(root, name); err == nil {
			inside(t, "ResolveChild", name, got)
			if filepath.Dir(got) != root {
				t.Errorf("ResolveChild(%q) = %q, which is not directly below the root", name, got)
			}
		}
		if got, err := ResolveUnderRoot(root, name); err == nil {
			inside(t, "ResolveUnderRoot", name, got)
		}
		if got, err := ResolveWithinRoot(filepath.Join(root, name), root); err == nil {
			inside(t, "ResolveWithinRoot", name, got)
		}
		if got, err := ResolveWithinRoot(name, root); err == nil {
			inside(t, "ResolveWithinRoot", name, got)
		}
		if got, err := ResolveWithinAnyRoot(filepath.Join(root, name), []string{root, filepath.Join(root, "sub")}); err == nil {
			inside(t, "ResolveWithinAnyRoot", name, got)
		}
	})
}
