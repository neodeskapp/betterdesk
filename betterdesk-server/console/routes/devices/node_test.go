package devices_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is what testdata/nodeoracle/routes.js recorded: the rows to
// start from, the API server's table of answers, and each request with
// the answer of the Node console's route and the calls it made.
type recording struct {
	Dialect string           `json:"dialect"`
	Seed    []replay.Seed    `json:"seed"`
	Table   map[string]reply `json:"table"`
	Steps   []struct {
		Name   string           `json:"name"`
		Role   string           `json:"role"`
		Method string           `json:"method"`
		Path   string           `json:"path"`
		Body   *string          `json:"body"`
		API    map[string]reply `json:"api"`
		SQL    string           `json:"sql"`
		Status int              `json:"status"`
		Answer string           `json:"answer"`
		Rows   string           `json:"rows"`
		// StatusOnly marks a request that reached the end of the oracle's
		// app, whose 404 is its own: the status and the calls are compared.
		StatusOnly bool `json:"statusOnly"`
		Extra      struct {
			Calls []string `json:"calls"`
		} `json:"extra"`
	} `json:"steps"`
}

// reply is one answer of the made-up API server: JSON, text, no body, or
// a connection closed without an answer.
type reply struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	Text   *string         `json:"text"`
	Hangup bool            `json:"hangup"`
}

// fakeAPI answers from the table of a recording, with the answers of the
// step in front of it, and remembers what it was asked.
type fakeAPI struct {
	table    map[string]reply
	mu       sync.Mutex
	override map[string]reply
	calls    []string
}

func (a *fakeAPI) step(override map[string]reply) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.override, a.calls = override, nil
}

func (a *fakeAPI) taken() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.RequestURI
	a.mu.Lock()
	call := key
	if len(raw) > 0 {
		call += " " + string(raw)
	}
	a.calls = append(a.calls, call)
	answer, ok := a.override[key]
	if !ok {
		answer, ok = a.table[key]
	}
	a.mu.Unlock()
	if !ok {
		answer = reply{Status: http.StatusNotFound, Body: json.RawMessage(`{"error":"not found"}`)}
	}
	if answer.Hangup {
		panic(http.ErrAbortHandler)
	}
	if answer.Text != nil {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(answer.Status)
		_, _ = io.WriteString(w, *answer.Text)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.Status)
	var body bytes.Buffer
	if len(answer.Body) > 0 {
		_ = json.Compact(&body, answer.Body)
	}
	_, _ = w.Write(body.Bytes())
}

// randomGUID is a guid the database or the console made up during a run.
var randomGUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

// loneSurrogate is half of a character outside the Basic Multilingual
// Plane, which JSON.stringify writes as an escape and a Go string cannot
// hold: the Go routes write U+FFFD in its place (BUGS DGR2).
var loneSurrogate = regexp.MustCompile(`\\ud[89ab][0-9a-f]{2}`)

func normalize(s string) string {
	s = loneSurrogate.ReplaceAllString(s, "�")
	return randomGUID.ReplaceAllString(replay.Normalize(s), "<guid>")
}

// rows returns the rows of a query as the JSON Node's drivers gave:
// objects with the columns in order.
func rows(t *testing.T, db *store.DB, query string) string {
	t.Helper()
	res, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer res.Close()
	columns, _ := res.Columns()
	var out []string
	for res.Next() {
		cells := make([]jsbind.Cell, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := res.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(columns))
		for i, column := range columns {
			name, _ := json.Marshal(column)
			value, _ := cells[i].MarshalJSON()
			parts[i] = string(name) + ":" + string(value)
		}
		out = append(out, "{"+strings.Join(parts, ",")+"}")
	}
	return "[" + strings.Join(out, ",") + "]"
}

// TestNodeAnswers replays what the Node console's device routes answered
// with the real database adapter, on SQLite and PostgreSQL
// (testdata/node_*.json): lists, one device with what the console adds to
// it, groups and members, tags, every write, odd values of every JSON
// type, failures of the API server. The Go routes must answer the same,
// byte for byte, make the same calls to the API server and leave the same
// rows.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		raw, err := os.ReadFile(filepath.Join("testdata", "node_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec recording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		api := &fakeAPI{table: rec.Table}
		w := routetest.New(t, routetest.Options{DB: db, API: api, Env: raisedLimits, Config: nodeLanguages})
		ctx := context.Background()
		for _, s := range rec.Seed {
			args := make([]any, len(s.Params))
			for i, p := range s.Params {
				args[i] = p
				if f, ok := p.(float64); ok && f == float64(int64(f)) {
					args[i] = int64(f)
				}
			}
			if _, err := db.Exec(ctx, s.SQL, args...); err != nil {
				t.Fatalf("seed %s: %v", s.SQL, err)
			}
		}
		agents := map[string]*routetest.Agent{}
		failed := 0
		for i, step := range rec.Steps {
			if step.SQL != "" && !strings.HasPrefix(step.SQL, "SELECT") {
				if _, err := db.Exec(ctx, step.SQL); err != nil {
					t.Fatalf("step %d %s: %v", i, step.Name, err)
				}
				continue
			}
			if step.SQL != "" {
				if got := rows(t, db, step.SQL); normalize(got) != normalize(step.Rows) {
					t.Errorf("step %d %s:\n node: %s\n   go: %s", i, step.Name, step.Rows, got)
				}
				continue
			}
			a := agents[step.Role]
			if a == nil {
				if step.Role == "signed-out" {
					a = w.SignedOut()
				} else {
					a = w.As(step.Role)
				}
				agents[step.Role] = a
			}
			api.step(step.API)
			body := ""
			if step.Body != nil {
				body = *step.Body
			}
			got := a.Do(step.Method, step.Path, body, "Accept", "application/json")
			calls := api.taken()
			same := got.Status == step.Status && (step.StatusOnly || normalize(got.Body) == normalize(step.Answer)) &&
				normalize(strings.Join(calls, "\n")) == normalize(strings.Join(step.Extra.Calls, "\n"))
			if same {
				continue
			}
			if failed++; failed <= 30 {
				t.Errorf("step %d %s\n node: %d %s\n       calls %q\n   go: %d %s\n       calls %q", i, step.Name,
					step.Status, step.Answer, step.Extra.Calls, got.Status, got.Body, calls)
			}
		}
		if failed > 30 {
			t.Errorf("%d more steps differ", failed-30)
		}
	})
}

// raisedLimits are the rate limits of the test instances, so the replay
// is not throttled.
var raisedLimits = map[string]string{
	"RATE_LIMIT_MAX": "1000000", "PANEL_READ_RATE_LIMIT_MAX": "1000000", "WIDGET_RATE_LIMIT_MAX": "1000000",
}

// nodeLanguages gives the console the language files of the source tree
// (or of /repo, for a copy of the server without the tree around it).
func nodeLanguages(cfg *config.Config) {
	cfg.LangDir = filepath.Join("..", "..", "..", "..", "web-nodejs", "lang")
	if _, err := os.Stat(filepath.Join(cfg.LangDir, "en.json")); err != nil {
		cfg.LangDir = "/repo/web-nodejs/lang"
	}
}
