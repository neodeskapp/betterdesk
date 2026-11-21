package reports_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	engine "github.com/unitronix/betterdesk-server/console/reports"
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

// checkAnswers is a Check that compares status and body after
// answerFixes and normalize, and the headers the recording kept:
// Content-Type, and Content-Disposition with the day of the export, which
// has to be today in UTC. used counts the steps each answer fix changed.
func checkAnswers(used map[string]int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		t.Helper()
		answer := step.Answer
		for _, f := range answerFixes {
			if rewritten := f.rewrite(answer); rewritten != answer {
				used[f.bug]++
				answer = rewritten
			}
		}
		return checkAnswer(t, step, answer, got)
	}
}

// checkAnswer is checkAnswers with Node's answer to compare with.
func checkAnswer(t *testing.T, step replay.Step, answer string, got replay.Answer) bool {
	t.Helper()
	if step.Method == "" {
		return false
	}
	label := step.Name + " as " + step.Role
	if want, have := normalize(answer), normalize(got.Body); got.Status != step.Status || want != have {
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

// S17: a body that is no JSON object says why, also in production, where
// Node said "Internal Server Error". The recording sends one, the text
// "devices" to generate, as three accounts; refusedBodies lists them.
var _ = reportFixes.Add("S17", refusedBodies())

func refusedBodies() map[string]replay.Fix {
	steps := map[string]replay.Fix{}
	for _, step := range recordedSteps() {
		if step.Status == 400 && step.Answer == `{"success":false,"error":"Internal Server Error"}` {
			steps[replay.FixKey(step)] = replay.Fix{Status: 400, Answer: `{"success":false,"error":"invalid JSON: the body has to be an object or an array"}`}
		}
	}
	if len(steps) != 3 {
		panic(fmt.Sprintf("S17: %d bodies the parser refused, want 3", len(steps)))
	}
	return steps
}

// recordedSteps are the steps of the recording on SQLite; the recording
// on PostgreSQL sends the same requests.
func recordedSteps() []replay.Step {
	return recordedStepsOn("sqlite")
}

// recordedStepsOn are the steps of the recording on one database.
func recordedStepsOn(dialect string) []replay.Step {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_"+dialect+".json"))
	if err != nil {
		panic(err)
	}
	var rec replay.Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		panic(err)
	}
	return rec.Steps
}

// RP5: from and to are read as times. A value that is no date or time is
// 400 in every route, where Node made a report of it or failed with 500.
// The filters of a report are from and to: device_id and limit leave
// the report's filters, and a saved report's filters; After puts Node's
// filters back into the saved row, so the steps that read it compare with
// Node. On SQLite, where Node compared the text, the date alone as to
// covers that whole day: the Chrome session that ends at midnight of
// 2019-11-01 counts in the range from 2019-10-31 to 2019-11-01, as it did
// on PostgreSQL.
var _ = reportFixes.Add("RP5", rangeFixes("sqlite"))
var _ = reportFixes.Add("RP5", rangeFixes("postgres"))

func rangeFixes(dialect string) map[string]replay.Fix {
	const chrome = `{"name":"Chrome","category":"browser","total_secs":3600,"sessions":1,"devices":1}`
	saves := map[string]map[string][]string{
		`{"type":"tickets","title":"Contract tickets","from":"2019-11-01","limit":5}`: {
			"sqlite":   {`"filters":"{\"from\":\"2019-11-01\"}"`},
			"postgres": {`"filters":{"from":"2019-11-01"}`},
		},
		`{"type":"activity","title":"Range","from":"2019-10-31","to":"2019-11-01","device_id":["x"]}`: {
			"sqlite": {`"filters":"{\"from\":\"2019-10-31\",\"to\":\"2019-11-01\"}"`, strings.ReplaceAll(`"top_applications":[`+chrome+`]`, `"`, `\"`)},
			// jsonb keeps the shorter key first
			"postgres": {`"filters":{"to":"2019-11-01","from":"2019-10-31"}`},
		},
	}
	steps := map[string]replay.Fix{}
	for _, step := range recordedStepsOn(dialect) {
		switch step.Path {
		case "/api/reports/generate", "/api/reports/generate/csv", "/api/reports/save":
		default:
			continue
		}
		if step.Status == 401 || step.Status == 403 {
			continue
		}
		var fix replay.Fix
		body := *step.Body
		from, _ := bodyMember(step, "from")
		to, _ := bodyMember(step, "to")
		_, deviceID := bodyMember(step, "device_id")
		_, limit := bodyMember(step, "limit")
		switch {
		case !isTime(from):
			fix = replay.Fix{Status: 400, Answer: `{"error":"from must be a date or a time"}`}
		case !isTime(to):
			fix = replay.Fix{Status: 400, Answer: `{"error":"to must be a date or a time"}`}
		case saves[body] != nil:
			fix = replay.Fix{Status: 201, Has: saves[body][dialect], Lacks: []string{"device_id", "limit"}, After: restoreAsNode}
		case deviceID || limit:
			fix = replay.Fix{Status: step.Status, Answer: withAnswerFixes(withoutFilters(step.Answer, "device_id", "limit"))}
		case dialect == "sqlite" && body == `{"type":"activity","from":"2019-10-31","to":"2019-11-01"}`:
			fix = replay.Fix{Status: 200, Answer: strings.Replace(step.Answer, `"top_applications":[]`, `"top_applications":[`+chrome+`]`, 1)}
		default:
			continue
		}
		fix.Dialect = dialect
		steps[replay.FixKey(step)] = fix
	}
	return steps
}

// RV96: an activity report covers at most the 366 days before its end,
// or before now without one. The recorded activity lies in 2019, so a
// report without an end has none of it, where Node read every summary:
// its counts are 0, its top applications empty, and the CSV of them has
// no rows.
var _ = reportFixes.Add("RV96", yearFixes("sqlite"))
var _ = reportFixes.Add("RV96", yearFixes("postgres"))

func yearFixes(dialect string) map[string]replay.Fix {
	const none = `{"devices_with_activity":0,"daily_reports":0,"total_active_secs":0,"total_idle_secs":0,"top_applications":[]}`
	steps := map[string]replay.Fix{}
	for _, step := range recordedStepsOn(dialect) {
		typ, _ := bodyMember(step, "type")
		from, _ := bodyMember(step, "from")
		to, _ := bodyMember(step, "to")
		if typ != "activity" || step.Status != 200 || !isTime(from) || !isTime(to) || (to != nil && to != false && to != 0.0 && to != "") {
			continue
		}
		var fix replay.Fix
		switch step.Path {
		case "/api/reports/generate":
			parsed, err := goapi.Parse([]byte(step.Answer))
			if err != nil {
				panic(err)
			}
			empty, _ := goapi.Parse([]byte(none))
			parsed.(*goapi.Object).Set("data", empty)
			fix = replay.Fix{Status: 200, Answer: string(engine.Stringify(parsed))}
		case "/api/reports/generate/csv":
			if section, _ := bodyMember(step, "section"); section == "top_applications" {
				fix = replay.Fix{Status: 200, Lacks: []string{"name", "Chrome"}}
			} else {
				header, _, _ := strings.Cut(step.Answer, "\n")
				fix = replay.Fix{Status: 200, Answer: header + "\n0,0,0,0"}
			}
		default:
			continue
		}
		fix.Dialect = dialect
		steps[replay.FixKey(step)] = fix
	}
	if len(steps) < 10 {
		panic(fmt.Sprintf("RV96: %d activity reports without an end on %s", len(steps), dialect))
	}
	return steps
}

// isTime reports whether a from or to of a recorded body sets a valid
// bound or none: a value that is not true, a date alone, or a time with
// seconds.
func isTime(v any) bool {
	text, isText := v.(string)
	if v == nil || v == false || v == 0.0 || v == "" {
		return true
	}
	if !isText {
		return false
	}
	for _, layout := range []string{time.DateOnly, time.RFC3339Nano, time.DateTime} {
		if _, err := time.Parse(layout, text); err == nil {
			return true
		}
	}
	return false
}

// withoutFilters is a report's JSON without the named members of its
// filters.
func withoutFilters(report string, names ...string) string {
	parsed, err := goapi.Parse([]byte(report))
	if err != nil {
		panic(err)
	}
	filters, _ := goapi.Dig(parsed, "filters").(*goapi.Object)
	for _, name := range names {
		filters.Delete(name)
	}
	return string(engine.Stringify(parsed))
}

// restoreAsNode is a Fix.After for a save whose row the fix changed: it
// puts the filters and payload Node stored into the row.
func restoreAsNode(t *testing.T, w *replay.World, step replay.Step) {
	t.Helper()
	var row struct {
		ID               float64
		Filters, Payload json.RawMessage
	}
	if err := json.Unmarshal([]byte(step.Answer), &row); err != nil {
		t.Fatal(err)
	}
	stored := func(raw json.RawMessage) any {
		if w.DB.IsPostgres() {
			return string(raw)
		}
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			t.Fatal(err)
		}
		return text
	}
	value := w.DB.Pick("?", "CAST(? AS jsonb)")
	w.Exec("UPDATE saved_reports SET filters = "+value+", payload = "+value+" WHERE id = ?", []any{stored(row.Filters), stored(row.Payload), row.ID})
}

