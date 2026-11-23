package updater

import (
	"encoding/json"
	"strings"
	"testing"
)

var rules = ManifestRules{Owner: "neodeskapp", Repo: "neodesk", Branch: "main"}

func manifestJSON(mutate func(m map[string]any)) []byte {
	m := map[string]any{
		"schema_version": 1, "product": "neodesk-server", "version": "3.6.0", "channel": "stable",
		"created": "2026-11-18T10:00:00Z", "owner": "neodeskapp", "repo": "neodesk", "branch": "main", "sha": newCommit,
		"files": []any{map[string]any{"path": asset, "sha256": strings.Repeat("a", 64), "size": 10}},
	}
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	return data
}

func TestParseManifest(t *testing.T) {
	m, err := ParseManifest(manifestJSON(nil), rules)
	if err != nil || m.Version != "3.6.0" || m.File(asset) == nil || m.File("x") != nil {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParseManifest(manifestJSON(func(m map[string]any) { m["owner"] = "NeoDeskApp" }), rules); err != nil {
		t.Errorf("owner in another case: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"schema 2":            func(m map[string]any) { m["schema_version"] = 2 },
		"product":             func(m map[string]any) { m["product"] = "other" },
		"version":             func(m map[string]any) { m["version"] = "3.6" },
		"version with a path": func(m map[string]any) { m["version"] = "3.6.0-../../x" },
		"min rollback":        func(m map[string]any) { m["min_rollback_version"] = "x" },
		"short sha":           func(m map[string]any) { m["sha"] = "abc1234" },
		"upper-case sha":      func(m map[string]any) { m["sha"] = strings.ToUpper("abcdef" + newCommit[6:]) },
		"owner":               func(m map[string]any) { m["owner"] = "evil" },
		"repo":                func(m map[string]any) { m["repo"] = "Other" },
		"branch":              func(m map[string]any) { m["branch"] = "dev"; m["channel"] = "development" },
		"channel":             func(m map[string]any) { m["channel"] = "development" },
		"no files":            func(m map[string]any) { m["files"] = []any{} },
		"file outside": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": "../neodesk-server-linux-amd64", "sha256": strings.Repeat("a", 64)}}
		},
		"file name with a slash": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": "neodesk-server-linux/amd64", "sha256": strings.Repeat("a", 64)}}
		},
		"other file": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": "install.sh", "sha256": strings.Repeat("a", 64)}}
		},
		"bad sha256": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": asset, "sha256": strings.Repeat("A", 64)}}
		},
		"negative size": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": asset, "sha256": strings.Repeat("a", 64), "size": -1}}
		},
		"twice": func(m map[string]any) {
			f := map[string]any{"path": asset, "sha256": strings.Repeat("a", 64)}
			m["files"] = []any{f, f}
		},
		"path not text": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": 5, "sha256": strings.Repeat("a", 64)}}
		},
	} {
		if _, err := ParseManifest(manifestJSON(mutate), rules); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, text := range []string{"", "null", "[]", "{} {}", strings.Repeat(" ", maxManifestBytes+1)} {
		if _, err := ParseManifest([]byte(text), rules); err == nil {
			t.Errorf("%q: accepted", text[:min(len(text), 10)])
		}
	}
}
