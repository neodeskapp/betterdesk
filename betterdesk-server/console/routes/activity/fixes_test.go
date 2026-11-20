package activity_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// activityFixes are the steps of testdata/node_*.json that the bug fixes
// change.
var activityFixes replay.Fixes

// A device with a note is listed under its note, also when the console
// cannot read its info (BUGS AI5).
func TestTheNoteNamesADeviceWhoseInfoIsUnreadable(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for id, info := range map[string]string{"D1": db.Pick("not json", `"not json"`), "D2": db.Pick("null", `"null"`)} {
			w.Exec("INSERT INTO peer (id, note, info) VALUES (?, 'front desk', ?)", []any{id, info})
			w.Exec("INSERT INTO activity_summaries (device_id, idle_seconds, session_count, total_active_secs, reported_at) VALUES (?, 0, 1, 60, '2019-03-04T00:00:00.000Z')", []any{id})
		}
		for _, path := range []string{"/api/activity", "/api/activity/D1", "/api/activity/D2"} {
			got := w.Do(replay.Request{Role: "admin", Path: path})
			if got.Status != 200 || !strings.Contains(got.Body, `"hostname":"front desk"`) || strings.Contains(got.Body, `"hostname":"D`) {
				t.Errorf("GET %s: %d %s, want every device under its note", path, got.Status, got.Body)
			}
		}
	})
}

// notesOf reads the notes of the devices the seed of a recording puts
// into the console's table.
func notesOf(t *testing.T, rec replay.Recording) map[string]string {
	t.Helper()
	notes := map[string]string{}
	for _, seed := range rec.Seed {
		if !strings.HasPrefix(seed.SQL, "INSERT INTO peer (id, note") {
			continue
		}
		id, _ := seed.Params[0].(string)
		note, _ := seed.Params[1].(string)
		notes[id] = note
	}
	if len(notes) < 10 {
		t.Fatalf("the seed has the notes of %d devices", len(notes))
	}
	return notes
}

// adjusted checks the steps whose answer differs from Node's only where
// the fixes change it, and counts them by the change:
//
//   - A device with a note is shown under its note where Node showed its
//     id because it could not read the device's info (BUGS AI5), and one
//     whose info has a hostname that is no text under its id (BUGS AI6).
//   - On PostgreSQL the detail count of a summary and the sums and counts
//     of the top apps are numbers, where Node sent texts of digits (BUGS
//     AI10).
func adjusted(notes map[string]string, postgres bool, renames, numbers *int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Status != 200 || got.Status != 200 || got.Body == step.Answer {
			return false
		}
		node, have := decoded(step.Answer), decoded(got.Body)
		if node == nil || have == nil {
			return false
		}
		renamed := rename(node, have, notes)
		numbered := postgres && number(node)
		if !reflect.DeepEqual(node, have) {
			return false
		}
		if renamed {
			*renames++
		}
		if numbered {
			*numbers++
		}
		return true
	}
}

// AI10: PostgreSQL makes one row per app too, under the category the app
// was used the longest in, with numbers, as SQLite did.
var _ = activityFixes.Add("AI10", map[string]replay.Fix{
	"admin GET /api/activity/TWO-CATEGORIES/top ": {Dialect: "postgres", Status: 200,
		Answer: `{"device_id":"TWO-CATEGORIES","top_apps":[{"app_name":"Mail","category":"communication","total_secs":900,"session_count":2},{"app_name":"Notes","category":"other","total_secs":10,"session_count":1}]}`},
	"admin GET /api/activity/TWO-CATEGORIES/top?limit=1 ": {Dialect: "postgres", Status: 200,
		Answer: `{"device_id":"TWO-CATEGORIES","top_apps":[{"app_name":"Mail","category":"communication","total_secs":900,"session_count":2}]}`},
	"admin GET /api/activity/TWO-CATEGORIES/top?limit=2 ": {Dialect: "postgres", Status: 200,
		Answer: `{"device_id":"TWO-CATEGORIES","top_apps":[{"app_name":"Mail","category":"communication","total_secs":900,"session_count":2},{"app_name":"Notes","category":"other","total_secs":10,"session_count":1}]}`},
	"admin GET /api/activity/TWO-CATEGORIES/top?to=2019-03-03T09:10:00.000Z ": {Dialect: "postgres", Status: 200,
		Answer: `{"device_id":"TWO-CATEGORIES","top_apps":[{"app_name":"Mail","category":"communication","total_secs":900,"session_count":2},{"app_name":"Notes","category":"other","total_secs":10,"session_count":1}]}`},
})