// RP6: the CSV export writes an object as JSON, where Node wrote "[object
// Object]" (the seeded banned device whose note is an object), and a
// PostgreSQL time in the ISO form, where Node wrote the long local form.
// Such an export keeps its header; console/reports compares the fields
// with Node's one by one.
var _ = reportFixes.Add("RP6", csvFixes("sqlite"))
var _ = reportFixes.Add("RP6", csvFixes("postgres"))

func csvFixes(dialect string) map[string]replay.Fix {
	steps := map[string]replay.Fix{}
	for _, step := range recordedStepsOn(dialect) {
		if step.Path != "/api/reports/generate/csv" || step.Status != 200 || !strings.Contains(step.Answer, "[object Object]") && !jsTime.MatchString(step.Answer) {
			continue
		}
		header, _, _ := strings.Cut(step.Answer, "\n")
		steps[replay.FixKey(step)] = replay.Fix{
			Dialect: dialect, Status: 200, Has: []string{header + "\n"}, Lacks: []string{"[object Object]", " GMT"},
			Headers: map[string]string{"Content-Type": "text/csv; charset=utf-8"},
		}
	}
	return steps
}

// RP10: a saved report's title is a text; a number, a flag, a list or an
// object is 400, where Node stored the title as its driver bound it (5
// as "5.0" on SQLite) or failed with 500. Where Node stored a report,
// After saves it again with the title as text, as Node's answer shows it.
var _ = reportFixes.Add("RP10", titleFixes("sqlite"))
var _ = reportFixes.Add("RP10", titleFixes("postgres"))

