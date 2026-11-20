package reports

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is a file testdata/nodeoracle/engine.js wrote.
type recording struct {
	Dialect string `json:"dialect"`
	Zone    string `json:"zone"`
	Now     string `json:"now"`
	Types   string `json:"types"`
	Worlds  []struct {
		Name  string            `json:"name"`
		Seed  []json.RawMessage `json:"seed"`
		Cases []struct {
			Body   string  `json:"body"`
			Report *string `json:"report"`
			Error  *string `json:"error"`
			CSV    []struct {
				Section string `json:"section"`
				CSV     string `json:"csv"`
			} `json:"csv"`
		} `json:"cases"`
	} `json:"worlds"`
	CSV []struct {
		Rows string `json:"rows"`
		CSV  string `json:"csv"`
	} `json:"csv"`
}

// zoneNames are the names JavaScript prints in a date for the zones of
// the recordings.
func zoneNames(t time.Time) string {
	switch name, _ := t.Zone(); name {
	case "CET":
		return "Central European Standard Time"
	case "CEST":
		return "Central European Summer Time"
	}
	return DefaultZoneName(t)
}

// bodyOf reads a JSON body the way the console's body parser does.
func bodyOf(t *testing.T, text string) middleware.JSONObject {
	t.Helper()
	var got middleware.JSONObject
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.Body(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(text))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the body parser refused %s: %d", text, rec.Code)
	}
	return got
}

// valueOf is a JSON value of a recording as a request value; "@undefined"
// is undefined.
func valueOf(t *testing.T, text string) jsbind.Value {
	t.Helper()
	if text == "@undefined" {
		return jsbind.Undefined
	}
	return jsbind.Of(bodyOf(t, `{"v":`+text+`}`).Get("v"))
}

// seed runs a statement of a recording with its parameters bound as the
// old drivers bound them. {"buffer": [...]} is a BLOB.
func seed(t *testing.T, db *store.DB, raw json.RawMessage) {
	t.Helper()
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		t.Fatalf("seed %s: %v", raw, err)
	}
	var query string
	var params []json.RawMessage
	if err := json.Unmarshal(pair[0], &query); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(pair[1], &params); err != nil {
		t.Fatal(err)
	}
	var args []any
	for _, p := range params {
		var numbers struct {
			Buffer []int `json:"buffer"`
		}
		if json.Unmarshal(p, &numbers) == nil && numbers.Buffer != nil {
			blob := make([]byte, len(numbers.Buffer))
			for i, n := range numbers.Buffer {
				blob[i] = byte(n)
			}
			args = append(args, blob)
			continue
		}
		bound, err := jsbind.Args(db.Dialect(), 1, valueOf(t, string(p)))
		if err != nil {
			t.Fatalf("seed %s: %v", raw, err)
		}
		args = append(args, bound...)
	}
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("seed %s: %v", raw, err)
	}
}

// engineFix is a case of the recordings whose result a bug fix changes on
// purpose. Go fails with err where it is set; otherwise its report holds
// every text of has and none of lacks.
type engineFix struct {
	bug        string
	err        string
	has, lacks []string
	// dialect limits the fix to one database ("sqlite", "postgres").
	dialect string
}

// engineFixes are the cases of testdata/engine_*.json that the bug fixes
// change, by world and body ("* <body>" for every world): the package's
// list of intended differences.
var engineFixes = map[string]engineFix{}

// fixEngine puts the cases of a bug fix on the list and returns true, so
// the list can be filled in var declarations.
func fixEngine(bug string, cases map[string]engineFix) bool {
	for key, f := range cases {
		if f.dialect != "" {
			key = f.dialect + ": " + key
		}
		if _, twice := engineFixes[key]; twice {
			panic("two fixes for " + key)
		}
		f.bug = bug
		engineFixes[key] = f
	}
	return true
}

// unknownType is the fix of a case whose type names no report.
func unknownType(text string) engineFix {
	return engineFix{err: "Unknown report type: " + text}
}

// RP2: the type is a text that names a report. A list holding the name
// and a name every JavaScript object has are unknown types.
var _ = fixEngine("RP2", map[string]engineFix{
	`empty {"type":["devices"]}`:      unknownType("devices"),
	`empty {"type":[["alerts"]]}`:     unknownType("alerts"),
	`empty {"type":"constructor"}`:    unknownType("constructor"),
	`empty {"type":"__proto__"}`:      unknownType("__proto__"),
	`empty {"type":"toString"}`:       unknownType("toString"),
	`empty {"type":"hasOwnProperty"}`: unknownType("hasOwnProperty"),
})

// engineFixOf is the fix of a case on db, with the key it is listed under.
func engineFixOf(db *store.DB, world, body string) (string, engineFix, bool) {
	for _, key := range []string{world + " " + body, "* " + body} {
		for _, k := range []string{db.Dialect().String() + ": " + key, key} {
			if f, ok := engineFixes[k]; ok {
				return k, f, true
			}
		}
	}
	return "", engineFix{}, false
}

