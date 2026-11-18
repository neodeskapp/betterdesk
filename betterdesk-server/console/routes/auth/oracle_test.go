package auth_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The recordings of testdata/nodeoracle/auth.js: what the Node console
// answered for the account typeahead and for sign-ins that the lockout
// check decides, on each database.

type oracleSeed struct {
	SQL    string
	Params []any
}

func (s *oracleSeed) UnmarshalJSON(raw []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		return fmt.Errorf("a seed is [sql, params]: %s", raw)
	}
	if err := json.Unmarshal(pair[0], &s.SQL); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &s.Params)
}

type lookupRecording struct {
	Dialect string       `json:"dialect"`
	Seed    []oracleSeed `json:"seed"`
	Steps   []struct {
		Query  string `json:"query"`
		Status int    `json:"status"`
		Answer string `json:"answer"`
	} `json:"steps"`
}

type lockoutRecording struct {
	Dialect string       `json:"dialect"`
	Seed    []oracleSeed `json:"seed"`
	Steps   []struct {
		Label      string `json:"label"`
		IP         string `json:"ip"`
		Body       string `json:"body"`
		Status     int    `json:"status"`
		RetryAfter string `json:"retryAfter"`
		Answer     string `json:"answer"`
	} `json:"steps"`
	Selects map[string]struct {
		SQL  string           `json:"sql"`
		Rows []map[string]any `json:"rows"`
	} `json:"selects"`
}

func readRecording(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("read the recording %s: %v", name, err)
	}
}

// oracleArgs are the parameters of a seed: a whole number is bound as an
// integer.
func oracleArgs(params []any) []any {
	out := make([]any, len(params))
	for i, p := range params {
		out[i] = p
		if f, ok := p.(float64); ok && f == float64(int64(f)) {
			out[i] = int64(f)
		}
	}
	return out
}

func dialectName(db *store.DB) string { return db.Pick("sqlite", "postgres") }

func seedRecording(w *world, seed []oracleSeed) {
	w.t.Helper()
	for _, s := range seed {
		w.exec(s.SQL, oracleArgs(s.Params)...)
	}
}

// TestLookupAnswersOfNode replays every typeahead request of the
// recording: missing, repeated and bracketed parameters, prefixes in
// every letter case with white space around them, the length limits in
// UTF-16 units, and names whose lower case JavaScript writes its own way.
func TestLookupAnswersOfNode(t *testing.T) {
	eachWorld(t, setup{env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "1000000"}}, func(t *testing.T, w *world) {
		var rec lookupRecording
		readRecording(t, "lookup_"+dialectName(w.db)+".json", &rec)
		seedRecording(w, rec.Seed)
		a := w.agent()
		failed := 0
		for _, step := range rec.Steps {
			target := "/login/lookup"
			if step.Query != "" {
				target += "?" + step.Query
			}
			got := a.get(target)
			if got.status != step.Status || got.body != step.Answer {
				if failed++; failed <= 20 {
					t.Errorf("%s:\n   node: %d %s\n     go: %d %s", step.Query, step.Status, step.Answer, got.status, got.body)
				}
			}
		}
		if failed > 20 {
			t.Errorf("%d more differences", failed-20)
		}
		if len(rec.Steps) < 800 {
			t.Errorf("the recording has only %d requests", len(rec.Steps))
		}
	})
}

// retryAfter matches the seconds of a lockout in an answer.
var retryAfter = regexp.MustCompile(`"retry_after":(\d+)`)

// roundWait puts the seconds left of a lockout on the full ones the seed
// gave (600, and 900 for a new lockout or a blocked address): the replay
// runs the steps later than the recording did, by up to two minutes under
// the race detector. The 60 seconds of an unreadable end are exact.
func roundWait(text string) string {
	n, err := strconv.Atoi(text)
	if err != nil {
		return text
	}
	for _, full := range []int{600, 900} {
		if n <= full && n > full-120 {
			return strconv.Itoa(full)
		}
	}
	return text
}

// TestLockoutAnswersOfNode replays the sign-ins of the recording that the
// lockout check decides: locked names and addresses, user names of every
// JSON type as each database driver bound them, the order of the checks,
// a name locked by its own failures, and the attempts, lockouts and audit
// entries all of that left.
func TestLockoutAnswersOfNode(t *testing.T) {
	eachWorld(t, setup{trustProxy: "true", env: map[string]string{"LOGIN_RATE_LIMIT_MAX": "1000000"}}, func(t *testing.T, w *world) {
		var rec lockoutRecording
		readRecording(t, "lockout_"+dialectName(w.db)+".json", &rec)
		seedRecording(w, rec.Seed)
		for _, step := range rec.Steps {
			got := w.agentFrom(step.IP).post("/api/auth/login", step.Body)
			wantBody := retryAfter.ReplaceAllStringFunc(step.Answer, func(m string) string { return `"retry_after":` + roundWait(m[14:]) })
			gotBody := retryAfter.ReplaceAllStringFunc(got.body, func(m string) string { return `"retry_after":` + roundWait(m[14:]) })
			if got.status != step.Status || gotBody != wantBody {
				t.Errorf("%s (%s):\n   node: %d %s\n     go: %d %s", step.Label, step.Body, step.Status, step.Answer, got.status, got.body)
			}
			if roundWait(got.header.Get("Retry-After")) != roundWait(step.RetryAfter) {
				t.Errorf("%s: Retry-After %q, node %q", step.Label, got.header.Get("Retry-After"), step.RetryAfter)
			}
		}
		for _, name := range []string{"attempts", "lockouts", "audit"} {
			sel := rec.Selects[name]
			var want []string
			for _, row := range sel.Rows {
				want = append(want, rowLine(sel.SQL, row))
			}
			equal(t, name, w.rows(sel.SQL), want)
		}
		if len(rec.Steps) < 60 {
			t.Errorf("the recording has only %d requests", len(rec.Steps))
		}
	})
}

// selectColumns are the column names of a recorded SELECT, in order.
var selectColumns = regexp.MustCompile(`(?:AS )?(\w+)(?:,| FROM)`)

// rowLine is a recorded row as world.rows writes one: the columns in the
// order of the statement, joined by "|", NULL as "".
func rowLine(query string, row map[string]any) string {
	head, _, _ := strings.Cut(query, " FROM ")
	var parts []string
	for _, m := range selectColumns.FindAllStringSubmatch(head+" FROM", -1) {
		switch v := row[m[1]].(type) {
		case nil:
			parts = append(parts, "")
		case float64:
			parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64))
		default:
			parts = append(parts, fmt.Sprint(v))
		}
	}
	return strings.Join(parts, "|")
}

func TestRowLine(t *testing.T) {
	got := rowLine(`SELECT username, ip_address, CASE WHEN success = 1 THEN 1 ELSE 0 END AS success FROM login_attempts ORDER BY id`,
		map[string]any{"username": "a", "ip_address": nil, "success": float64(1)})
	if got != "a||1" {
		t.Errorf("rowLine = %q", got)
	}
}
