package registration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/registrations"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// recording is what testdata/nodeoracle/registration.js recorded from
// the Node console's registration routes on one database.
type recording struct {
	Dialect   string              `json:"dialect"`
	ServerKey string              `json:"serverKey"`
	Seed      []replay.Seed       `json:"seed"`
	API       map[string]apiReply `json:"api"`
	Steps     []recordedStep      `json:"steps"`
}

type apiReply struct {
	Status int    `json:"status"`
	Type   string `json:"type"`
	Body   string `json:"body"`
	Hang   bool   `json:"hang"`
}

type apiCall struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

type recordedStep struct {
	Name          string              `json:"name"`
	Role          string              `json:"role"`
	Method        string              `json:"method"`
	Path          string              `json:"path"`
	Body          *string             `json:"body"`
	Type          string              `json:"type"`
	Headers       map[string]string   `json:"headers"`
	Host          string              `json:"host"`
	API           map[string]apiReply `json:"api"`
	Capture       map[string]string   `json:"capture"`
	SQL           string              `json:"sql"`
	Params        []any               `json:"params"`
	Rows          string              `json:"rows"`
	Notifications bool                `json:"notifications"`
	Items         string              `json:"items"`
	Status        int                 `json:"status"`
	Answer        string              `json:"answer"`
	Calls         []apiCall           `json:"calls"`
	Events        []string            `json:"events"`
}

// TestNodeAnswers replays what the Node console's registration routes
// answered with its real database adapter, permission loading, panel
// access guard, API client, device layer and device group service
// (testdata/node_sqlite.json, testdata/node_postgres.json): every route at
// both mounts as every role, device requests with bodies and addresses of
// every kind, the status a device polls with every query, the list with
// every filter, ids in every spelling, approvals with folders, device
// groups, display names and tags, rejections, deletions, the enrollment
// queue and the help requests with the API server answering in every way,
// and the notification view model. Every answer must be the same, byte
// for byte after the times of the run and the random access tokens, and
// so must the rows written, the audit log, the calls to the API server
// and the events on the panel socket.
func TestNodeAnswers(t *testing.T) {
	if _, offset := time.Now().Zone(); offset != 0 {
		t.Fatal("the recordings were made in UTC: run the tests with TZ=UTC")
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		rec := loadRecording(t, "node_"+db.Dialect().String())
		newReplayer(t, db, rec).run()
	})
}

func loadRecording(t *testing.T, name string) recording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	return rec
}

type replayer struct {
	t     *testing.T
	db    *store.DB
	rec   recording
	world *routetest.World
	api   *routetest.API
	gate  sync.Mutex
	// overrides are the API server's answers for the step being replayed
	overrides map[string]apiReply
	agents    map[string]*agent
	vars      map[string]string
	panel     *wstest.Client
	client    *http.Client
}

type agent struct {
	cookies map[string]string
	token   string
}

