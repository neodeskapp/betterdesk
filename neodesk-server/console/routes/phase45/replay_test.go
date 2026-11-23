package phase45_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// writeReleaseCache puts the release cache of testdata/releases.json,
// which the Node routes answered from, into the console's data folder.
func writeReleaseCache(t *testing.T, dataDir string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "releases.json"))
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
	root := filepath.Join(dataDir, "modules", "neodesk-support-releases")
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
//
// A profile save keeps the fields its body leaves out, where Node emptied
// them (BUGS C3): keptFields checks that at every save and then empties
// them as Node did, so the steps after it start from Node's profile.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		writeReleaseCache(t, w.Config.DataDir)
		kept, keptCount := keptFields(w)
		defer func() {
			if *keptCount == 0 {
				t.Error("no profile save left a field out")
			}
		}()
		w.Run(replay.Load(t, db, "node"), func(t *testing.T, step replay.Step, got replay.Answer) bool {
			kept(t, step)
			// A new template's token is random: 48 hex digits in place of
			// each other
			if strings.HasPrefix(step.Path, "/api/agent-templates") {
				// UM3: a name that is taken is name_exists on PostgreSQL
				// too, where Node looked for SQLite's "UNIQUE" in the
				// message and answered the database's text with 500
				if strings.Contains(step.Answer, "duplicate key value violates unique constraint") {
					step.Status, step.Answer = http.StatusConflict, `{"success":false,"error":"name_exists"}`
				}
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

// profileColumns are the profile fields a save writes.
var profileColumns = []string{"first_name", "last_name", "email", "phone", "role_display", "avatar_url"}

// keptFields follows the profiles through the recording. At a profile
// save Node stored, each field the body leaves out must still hold what
// it held before the save (BUGS C3); it is then emptied, as Node's save
// emptied it. count is the number of fields so checked.
func keptFields(w *replay.World) (check func(t *testing.T, step replay.Step), count *int) {
	count = new(int)
	last := map[string]map[string]any{}
	read := func(role string) map[string]any {
		var rows []map[string]any
		raw := w.Rows("SELECT "+strings.Join(profileColumns, ", ")+" FROM users WHERE username = ?", []any{"e2e_" + role})
		if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) != 1 {
			w.T.Fatalf("the profile of %s: %s", role, raw)
		}
		return rows[0]
	}
	return func(t *testing.T, step replay.Step) {
		t.Helper()
		if step.Role == "" {
			return
		}
		if step.Method == http.MethodPut && step.Path == "/api/users/me/profile" && step.Status == http.StatusOK {
			var body map[string]json.RawMessage
			if step.Body != nil {
				_ = json.Unmarshal([]byte(*step.Body), &body)
			}
			before, after := last[step.Role], read(step.Role)
			for _, column := range profileColumns {
				if _, sent := body[column]; sent {
					continue
				}
				if before != nil {
					*count++
					if after[column] != before[column] {
						t.Errorf("step %q (C3): %s is %v, it was %v before the save", step.Name, column, after[column], before[column])
					}
				}
				w.Exec("UPDATE users SET "+column+" = '' WHERE username = ?", []any{"e2e_" + step.Role})
			}
		}
		last[step.Role] = read(step.Role)
	}, count
}

// randomToken is an enrollment token as a new template gets it.
var randomToken = regexp.MustCompile(`"[0-9a-f]{48}"`)
