package reports

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

// parseBody reads text the way the console's body parser reads a JSON
// body; false when the parser refuses it or it is no object.
func parseBody(text string) (middleware.JSONObject, bool) {
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return middleware.JSONObject{}, false
	}
	var got middleware.JSONObject
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.Body(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(text))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return got, rec.Code == http.StatusOK
}

// csvFields splits CSV text as toCsv writes it back into lines of fields:
// a field in quotes holds doubled quotes, commas and "\n"; lines end at a
// "\n" outside quotes.
func csvFields(text string) ([][]string, bool) {
	lines := [][]string{{}}
	var field strings.Builder
	quoted, inQuotes := false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case inQuotes && c == '"' && i+1 < len(text) && text[i+1] == '"':
			field.WriteByte('"')
			i++
		case inQuotes && c == '"':
			inQuotes = false
		case inQuotes:
			field.WriteByte(c)
		case c == '"' && field.Len() == 0 && !quoted:
			inQuotes, quoted = true, true
		case c == '"':
			return nil, false
		case c == ',' || c == '\n':
			lines[len(lines)-1] = append(lines[len(lines)-1], field.String())
			field.Reset()
			quoted = false
			if c == '\n' {
				lines = append(lines, []string{})
			}
		default:
			if quoted {
				return nil, false
			}
			field.WriteByte(c)
		}
	}
	if inQuotes {
		return nil, false
	}
	lines[len(lines)-1] = append(lines[len(lines)-1], field.String())
	return lines, true
}

// FuzzToCSV writes rows of any JSON as CSV and reads them back: there is
// a line per row after the header, a field per column, and each field is
// the JavaScript text of the value, or empty for null and missing values.
func FuzzToCSV(f *testing.F) {
	for _, seed := range []string{
		`[{"a":1,"b":"x"}]`, `[{"a,b":"q\"","c":"line\nbreak"},{"c":null}]`, `[{"2":1,"10":2,"x":[1,[2,"a,b"]]}]`,
		`[{"o":{"a":1},"cr":"a\rb","u":"Ü "}]`, `[]`, `[{}]`, `[{"a":1},{"b":2}]`, `[{"":"","\"":"\"\""}]`,
	} {
		f.Add(seed)
	}
	e := New(Options{})
	f.Fuzz(func(t *testing.T, text string) {
		parsed, err := goapi.Parse([]byte(text))
		if err != nil {
			return
		}
		rows, ok := parsed.([]any)
		if !ok {
			return
		}
		out := e.ToCSV(rows)
		if len(rows) == 0 {
			if out != "" {
				t.Fatalf("no rows gave %q", out)
			}
			return
		}
		first, isObject := rows[0].(*goapi.Object)
		if !isObject {
			return
		}
		columns := first.Keys()
		lines, readable := csvFields(out)
		if !readable {
			t.Fatalf("%s gave CSV that does not read back: %q", text, out)
		}
		if len(lines) != len(rows)+1 {
			t.Fatalf("%s gave %d lines for %d rows: %q", text, len(lines), len(rows), out)
		}
		width := len(columns)
		if width == 0 {
			width = 1 // an empty line reads as one empty field
		}
		for i, line := range lines {
			if len(line) != width {
				t.Fatalf("%s: line %d has %d fields for %d columns: %q", text, i, len(line), len(columns), out)
			}
		}
		for i, column := range columns {
			if lines[0][i] != column {
				t.Fatalf("%s: header %d is %q, want %q", text, i, lines[0][i], column)
			}
			for r, row := range rows {
				v, found := prop(row, column)
				want := ""
				if found && v != nil {
					want = e.clock.String(v)
				}
				if lines[r+1][i] != want {
					t.Fatalf("%s: row %d column %q is %q, want %q", text, r, column, lines[r+1][i], want)
				}
			}
		}
	})
}

