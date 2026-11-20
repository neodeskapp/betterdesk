package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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

// engineRule is a bug fix that changes the reports of every case of a
// world in one way: where Go's report differs from Node's, same compares
// the two (Node's first).
type engineRule struct {
	bug, world string
	same       func(node, got string) error
}

// engineRules are the fixes of whole worlds, after engineFixes.
var engineRules []engineRule

func ruleEngine(bug string, same func(node, got string) error, worlds ...string) bool {
	for _, world := range worlds {
		engineRules = append(engineRules, engineRule{bug: bug, world: world, same: same})
	}
	return true
}

// RP3: names are counted each as its own key, so a name every JavaScript
// object has counts from 1 (Node counted the inherited function's text,
// and never __proto__), and apps with such names stay in the top
// applications.
var _ = ruleEngine("RP3", withoutObjectMembers, "fleet", "many")

// objectMembers are the names every JavaScript object has.
var objectMembers = map[string]bool{
	"constructor": true, "__proto__": true, "__defineGetter__": true, "__defineSetter__": true, "hasOwnProperty": true,
	"__lookupGetter__": true, "__lookupSetter__": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"toString": true, "valueOf": true, "toLocaleString": true,
}

// withoutObjectMembers compares two reports without the names every
// JavaScript object has: their counts leave the distributions and their
// apps the top applications. What is left of Go's top applications comes
// first in Node's, as Go's list makes room for the apps it keeps.
func withoutObjectMembers(node, got string) error {
	if strings.Contains(got, "[native code]") {
		return errors.New("go counts a name as a function's text")
	}
	var reports [2]struct {
		Rest map[string]any
		Data map[string]any
	}
	for i, text := range []string{node, got} {
		if err := json.Unmarshal([]byte(text), &reports[i].Rest); err != nil {
			return err
		}
		reports[i].Data, _ = reports[i].Rest["data"].(map[string]any)
		delete(reports[i].Rest, "data")
		for key, v := range reports[i].Data {
			if counts, ok := v.(map[string]any); ok && strings.HasSuffix(key, "_distribution") {
				for name := range counts {
					if objectMembers[name] {
						delete(counts, name)
					}
				}
			}
		}
		if apps, ok := reports[i].Data["top_applications"].([]any); ok {
			kept := []any{}
			for _, app := range apps {
				if name, _ := app.(map[string]any)["name"].(string); !objectMembers[name] {
					kept = append(kept, app)
				}
			}
			reports[i].Data["top_applications"] = kept
		}
	}
	nodeApps, _ := reports[0].Data["top_applications"].([]any)
	goApps, _ := reports[1].Data["top_applications"].([]any)
	if len(goApps) > len(nodeApps) || !reflect.DeepEqual(goApps, nodeApps[:len(goApps)]) {
		return fmt.Errorf("the top applications differ in more than the names every object has")
	}
	if goApps != nil {
		reports[0].Data["top_applications"], reports[1].Data["top_applications"] = nil, nil
	}
	if !reflect.DeepEqual(reports[0], reports[1]) {
		return fmt.Errorf("the reports differ in more than the names every object has")
	}
	return nil
}