// checkEngineFix compares the result of a case with its fix.
func checkEngineFix(t *testing.T, key string, f engineFix, report *Report, err error) {
	t.Helper()
	if f.err != "" {
		if err == nil || err.Error() != f.err {
			t.Errorf("%s (%s): fix fails with %q, go: %v", key, f.bug, f.err, err)
		}
		return
	}
	if err != nil {
		t.Errorf("%s (%s): go failed: %v", key, f.bug, err)
		return
	}
	got, _ := report.MarshalJSON()
	for _, part := range f.has {
		if !strings.Contains(string(got), part) {
			t.Errorf("%s (%s): the report lacks %s: %s", key, f.bug, part, got)
		}
	}
	for _, part := range f.lacks {
		if strings.Contains(string(got), part) {
			t.Errorf("%s (%s): the report holds %s: %s", key, f.bug, part, got)
		}
	}
}

// TestNodeReports makes every report the Node engine made
// (testdata/engine_*.json, written by testdata/nodeoracle/engine.js) on
// the same rows and compares the JSON and the CSV of every part, byte for
// byte, but for the cases the bug fixes change (engineFixes).
func TestNodeReports(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		files, err := filepath.Glob(filepath.Join("testdata", "engine_"+db.Dialect().String()+"_*.json"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no recordings for %s: %v", db.Dialect(), err)
		}
		used := map[string]bool{}
		for _, file := range files {
			t.Run(filepath.Base(file), func(t *testing.T) { replayRecording(t, db, file, used) })
		}
		for key, f := range engineFixes {
			if (f.dialect == "" || f.dialect == db.Dialect().String()) && !used[key] {
				t.Errorf("the fix of %s names no case of the recordings: %s", f.bug, key)
			}
		}
	})
}

func replayRecording(t *testing.T, db *store.DB, file string, used map[string]bool) {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation(rec.Zone)
	if err != nil {
		t.Fatal(err)
	}
	now, err := time.Parse(time.RFC3339Nano, rec.Now)
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{DB: db, Now: func() time.Time { return now }, Location: loc, ZoneName: zoneNames})
	ctx := context.Background()

	if got, _ := json.Marshal(ListTypes()); string(got) != rec.Types {
		t.Errorf("types:\n node: %s\n   go: %s", rec.Types, got)
	}
	reports, failures := 0, 0
	fail := func(format string, args ...any) {
		failures++
		if failures <= 30 {
			t.Errorf(format, args...)
		}
	}
	for _, world := range rec.Worlds {
		for _, statement := range world.Seed {
			seed(t, db, statement)
		}
		for _, c := range world.Cases {
			reports++
			body := bodyOf(t, c.Body)
			f := Filters{From: jsbind.Of(body.Get("from")), To: jsbind.Of(body.Get("to")), DeviceID: jsbind.Of(body.Get("device_id")), Limit: jsbind.Of(body.Get("limit"))}
			report, err := e.Generate(ctx, jsbind.Of(body.Get("type")), f)
			if key, fix, listed := engineFixOf(db, world.Name, c.Body); listed {
				used[key] = true
				checkEngineFix(t, key, fix, report, err)
				continue
			}
			if c.Error != nil {
				var unknown *UnknownTypeError
				switch {
				case err == nil:
					fail("%s %s: node failed (%s), go made a report", world.Name, c.Body, *c.Error)
				case strings.HasPrefix(*c.Error, "Unknown report type"):
					if !errors.As(err, &unknown) || err.Error() != *c.Error {
						fail("%s %s:\n node: %s\n   go: %v", world.Name, c.Body, *c.Error, err)
					}
				case errors.As(err, &unknown):
					fail("%s %s: node failed (%s), go says %v", world.Name, c.Body, *c.Error, err)
				}
				continue
			}
			if err != nil {
				fail("%s %s: go failed: %v", world.Name, c.Body, err)
				continue
			}
			got, _ := report.MarshalJSON()
			if string(got) != *c.Report {
				fail("%s %s:\n node: %s\n   go: %s", world.Name, c.Body, *c.Report, got)
			}
			for _, s := range c.CSV {
				if csv := e.ToCSV(report.CSVRows(valueOf(t, s.Section))); csv != s.CSV {
					fail("%s %s, csv of %s:\n node: %q\n   go: %q", world.Name, c.Body, s.Section, s.CSV, csv)
				}
			}
		}
	}
	for _, c := range rec.CSV {
		rows, err := goapi.Parse([]byte(c.Rows))
		if err != nil {
			t.Fatal(err)
		}
		list, _ := rows.([]any)
		if got := e.ToCSV(list); got != c.CSV {
			fail("toCsv(%s):\n node: %q\n   go: %q", c.Rows, c.CSV, got)
		}
	}
	if failures > 30 {
		t.Errorf("%d more differences", failures-30)
	}
	if reports < 300 {
		t.Errorf("%s holds %d reports, expected more", file, reports)
	}
	t.Logf("%d reports and %d csv texts of %s compared", reports, len(rec.CSV), file)
}