func newReplayer(t *testing.T, db *store.DB, rec recording) *replayer {
	p := &replayer{t: t, db: db, rec: rec, agents: map[string]*agent{}, vars: map[string]string{}}
	p.api = routetest.NewAPI()
	p.api.Fallback = func(c routetest.Call) routetest.Reply {
		path, _, _ := strings.Cut(c.Target, "?")
		key := c.Method + " " + path
		p.gate.Lock()
		reply, ok := p.overrides[key]
		p.gate.Unlock()
		if !ok {
			reply, ok = rec.API[key]
		}
		if !ok {
			return routetest.Reply{Status: http.StatusNotFound, Type: "application/json", Body: `{"error":"not found"}`}
		}
		if reply.Hang {
			return routetest.Reply{}
		}
		return routetest.Reply{Status: reply.Status, Type: reply.Type, Body: reply.Body}
	}
	keyFile := filepath.Join(t.TempDir(), "id_ed25519.pub")
	if err := os.WriteFile(keyFile, []byte(rec.ServerKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.world = routetest.New(t, routetest.Options{
		DB:  db,
		API: p.api,
		Env: map[string]string{
			"RATE_LIMIT_MAX": "1000000", "PANEL_READ_RATE_LIMIT_MAX": "1000000", "WIDGET_RATE_LIMIT_MAX": "1000000",
		},
		Config: func(cfg *config.Config) {
			cfg.NodeEnv, cfg.Production = "production", true
			cfg.Port = 5000
			cfg.PubKeyPath = keyFile
		},
	})
	p.client = &http.Client{
		Transport:     &http.Transport{DisableCompression: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       30 * time.Second,
	}
	return p
}

// args are the parameters of a statement of the recording: a whole number
// is bound as an integer.
func args(params []any) []any {
	out := make([]any, len(params))
	for i, v := range params {
		out[i] = v
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			out[i] = int64(f)
		}
	}
	return out
}

func (p *replayer) run() {
	t := p.t
	ctx := context.Background()
	for _, seed := range p.rec.Seed {
		if strings.HasPrefix(seed.SQL, "DELETE FROM users") || strings.HasPrefix(seed.SQL, "INSERT INTO users") {
			// routetest made the same accounts
			continue
		}
		if _, err := p.db.Exec(ctx, seed.SQL, args(seed.Params)...); err != nil {
			t.Fatalf("seed %s: %v", seed.SQL, err)
		}
	}
	p.openPanel()
	failed := 0
	report := func(format string, a ...any) {
		failed++
		if failed <= 30 {
			t.Errorf(format, a...)
		}
	}
	dialect := p.db.Dialect().String()
	steps, errs := fixedSteps(dialect, p.rec.Steps)
	for _, err := range errs {
		report("%v", err)
	}
	fixed := loadFixed(t, dialect)
	rewrite := os.Getenv("REPLAY_FIX")
	written := 0
	for i, step := range steps {
		if step.SQL != "" && !strings.HasPrefix(step.SQL, "SELECT") {
			if _, err := p.db.Exec(ctx, step.SQL, args(step.Params)...); err != nil {
				t.Fatalf("step %d %s: %v", i, step.Name, err)
			}
			continue
		}
		want := outcomeOf(step)
		entry, isFixed := fixed[step.Name]
		if isFixed {
			want = entry.outcome
		}
		have := p.play(step, want)
		if isFixed && len(compare(step, outcomeOf(step), have)) == 0 {
			if rewrite == "" {
				report("step %d %q: listed in %s, but answered as Node answered", i, step.Name, fixedFile(dialect))
			}
			delete(fixed, step.Name)
			written++
			continue
		}
		diffs := compare(step, want, have)
		if len(diffs) == 0 {
			continue
		}
		if rewrite != "" {
			bugs := entry.Bugs
			if !slices.Contains(bugs, rewrite) {
				bugs = append(bugs, rewrite)
			}
			fixed[step.Name] = fixedStep{Bugs: bugs, outcome: have}
			written++
			continue
		}
		for _, d := range diffs {
			report("step %d %q: %s", i, step.Name, d)
		}
	}
	if extra := p.events(0); len(extra) > 0 {
		report("panel events after the last step: %q", extra)
	}
	for _, name := range unknownSteps(fixed, steps) {
		report("%s lists a step the recording does not have: %q", fixedFile(dialect), name)
	}
	if rewrite != "" {
		saveFixed(t, dialect, fixed, steps)
		t.Errorf("REPLAY_FIX=%s: %d steps written into %s; look at the diff, then run without REPLAY_FIX", rewrite, written, fixedFile(dialect))
	}
	if failed > 30 {
		t.Errorf("%d more steps differ", failed-30)
	}
}

// outcome is what a step gave: its status and answer, the rows a query
// read, the notifications, the API server's calls and the panel events.
type outcome struct {
	Status int       `json:"status,omitempty"`
	Answer string    `json:"answer,omitempty"`
	Rows   string    `json:"rows,omitempty"`
	Items  string    `json:"items,omitempty"`
	Calls  []apiCall `json:"calls,omitempty"`
	Events []string  `json:"events,omitempty"`
}

// outcomeOf is what Node gave for the step.
func outcomeOf(step recordedStep) outcome {
	return outcome{Status: step.Status, Answer: step.Answer, Rows: step.Rows, Items: step.Items, Calls: step.Calls, Events: step.Events}
}

// play runs a step and returns what the Go console gave. want is what the
// step should give, for the number of panel events to wait for.
func (p *replayer) play(step recordedStep, want outcome) outcome {
	ctx := context.Background()
	switch {
	case step.SQL != "":
		return outcome{Rows: p.rows(step.SQL, step.Params)}
	case step.Notifications:
		p.setOverrides(step.API)
		p.api.Calls()
		client := goapi.New(p.api)
		items := registrations.New(p.db, client).WithClock(time.Now, time.UTC).Notifications(ctx)
		raw, err := httpx.Marshal(items)
		if err != nil {
			p.t.Fatal(err)
		}
		return outcome{Items: string(raw), Calls: p.calls()}
	}
	p.setOverrides(step.API)
	p.api.Calls()
	status, body := p.send(step)
	o := outcome{Status: status, Answer: body, Calls: p.calls(), Events: p.events(len(want.Events))}
	p.capture(step, body)
	return o
}

// compare lists how what the Go console gave differs from what the step
// should give.
func compare(step recordedStep, want, have outcome) []string {
	var diffs []string
	switch {
	case step.SQL != "":
		if have.Rows != want.Rows {
			diffs = append(diffs, fmt.Sprintf("\n   node: %s\n     go: %s", want.Rows, have.Rows))
		}
		return diffs
	case step.Notifications:
		if normalize(want.Items) != normalize(have.Items) {
			diffs = append(diffs, fmt.Sprintf("\n   node: %s\n     go: %s", normalize(want.Items), normalize(have.Items)))
		}
	default:
		if have.Status != want.Status || normalize(have.Answer) != normalize(want.Answer) {
			diffs = append(diffs, fmt.Sprintf("\n   node: %d %s\n     go: %d %s", want.Status, normalize(want.Answer), have.Status, normalize(have.Answer)))
		}
		if !sameEvents(have.Events, want.Events) {
			diffs = append(diffs, fmt.Sprintf("panel events\n   node: %q\n     go: %q", want.Events, have.Events))
		}
	}
	if !sameCalls(have.Calls, want.Calls) {
		diffs = append(diffs, fmt.Sprintf("API calls\n   node: %v\n     go: %v", want.Calls, have.Calls))
	}
	return diffs
}

// fixedStep is a step of testdata/fixed_<database>.json: what the Go
// console gives since the bug fixes it names, for a step whose answer or
// rows a fix changed, also through what an earlier step stored. The file
// is written by a run with REPLAY_FIX=<bug id> after a fix, from the
// steps that differ, and read as the steps' answers by every other run.
type fixedStep struct {
	Bugs []string `json:"bugs"`
	outcome
}

func fixedFile(dialect string) string {
	return filepath.Join("testdata", "fixed_"+dialect+".json")
}

type fixedEntry struct {
	Step string `json:"step"`
	fixedStep
}

func loadFixed(t *testing.T, dialect string) map[string]fixedStep {
	t.Helper()
	out := map[string]fixedStep{}
	raw, err := os.ReadFile(fixedFile(dialect))
	if errors.Is(err, fs.ErrNotExist) {
		return out
	}
	if err != nil {
		t.Fatal(err)
	}
	var list []fixedEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("read %s: %v", fixedFile(dialect), err)
	}
	for _, e := range list {
		out[e.Step] = e.fixedStep
	}
	return out
}

// saveFixed writes the fixed steps in the order of the recording.
func saveFixed(t *testing.T, dialect string, fixed map[string]fixedStep, steps []recordedStep) {
	t.Helper()
	var list []fixedEntry
	for _, step := range steps {
		if f, ok := fixed[step.Name]; ok {
			list = append(list, fixedEntry{step.Name, f})
		}
	}
	raw, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixedFile(dialect), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unknownSteps are the names of fixed steps the recording does not have.
func unknownSteps(fixed map[string]fixedStep, steps []recordedStep) []string {
	names := map[string]bool{}
	for _, step := range steps {
		names[step.Name] = true
	}
	var out []string
	for name := range fixed {
		if !names[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// fix is how a bug fix changed recorded steps on purpose: the status a
// step answers now, its whole answer, or text of what Node answered, the
// rows it stored, its panel events or its notifications replaced by what
// the Go console writes since the fix. Fixes apply in their order, so a
// later one edits what an earlier one wrote.
type fix struct {
	bug string
	// dialect limits the fix to one database's recording
	dialect string
	// steps are the names of the steps the fix changes; a name ending in
	// "..." stands for the step with the name before it and every step
	// after it, for a fix whose changed rows later steps list
	steps []string
	// status is the step's status now; 0 keeps Node's
	status int
	// answer is the step's whole answer now; "" keeps Node's
	answer string
	// edits are pairs of the text Node wrote and the text Go writes
	edits []string
}

// fixes are the bug fixes that changed answers of the recording.
var fixes = []fix{
	// The replay sends from 127.0.0.1 without TRUST_PROXY: the addresses
	// its requests name in X-Forwarded-For and X-Real-IP are not taken,
	// and a search for one of them finds nothing
	{bug: "RG1", steps: []string{"355 address POST /api/bd/register-request..."}, edits: forwardedAddresses()},
	{bug: "RG1", steps: []string{"411 GET /api/registrations?search=100%25_"}, answer: `{"success":true,"data":[],"total":0}`},
}

// forwardedAddresses are the edits of RG1: each address the requests of
// the replay sent in a header, as a row, an event and a notification show
// it, becomes the connection's address.
func forwardedAddresses() []string {
	var edits []string
	for _, address := range []string{"203.0.113.5", "198.51.100.7", "192.0.2.4", "192.0.2.9", "spaced", "not an address", "198.51.100.8", "2001:db8::1"} {
		for _, form := range []string{`"ip_address":"%s"`, `"ip":"%s"`, `· %s"`} {
			edits = append(edits, fmt.Sprintf(form, address), fmt.Sprintf(form, routetest.ClientIP))
		}
	}
	return edits
}

// fixedSteps returns the steps as the Go console answers them since the
// fixes. A fix that changes no step, and a step a fix names that it does
// not change, are errors, so the list cannot go stale.
func fixedSteps(dialect string, steps []recordedStep) ([]recordedStep, []error) {
	out := make([]recordedStep, len(steps))
	for i, step := range steps {
		step.Events = append([]string(nil), step.Events...)
		out[i] = step
	}
	var errs []error
	for _, f := range fixes {
		if f.dialect != "" && f.dialect != dialect {
			continue
		}
		changed := 0
		for _, name := range f.steps {
			from, isRange := strings.CutSuffix(name, "...")
			first := -1
			for i := range out {
				if out[i].Name == from {
					first = i
					break
				}
			}
			if first < 0 {
				errs = append(errs, fmt.Errorf("the %s fix names a step the recording does not have: %q", f.bug, from))
				continue
			}
			last := first
			if isRange {
				last = len(out) - 1
			}
			for i := first; i <= last; i++ {
				if f.apply(&out[i]) {
					changed++
				} else if !isRange {
					errs = append(errs, fmt.Errorf("the %s fix does not change step %q", f.bug, from))
				}
			}
		}
		if changed == 0 {
			errs = append(errs, fmt.Errorf("the %s fix changes no step", f.bug))
		}
	}
	return out, errs
}

// apply changes the step and reports whether anything changed.
func (f fix) apply(step *recordedStep) bool {
	changed := false
	texts := []*string{&step.Answer, &step.Rows, &step.Items}
	for i := range step.Events {
		texts = append(texts, &step.Events[i])
	}
	for i := 0; i+1 < len(f.edits); i += 2 {
		for _, text := range texts {
			if strings.Contains(*text, f.edits[i]) {
				*text = strings.ReplaceAll(*text, f.edits[i], f.edits[i+1])
				changed = true
			}
		}
	}
	if f.status != 0 && f.status != step.Status {
		step.Status, changed = f.status, true
	}
	if f.answer != "" && f.answer != step.Answer {
		step.Answer, changed = f.answer, true
	}
	return changed
}

func (p *replayer) setOverrides(o map[string]apiReply) {
	p.gate.Lock()
	defer p.gate.Unlock()
	p.overrides = o
}

// calls are the API server's calls of the step, without the role
// permission lookups, which the Node console made and the Go console
// does not, with the escapes of the query that axios did not make undone.
func (p *replayer) calls() []apiCall {
	var out []apiCall
	for _, line := range p.api.Calls() {
		method, rest, _ := strings.Cut(line, " ")
		target, body, _ := strings.Cut(rest, " ")
		if strings.HasPrefix(target, "/api/roles/") {
			continue
		}
		out = append(out, apiCall{Method: method, URL: axiosQuery.Replace(target), Body: body})
	}
	return out
}

func sameCalls(got, want []apiCall) bool {
	var w []apiCall
	for _, c := range want {
		if !strings.HasPrefix(c.URL, "/api/roles/") {
			w = append(w, c)
		}
	}
	if len(got) != len(w) {
		return false
	}
	for i := range got {
		g := got[i]
		g.URL = axiosQuery.Replace(g.URL)
		if g != w[i] {
			return false
		}
	}
	return true
}

// axiosQuery undoes the escapes of the API client's query strings that
// axios did not make: it leaves ",", ":" and "$" as they are, package goapi
// escapes them (url.QueryEscape). The API server reads both alike.
var axiosQuery = strings.NewReplacer("%2C", ",", "%3A", ":", "%24", "$")

// openPanel opens /ws/panel-events as the admin, where the routes' events
// arrive. The socket takes a session only from a page of the console
// itself, so the upgrade names the console as its Origin.
func (p *replayer) openPanel() {
	a := p.agent("admin")
	header := http.Header{"Cookie": {a.cookieHeader()}, "Origin": {"http://" + p.world.Addr()}}
	panel, res, err := wstest.Dial(p.world.Addr(), "/ws/panel-events", header)
	if err != nil || panel == nil {
		status := 0
		if res != nil {
			status = res.StatusCode
		}
		p.t.Fatalf("open the panel socket: %d %v", status, err)
	}
	p.t.Cleanup(func() { panel.Close(1000) })
	p.panel = panel
}

// events waits for n events and returns what came so far.
func (p *replayer) events(n int) []string {
	wait := 2 * time.Second
	if n == 0 {
		wait = 0
	}
	p.panel.Wait(wait, func(frames []wstest.Frame, _ bool) bool { return len(frames) >= n })
	var out []string
	for _, f := range p.panel.Take() {
		out = append(out, f.Text())
	}
	return out
}

func sameEvents(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if normalize(got[i]) != normalize(want[i]) {
			return false
		}
	}
	return true
}

func (a *agent) cookieHeader() string {
	var pairs []string
	for name, value := range a.cookies {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, "; ")
}

func (p *replayer) agent(role string) *agent {
	if a := p.agents[role]; a != nil {
		return a
	}
	a := &agent{cookies: map[string]string{}}
	p.agents[role] = a
	if role != "" {
		if status, body := p.do(a, http.MethodGet, "/__test/signin/"+role, nil, "", nil, ""); status != http.StatusOK {
			p.t.Fatalf("sign in as %s: %d %s", role, status, body)
		}
	}
	var out struct {
		Token string `json:"token"`
	}
	_, body := p.do(a, http.MethodGet, "/__test/token", nil, "", nil, "")
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Token == "" {
		p.t.Fatalf("no CSRF token for %q: %s", role, body)
	}
	a.token = out.Token
	return a
}

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)\}`)

func (p *replayer) send(step recordedStep) (int, string) {
	a := p.agent(step.Role)
	target := placeholder.ReplaceAllStringFunc(step.Path, func(m string) string {
		if v, ok := p.vars[m[1:len(m)-1]]; ok {
			return url.PathEscape(v)
		}
		return "999999999"
	})
	headers := map[string]string{"Accept": "application/json"}
	if step.Method != http.MethodGet && step.Method != http.MethodHead {
		headers["X-CSRF-Token"] = a.token
	}
	for name, value := range step.Headers {
		headers[name] = value
	}
	contentType := ""
	var body []byte
	if step.Body != nil {
		body = []byte(*step.Body)
		contentType = step.Type
		if contentType == "" {
			contentType = "application/json"
		}
	}
	return p.do(a, step.Method, target, body, contentType, headers, step.Host)
}

func (p *replayer) do(a *agent, method, target string, body []byte, contentType string, headers map[string]string, host string) (int, string) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+p.world.Addr()+"/", reader)
	if err != nil {
		p.t.Fatal(err)
	}
	req.URL.Opaque = target
	if host != "" {
		req.Host = host
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range headers {
		// Latin-1 text goes out byte for byte, as Node's client sent it
		req.Header[http.CanonicalHeaderKey(name)] = []string{latin1Bytes(value)}
	}
	if cookies := a.cookieHeader(); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	res, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, target, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 || c.Value == "" {
			delete(a.cookies, c.Name)
			continue
		}
		a.cookies[c.Name] = c.Value
	}
	return res.StatusCode, string(raw)
}

// latin1Bytes is s with each character below 256 as one byte.
func latin1Bytes(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 256 {
			b.WriteByte(byte(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (p *replayer) capture(step recordedStep, body string) {
	if len(step.Capture) == 0 {
		return
	}
	v, err := goapi.Parse([]byte(body))
	if err != nil {
		return
	}
	for name, path := range step.Capture {
		value := goapi.Dig(v, strings.Split(path, ".")[0])
		if value != nil {
			p.vars[name] = jsbindText(value)
		}
	}
}

func jsbindText(v any) string {
	if f, ok := v.(float64); ok {
		return jsbind.NumberString(f)
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// rows runs a SELECT of the recording and returns its rows as the JSON
// the Node drivers' rows had.
func (p *replayer) rows(query string, params []any) string {
	rows, err := p.db.Query(context.Background(), query, args(params)...)
	if err != nil {
		p.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
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
			name, _ := httpx.Marshal(column)
			value, _ := cells[i].MarshalJSON()
			out.Write(name)
			out.WriteByte(':')
			out.Write(value)
		}
		out.WriteByte('}')
	}
	out.WriteByte(']')
	return out.String()
}

var (
	accessToken = regexp.MustCompile(`"[0-9a-f]{64}"`)
	timestamp   = regexp.MustCompile(`"timestamp":[0-9]{13}`)
	// A help request without a readable time gets the time of the request
	helpNow = regexp.MustCompile(`"created_at":1[7-9][0-9]{11}`)
)

// normalize replaces what differs from run to run: the times the
// databases filled in, the random access tokens, the time stamps of the
// events and of help requests without a time of their own.
func normalize(s string) string {
	s = replay.Normalize(s)
	s = accessToken.ReplaceAllString(s, `"<token>"`)
	s = timestamp.ReplaceAllString(s, `"timestamp":"<now>"`)
	return helpNow.ReplaceAllString(s, `"created_at":"<now>"`)
}