// digits is a whole number as PostgreSQL's driver gave a bigint: text.
var digits = regexp.MustCompile(`^-?[0-9]+$`)

// number turns the texts of digits in Node's answer that the Go console
// sends as numbers into numbers: the detail count of each summary and the
// sums and counts of each top app. It reports whether it changed one.
func number(node any) bool {
	answer, _ := node.(map[string]any)
	changed := false
	for list, keys := range map[string][]string{"summaries": {"detail_count"}, "top_apps": {"total_secs", "session_count"}} {
		rows, _ := answer[list].([]any)
		for _, row := range rows {
			o, _ := row.(map[string]any)
			for _, key := range keys {
				if text, isText := o[key].(string); isText && digits.MatchString(text) {
					o[key] = json.Number(text)
					changed = true
				}
			}
		}
	}
	return changed
}

// decoded is a JSON text as Go values, nil when it is no JSON.
func decoded(text string) any {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil
	}
	return v
}

// rename walks Node's answer beside the Go console's and puts the name
// the fixes give a device into each object of Node's that names one. It
// reports whether it changed one.
func rename(node, have any, notes map[string]string) bool {
	changed := false
	switch n := node.(type) {
	case map[string]any:
		h, ok := have.(map[string]any)
		if !ok {
			return false
		}
		if id, isText := n["device_id"].(string); isText {
			if name, ok := n["hostname"]; ok && name == id && notes[id] != "" && h["hostname"] == notes[id] {
				n["hostname"], changed = notes[id], true
			}
			if name, ok := n["hostname"]; ok && h["hostname"] == id {
				if _, isText := name.(string); !isText {
					n["hostname"], changed = id, true
				}
			}
		}
		for key, value := range n {
			changed = rename(value, h[key], notes) || changed
		}
	case []any:
		h, ok := have.([]any)
		if !ok || len(h) != len(n) {
			return false
		}
		for i := range n {
			changed = rename(n[i], h[i], notes) || changed
		}
	}
	return changed
}

// sessionsOf reads the apps of a device's sessions in an answer of GET
// /api/activity/{id}, or of its top apps in one of GET .../top, in the
// order of the answer.
func sessionsOf(body string) string {
	type app struct {
		AppName string `json:"app_name"`
	}
	var answer struct {
		Sessions []app `json:"sessions"`
		TopApps  []app `json:"top_apps"`
	}
	_ = json.Unmarshal([]byte(body), &answer)
	var apps []string
	for _, s := range append(answer.Sessions, answer.TopApps...) {
		apps = append(apps, s.AppName)
	}
	return strings.Join(apps, " ")
}