func titleFixes(dialect string) map[string]replay.Fix {
	fixes := map[string]replay.Fix{}
	for _, step := range recordedStepsOn(dialect) {
		if step.Path != "/api/reports/save" || step.Status == 401 || step.Status == 403 {
			continue
		}
		title, given := bodyMember(step, "title")
		if _, isText := title.(string); !given || title == nil || isText {
			continue
		}
		fix := replay.Fix{Dialect: dialect, Status: 400, Answer: `{"error":"Title must be text"}`}
		if step.Status == 201 {
			var body, saved map[string]any
			if json.Unmarshal([]byte(*step.Body), &body) != nil || json.Unmarshal([]byte(step.Answer), &saved) != nil {
				panic("RP10: " + step.Name)
			}
			body["title"] = saved["title"]
			resent, _ := json.Marshal(body)
			fix.After = replay.ResendBy(string(resent), string(resent))
		}
		fixes[replay.FixKey(step)] = fix
	}
	return fixes
}

// RP8: a saved report whose payload is text and no JSON (the seeded report
// 2) is read with the payload {}, where Node answered 500. The answer is
// the row as Node's list of saved reports shows it, with that payload.
var _ = reportFixes.Add("RP8", brokenPayloads("sqlite"))
var _ = reportFixes.Add("RP8", brokenPayloads("postgres"))

