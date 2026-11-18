//go:build linux

package servermgmt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
)

// nodeFiles is testdata/node_files.json, written by
// testdata/nodeoracle/files.js from the old console's serverManagement.js.
type nodeFiles struct {
	Tree      [][]any `json:"tree"`
	Scenarios []struct {
		Name  string            `json:"name"`
		Env   map[string]string `json:"env"`
		Cwd   string            `json:"cwd"`
		Steps []struct {
			Op     string `json:"op"`
			Args   []any  `json:"args"`
			Result any    `json:"result"`
			Error  string `json:"error"`
			Tree   any    `json:"tree"`
		} `json:"steps"`
	} `json:"scenarios"`
}

// buildTree makes the oracle's tree in base.
func buildTree(t *testing.T, base string, tree [][]any) {
	t.Helper()
	for _, entry := range tree {
		kind, rel := entry[0].(string), entry[1].(string)
		full := filepath.Join(base, rel)
		var err error
		switch kind {
		case "dir":
			err = os.Mkdir(full, 0o777)
		case "file":
			extra := entry[2].(map[string]any)
			var data []byte
			switch {
			case extra["text"] != nil:
				data = []byte(extra["text"].(string))
			case extra["hex"] != nil:
				data, err = hex.DecodeString(extra["hex"].(string))
			default:
				rep := extra["repeat"].([]any)
				data = []byte(strings.Repeat(rep[0].(string), int(rep[1].(float64))))
				if tail, ok := extra["tail"].(string); ok {
					more, _ := hex.DecodeString(tail)
					data = append(data, more...)
				}
			}
			if err == nil {
				err = os.WriteFile(full, data, 0o666)
			}
			if mode, ok := extra["mode"].(float64); ok && err == nil {
				err = os.Chmod(full, fileMode(uint32(mode)))
			}
		case "link":
			err = os.Symlink(entry[2].(string), full)
		case "sparse":
			if err = os.WriteFile(full, nil, 0o666); err == nil {
				err = os.Truncate(full, int64(entry[2].(float64)))
			}
		case "fifo":
			err = syscall.Mkfifo(full, 0o666)
		}
		if err != nil {
			t.Fatalf("build %s %s: %v", kind, rel, err)
		}
	}
}

// fileMode turns permission bits with setuid, setgid and sticky into an
// os.FileMode.
func fileMode(bits uint32) os.FileMode {
	m := os.FileMode(bits & 0o777)
	if bits&0o4000 != 0 {
		m |= os.ModeSetuid
	}
	if bits&0o2000 != 0 {
		m |= os.ModeSetgid
	}
	if bits&0o1000 != 0 {
		m |= os.ModeSticky
	}
	return m
}

// treeSnapshot is the oracle's snapshot(): every entry below base with
// its kind, and size, permission bits and content hash or link target.
func treeSnapshot(t *testing.T, base string) []any {
	t.Helper()
	out := []any{}
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		sort.Strings(names)
		for _, name := range names {
			full := filepath.Join(dir, name)
			rel, _ := filepath.Rel(base, full)
			info, err := os.Lstat(full)
			if err != nil {
				t.Fatalf("lstat %s: %v", full, err)
			}
			st := info.Sys().(*syscall.Stat_t)
			bits := float64(st.Mode & 0o7777)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				target, _ := os.Readlink(full)
				out = append(out, []any{rel, "link", target})
			case info.IsDir():
				out = append(out, []any{rel, "dir", bits})
				walk(full)
			case info.Mode()&os.ModeNamedPipe != 0:
				out = append(out, []any{rel, "fifo"})
			default:
				data, _ := os.ReadFile(full)
				sum := sha256.Sum256(data)
				out = append(out, []any{rel, "file", float64(info.Size()), bits, hex.EncodeToString(sum[:])[:16]})
			}
		}
	}
	if _, err := os.Stat(base); err == nil {
		walk(base)
	}
	return out
}

var isoFormat = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

// onlyBelow is a change guard for tests: a change outside dir fails the
// test and does not happen, whatever a broken check let through.
func onlyBelow(t *testing.T, dir string) func(paths ...string) error {
	return func(paths ...string) error {
		for _, p := range paths {
			if p != dir && !strings.HasPrefix(p, dir+string(filepath.Separator)) {
				t.Errorf("a change outside the test's folder got through the checks: %s", p)
				return fmt.Errorf("test guard: %s is outside %s", p, dir)
			}
		}
		return nil
	}
}

