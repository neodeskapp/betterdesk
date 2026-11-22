package i18n

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// realLangDir is the console's language folder: BD_LANG_DIR when set,
// else the one in the repository.
var realLangDir = func() string {
	if dir := os.Getenv("BD_LANG_DIR"); dir != "" {
		return dir
	}
	return "../static/files/lang"
}()

// vectors reads a file of test vectors recorded from the Node console
// with i18n-dump.js, which is kept with the notes of the port. The
// package's own JSON reader is used so
// lone surrogates in the vectors survive.
func vectors(t testing.TB, name string) any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	v, err := parseJSON(string(raw))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return v
}

// at walks into parsed JSON: at(v, "a", "b") is v.a.b.
func at(v any, path ...string) any {
	for _, p := range path {
		v = property(v, p)
	}
	return v
}

func str(v any, path ...string) string {
	s, _ := at(v, path...).(string)
	return s
}

func list(v any, path ...string) []any {
	l, _ := at(v, path...).([]any)
	return l
}

func num(v any, path ...string) float64 {
	f, _ := at(v, path...).(float64)
	return f
}

func flag(v any, path ...string) bool {
	b, _ := at(v, path...).(bool)
	return b
}

func strs(v any, path ...string) []string {
	var out []string
	for _, e := range list(v, path...) {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// described writes a value the way the vector script describes results.
func described(v any) (kind, value string) {
	switch x := v.(type) {
	case nil:
		return "null", ""
	case undefinedType:
		return "undefined", ""
	case bool:
		return "boolean", fmt.Sprint(x)
	case float64:
		return "number", numberString(x)
	case string:
		return "string", x
	case []any:
		return "array", stringify(x, "")
	case *Object:
		return "object", stringify(x, "")
	}
	return "unknown", fmt.Sprint(v)
}

func wantDescribed(v any) (kind, value string) {
	kind = str(v, "type")
	switch x := at(v, "value").(type) {
	case string:
		value = x
	case bool:
		value = fmt.Sprint(x)
	}
	return kind, value
}

// logs collects a bundle's log lines.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.lines
	l.lines = nil
	return out
}

// loaded returns a bundle loaded from dir and its log lines.
func loaded(t testing.TB, dir, defaultLang string) (*Bundle, *logs) {
	t.Helper()
	l := &logs{}
	b := New(dir, defaultLang)
	b.Logf = l.logf
	b.Load()
	return b, l
}

// fixtureFile is one file of a fixture folder, relative to it.
type fixtureFile struct {
	name string
	data []byte
}

var (
	fixtureMu    sync.Mutex
	fixtureCache = map[string][]fixtureFile{}
)

// copyDir copies a fixture folder into a new temporary folder. The
// fixture is read once per test run.
func copyDir(t testing.TB, from string) string {
	t.Helper()
	fixtureMu.Lock()
	files, ok := fixtureCache[from]
	if !ok {
		err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(from, path)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			files = append(files, fixtureFile{rel, raw})
			return err
		})
		if err != nil {
			fixtureMu.Unlock()
			t.Fatalf("read %s: %v", from, err)
		}
		fixtureCache[from] = files
	}
	fixtureMu.Unlock()
	to := t.TempDir()
	for _, f := range files {
		dst := filepath.Join(to, f.name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, f.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return to
}

// varSets are the placeholder sets the translate vectors refer to by index.
func varSets(t testing.TB) []Vars {
	t.Helper()
	var sets []Vars
	for _, set := range list(vectors(t, "varsets.json")) {
		if set == nil {
			sets = append(sets, nil)
			continue
		}
		vars := Vars{}
		for _, pair := range set.([]any) {
			name := str(pair, "0")
			var value any
			switch str(pair, "1", "t") {
			case "s":
				value = str(pair, "1", "v")
			case "b":
				value = flag(pair, "1", "v")
			case "null":
				value = nil
			case "undef":
				value = Undefined
			case "n":
				text := str(pair, "1", "v")
				if text == "-0" {
					value = negZero()
				} else {
					value = parseFloat(strings.TrimSpace(text))
				}
			}
			vars = append(vars, Var{Name: name, Value: value})
		}
		sets = append(sets, vars)
	}
	return sets
}

func negZero() float64 {
	zero := 0.0
	return -zero
}

// checkTranslate runs the translate vectors of one recorded bundle.
func checkTranslate(t *testing.T, b *Bundle, cases []any, sets []Vars) (ran, builtin int) {
	t.Helper()
	for _, c := range cases {
		lang, key := str(c, "lang"), str(c, "key")
		var vars Vars
		if idx := int(num(c, "vars")); idx >= 0 {
			vars = sets[idx]
			if vars == nil {
				vars = Vars{}
			}
		}
		wantKind, want := wantDescribed(at(c, "result"))
		if flag(c, "builtin") || wantKind == "function" {
			// Node found a property of a JavaScript built-in here.
			builtin++
			continue
		}
		gotKind, got := described(b.Translate(lang, key, vars))
		if gotKind != wantKind || got != want {
			t.Errorf("Translate(%q, %q, set %v) = %s %q, Node gave %s %q", lang, key, num(c, "vars"), gotKind, got, wantKind, want)
		}
		ran++
	}
	return ran, builtin
}
