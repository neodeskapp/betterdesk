package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is one of the files testdata/engine_*.json (testdata/README.md).
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
// every text of has and none of lacks, and where as is set, its data are
// those of the report Go makes for the body as.
type engineFix struct {
	bug        string
	err        string
	has, lacks []string
	as         string
	// dialect limits the fix to one database ("sqlite", "postgres").
	dialect string
}

// engineRewrite is a change a bug fix makes to every report: Node's
// report goes through rewrite before it is compared.
type engineRewrite struct {
	bug     string
	rewrite func(report string) string
}

// engineRewrites are the changes of every report, applied before
// engineRules.
var engineRewrites []engineRewrite

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

// RP5: from and to are read as times and bound as ISO times in UTC. A
// value that is no date or time fails every report with a RangeError,
// where Node bound it as it came: compared as text on SQLite, an error
// for PostgreSQL to find, or for the driver. A date alone as to covers
// that whole day, and a time with an offset is that instant, where Node
// compared the text; such a case makes the report of the same range
// written in UTC with milliseconds, which the cases that did not change
// show to be Node's. device_id and limit leave the filters.
var _ = fixEngine("RP5", map[string]engineFix{
	`* {"type":"activity","from":"2019-11"}`:                        badRange("from"),
	`* {"type":"activity","from":"2019"}`:                           badRange("from"),
	`* {"type":"activity","to":"2019-11"}`:                          badRange("to"),
	`* {"type":"activity","from":"garbage"}`:                        badRange("from"),
	`* {"type":"activity","from":"Ü"}`:                              badRange("from"),
	`* {"type":"activity","from":"0"}`:                              badRange("from"),
	`* {"type":"activity","from":5}`:                                badRange("from"),
	`* {"type":"activity","from":1.5}`:                              badRange("from"),
	`* {"type":"activity","from":true}`:                             badRange("from"),
	`* {"type":"activity","to":true}`:                               badRange("to"),
	`* {"type":"activity","from":["2019-11-01"]}`:                   badRange("from"),
	`* {"type":"activity","from":["a","b"]}`:                        badRange("from"),
	`* {"type":"activity","from":[]}`:                               badRange("from"),
	`* {"type":"activity","from":{}}`:                               badRange("from"),
	`* {"type":"activity","from":{"a":1}}`:                          badRange("from"),
	`* {"type":"activity","from":"2019-11-01","to":["2019-11-30"]}`: badRange("to"),
	`* {"type":"activity","from":{"a":1},"to":["2019-11-30"]}`:      badRange("from"),
	`* {"type":"activity","from":["2019-10-31","2019-11-01"]}`:      badRange("from"),
	`* {"type":"activity","to":"2019-10-31"}`:                       {as: `{"type":"activity","to":"2019-10-31T23:59:59.999Z"}`},
	`* {"type":"activity","to":"2019-11-01"}`:                       {as: `{"type":"activity","to":"2019-11-01T23:59:59.999Z"}`},
	`* {"type":"activity","from":"2019-10-31","to":"2019-11-01"}`:   {as: `{"type":"activity","from":"2019-10-31T00:00:00.000Z","to":"2019-11-01T23:59:59.999Z"}`},
	`* {"type":"activity","from":"2019-11-01","to":"2019-11-30"}`:   {as: `{"type":"activity","from":"2019-11-01T00:00:00.000Z","to":"2019-11-30T23:59:59.999Z"}`},
	`* {"type":"activity","from":"2019-11-30","to":"2019-12-01"}`:   {as: `{"type":"activity","from":"2019-11-30T00:00:00.000Z","to":"2019-12-01T23:59:59.999Z"}`},
	`* {"type":"activity","from":"2019-10-31T23:00:00Z"}`:           {as: `{"type":"activity","from":"2019-10-31T23:00:00.000Z"}`},
	`* {"type":"activity","from":"2019-11-01T00:00:00+01:00"}`:      {as: `{"type":"activity","from":"2019-10-31T23:00:00.000Z"}`},
	`* {"type":"activity","to":"2019-10-31T23:59:59.999+01:00"}`:    {as: `{"type":"activity","to":"2019-10-31T22:59:59.999Z"}`},
	`* {"type":"activity","from":"2019-10-27T02:00:00+02:00"}`:      {as: `{"type":"activity","from":"2019-10-27T00:00:00.000Z"}`},
})

