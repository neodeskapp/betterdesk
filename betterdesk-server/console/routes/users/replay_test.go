package users_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is what testdata/nodeoracle/users.js recorded from the Node
// console's account routes on one database shape.
type recording struct {
	Dialect string              `json:"dialect"`
	Shape   string              `json:"shape"`
	Seed    []replay.Seed       `json:"seed"`
	API     map[string]apiReply `json:"api"`
	Steps   []recordedStep      `json:"steps"`
}

type apiReply struct {
	Status int    `json:"status"`
	Type   string `json:"type"`
	Body   string `json:"body"`
}

type apiCall struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type recordedStep struct {
	Name    string            `json:"name"`
	Role    string            `json:"role"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    *string           `json:"body"`
	Headers map[string]string `json:"headers"`
	Capture map[string]string `json:"capture"`
	SQL     string            `json:"sql"`
	Params  []any             `json:"params"`
	Rows    string            `json:"rows"`
	Status  int               `json:"status"`
	Answer  string            `json:"answer"`
	Extra   struct {
		Calls []apiCall `json:"calls"`
	} `json:"extra"`
}

// The four shapes a database can have: the Node console's own tables, or
// the ones the signal server made first, on each database.
var variants = []struct {
	name     string
	postgres bool
	origin   storetest.Origin
}{
	{"sqlite", false, storetest.NodeFirst},
	{"sqlite-go", false, storetest.GoServerFirst},
	{"postgres", true, storetest.NodeFirst},
	{"postgres-go", true, storetest.GoServerFirst},
}

// TestNodeAnswers replays what the Node console's account routes
// answered (testdata/node_<shape>.json): every route as every role, the
// order of the checks, roles above the caller's own, the password limit,
// values of every JSON type, the rows written, the audit entries and the
// calls to the API server. The Go console must answer the same, byte for
// byte after times and random guids, on a database of the same shape,
// except for the steps of fixes (fixes_test.go).
func TestNodeAnswers(t *testing.T) {
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			var db *store.DB
			if v.postgres {
				db = storetest.Postgres(t)
			} else {
				db = storetest.SQLite(t)
			}
			db = storetest.Prepare(t, db, v.origin)
			rec := loadRecording(t, v.name)
			newReplayer(t, db, rec).run()
		})
	}
}

func loadRecording(t *testing.T, name string) recording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_"+name+".json"))
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	return rec
}

// langDir is the folder of the test console's language file: the English
// texts of the console's lang/en.json that the routes answer with.
func langDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "lang"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

type replayer struct {
	t      *testing.T
	db     *store.DB
	rec    recording
	world  *routetest.World
	api    *routetest.API
	agents map[string]*routetest.Agent
	vars   map[string]string
}

func newReplayer(t *testing.T, db *store.DB, rec recording) *replayer {
	api := routetest.NewAPI()
	api.Fallback = func(c routetest.Call) routetest.Reply {
		path, _, _ := strings.Cut(c.Target, "?")
		if reply, ok := rec.API[c.Method+" "+path]; ok {
			return routetest.Reply{Status: reply.Status, Type: reply.Type, Body: reply.Body}
		}
		return routetest.Reply{Status: http.StatusNotFound, Body: `{"error":"not found"}`}
	}
	lang := langDir(t)
	trust, err := httpx.ParseTrustProxy("true")
	if err != nil {
		t.Fatal(err)
	}
	w := routetest.New(t, routetest.Options{
		DB:  db,
		API: api,
		Env: map[string]string{
			"RATE_LIMIT_MAX": "1000000", "PANEL_READ_RATE_LIMIT_MAX": "1000000", "WIDGET_RATE_LIMIT_MAX": "1000000",
			"LOGIN_RATE_LIMIT_MAX": "1000000", "UPLOAD_RATE_LIMIT_MAX": "1000000", "FILE_ACCESS_RATE_LIMIT_MAX": "1000000",
		},
		Config: func(cfg *config.Config) {
			cfg.LangDir = lang
			cfg.TrustProxy = trust
		},
	})
	// A session whose userId is text, as the sign-in writes it for 64-bit
	// ids, and back
	w.Server.Router().Get("/__users/session-id/{form}", func(rw http.ResponseWriter, r *http.Request) {
		id := kit.UserID(r)
		if r.PathValue("form") == "text" {
			kit.Session(r).Set("userId", strconv.FormatInt(id, 10))
		} else {
			kit.Session(r).Set("userId", id)
		}
		kit.OK(rw, r, nil)
	})
	return &replayer{t: t, db: db, rec: rec, world: w, api: api, agents: map[string]*routetest.Agent{}, vars: map[string]string{}}
}

// args are the parameters of a statement of the recording: a whole number
// is bound as an integer.
func args(params []any) []any {
	out := make([]any, len(params))
	for i, p := range params {
		out[i] = p
		if f, ok := p.(float64); ok && f == float64(int64(f)) {
			out[i] = int64(f)
		}
	}
	return out
}

func (p *replayer) agent(role string) *routetest.Agent {
	if a := p.agents[role]; a != nil {
		return a
	}
	a := p.world.SignedOut()
	if role != "" {
		a = p.world.As(role)
	}
	p.agents[role] = a
	return a
}

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func (p *replayer) fill(path string) string {
	return placeholder.ReplaceAllStringFunc(path, func(m string) string {
		if v, ok := p.vars[m[1:len(m)-1]]; ok {
			return url.PathEscape(v)
		}
		return "999999999"
	})
}

func (p *replayer) run() {
	t := p.t
	ctx := context.Background()
	for _, seed := range p.rec.Seed {
		if _, err := p.db.Exec(ctx, seed.SQL, args(seed.Params)...); err != nil {
			t.Fatalf("seed %s: %v", seed.SQL, err)
		}
	}
	failed := 0
	// USERS_REPLAY_FAILFAST=1 stops at the first difference, for mutation runs
	failFast := os.Getenv("USERS_REPLAY_FAILFAST") != ""
	report := func(format string, a ...any) {
		failed++
		if failed <= 40 {
			t.Errorf(format, a...)
		}
		if failFast {
			t.FailNow()
		}
	}
	for i, step := range p.rec.Steps {
		if step.SQL != "" {
			if !strings.HasPrefix(step.SQL, "SELECT") {
				if _, err := p.db.Exec(ctx, step.SQL, args(step.Params)...); err != nil {
					t.Fatalf("step %d %q: %v", i, step.Name, err)
				}
				continue
			}
			have := p.rows(step.SQL, step.Params)
			if strings.Contains(step.SQL, "FROM audit_log") {
				have = nodeLabels(have)
			}
			if normalize(have) != normalize(step.Rows) {
				report("step %d %q:\n   node: %s\n     go: %s", i, step.Name, step.Rows, have)
			}
			continue
		}
		got := p.send(step)
		status, answer, wantCalls := step.Status, step.Answer, nodeCalls(step.Extra.Calls)
		f, fixed := fixFor(p.rec, step.Name)
		if fixed {
			if f.status != 0 {
				status, wantCalls = f.status, nil
			}
			if f.answer != nil {
				answer = f.answer(step.Answer)
			}
		}
		want, have := normalize(answer), normalize(got.Body)
		if got.Status != status || want != have {
			body := ""
			if step.Body != nil {
				body = " " + *step.Body
			}
			label := "node"
			if fixed {
				label = "fix " + f.bug
			}
			report("step %d %q: %s %s%s as %q\n   %s: %d %s\n     go: %d %s", i, step.Name, step.Method, step.Path, body, step.Role, label, status, want, got.Status, have)
		}
		if haveCalls := p.api.Calls(); !sameCalls(wantCalls, haveCalls) {
			report("step %d %q: calls to the API server\n   node: %q\n     go: %q", i, step.Name, wantCalls, haveCalls)
		}
		p.nodeGUIDs(step)
		if fixed && f.node != nil {
			f.node(&nodeWrites{p: p, step: step})
		}
		// The step ended these roles' sessions, which Node's kept: the
		// steps after it sign in again
		for _, role := range f.endsSessionsOf {
			delete(p.agents, role)
		}
		p.capture(step, got.Body)
	}
	if failed > 40 {
		t.Errorf("%d more differences", failed-40)
	}
}

// nodeCalls are the calls the Node console made to the API server, without
// the list of users that its user sync asked for on PostgreSQL to copy
// roles back (not ported, DECISIONS A17, BUGS D13).
func nodeCalls(calls []apiCall) []string {
	var out []string
	for _, c := range calls {
		if c.Method == http.MethodGet && c.URL == "/api/users" {
			continue
		}
		line := c.Method + " " + c.URL
		if c.Body != "" {
			line += " " + c.Body
		}
		out = append(out, line)
	}
	return out
}

// sameCalls compares two lists of calls with the random guids in them
// masked.
func sameCalls(a, b []string) bool {
	mask := func(list []string) string { return callGUID.ReplaceAllString(strings.Join(list, "\n"), "<guid>") }
	return mask(a) == mask(b)
}

func (p *replayer) send(step recordedStep) routetest.Answer {
	a := p.agent(step.Role)
	var header []string
	textID := false
	for name, value := range step.Headers {
		if name == "X-Oracle-String-Id" {
			textID = true
			continue
		}
		header = append(header, name, value)
	}
	if textID {
		a.Do(http.MethodGet, "/__users/session-id/text", "")
		defer a.Do(http.MethodGet, "/__users/session-id/number", "")
	}
	body := ""
	if step.Body != nil {
		body = *step.Body
	}
	return a.Do(step.Method, p.fill(step.Path), body, header...)
}

func (p *replayer) capture(step recordedStep, body string) {
	if len(step.Capture) == 0 {
		return
	}
	var parsed any
	if json.Unmarshal([]byte(body), &parsed) != nil {
		return
	}
	for name, dotted := range step.Capture {
		v := parsed
		for _, key := range strings.Split(dotted, ".") {
			object, _ := v.(map[string]any)
			v = object[key]
		}
		switch x := v.(type) {
		case string:
			p.vars[name] = x
		case float64:
			p.vars[name] = jsbind.NumberString(x)
		}
	}
}

// rows runs a SELECT of the recording and returns its rows as the JSON the
// Node drivers' rows had. The statements select text and whole numbers
// only.
func (p *replayer) rows(query string, params []any) string {
	p.t.Helper()
	rows, err := p.db.Query(context.Background(), query, args(params)...)
	if err != nil {
		p.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		p.t.Fatal(err)
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for n := 0; rows.Next(); n++ {
		cells := make([]jsbind.Cell, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			p.t.Fatalf("%s: %v", query, err)
		}
		if n > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		for i, column := range columns {
			if i > 0 {
				out.WriteByte(',')
			}
			name, _ := json.Marshal(column)
			value, _ := cells[i].MarshalJSON()
			out.Write(name)
			out.WriteByte(':')
			out.Write(value)
		}
		out.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		p.t.Fatalf("%s: %v", query, err)
	}
	out.WriteByte(']')
	return out.String()
}

// Times the database fills in and random guids differ from run to run.
var (
	runTime  = regexp.MustCompile(`"\d{4}-\d\d-\d\d( |T)\d\d:\d\d:\d\d(\.\d{3}Z)?"`)
	runGUID  = regexp.MustCompile(`"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"`)
	callGUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
)

func normalize(s string) string {
	return runGUID.ReplaceAllString(runTime.ReplaceAllString(s, `"<time>"`), `"<guid>"`)
}
