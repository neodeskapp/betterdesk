package advancedconfig

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The vectors are what services/advancedConfigService.js of the Node
// console did in the scenarios of testdata/vectors.json.gz:
// per step the result or the error, and the files afterwards. The test
// runs the same scenarios on a temporary folder.

type vectorScenario struct {
	Name  string            `json:"name"`
	Env   map[string]string `json:"env"`
	Setup [][]any           `json:"setup"`
	Steps []vectorStep      `json:"steps"`
}

type vectorStep struct {
	Step   []any        `json:"step"`
	Result any          `json:"result"`
	Error  *string      `json:"error"`
	Tree   []vectorFile `json:"tree"`
}

type vectorFile struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   string `json:"mode,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

func loadVectors(t *testing.T) []vectorScenario {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "vectors.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []vectorScenario
	if err := json.NewDecoder(gz).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// undefinedValue stands for JavaScript's undefined in a step.
type undefinedValue struct{}

// stepValue turns a value of a step into what the Go call takes.
func stepValue(v any, root string) any {
	if m, ok := v.(map[string]any); ok {
		if m["undefined"] == true {
			return undefinedValue{}
		}
		if r, ok := m["repeat"].(string); ok {
			return strings.Repeat(r, int(m["n"].(float64)))
		}
		if r, ok := m["root"].(string); ok {
			return root + r
		}
	}
	return v
}

func setupFiles(t *testing.T, root string, steps [][]any) {
	t.Helper()
	for _, step := range steps {
		op, rel := step[0].(string), step[1].(string)
		p := filepath.Join(root, rel)
		switch op {
		case "dir":
			if err := os.MkdirAll(p, 0o777); err != nil {
				t.Fatal(err)
			}
		case "file":
			if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, contentOf(step[2]), 0o666); err != nil {
				t.Fatal(err)
			}
			if len(step) > 3 {
				if err := os.Chmod(p, fs.FileMode(step[3].(float64))); err != nil {
					t.Fatal(err)
				}
			}
		case "symlink":
			if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
				t.Fatal(err)
			}
			target := step[2].(string)
			if !strings.HasPrefix(target, "/") {
				target = filepath.Join(root, target)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("setup %s", op)
		}
	}
}

func contentOf(spec any) []byte {
	switch v := spec.(type) {
	case string:
		return []byte(v)
	case map[string]any:
		if r, ok := v["repeat"].(string); ok {
			return []byte(strings.Repeat(r, int(v["n"].(float64))))
		}
		var out []byte
		for _, b := range v["bytes"].([]any) {
			out = append(out, byte(b.(float64)))
		}
		return out
	}
	panic(fmt.Sprintf("content %v", spec))
}

func treeOf(t *testing.T, root string) []vectorFile {
	t.Helper()
	var out []vectorFile
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			p := filepath.Join(dir, name)
			rel, _ := filepath.Rel(root, p)
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			entry := vectorFile{Path: rel, Mode: strconv.FormatUint(uint64(info.Mode().Perm()), 8)}
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, _ := os.Readlink(p)
				entry = vectorFile{Path: rel, Type: "symlink", Target: strings.ReplaceAll(target, root, "<root>")}
			case info.IsDir():
				entry.Type = "dir"
				out = append(out, entry)
				walk(p)
				continue
			default:
				data, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(data)
				entry.Type, entry.Size, entry.SHA256 = "file", int64(len(data)), hex.EncodeToString(sum[:])
			}
			out = append(out, entry)
		}
	}
	walk(root)
	return out
}

var isoTime = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

// cleanJSON replaces the root with <root> and checks and hides the times,
// as the oracle did.
func cleanJSON(t *testing.T, v any, root string) any {
	switch x := v.(type) {
	case string:
		return strings.ReplaceAll(x, root, "<root>")
	case []any:
		for i := range x {
			x[i] = cleanJSON(t, x[i], root)
		}
		return x
	case map[string]any:
		for k, val := range x {
			if s, ok := val.(string); ok && k == "mtime" {
				if !isoTime.MatchString(s) {
					t.Errorf("mtime %q is not an ISO time", s)
				}
				x[k] = "<mtime>"
				continue
			}
			x[k] = cleanJSON(t, val, root)
		}
		return x
	}
	return v
}

func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := httpx.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// truthy is JavaScript's !!v for a value of a step.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedValue:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}

func TestNodeVectors(t *testing.T) {
	scenarios := loadVectors(t)
	steps, fixed := 0, 0
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "web-nodejs"), 0o777); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			for k, v := range sc.Env {
				env[k] = strings.ReplaceAll(v, "<root>", root)
			}
			setupFiles(t, root, sc.Setup)
			tick := 0
			s := New(Options{
				ConsoleDir: filepath.Join(root, "web-nodejs"),
				DataDir:    filepath.Join(root, "data"),
				KeysPath:   filepath.Join(root, "keys"),
				Getenv:     func(k string) string { return env[k] },
				Platform:   "linux",
				Now: func() time.Time {
					tick++
					return time.Date(2026, 1, 1, 0, 0, tick-1, 0, time.UTC)
				},
			})
			for i, step := range sc.Steps {
				steps++
				name := fmt.Sprintf("step %d %v", i, step.Step)
				var result any
				var err error
				op := step.Step[0].(string)
				args := make([]any, len(step.Step)-1)
				for j, a := range step.Step[1:] {
					args[j] = stepValue(a, root)
				}
				idIsText := false
				if len(args) > 0 {
					_, idIsText = args[0].(string)
				}
				switch {
				case op == "list":
					result = s.ListFiles()
				case op == "read" && idIsText:
					result, err = s.ReadFile(args[0].(string))
				case op == "write" && idIsText:
					content := args[1]
					if _, isUndefined := content.(undefinedValue); isUndefined {
						content = nil
					}
					result, err = s.WriteFile(args[0].(string), content)
				case op == "read" || op == "write":
					if _, ok := s.lookup(args[0]); ok {
						t.Fatalf("%s: an id that is not text was found", name)
					}
					err = ErrUnknownFile
				case op == "restore":
					created := len(args) > 2 && truthy(args[2])
					id := args[0]
					if _, isUndefined := id.(undefinedValue); isUndefined {
						id = nil
					}
					before := treeOf(t, root)
					err = s.RestoreBackup(id, args[1], created)
					if failedCopyKeepsTheFile(t, root, step, before) {
						fixed++
					}
				default:
					t.Fatalf("unknown step %s", op)
				}
				switch {
				case step.Error != nil:
					if err == nil {
						t.Errorf("%s: no error, want %q", name, *step.Error)
					} else if got := strings.ReplaceAll(err.Error(), root, "<root>"); got != *step.Error {
						t.Errorf("%s: error %q, want %q", name, got, *step.Error)
					}
				case err != nil:
					t.Errorf("%s: error %v", name, err)
				case op != "restore":
					got := cleanJSON(t, asJSON(t, result), root)
					if !reflect.DeepEqual(got, step.Result) {
						gb, _ := json.Marshal(got)
						wb, _ := json.Marshal(step.Result)
						t.Errorf("%s:\n got  %.600s\n want %.600s", name, gb, wb)
					}
				}
				if got := treeOf(t, root); !reflect.DeepEqual(got, step.Tree) {
					t.Errorf("%s: files\n got  %+v\n want %+v", name, got, step.Tree)
				}
			}
		})
	}
	if steps < 150 {
		t.Fatalf("only %d steps in the vectors", steps)
	}
	if fixed == 0 {
		t.Error("no restore of the vectors failed in its copy (BUGS SE3)")
	}
}

// failedCopyKeepsTheFile is BUGS SE3 in the replay: a restore whose copy
// fails leaves the configuration file as it was, where Node's copyFile
// had removed it. It checks that the step changed no file, then removes
// what Node's step removed, so the next steps start from Node's folder,
// and reports whether the step was one of these.
func failedCopyKeepsTheFile(t *testing.T, root string, step vectorStep, before []vectorFile) bool {
	t.Helper()
	if step.Error == nil || !strings.Contains(*step.Error, ", copyfile '") {
		return false
	}
	if got := treeOf(t, root); !reflect.DeepEqual(got, before) {
		t.Errorf("a restore that failed in its copy changed the files:\n got  %+v\n were %+v", got, before)
	}
	kept := map[string]bool{}
	for _, f := range step.Tree {
		kept[f.Path] = true
	}
	for _, f := range before {
		if !kept[f.Path] {
			if err := os.Remove(filepath.Join(root, f.Path)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return true
}