// RP7: each row is read on its own. An event whose details are no JSON
// gets details {} while the others are parsed, where Node kept every
// event's details as stored text. An inventory whose hardware is no JSON
// (on PostgreSQL the JSON text "str" of fleet's tenth row) is left out
// alone, and the software is not read, where Node counted no inventory
// at all: 17 rows on SQLite, of which 7 have no system, and 15 on
// PostgreSQL.
var _ = fixEngine("RP7", map[string]engineFix{
	`event details that are no JSON {"type":"security"}`: {
		has:   []string{`"action":"log","details":{},"created_at":"2019-11-02`, `"details":{"file":"a,b.txt","size":10}`},
		lacks: []string{`"details":"`},
	},
	`inventory that is no JSON {"type":"inventory"}`: {dialect: "sqlite", has: []string{
		`"total_inventoried":17,"os_distribution":{"5":1,"12":1,"Unknown":7,"__proto__":1,"A,B":1,"Half":1,"constructor":1,"Windows 11 Pro":2,"macOS 15.1":1,"Ubuntu 24.04":1}`,
	}},
	`inventory hardware that is no JSON {"type":"inventory"}`: {has: []string{`"total_inventoried":1,"os_distribution":{"Fine":1},"avg_ram_mb":0`}},
})

var _ = fixEngine("RP7", map[string]engineFix{
	`inventory that is no JSON {"type":"inventory"}`: {dialect: "postgres", has: []string{
		`"total_inventoried":15,"os_distribution":{"5":1,"12":1,"Unknown":5,"__proto__":1,"A,B":1,"Half":1,"constructor":1,"Windows 11 Pro":2,"macOS 15.1":1,"Ubuntu 24.04":1}`,
	}},
	`fleet {"type":"inventory"}`: {dialect: "postgres", has: []string{`"total_inventoried":14,`}},
})

var _ = rewriteEngine("RP5", func(report string) string {
	parsed, err := goapi.Parse([]byte(report))
	if err != nil {
		return report
	}
	filters := goapi.Dig(parsed, "filters")
	o, isObject := filters.(*goapi.Object)
	if !isObject || !o.Has("device_id") && !o.Has("limit") {
		return report
	}
	o.Delete("device_id")
	o.Delete("limit")
	return string(Stringify(parsed))
})

func rewriteEngine(bug string, rewrite func(report string) string) bool {
	engineRewrites = append(engineRewrites, engineRewrite{bug: bug, rewrite: rewrite})
	return true
}

