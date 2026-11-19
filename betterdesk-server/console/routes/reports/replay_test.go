package reports_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Times a run makes itself, wherever they stand: generated_at, the
// default title, a saved report's created_at, the day in the name of an
// export. Seeded times lie before 2020.
var (
	isoNow    = regexp.MustCompile(`20[2-9][0-9]-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z`)
	sqliteNow = regexp.MustCompile(`20[2-9][0-9]-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}`)
	exportDay = regexp.MustCompile(`_(20[2-9][0-9]-[0-9]{2}-[0-9]{2})\.csv"$`)
)

func normalize(s string) string {
	s = isoNow.ReplaceAllString(s, "<now, ISO form>")
	return sqliteNow.ReplaceAllString(s, "<now, SQLite form>")
}

// checkAnswer compares status and body after normalize, and the headers
// the recording kept: Content-Type, and Content-Disposition with the day
// of the export, which has to be today in UTC.
func checkAnswer(t *testing.T, step replay.Step, got replay.Answer) bool {
	t.Helper()
	if step.Method == "" {
		return false
	}
	label := step.Name + " as " + step.Role
	if want, have := normalize(step.Answer), normalize(got.Body); got.Status != step.Status || want != have {
		t.Errorf("%s: %s %s\n   node: %d %s\n     go: %d %s", label, step.Method, step.Path, step.Status, want, got.Status, have)
		return true
	}
	for _, name := range []string{"content-type", "content-disposition"} {
		want := ""
		if raw, kept := step.Extra[name]; kept {
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("%s: header %s: %v", label, name, err)
			}
		}
		have := got.Header.Get(name)
		if name == "content-disposition" && have != "" {
			if m := exportDay.FindStringSubmatch(have); m != nil {
				today := time.Now().UTC()
				if m[1] != today.Format("2006-01-02") && m[1] != today.Add(-time.Minute).Format("2006-01-02") {
					t.Errorf("%s: the export is named after %s, today is %s", label, m[1], today.Format("2006-01-02"))
				}
			}
			want, have = exportDay.ReplaceAllString(want, `_<day>.csv"`), exportDay.ReplaceAllString(have, `_<day>.csv"`)
		}
		if want != have {
			t.Errorf("%s: header %s\n   node: %q\n     go: %q", label, name, want, have)
		}
	}
	return true
}

// reportFixes are the steps of testdata/node_*.json that the bug fixes
// change.
var reportFixes replay.Fixes

// RP1: saving and deleting ask for a session before the role, so someone
// who is not signed in gets 401 where Node answered 403 "Admin access
// required". The recording sends 15 saves and 50 deletes without a
// session, the same on both databases; signedOutWrites lists them.
var _ = reportFixes.Add("RP1", signedOutWrites())

// signedOutWrites are the fixes of the steps of the recording that save or
// delete a report without a session.
func signedOutWrites() map[string]replay.Fix {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_sqlite.json"))
	if err != nil {
		panic(err)
	}
	var rec replay.Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		panic(err)
	}
	steps := map[string]replay.Fix{}
	for _, step := range rec.Steps {
		write := step.Method == "POST" && step.Path == "/api/reports/save" || step.Method == "DELETE"
		if step.Role == "" && write {
			if step.Status != 403 || step.Answer != `{"error":"Admin access required"}` {
				panic("RP1: Node answered " + step.Name + " signed out with " + step.Answer)
			}
			steps[replay.FixKey(step)] = replay.Fix{Status: 401, Answer: `{"error":"Authentication required"}`}
		}
	}
	if len(steps) != 65 {
		panic(fmt.Sprintf("RP1: %d saves and deletes without a session, want 65", len(steps)))
	}
	return steps
}

// TestNodeAnswers replays what the Node console's report routes answered
// with its real engine and database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for the accounts the route decides for, every report and its CSV, saved
// reports of every kind, ids in many spellings. The Go console has to
// answer the same behind its whole pipeline, byte for byte, but for the
// steps the bug fixes change (reportFixes).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		rec := replay.Load(t, db, "node")
		if len(rec.Steps) < 780 {
			t.Fatalf("the recording has %d steps", len(rec.Steps))
		}
		defer reportFixes.Unused(t, db)
		w.Run(rec, reportFixes.Check(w, checkAnswer))
	})
}
