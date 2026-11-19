package phase45_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// writeReleaseCache puts the release cache of testdata/nodeoracle/releases.json,
// which the Node routes answered from, into the console's data folder.
func writeReleaseCache(t *testing.T, dataDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "nodeoracle", "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cache struct {
		Files map[string]string `json:"files"`
		Dirs  []string          `json:"dirs"`
	}
	if err := json.Unmarshal(raw, &cache); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dataDir, "modules", "betterdesk-support-releases")
	for rel, content := range cache.Files {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range cache.Dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNodeAnswers replays what the Node console's routes/phase4_5.routes.js
// answered with its real database adapter, avatar service and release
// cache, on SQLite and on PostgreSQL (testdata/node_sqlite.json,
// testdata/node_postgres.json): the profile with stored values of every
// kind, saves of every field with values of every JSON type, the consent
// popup's data, templates, enrollment, the release list, the downloads
// with their headers and the portal's installers. The audit log and the
// stored pictures are compared after the requests.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		writeReleaseCache(t, w.Config.DataDir)
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			// A new template's token is random: 48 hex digits in place of
			// each other
			if strings.HasPrefix(step.Path, "/api/agent-templates") {
				want, have := randomToken.ReplaceAllString(replay.Normalize(step.Answer), "<token>"), randomToken.ReplaceAllString(replay.Normalize(got.Body), "<token>")
				if got.Status != step.Status || have != want {
					t.Errorf("step %q: %d %s\n   node: %d %s", step.Name, got.Status, have, step.Status, want)
				}
				return true
			}
			raw, ok := step.Extra["headers"]
			if !ok {
				return false
			}
			// Express matched a path with a "." or ".." segment as a
			// parameter; the console's route table matches no such path
			// (kit.Router), so without the Node console it is the 404 of
			// an unknown address. While Node runs behind Go, Node answers
			// these paths as before.
			if dotSegment(step.Path) {
				if got.Status != http.StatusNotFound || got.Body != `{"success":false,"error":"Not Found"}` {
					t.Errorf("step %q: %d %q, want the 404 of an unknown address", step.Name, got.Status, got.Body)
				}
				return true
			}
			var text string
			var want map[string]*string
			if err := json.Unmarshal(raw, &text); err != nil || json.Unmarshal([]byte(text), &want) != nil {
				t.Fatalf("step %q: headers %s", step.Name, raw)
			}
			for name, value := range want {
				have := got.Header.Get(name)
				if (value == nil && have != "") || (value != nil && *value != have) {
					t.Errorf("step %q: header %s: node %v, go %q", step.Name, name, deref(value), have)
				}
			}
			if got.Status != step.Status || got.Body != step.Answer {
				t.Errorf("step %q: node %d %q, go %d %q", step.Name, step.Status, step.Answer, got.Status, got.Body)
			}
			return true
		})
	})
}

func dotSegment(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// randomToken is an enrollment token as a new template gets it.
var randomToken = regexp.MustCompile(`"[0-9a-f]{48}"`)
