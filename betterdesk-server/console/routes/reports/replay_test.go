package reports_test

import (
	"encoding/json"
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

// TestNodeAnswers replays what the Node console's report routes answered
// with its real engine and database adapter, on SQLite and on PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route
// for the accounts the route decides for, every report and its CSV, saved
// reports of every kind, ids in many spellings. The Go console has to
// answer the same behind its whole pipeline, byte for byte.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		rec := replay.Load(t, db, "node")
		if len(rec.Steps) < 780 {
			t.Fatalf("the recording has %d steps", len(rec.Steps))
		}
		w.Run(rec, checkAnswer)
	})
}