// normalize makes a result comparable with the recording: {B} for the
// base, the fixed folders by their real names, a file's time by its
// format, a folder's size and the test's own user and group ids as the
// recording's root, and a long content by its length, hash and start.
func normalize(v any, base, sys string, uid, gid float64) any {
	switch x := v.(type) {
	case string:
		x = strings.ReplaceAll(x, sys, "")
		return strings.ReplaceAll(x, base, "{B}")
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e, base, sys, uid, gid)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalize(e, base, sys, uid, gid)
		}
		if m, ok := out["mtime"].(string); ok && isoFormat.MatchString(m) {
			out["mtime"] = "<time>"
		}
		if dir, _ := out["isDirectory"].(bool); dir {
			if _, ok := out["size"].(float64); ok {
				out["size"] = "<size>"
			}
		}
		for _, key := range []string{"uid", "gid"} {
			if n, ok := out[key].(float64); ok && (key == "uid" && n == uid || key == "gid" && n == gid) {
				out[key] = 0.0
			}
		}
		if c, ok := out["content"].(string); ok {
			units := utf16.Encode([]rune(c))
			if len(units) > 2000 {
				sum := sha256.Sum256([]byte(c))
				out["content"] = map[string]any{
					"length": float64(len(units)), "sha256": hex.EncodeToString(sum[:]), "head": string(utf16.Decode(units[:100])),
				}
			}
		}
		return out
	}
	return v
}

// TestNodeFileVectors replays every call the oracle made of the old
// serverManagement.js on the same tree and the same environment: folder
// lists, list, read, write, mkdir, rename and delete must answer the same
// values and errors and leave the same tree.
func TestNodeFileVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/node_files.json")
	if err != nil {
		t.Fatal(err)
	}
	var rec nodeFiles
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	uid, gid := float64(os.Getuid()), float64(os.Getgid())
	steps := 0
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			// The tree in b, the folders every install may read in sys. The
			// folder's name must not hold a comma, which separates folders
			// in the environment.
			root, err := os.MkdirTemp("", "smfiles")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(root) })
			base, sys := filepath.Join(root, "b"), filepath.Join(root, "sys")
			if err := os.Mkdir(base, 0o777); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{"var/log", "tmp", "var/tmp"} {
				if err := os.MkdirAll(filepath.Join(sys, dir), 0o777); err != nil {
					t.Fatal(err)
				}
			}
			buildTree(t, base, rec.Tree)
			real := func(a any) string {
				if m, ok := a.(map[string]any); ok {
					rep := m["repeat"].([]any)
					return strings.Repeat(rep[0].(string), int(rep[1].(float64)))
				}
				return strings.ReplaceAll(a.(string), "{B}", base)
			}
			env := map[string]string{}
			for k, v := range sc.Env {
				env[k] = real(v)
			}
			s := New(Options{
				Getenv: func(k string) string { return env[k] }, WorkDir: real(sc.Cwd), GOOS: "linux",
				LogDir: sys + "/var/log", TempDirs: []string{sys + "/tmp", sys + "/var/tmp"},
				beforeChange: onlyBelow(t, base),
			})
			for i, step := range sc.Steps {
				steps++
				args := make([]string, len(step.Args))
				for j, a := range step.Args {
					args[j] = real(a)
				}
				var result any
				switch step.Op {
				case "roots":
					result = map[string]any{"read": s.ReadRoots(), "write": s.WriteRoots()}
					err = nil
				case "list":
					result, err = s.ListDirectory(args[0])
				case "read":
					result, err = s.ReadFilePreview(args[0])
				case "write":
					result, err = s.WriteFile(args[0], args[1])
				case "mkdir":
					result, err = s.MakeDirectory(args[0])
				case "rename":
					result, err = s.RenamePath(args[0], args[1])
				case "delete":
					result, err = s.DeletePath(args[0])
				default:
					t.Fatalf("unknown op %s", step.Op)
				}
				where := fmt.Sprintf("step %d: %s %q", i, step.Op, step.Args)
				if step.Error != "" || err != nil {
					got := ""
					if err != nil {
						got = normalize(err.Error(), base, sys, uid, gid).(string)
					}
					if got != step.Error {
						t.Errorf("%s: error %q, want %q", where, got, step.Error)
					}
				} else {
					b, _ := json.Marshal(result)
					var got any
					_ = json.Unmarshal(b, &got)
					got = normalize(got, base, sys, uid, gid)
					want := normalize(step.Result, "\x00", "\x01", -1, -1)
					if !reflect.DeepEqual(got, want) {
						gb, _ := json.Marshal(got)
						wb, _ := json.Marshal(want)
						t.Errorf("%s:\n got %.1500s\nwant %.1500s", where, gb, wb)
					}
				}
				if step.Tree != nil {
					got := normalize(treeSnapshot(t, base), base, sys, uid, gid)
					if !reflect.DeepEqual(got, step.Tree) {
						gb, _ := json.Marshal(got)
						wb, _ := json.Marshal(step.Tree)
						t.Errorf("%s: tree\n got %.3000s\nwant %.3000s", where, gb, wb)
					}
				}
			}
		})
	}
	if steps < 250 {
		t.Errorf("only %d steps replayed", steps)
	}
}
