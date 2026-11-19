//go:build linux

package servermgmt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
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

// fileChange is a step of the recording that the Go file browser answers
// otherwise on purpose, fixing the bug it names.
type fileChange struct {
	bug string
	// err is the error the step gives now; result makes the answer it
	// gives now from the recorded one, in the recording's form ({B} for
	// the base)
	err    string
	result func(recorded any) any
	// unchanged: the step changes nothing now, so the tree stays as it was
	// before it
	unchanged bool
	// drop, keep and add edit the recorded tree of this step and of every
	// later one: the entries at or below a dropped path go, those at or
	// below a kept path come back as they were before the scenario's first
	// step, and the added entries come in
	drop, keep []string
	add        [][]any
}

// fileChanges are the steps of the recording where the Go file browser
// differs on purpose, by scenario and step:
//
//   - SM5: a write does not go through a link, also not one that points
//     nowhere (the recording created server/nothing, and outside/planted.txt
//     outside every folder) or to itself.
//   - SM6: a delete or rename acts on a link itself, not on the folder it
//     points to (the recording removed server/sub through link-in, and
//     moved it), and the server's folder itself is neither removed nor
//     moved.
var fileChanges = map[string]map[int]fileChange{
	"writes": {
		17: {bug: "SM5", err: "Not a regular file", drop: []string{"server/nothing"}},
		18: {bug: "SM5", err: "Not a regular file", drop: []string{"outside/planted.txt"}},
		19: {bug: "SM5", err: "Not a regular file"},
		38: {bug: "SM5", result: writesListing},
	},
	"deletes": {
		2:  {bug: "SM6", result: answer(map[string]any{"path": "{B}/server/link-in"}), drop: []string{"server/link-in"}, keep: []string{"server/sub"}},
		3:  {bug: "SM6", err: "ENOENT: no such file or directory, lstat '{B}/server/link-in'"},
		18: {bug: "SM6", err: ErrChangeRoot.Error(), unchanged: true},
	},
	"renames": {
		11: {
			bug: "SM6", result: answer(map[string]any{"from": "{B}/server/link-in", "to": "{B}/server/link-in-renamed"}),
			drop: []string{"server/link-in", "server/link-in-renamed"}, keep: []string{"server/sub"}, add: [][]any{{"server/link-in-renamed", "link", "sub"}},
		},
		20: {bug: "SM6", err: ErrChangeRoot.Error(), unchanged: true},
	},
}

// answer is a change's answer that does not depend on the recorded one.
func answer(v any) func(any) any { return func(any) any { return v } }

// writesListing is the list of the server's folder at the end of the
// writes: server/nothing was never made, and dangling-out still points
// nowhere, so it is listed as a link (the old console left it out once it
// pointed out of the folder).
func writesListing(recorded any) any {
	out := maps.Clone(recorded.(map[string]any))
	var items []any
	for _, item := range out["items"].([]any) {
		entry := item.(map[string]any)
		switch entry["name"] {
		case "nothing":
			continue
		case "dangling":
			link := maps.Clone(entry)
			link["name"], link["path"], link["size"] = "dangling-out", "{B}/server/dangling-out", float64(len("../outside/planted.txt"))
			items = append(items, item, link)
			continue
		}
		items = append(items, item)
	}
	out["items"] = items
	return out
}

// below reports whether a tree entry's path is p or lies below it.
func below(entry []any, p string) bool {
	name := entry[0].(string)
	return name == p || strings.HasPrefix(name, p+"/")
}

// editTree applies the edits of the changes so far to a recorded tree,
// and keeps the snapshot's order: by path, part by part.
func editTree(recorded any, start []any, changes []fileChange) []any {
	var tree [][]any
	for _, e := range recorded.([]any) {
		tree = append(tree, e.([]any))
	}
	for _, c := range changes {
		for _, p := range append(append([]string(nil), c.drop...), c.keep...) {
			var kept [][]any
			for _, e := range tree {
				if !below(e, p) {
					kept = append(kept, e)
				}
			}
			tree = kept
		}
		for _, p := range c.keep {
			for _, e := range start {
				if below(e.([]any), p) {
					tree = append(tree, e.([]any))
				}
			}
		}
		tree = append(tree, c.add...)
	}
	sort.SliceStable(tree, func(i, j int) bool {
		a, b := strings.Split(tree[i][0].(string), "/"), strings.Split(tree[j][0].(string), "/")
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	out := make([]any, len(tree))
	for i, e := range tree {
		out[i] = e
	}
	return out
}

// TestNodeFileVectors replays every call the oracle made of the old
// serverManagement.js on the same tree and the same environment: folder
// lists, list, read, write, mkdir, rename and delete must answer the same
// values and errors and leave the same tree, except where fileChanges
// says otherwise.
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
			start := normalize(treeSnapshot(t, base), base, sys, uid, gid).([]any)
			var changes []fileChange
			var lastTree any = start
			for i, step := range sc.Steps {
				steps++
				change, changed := fileChanges[sc.Name][i]
				wantErr, wantResult := step.Error, step.Result
				if changed {
					changes = append(changes, change)
					if change.err != "" {
						wantErr, wantResult = change.err, nil
					}
					if change.result != nil {
						wantErr, wantResult = "", change.result(step.Result)
					}
				}
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
				if changed {
					where += " (changed by " + change.bug + ")"
				}
				if wantErr != "" || err != nil {
					got := ""
					if err != nil {
						got = normalize(err.Error(), base, sys, uid, gid).(string)
					}
					if got != wantErr {
						t.Errorf("%s: error %q, want %q", where, got, wantErr)
					}
				} else {
					b, _ := json.Marshal(result)
					var got any
					_ = json.Unmarshal(b, &got)
					got = normalize(got, base, sys, uid, gid)
					want := normalize(wantResult, "\x00", "\x01", -1, -1)
					if !reflect.DeepEqual(got, want) {
						gb, _ := json.Marshal(got)
						wb, _ := json.Marshal(want)
						t.Errorf("%s:\n got %.1500s\nwant %.1500s", where, gb, wb)
					}
				}
				if step.Tree != nil {
					want := step.Tree
					switch {
					case changed && change.unchanged:
						want = lastTree
					case len(changes) > 0:
						want = editTree(step.Tree, start, changes)
					}
					lastTree = want
					got := normalize(treeSnapshot(t, base), base, sys, uid, gid)
					if !reflect.DeepEqual(got, want) {
						gb, _ := json.Marshal(got)
						wb, _ := json.Marshal(want)
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