func brokenPayloads(dialect string) map[string]replay.Fix {
	steps := recordedStepsOn(dialect)
	var row *goapi.Object
	for _, step := range steps {
		if step.Method != "GET" || step.Path != "/api/reports/saved" || step.Status != 200 {
			continue
		}
		list, err := goapi.Parse([]byte(step.Answer))
		if err != nil {
			panic(err)
		}
		for _, item := range list.([]any) {
			if o, _ := item.(*goapi.Object); o != nil && o.Get("id") == 2.0 {
				row = o.Clone().Set("payload", goapi.NewObject())
			}
		}
		break
	}
	if row == nil {
		panic("RP8: Node's list of saved reports has no report 2")
	}
	fixes := map[string]replay.Fix{}
	for _, step := range steps {
		if step.Path == "/api/reports/saved/2" && step.Status == 500 {
			fixes[replay.FixKey(step)] = replay.Fix{Dialect: dialect, Status: 200, Answer: string(engine.Stringify(row))}
		}
	}
	return fixes
}

// jsTime is a time in the form of JavaScript's Date.toString.
var jsTime = regexp.MustCompile(`\d{2}:\d{2}:\d{2} GMT[+-]\d{4} \(`)

// answerFix is a change a bug fix makes to answers wherever they show a
// part of a report: in many steps, also in a request sent again with
// another answer, which reportFixes cannot tell apart. Node's answer of a
// step reportFixes does not list goes through rewrite before it is
// compared.
type answerFix struct {
	bug     string
	rewrite func(answer string) string
}

// answerFixes are the package's answer changes, after reportFixes.
var answerFixes = []answerFix{
	// RP3: the reports count names each as its own key, so the seeded
	// device whose system is "constructor" counts 1, where Node wrote the
	// text of the function every object inherits with a "1" added. The
	// device report, generated, saved or read back, changes there and only
	// there.
	{"RP3", func(answer string) string {
		return nativeCount.ReplaceAllStringFunc(answer, func(count string) string {
			m := nativeCount.FindStringSubmatch(count)
			return m[1] + ":" + strconv.Itoa(len(m[2]))
		})
	}},
}

// withAnswerFixes is an answer after every answer fix.
func withAnswerFixes(answer string) string {
	for _, f := range answerFixes {
		answer = f.rewrite(answer)
	}
	return answer
}

// nativeCount is a count Node made of a name every object has, in JSON
// or in JSON inside a JSON text: the key, then the function's text with a
// "1" per count.
var nativeCount = regexp.MustCompile(`(\\?"[^"\\]+\\?"):\\?"function \w+\(\) \{ \[native code\] \}(1+)\\?"`)

// bodyMember is a member of the JSON object a step sent, and whether the
// body is an object that has it.
func bodyMember(step replay.Step, name string) (any, bool) {
	if step.Body == nil {
		return nil, false
	}
	var body map[string]any
	if json.Unmarshal([]byte(*step.Body), &body) != nil {
		return nil, false
	}
	v, ok := body[name]
	return v, ok
}