// RP4: a device whose info is the JSON null, or on SQLite whose tags are
// no text, is read with info {}; the device and security reports, which
// Node failed, count it.
var _ = fixEngine("RP4", map[string]engineFix{
	`device info that is null {"type":"devices"}`: {has: []string{
		`"total_devices":2,"online":0,"offline":2,"banned":0,"platform_distribution":{"Fine":1,"Unknown":1}`,
	}},
	`device info that is null {"type":"security"}`: {has: []string{`"banned_devices":[]`}},
	`device tags that are no text {"type":"devices"}`: {dialect: "sqlite", has: []string{
		`"total_devices":1,"online":0,"offline":1,"banned":0,"platform_distribution":{"Unknown":1}`,
	}},
	`device tags that are no text {"type":"security"}`: {dialect: "sqlite", has: []string{`"banned_devices":[]`}},
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

// TestNamesEveryObjectHasCount: a platform, ticket category or app named
// after a member every JavaScript object has counts like any other name,
// and such an app stays in the top applications (BUGS.md RP3).
func TestNamesEveryObjectHasCount(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO peer (id, info, status_online, is_banned, is_deleted) VALUES ('1', '{"os":"constructor"}', FALSE, FALSE, FALSE),
				('2', '{"os":"__proto__"}', FALSE, FALSE, FALSE), ('3', '{"os":"constructor"}', FALSE, FALSE, FALSE)`,
			`INSERT INTO tickets (title, created_by, category, priority) VALUES ('a', 'x', '__proto__', 'valueOf'), ('b', 'x', 'toString', 'low')`,
			`INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at) VALUES ('A', 60, 0, '2019-11-01T00:00:00.000Z', '2019-11-01 00:00:00')`,
			`INSERT INTO activity_sessions (device_id, app_name, category, started_at, ended_at, duration_secs) VALUES
				('A', 'constructor', 'x', '2019-11-01T01:00:00.000Z', '2019-11-01T02:00:00.000Z', 30),
				('A', '__proto__', 'y', '2019-11-01T02:00:00.000Z', '2019-11-01T03:00:00.000Z', 20),
				('A', 'Chrome', 'browser', '2019-11-01T03:00:00.000Z', '2019-11-01T04:00:00.000Z', 10)`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		e := New(Options{DB: db})
		for typ, want := range map[string]string{
			"devices": `"platform_distribution":{"constructor":2,"__proto__":1}`,
			"tickets": `"category_distribution":{"__proto__":1,"toString":1},"priority_distribution":{"valueOf":1,"low":1}`,
			"activity": `"top_applications":[{"name":"constructor","category":"x","total_secs":30,"sessions":1,"devices":1},` +
				`{"name":"__proto__","category":"y","total_secs":20,"sessions":1,"devices":1},{"name":"Chrome","category":"browser","total_secs":10,"sessions":1,"devices":1}]`,
		} {
			report, err := e.Generate(ctx, jsbind.String(typ), Filters{})
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			if got, _ := report.MarshalJSON(); !strings.Contains(string(got), want) {
				t.Errorf("%s: %s\nwant %s", typ, got, want)
			}
		}
	})
}

// TestReportsSurviveADeviceWithNullInfo: a device whose info is the JSON
// null, or on SQLite whose tags are no text, is read with info {} and
// cannot fail the device and security reports (BUGS.md RP4).
func TestReportsSurviveADeviceWithNullInfo(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		queries := []string{
			`INSERT INTO peer (id, note, info, status_online, is_banned, banned_reason, is_deleted) VALUES
				('1', 'Front desk', ` + db.Pick(`'null'`, `'"null"'`) + `, FALSE, TRUE, 'Stolen', FALSE),
				('2', '', '{"os":"Linux"}', TRUE, FALSE, '', FALSE)`,
		}
		if !db.IsPostgres() {
			queries = append(queries, `INSERT INTO peer (id, info, status_online, is_banned, is_deleted, tags) VALUES ('3', '{"os":"Linux"}', 0, 0, 0, X'00')`)
		}
		for _, q := range queries {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		e := New(Options{DB: db})
		linux := db.Pick(`"Linux":2`, `"Linux":1`)
		for typ, want := range map[string]string{
			"devices":  `"platform_distribution":{"Unknown":1,` + linux + `}`,
			"security": `"banned_devices":[{"id":"1","hostname":"Front desk","ip":"","ban_reason":"Stolen"}]`,
		} {
			report, err := e.Generate(ctx, jsbind.String(typ), Filters{})
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			if got, _ := report.MarshalJSON(); !strings.Contains(string(got), want) {
				t.Errorf("%s: %s\nwant %s", typ, got, want)
			}
		}
	})
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
		for _, rule := range engineRules {
			if !used["rule "+rule.bug+" "+rule.world] {
				t.Errorf("the fix of %s changes no report of the world %s", rule.bug, rule.world)
			}
		}
	})
}

// ruleOf is the rule of a world.
func ruleOf(world string) (engineRule, bool) {
	for _, rule := range engineRules {
		if rule.world == world {
			return rule, true
		}
	}
	return engineRule{}, false
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
			if rule, ruled := ruleOf(world.Name); ruled && string(got) != *c.Report {
				used["rule "+rule.bug+" "+world.Name] = true
				if err := rule.same(*c.Report, string(got)); err != nil {
					fail("%s %s (%s): %v:\n node: %s\n   go: %s", world.Name, c.Body, rule.bug, err, *c.Report, got)
				}
				continue
			}
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