// badRange is the fix of a case whose from or to (name) is no time.
func badRange(name string) engineFix {
	return engineFix{err: name + " must be a date or a time"}
}

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
func checkEngineFix(t *testing.T, e *Engine, key string, f engineFix, report *Report, err error) {
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
	if f.as != "" {
		body := bodyOf(t, f.as)
		same, err := e.Generate(context.Background(), jsbind.Of(body.Get("type")), Filters{From: jsbind.Of(body.Get("from")), To: jsbind.Of(body.Get("to"))})
		if err != nil {
			t.Fatalf("%s (%s): %s: %v", key, f.bug, f.as, err)
		}
		if want, got := Stringify(same.Data), Stringify(report.Data); string(want) != string(got) {
			t.Errorf("%s (%s): the data differ from those of %s:\n   as: %s\n   go: %s", key, f.bug, f.as, want, got)
		}
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
		// the activity lies within the year before the report
		e := New(Options{DB: db, Now: func() time.Time { return time.Date(2019, 11, 10, 0, 0, 0, 0, time.UTC) }})
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

// TestOneUnreadableRowLeavesTheOthers: a data guard event whose details
// are no JSON gets details {} while the other events keep theirs, and an
// inventory whose hardware is no JSON is left out of the inventory report
// alone (BUGS.md RP7).
func TestOneUnreadableRowLeavesTheOthers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		bad := db.Pick(`'not json'`, `'"not json"'`)
		for _, q := range []string{
			`INSERT INTO dlp_events (device_id, details, created_at) VALUES ('D1', '{"a":1}', '2019-11-01 10:00:00'), ('D2', ` + bad + `, '2019-11-01 09:00:00')`,
			`INSERT INTO device_inventory (device_id, hardware, software) VALUES
				('I1', '{"os_name":"Linux","memory":{"total_bytes":1048576}}', '{}'), ('I2', ` + bad + `, '{}')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		e := New(Options{DB: db})
		for typ, want := range map[string][]string{
			"security":  {`"device_id":"D1"`, `"details":{"a":1}`, `"device_id":"D2"`, `"details":{}`},
			"inventory": {`"total_inventoried":1,"os_distribution":{"Linux":1},"avg_ram_mb":1`},
		} {
			report, err := e.Generate(ctx, jsbind.String(typ), Filters{})
			if err != nil {
				t.Fatalf("%s: %v", typ, err)
			}
			got, _ := report.MarshalJSON()
			for _, part := range want {
				if !strings.Contains(string(got), part) {
					t.Errorf("%s lacks %s: %s", typ, part, got)
				}
			}
		}
	})
}

// statements is the database of an engine that notes the statements that
// read rows.
type statements struct {
	store.Querier
	queries []string
}

func (s *statements) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	s.queries = append(s.queries, query)
	return s.Querier.Query(ctx, query, args...)
}

// TestReportsReadBoundedRows: the activity report covers at most the 366
// days before its end (now without one), reads the totals and the apps
// of every device in a few statements whatever the number of devices, the
// ticket report counts in the database, the inventory report reads the
// hardware alone and the alert report the 200 newest alerts (BUGS.md
// RV96).
func TestReportsReadBoundedRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		var summaries, sessions, alerts []string
		for i := 0; i < 30; i++ {
			id := fmt.Sprintf("'D%02d'", i)
			summaries = append(summaries, "("+id+", 10, 1, '2026-11-01T00:00:00.000Z', '2026-11-01 00:00:00')")
			sessions = append(sessions, "("+id+", 'App', 'work', '2026-11-01T01:00:00.000Z', '2026-11-01T02:00:00.000Z', 5)")
		}
		for i := 0; i < 250; i++ {
			alerts = append(alerts, fmt.Sprintf("(1, 'D00', 'alert %d', '2026-11-01 %02d:%02d:00')", i, i/60, i%60))
		}
		for _, q := range []string{
			"INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at) VALUES " + strings.Join(summaries, ", ") +
				", ('OLD', 1000, 100, '2024-01-01T00:00:00.000Z', '2024-01-01 00:00:00')",
			"INSERT INTO activity_sessions (device_id, app_name, category, started_at, ended_at, duration_secs) VALUES " + strings.Join(sessions, ", ") +
				", ('OLD', 'Old app', 'work', '2024-01-01T01:00:00.000Z', '2024-01-01T02:00:00.000Z', 500)",
			`INSERT INTO tickets (title, created_by, category, priority, created_at) VALUES
				('a', 'x', 'network', 'high', '2026-11-03 10:00:00'), ('b', 'x', NULL, 'low', '2026-11-02 10:00:00'),
				('c', 'x', 'network', '', '2026-11-01 10:00:00')`,
			`INSERT INTO device_inventory (device_id, hardware, software) VALUES ('D00', '{"os_name":"Linux"}', '{"apps":"` + strings.Repeat("x", 1000) + `"}')`,
			"INSERT INTO alert_rules (id, name, condition_type) VALUES (1, 'Offline', 'offline')",
			"INSERT INTO alert_history (rule_id, device_id, message, triggered_at) VALUES " + strings.Join(alerts, ", "),
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		e := New(Options{DB: db, Now: func() time.Time { return now }, Location: time.UTC})
		read := &statements{Querier: db}
		e.query = read
		recent := `"devices_with_activity":30,"daily_reports":30,"total_active_secs":300,"total_idle_secs":30,` +
			`"top_applications":[{"name":"App","category":"work","total_secs":150,"sessions":30,"devices":30}]`
		for _, c := range []struct {
			typ      string
			from, to jsbind.Value
			want     string
		}{
			{"activity", jsbind.Undefined, jsbind.Undefined, recent},
			{"activity", jsbind.String("2020-01-01"), jsbind.Undefined, recent},
			{"activity", jsbind.String("2020-01-01"), jsbind.String("2026-11-05"), recent},
			{"activity", jsbind.Undefined, jsbind.String("2024-06-01"), `"devices_with_activity":1,"daily_reports":1,"total_active_secs":1000,"total_idle_secs":100,` +
				`"top_applications":[{"name":"Old app","category":"work","total_secs":500,"sessions":1,"devices":1}]`},
			{"tickets", jsbind.Undefined, jsbind.Undefined, `"total_tickets":3,"category_distribution":{"network":2,"uncategorized":1},"priority_distribution":{"high":1,"low":1,"normal":1}`},
			{"inventory", jsbind.Undefined, jsbind.Undefined, `"total_inventoried":1,"os_distribution":{"Linux":1}`},
			{"alerts", jsbind.Undefined, jsbind.Undefined, `"total_rules":1,"active_rules":1,"total_alerts":200,"alerts_per_rule":{"1":200},"recent_alerts":[{"id":250,`},
		} {
			read.queries = nil
			report, err := e.Generate(ctx, jsbind.String(c.typ), Filters{From: c.from, To: c.to})
			if err != nil {
				t.Fatalf("%s from %s to %s: %v", c.typ, c.from.JSON(), c.to.JSON(), err)
			}
			if got, _ := report.MarshalJSON(); !strings.Contains(string(got), c.want) {
				t.Errorf("%s from %s to %s: %s\nwant %s", c.typ, c.from.JSON(), c.to.JSON(), got, c.want)
			}
			if c.typ == "activity" && len(read.queries) > 3 {
				t.Errorf("the activity report ran %d statements for 30 devices", len(read.queries))
			}
			for _, q := range read.queries {
				if wide.MatchString(q) {
					t.Errorf("the %s report reads whole rows: %s", c.typ, q)
				}
				if strings.Contains(q, "alert_history") && !strings.Contains(q, "LIMIT 200") {
					t.Errorf("the alert report reads every alert: %s", q)
				}
			}
		}
	})
}

// wide finds a statement that reads every column of the tickets or the
// inventories.
var wide = regexp.MustCompile(`(?is)SELECT\s+\*\s+FROM\s+(tickets|device_inventory)\b`)

// jsDate is a time as JavaScript's Date.toString writes it, with the name
// of its zone in brackets.
var jsDate = regexp.MustCompile(`^([A-Z][a-z]{2} [A-Z][a-z]{2} \d{2} \d{4} \d{2}:\d{2}:\d{2} GMT[+-]\d{4}) \(.*\)$`)

// sameCSV compares a CSV the Go engine wrote with Node's after RP6: the
// export quotes a value with "\r", writes lists and objects as JSON and
// times in the ISO form, and takes its columns from every row. Both are
// read back field by field. A field is Node's, or Node's time to the
// second (Node's form has no milliseconds), or the JSON of Node's list
// (Node joined it with commas) or of an object (Node wrote "[object
// Object]"). Columns Go adds after Node's are free.
func sameCSV(node, got string) error {
	nodeLines, ok := csvFields(node)
	if !ok {
		return fmt.Errorf("node's csv does not read back")
	}
	goLines, ok := csvFields(got)
	if !ok {
		return fmt.Errorf("go's csv does not read back")
	}
	if len(nodeLines) != len(goLines) {
		return fmt.Errorf("%d lines, node has %d", len(goLines), len(nodeLines))
	}
	c := clock{loc: time.UTC, zoneName: DefaultZoneName}
	for i, nodeLine := range nodeLines {
		goLine := goLines[i]
		if len(goLine) < len(nodeLine) {
			return fmt.Errorf("line %d has %d fields, node's %d", i, len(goLine), len(nodeLine))
		}
		for j, want := range nodeLine {
			have := goLine[j]
			if have == want {
				continue
			}
			if m := jsDate.FindStringSubmatch(want); m != nil {
				at, err := time.Parse("Mon Jan 02 2006 15:04:05 GMT-0700", m[1])
				iso, isoErr := time.Parse(time.RFC3339Nano, have)
				if err == nil && isoErr == nil && store.ISOTime(iso.Truncate(time.Second)) == store.ISOTime(at) {
					continue
				}
			}
			if parsed, err := goapi.Parse([]byte(have)); err == nil {
				if _, isList := parsed.([]any); isList && c.String(parsed) == want {
					continue
				}
				if _, isObject := parsed.(*goapi.Object); isObject && want == "[object Object]" {
					continue
				}
			}
			return fmt.Errorf("line %d field %d is %q, node's %q", i, j, have, want)
		}
	}
	return nil
}

// TestCSVQuotesCarriageReturnsAndWritesISOTimes: the CSV export quotes a
// value with a carriage return, writes a list or an object as JSON and a
// PostgreSQL time in the ISO form of the JSON answers, whatever the zone
// of the process, and takes its columns from every row (BUGS.md RP6).
func TestCSVQuotesCarriageReturnsAndWritesISOTimes(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{Location: berlin})
	rows := []any{
		goapi.NewObject("a", "only\rcarriage", "list", []any{1.0, "a,b", nil}, "obj", goapi.NewObject("x", 1.0),
			"at", Date{T: time.Date(2019, 11, 26, 1, 25, 25, 500e6, time.UTC)}),
		goapi.NewObject("a", "plain", "extra", true),
	}
	want := "a,list,obj,at,extra\n" +
		"\"only\rcarriage\",\"[1,\"\"a,b\"\",null]\",\"{\"\"x\"\":1}\",2019-11-26T01:25:25.500Z,\n" +
		"plain,,,,true"
	if got := e.ToCSV(rows); got != want {
		t.Errorf("csv:\n got %q\nwant %q", got, want)
	}
}

// TestCSVKeepsFormulasAsText: a text that starts with a character a
// spreadsheet reads as the start of a formula gets a single quote in
// front, as the billing exports do; numbers keep their sign (BUGS.md
// RV92).
func TestCSVKeepsFormulasAsText(t *testing.T) {
	e := New(Options{})
	rows := []any{
		goapi.NewObject("name", `=HYPERLINK("http://evil.example/x","open")`, "n", -2.0),
		goapi.NewObject("name", "+1", "n", 3.0),
		goapi.NewObject("name", "@SUM(1+1)", "n", "-2+3"),
		goapi.NewObject("name", "\tcmd", "n", "\r=1"),
		goapi.NewObject("name", "plain = text", "n", "a-b"),
	}
	want := "name,n\n" +
		`"'=HYPERLINK(""http://evil.example/x"",""open"")",-2` + "\n" +
		"'+1,3\n" +
		"'@SUM(1+1),'-2+3\n" +
		"'\tcmd,\"'\r=1\"\n" +
		"plain = text,a-b"
	if got := e.ToCSV(rows); got != want {
		t.Errorf("csv:\n got %q\nwant %q", got, want)
	}
}

// TestNodeReports makes every report the Node engine made
// (testdata/engine_*.json, see testdata/README.md) on
// the same rows and compares the JSON and the CSV of every part, byte for
// byte, but for the cases the bug fixes change (engineFixes,
// engineRewrites and engineRules).
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
		for _, r := range engineRewrites {
			if !used["rewrite "+r.bug] {
				t.Errorf("the fix of %s changes no report", r.bug)
			}
		}
		if !used["csv RP6"] {
			t.Error("the fix of RP6 changes no csv")
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
	// The activity of the recordings lies years before their time: the
	// longest range covers it, as the old console had none (RV96)
	e := New(Options{DB: db, Now: func() time.Time { return now }, Location: loc, ZoneName: zoneNames, MaxActivityRange: 50 * 366 * 24 * time.Hour})
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
			f := Filters{From: jsbind.Of(body.Get("from")), To: jsbind.Of(body.Get("to"))}
			report, err := e.Generate(ctx, jsbind.Of(body.Get("type")), f)
			if key, fix, listed := engineFixOf(db, world.Name, c.Body); listed {
				used[key] = true
				checkEngineFix(t, e, key, fix, report, err)
				continue
			}
			nodeReport := c.Report
			if nodeReport != nil {
				for _, r := range engineRewrites {
					if rewritten := r.rewrite(*nodeReport); rewritten != *nodeReport {
						used["rewrite "+r.bug] = true
						nodeReport = &rewritten
					}
				}
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
			if rule, ruled := ruleOf(world.Name); ruled && string(got) != *nodeReport {
				used["rule "+rule.bug+" "+world.Name] = true
				if err := rule.same(*nodeReport, string(got)); err != nil {
					fail("%s %s (%s): %v:\n node: %s\n   go: %s", world.Name, c.Body, rule.bug, err, *nodeReport, got)
				}
				continue
			}
			if string(got) != *nodeReport {
				fail("%s %s:\n node: %s\n   go: %s", world.Name, c.Body, *nodeReport, got)
			}
			for _, s := range c.CSV {
				if csv := e.ToCSV(report.CSVRows(valueOf(t, s.Section))); csv != s.CSV {
					used["csv RP6"] = true
					if err := sameCSV(s.CSV, csv); err != nil {
						fail("%s %s, csv of %s (RP6): %v:\n node: %q\n   go: %q", world.Name, c.Body, s.Section, err, s.CSV, csv)
					}
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
			used["csv RP6"] = true
			if err := sameCSV(c.CSV, got); err != nil {
				fail("toCsv(%s) (RP6): %v:\n node: %q\n   go: %q", c.Rows, err, c.CSV, got)
			}
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