var (
	// isoTime is a time in the spellings the routes take: a date, or a date
	// and time with a T or a space, a part of a second and a zone or none.
	isoTime = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}([T ][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})?)?$`)
	// wholeLimit is a limit the routes take: up to four digits, from 1 to 500.
	wholeLimit = regexp.MustCompile(`^[0-9]{1,4}$`)
)

// checkedQuery is what the read routes make of the query of a recorded
// request: the name of the parameter they refuse ("" for none), and the
// query with from and to written as the ISO text with milliseconds the
// rows hold, a date alone as to as the last millisecond of its day.
func checkedQuery(path string) (refused, iso string) {
	route, query, _ := strings.Cut(path, "?")
	query, _, _ = strings.Cut(query, "#")
	parts := strings.Split(strings.Trim(strings.ToLower(route), "/"), "/")
	names := []string{"from", "to"}
	if len(parts) > 2 {
		names = append(names, "limit")
	}
	values := url.Values{}
	for _, name := range names {
		text, _, err := jsbind.QueryText(query, name)
		if err != nil {
			return name, ""
		}
		switch {
		case text == "":
			continue
		case name == "limit":
			n, _ := strconv.Atoi(text)
			if !wholeLimit.MatchString(text) || n < 1 || n > 500 {
				return name, ""
			}
			values.Set(name, text)
		case !isoTime.MatchString(text):
			return name, ""
		case len(text) == len(time.DateOnly) && name == "to":
			values.Set(name, text+"T23:59:59.999Z")
		case len(text) == len(time.DateOnly):
			values.Set(name, text+"T00:00:00.000Z")
		default:
			spelled := strings.Replace(text, " ", "T", 1)
			if !strings.ContainsAny(spelled[10:], "Z+-") {
				spelled += "Z"
			}
			at, err := time.Parse(time.RFC3339Nano, spelled)
			if err != nil {
				return name, ""
			}
			if name == "from" && !at.Equal(at.Truncate(time.Millisecond)) {
				at = at.Truncate(time.Millisecond).Add(time.Millisecond)
			}
			values.Set(name, at.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z"))
		}
	}
	return "", route + "?" + values.Encode()
}

// checkedWindows checks the steps of the read routes whose from, to or
// limit the routes read as times and whole numbers (BUGS AI8, AI9), and
// counts them. One the routes refuse is 400, where Node read the limit
// with parseInt (0 or text meant no limit) and compared the window as
// text on SQLite and answered 500 for it on PostgreSQL. A window Node
// read otherwise, such as a date alone as to, which left out that day,
// gets the answer of the same times written as the ISO text the rows
// hold, which Node and the Go console read alike.
func checkedWindows(w *replay.World, refused, respelled *int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Method != "GET" || step.Status != 200 && step.Status != 500 || !strings.Contains(step.Path, "?") {
			return false
		}
		bad, iso := checkedQuery(step.Path)
		if bad != "" {
			*refused++
			if want := `{"error":"Invalid ` + bad + `"}`; got.Status != 400 || got.Body != want {
				t.Errorf("%s (AI8, AI9): %d %s, want 400 %s", step.Name, got.Status, got.Body, want)
			}
			return true
		}
		if got.Status == step.Status && replay.Normalize(got.Body) == replay.Normalize(step.Answer) {
			return false
		}
		*respelled++
		again := w.Do(replay.Request{Role: step.Role, Path: iso})
		if again.Status != got.Status || again.Body != got.Body {
			t.Errorf("%s (AI9): %d %s\n  written as ISO text, %s: %d %s", step.Name, got.Status, got.Body, iso, again.Status, again.Body)
		}
		return true
	}
}

// The limit of a device's sessions and of its most used apps is a whole
// number from 1 to 500, written in digits and given once; anything else is
// 400, where the old routes read it with parseInt: no limit at all for 0
// or text, a 500 for a number beyond what the database takes (BUGS AI8).
func TestLimitIsAWholeNumber(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for i, app := range []string{"A1", "A2", "A3"} {
			w.Exec("INSERT INTO activity_sessions (device_id, app_name, started_at, ended_at, duration_secs) VALUES ('D1', ?, ?, ?, ?)",
				[]any{app, fmt.Sprintf("2019-03-04T0%d:00:00.000Z", i+1), fmt.Sprintf("2019-03-04T0%d:30:00.000Z", i+1), 10 * (i + 1)})
		}
		for _, path := range []string{"/api/activity/D1", "/api/activity/D1/top"} {
			for _, query := range []string{
				"limit=0", "limit=-1", "limit=abc", "limit=1.5", "limit=0x10", "limit=501", "limit=1e2", "limit=%2B2", "limit=%202",
				"limit=2%20", "limit=99999999999999999999", "limit=1&limit=2", "limit=&limit=2", "limit[]=2", "limit[rows]=2", "limit[0]=2",
			} {
				got := w.Do(replay.Request{Role: "admin", Path: path + "?" + query})
				if got.Status != 400 || got.Body != `{"error":"Invalid limit"}` {
					t.Errorf("GET %s?%s: %d %s, want 400 Invalid limit", path, query, got.Status, got.Body)
				}
			}
			for query, want := range map[string]string{"limit=2": "A3 A2", "limit=500": "A3 A2 A1", "limit=02": "A3 A2", "limit=": "A3 A2 A1", "": "A3 A2 A1"} {
				got := w.Do(replay.Request{Role: "admin", Path: path + "?" + query})
				if have := sessionsOf(got.Body); got.Status != 200 || have != want {
					t.Errorf("GET %s?%s: %d %q, want 200 %q: %s", path, query, got.Status, have, want, got.Body)
				}
			}
		}
	})
}

// From and to of the three read routes are times, compared as times on
// both databases: a date alone as to takes in that whole day, a time with
// a space or a zone counts as the time it is. Anything else, a list, an
// object or a parameter given twice is 400, where the old routes compared
// texts on SQLite and answered 500 on PostgreSQL (BUGS AI9).
func TestWindowIsATime(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, s := range []struct{ app, start, end string }{
			{"B4", "2019-03-04T23:00:00.000Z", "2019-03-04T23:30:00.000Z"},
			{"M5", "2019-03-05T08:00:00.000Z", "2019-03-05T08:30:00.000Z"},
			{"L5", "2019-03-05T23:59:00.000Z", "2019-03-05T23:59:59.500Z"},
			{"F6", "2019-03-06T00:00:00.000Z", "2019-03-06T00:10:00.000Z"},
		} {
			w.Exec("INSERT INTO activity_sessions (device_id, app_name, started_at, ended_at, duration_secs) VALUES ('D1', ?, ?, ?, 60)", []any{s.app, s.start, s.end})
			w.Exec("INSERT INTO activity_summaries (device_id, reported_at, received_at) VALUES (?, ?, ?)", []any{s.app, s.start, s.start})
		}
		for _, path := range []string{"/api/activity", "/api/activity/D1", "/api/activity/D1/top"} {
			for query, name := range map[string]string{
				"from=zzz": "from", "from=0": "from", "from=20190305": "from", "from=2019-13-01": "from", "from=2019-03-05T25:00:00Z": "from",
				"from=1551772800000": "from", "from=2019-03-05T08:00:00.000Z%00": "from", "from=a&from=b": "from", "from=&from=": "from",
				"from[]=2019-03-05": "from", "from[day]=2019-03-05": "from", "from[25]=2019-03-05": "from",
				"to=garbage": "to", "to=Tue,%2005%20Mar%202019%2008:00:00%20GMT": "to", "to=2019-03-05&to=2019-03-06": "to",
				"from[day]=x&to[]=2019-03-04&to[]=2019-03-06": "from",
			} {
				got := w.Do(replay.Request{Role: "admin", Path: path + "?" + query})
				if got.Status != 400 || got.Body != `{"error":"Invalid `+name+`"}` {
					t.Errorf("GET %s?%s: %d %s, want 400 Invalid %s", path, query, got.Status, got.Body, name)
				}
			}
		}
		// What each route finds: the summaries by their time of report, the
		// sessions from their start to their end
		for query, want := range map[string]string{
			"":                                 "F6 L5 M5 B4",
			"from=&to=":                        "F6 L5 M5 B4",
			"to=2019-03-05":                    "L5 M5 B4",
			"from=2019-03-05&to=2019-03-05":    "L5 M5",
			"from=2019-03-05%2008:00:00":       "F6 L5 M5",
			"from=2019-03-05+08:00:01":         "F6 L5",
			"from=2019-03-05T10:00:00%2B02:00": "F6 L5 M5",
			"from=2019-03-05T08:00:00Z":        "F6 L5 M5",
			"from=2019-03-05T08:00:00.0005Z":   "F6 L5",
			"to=2019-03-05T23:59:59.5Z":        "L5 M5 B4",
			"to=2019-03-05T23:59:59.4999Z":     "M5 B4",
			"to=2019-03-05T23:59:59.999Z":      "L5 M5 B4",
		} {
			got := w.Do(replay.Request{Role: "admin", Path: "/api/activity/D1?" + query})
			if have := sessionsOf(got.Body); got.Status != 200 || have != want {
				t.Errorf("the sessions of GET /api/activity/D1?%s: %d %q, want 200 %q: %s", query, got.Status, have, want, got.Body)
			}
		}
		for query, want := range map[string]string{
			"to=2019-03-05":                    "L5 M5 B4",
			"from=2019-03-05%2008:00:00":       "F6 L5 M5",
			"from=2019-03-05T10:00:00%2B02:00": "F6 L5 M5",
			"from=2019-03-06":                  "F6",
		} {
			got := w.Do(replay.Request{Role: "admin", Path: "/api/activity?" + query})
			var list struct {
				Summaries []struct {
					DeviceID string `json:"device_id"`
				}
			}
			_ = json.Unmarshal([]byte(got.Body), &list)
			devices := make([]string, len(list.Summaries))
			for i, s := range list.Summaries {
				devices[i] = s.DeviceID
			}
			if got.Status != 200 || strings.Join(devices, " ") != want {
				t.Errorf("the summaries of GET /api/activity?%s: %d %q, want 200 %q: %s", query, got.Status, strings.Join(devices, " "), want, got.Body)
			}
		}
		top := w.Do(replay.Request{Role: "admin", Path: "/api/activity/D1/top?to=2019-03-05&from=2019-03-05"})
		if have := sessionsOf(top.Body); top.Status != 200 || (have != "L5 M5" && have != "M5 L5") {
			t.Errorf("the top apps of the day 2019-03-05: %d %q: %s", top.Status, have, top.Body)
		}
	})
}

// A device is shown under the hostname in its info only when that is text
// with something in it, else under its id: the web app's schema takes
// text there (BUGS AI6).
func TestAHostnameThatIsNoTextIsNotShown(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for id, hostname := range map[string]string{"H-NUMBER": "5", "H-FRACTION": "0.5", "H-TRUE": "true", "H-LIST": `["pc"]`, "H-OBJECT": `{"name":"pc"}`, "H-TEXT": `"pc"`} {
			w.Exec("INSERT INTO peer (id, note, info) VALUES (?, '', ?)", []any{id, `{"hostname":` + hostname + `}`})
			w.Exec("INSERT INTO activity_summaries (device_id, reported_at) VALUES (?, '2019-03-04T00:00:00.000Z')", []any{id})
		}
		list := w.Do(replay.Request{Role: "admin", Path: "/api/activity"})
		for _, id := range []string{"H-NUMBER", "H-FRACTION", "H-TRUE", "H-LIST", "H-OBJECT"} {
			if want := `"device_id":"` + id + `","hostname":"` + id + `"`; list.Status != 200 || !strings.Contains(list.Body, want) {
				t.Errorf("the list: %d, lacks %s: %s", list.Status, want, list.Body)
			}
			if got := w.Do(replay.Request{Role: "admin", Path: "/api/activity/" + id}); !strings.HasPrefix(got.Body, `{"device_id":"`+id+`","hostname":"`+id+`"`) {
				t.Errorf("GET /api/activity/%s: %d %s", id, got.Status, got.Body)
			}
		}
		if !strings.Contains(list.Body, `"device_id":"H-TEXT","hostname":"pc"`) {
			t.Errorf("the list lacks the device named by text: %s", list.Body)
		}
	})
}

// withoutCopies changes what the recording reads after the lookups of
// devices only the signal server knows, or that the console deleted, into
// what a read that writes nothing leaves (BUGS AI7): the console's table
// keeps only the two deleted rows the recording put there, as it put them,
// and G-DELETED-HERE is named by the signal server's hostname, where Node
// showed the note of the row its lookup had brought back. It reports how
// many steps it changed.
func withoutCopies(rec *replay.Recording, db *store.DB) int {
	changed := 0
	for i, step := range rec.Steps {
		switch {
		case step.Name == "the copies":
			rec.Steps[i].Rows = db.Pick(
				`[{"id":"G-DELETED-HERE","uuid":"old-uuid","note":"old note","info":"{\"hostname\":\"old-host\"}","ip":"10.9.9.9","user":"old-user","status_online":0,"is_deleted":1,"is_banned":0,"banned_reason":"","tags":"","last_online":"2018-01-01 00:00:00","created_at":"2017-01-01 00:00:00","banned_at":null},`+
					`{"id":"G-DELETED-HERE-INFO","uuid":"old-uuid","note":"","info":"{\"hostname\":\"kept-host\"}","ip":"10.9.9.8","user":"old-user","status_online":0,"is_deleted":1,"is_banned":0,"banned_reason":"","tags":"","last_online":"2018-01-01 00:00:00","created_at":"2017-01-01 00:00:00","banned_at":null}]`,
				`[{"id":"G-DELETED-HERE","uuid":"old-uuid","note":"old note","info":"{\"hostname\": \"old-host\"}","ip":"10.9.9.9","user":"old-user","status_online":false,"is_deleted":true,"is_banned":false,"banned_reason":""},`+
					`{"id":"G-DELETED-HERE-INFO","uuid":"old-uuid","note":"","info":"{\"hostname\": \"kept-host\"}","ip":"10.9.9.8","user":"old-user","status_online":false,"is_deleted":true,"is_banned":false,"banned_reason":""}]`)
			changed++
		case step.Name == "the times of the copies":
			rec.Steps[i].Rows = `[{"id":"G-DELETED-HERE","last_online":"2018-01-01 00:00:00","created_at":"2017-01-01 00:00:00","banned_at":null},` +
				`{"id":"G-DELETED-HERE-INFO","last_online":"2018-01-01 00:00:00","created_at":"2017-01-01 00:00:00","banned_at":null}]`
			changed++
		case strings.Contains(step.Answer, `"device_id":"G-DELETED-HERE","hostname":"old note"`):
			rec.Steps[i].Answer = strings.Replace(step.Answer, `"hostname":"old note"`, `"hostname":"came-back"`, 1)
			changed++
		}
	}
	return changed
}

// The most used apps are one row per app on both databases, under the
// category the app was used the longest in, and the sums and counts are
// numbers, as the web app's schema wants them. PostgreSQL made one row
// per app and category and sent the numbers as text, as it did the detail
// count of the list (BUGS AI10).
func TestTopAppsAreTheSameOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		for _, s := range []struct {
			app, category string
			minute, secs  int
		}{{"Mail", "communication", 1, 600}, {"Notes", "other", 2, 10}, {"Mail", "other", 3, 300}, {"Mail", "other", 4, 400}} {
			w.Exec("INSERT INTO activity_sessions (device_id, app_name, category, started_at, ended_at, duration_secs) VALUES ('T', ?, ?, ?, ?, ?)",
				[]any{s.app, s.category, fmt.Sprintf("2019-03-04T08:%02d:00.000Z", s.minute), fmt.Sprintf("2019-03-04T08:%02d:30.000Z", s.minute), s.secs})
		}
		w.Exec("INSERT INTO activity_summaries (device_id, reported_at) VALUES ('T', '2019-03-04T08:00:00.000Z')", nil)
		want := `{"device_id":"T","top_apps":[{"app_name":"Mail","category":"other","total_secs":1300,"session_count":3},{"app_name":"Notes","category":"other","total_secs":10,"session_count":1}]}`
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/activity/T/top"}); got.Status != 200 || got.Body != want {
			t.Errorf("the top apps: %d %s\nwant %s", got.Status, got.Body, want)
		}
		want = `{"device_id":"T","top_apps":[{"app_name":"Mail","category":"other","total_secs":1300,"session_count":3}]}`
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/activity/T/top?limit=1"}); got.Status != 200 || got.Body != want {
			t.Errorf("the top app: %d %s\nwant %s", got.Status, got.Body, want)
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/activity"}); got.Status != 200 || !strings.Contains(got.Body, `"detail_count":4,`) {
			t.Errorf("the list: %d %s, want the detail count 4 as a number", got.Status, got.Body)
		}
	})
}