// FuzzGenerate makes reports for request bodies of any JSON on a database
// with rows in every table: a report or an error, never a panic, and a
// report is valid JSON whose type and filters are those of the body.
func FuzzGenerate(f *testing.F) {
	for _, seed := range []string{
		`{"type":"devices"}`, `{"type":"activity","from":"2019-11-01","to":["2019-12-01"]}`, `{"type":["security"]}`,
		`{"type":"constructor"}`, `{"type":{"a":1},"from":{}}`, `{"type":"inventory","device_id":{"b":1,"2":[1e400]},"limit":-0}`,
		`{"type":"alerts","section":"recent_alerts"}`, `{"type":"network","section":["targets"]}`, `{"type":"tickets","from":true}`,
	} {
		f.Add(seed)
	}
	db := fuzzDatabase(f)
	e := New(Options{DB: db})
	f.Fuzz(func(t *testing.T, text string) {
		body, parsed := parseBody(text)
		if !parsed {
			return
		}
		typ := jsbind.Of(body.Get("type"))
		filters := Filters{From: jsbind.Of(body.Get("from")), To: jsbind.Of(body.Get("to"))}
		report, err := e.Generate(context.Background(), typ, filters)
		if err != nil {
			return
		}
		out, _ := report.MarshalJSON()
		if !json.Valid(out) {
			t.Fatalf("%s gave JSON that does not parse: %s", text, out)
		}
		var got struct {
			Type    json.RawMessage `json:"type"`
			Filters json.RawMessage `json:"filters"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if string(got.Type) != typ.JSON() || string(got.Filters) != filters.JSON() {
			t.Fatalf("%s: type %s, filters %s", text, got.Type, got.Filters)
		}
		e.ToCSV(report.CSVRows(jsbind.Of(body.Get("section"))))
	})
}

// fuzzDatabase is a SQLite database with rows the engine reads.
func fuzzDatabase(f *testing.F) *store.DB {
	db, err := store.OpenSQLite(filepath.Join(f.TempDir(), "console.sqlite3"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		f.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO peer (id, info, status_online, is_banned, is_deleted) VALUES ('1', '{"os":"constructor","hostname":{"a":1}}', 1, 1, 0)`,
		`INSERT INTO peer (id, info, status_online, is_banned, is_deleted) VALUES ('2', 'not json', 0, 1, 0)`,
		`INSERT INTO activity_summaries (device_id, total_active_secs, idle_seconds, reported_at, received_at) VALUES ('A', 'abc', 1.5, '2019-11-01T00:00:00.000Z', '2019-11-01 00:00:00')`,
		`INSERT INTO activity_sessions (device_id, app_name, started_at, ended_at, duration_secs) VALUES ('A', '__proto__', '2019-11-01T00:00:00.000Z', '2019-11-01T01:00:00.000Z', 10)`,
		`INSERT INTO activity_sessions (device_id, app_name, started_at, ended_at, duration_secs) VALUES ('A', 'x,"y"', '2019-11-01T00:00:00.000Z', '2019-11-01T01:00:00.000Z', 1e308)`,
		`INSERT INTO dlp_events (device_id, details, created_at) VALUES ('D', '"x"', '2019-11-01T10:00:00.930Z')`,
		`INSERT INTO audit_log (user_id, action, details, created_at) VALUES (1, 'a', 'b,"c"', '2019-11-01 10:00:00')`,
		`INSERT INTO tickets (title, created_by, category, priority) VALUES ('t', 'x', '__proto__', NULL)`,
		`INSERT INTO device_inventory (device_id, hardware) VALUES ('I', '{"os_name":[1],"memory":{"total_bytes":"1e999"}}')`,
		`INSERT INTO alert_rules (id, name, condition_type, enabled) VALUES (1, 'r', 'cpu', 'yes')`,
		`INSERT INTO alert_history (rule_id, triggered_at, message) VALUES (1, '2019-11-01T00:00:00.000Z', 'm')`,
		`INSERT INTO network_targets (name, last_status, last_rtt_ms) VALUES ('n', 'UP', 0.5)`,
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			f.Fatalf("%s: %v", q, err)
		}
	}
	return db
}