// jsText is String(v) of a JSON value.
func jsText(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item != nil {
				parts[i] = jsText(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// RP2: generating, exporting and saving take the type only as a text that
// names a report, and answer 400 with the type's text for anything else,
// where Node made the report of a list holding a name, failed with 500 on
// a name every object has, and exported and saved nothing with 500 for
// every unknown type. Node stored the one list that names a report it
// saved; After stores the same row, with the type as Node bound the list:
// spread over the statement on SQLite, as an array on PostgreSQL.
var _ = reportFixes.Add("RP2", unknownTypes())

func unknownTypes() map[string]replay.Fix {
	names := map[string]bool{}
	for _, entry := range []string{"devices", "activity", "security", "tickets", "network", "inventory", "alerts"} {
		names[entry] = true
	}
	steps := map[string]replay.Fix{}
	for _, step := range recordedSteps() {
		switch step.Path {
		case "/api/reports/generate", "/api/reports/generate/csv", "/api/reports/save":
		default:
			continue
		}
		typ, _ := bodyMember(step, "type")
		if text, isText := typ.(string); isText && names[text] || step.Status == 401 || step.Status == 403 {
			continue
		}
		if typ == nil || typ == false || typ == 0.0 || typ == "" {
			continue // no type: "Report type is required"
		}
		answer := `{"error":"Unknown report type: ` + jsText(typ) + `"}`
		if step.Status == 400 && step.Answer == answer {
			continue
		}
		fix := replay.Fix{Status: 400, Answer: answer}
		if step.Status == 201 {
			fix.After = storeAsNode(typ)
		}
		steps[replay.FixKey(step)] = fix
	}
	return steps
}

// storeAsNode is a Fix.After for a save Node made with a type in a list:
// it saves the report under the name and gives the row the type Node
// stored.
func storeAsNode(typ any) func(t *testing.T, w *replay.World, step replay.Step) {
	return func(t *testing.T, w *replay.World, step replay.Step) {
		t.Helper()
		var body map[string]any
		if err := json.Unmarshal([]byte(*step.Body), &body); err != nil {
			t.Fatal(err)
		}
		body["type"] = jsText(typ)
		text, _ := json.Marshal(body)
		got := w.Do(replay.Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: replay.Text(string(text)), Headers: step.Headers})
		if got.Status != 201 {
			t.Fatalf("%s again as %s: %d %s", step.Name, text, got.Status, got.Body)
		}
		if w.DB.IsPostgres() {
			w.Exec(`UPDATE saved_reports SET report_type = '{"`+jsText(typ)+`"}' WHERE id = (SELECT MAX(id) FROM saved_reports)`, nil)
		}
	}
}

// signedOutWrites are the fixes of the steps of the recording that save or
// delete a report without a session.
func signedOutWrites() map[string]replay.Fix {
	steps := map[string]replay.Fix{}
	for _, step := range recordedSteps() {
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
// steps the bug fixes change (reportFixes and answerFixes).
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		rec := replay.Load(t, db, "node")
		if len(rec.Steps) < 780 {
			t.Fatalf("the recording has %d steps", len(rec.Steps))
		}
		defer reportFixes.Unused(t, db)
		used := map[string]int{}
		w.Run(rec, reportFixes.Check(w, checkAnswers(used)))
		for _, f := range answerFixes {
			if used[f.bug] == 0 {
				t.Errorf("the answer fix of %s changes no step of the recording", f.bug)
			}
		}
	})
}

// C7: an id that is no whole number, or one beyond 2147483647, names no
// saved report on either database, where PostgreSQL failed with 500.
var _ = reportFixes.Add("C7", noSavedReport())

func noSavedReport() map[string]replay.Fix {
	steps := map[string]replay.Fix{}
	for _, role := range []string{"viewer", "operator", "server_admin", "global_admin", "super_admin", "admin"} {
		for _, id := range []string{"1.5", "abc", "Infinity", "99999999999", "%C3%BC"} {
			steps[role+" GET /api/reports/saved/"+id+" "] = replay.Fix{Dialect: "postgres", Status: 404, Answer: `{"error":"Saved report not found"}`}
		}
	}
	for _, role := range []string{"super_admin", "admin"} {
		for _, id := range []string{"1.5", "abc", "Infinity", "99999999999"} {
			steps[role+" DELETE /api/reports/saved/"+id+" "] = replay.Fix{Dialect: "postgres", Status: 404, Answer: `{"error":"Report not found"}`}
		}
	}
	return steps
}
