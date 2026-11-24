package ndapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// recording is what testdata/node_<database>.json recorded from the Node
// console's device API on one database (testdata/README.md).
type recording struct {
	Dialect string              `json:"dialect"`
	Seeds   map[string]string   `json:"seeds"`
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

// proofSpec says how the oracle made the proof headers of a step; the
// test makes them the same way at the time it sends.
type proofSpec struct {
	Key        string  `json:"key"`
	Device     string  `json:"device"`
	Nonce      string  `json:"nonce"`
	Offset     float64 `json:"offset"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	Sign       string  `json:"sign"`
	Signature  *string `json:"signature"`
	Timestamp  *string `json:"timestamp"`
	PayloadKey *string `json:"payloadKey"`
}

type recordedStep struct {
	Name    string              `json:"name"`
	Role    string              `json:"role"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Body    *string             `json:"body"`
	Type    string              `json:"type"`
	Headers map[string]any      `json:"headers"`
	API     map[string]apiReply `json:"api"`
	Proof   *proofSpec          `json:"proof"`
	Capture map[string]string   `json:"capture"`
	SQL     string              `json:"sql"`
	Params  []any               `json:"params"`
	Rows    string              `json:"rows"`
	Status  int                 `json:"status"`
	Answer  string              `json:"answer"`
	Header  map[string]string   `json:"header"`
	Calls   []apiCall           `json:"calls"`
}

// TestNodeAnswers replays what the Node console's device API answered with
// its real database adapter, sign-in service, permission loading, panel
// access guard, branding service, registration limiter and API client
// (testdata/node_sqlite.json, testdata/node_postgres.json): registrations
// with every way of naming a device and every admission rule (approvals,
// enrollment tokens, Ed25519 proofs good and bad, renamed ids, banned and
// removed devices, key and uuid changes, values of every type),
// heartbeats, relay sessions, the device lookups, help requests, chat,
// the operator routes with tokens of every kind, the session history and
// the notification bell. Every answer must be the same, byte for byte
// after the times of the run and the random ids and tokens, and so must
// the rows written, the audit log and the calls to the API server.
func TestNodeAnswers(t *testing.T) {
	if _, offset := time.Now().Zone(); offset != 0 {
		t.Fatal("the recordings were made in UTC: run the tests with TZ=UTC")
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		raw, err := os.ReadFile(filepath.Join("testdata", "node_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Fatalf("read the recording: %v", err)
		}
		var rec recording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("read the recording: %v", err)
		}
		newReplayer(t, db, rec).run()
	})
}

type replayer struct {
	t         *testing.T
	db        *store.DB
	rec       recording
	world     *routetest.World
	api       *routetest.API
	gate      sync.Mutex
	overrides map[string]apiReply
	keys      map[string]ed25519.PrivateKey
	cookies   map[string]map[string]string
	tokens    map[string]string
	vars      map[string]string
	client    *http.Client
}

func newReplayer(t *testing.T, db *store.DB, rec recording) *replayer {
	p := &replayer{t: t, db: db, rec: rec, keys: map[string]ed25519.PrivateKey{}, cookies: map[string]map[string]string{},
		tokens: map[string]string{}, vars: map[string]string{}}
	for name, seed := range rec.Seeds {
		raw, err := hex.DecodeString(seed)
		if err != nil {
			t.Fatal(err)
		}
		p.keys[name] = ed25519.NewKeyFromSeed(raw)
	}
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
		return routetest.Reply{Status: reply.Status, Type: reply.Type, Body: reply.Body}
	}
	p.world = routetest.New(t, routetest.Options{
		DB:  db,
		API: p.api,
		Env: map[string]string{
			"RATE_LIMIT_MAX": "1000000", "PANEL_READ_RATE_LIMIT_MAX": "1000000", "WIDGET_RATE_LIMIT_MAX": "1000000",
			"ND_REGISTER_RATE_LIMIT_MAX": "1000000",
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
	// Node took every live token with a client id as that device's token.
	// The recording's tokens stand for device tokens but for the operator's,
	// which stands for the desktop client's operator sign-in (BUGS RV1).
	if _, err := p.db.Exec(ctx, `UPDATE access_tokens SET token_kind = 'device' WHERE token <> 'tok-operator-0123456789'`); err != nil {
		t.Fatalf("mark the device tokens: %v", err)
	}
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
		if step.Name == firstSessionHistory {
			p.sameSecond()
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

// outcome is what a step gave: its status, the answer's headers that the
// recording names, the answer, the rows a query read and the API
// server's calls.
type outcome struct {
	Status int               `json:"status,omitempty"`
	Header map[string]string `json:"header,omitempty"`
	Answer string            `json:"answer,omitempty"`
	Rows   string            `json:"rows,omitempty"`
	Calls  []apiCall         `json:"calls,omitempty"`
}

// outcomeOf is what Node gave for the step.
func outcomeOf(step recordedStep) outcome {
	return outcome{Status: step.Status, Header: step.Header, Answer: step.Answer, Rows: step.Rows, Calls: step.Calls}
}

// play runs a step and returns what the Go console gave. want is what the
// step should give, for the headers to read.
func (p *replayer) play(step recordedStep, want outcome) outcome {
	if step.SQL != "" {
		return outcome{Rows: p.rows(step.SQL, step.Params)}
	}
	p.gate.Lock()
	p.overrides = step.API
	p.gate.Unlock()
	p.api.Calls()
	status, header, body := p.send(step)
	o := outcome{Status: status, Answer: body, Calls: p.calls()}
	for name := range want.Header {
		if o.Header == nil {
			o.Header = map[string]string{}
		}
		o.Header[name] = header.Get(name)
	}
	p.capture(step, body)
	return o
}

// compare lists how what the Go console gave differs from what the step
// should give.
func compare(step recordedStep, want, have outcome) []string {
	var diffs []string
	if step.SQL != "" {
		if normalize(have.Rows) != normalize(want.Rows) {
			diffs = append(diffs, fmt.Sprintf("\n   node: %s\n     go: %s", want.Rows, have.Rows))
		}
		return diffs
	}
	if have.Status != want.Status || normalize(have.Answer) != normalize(want.Answer) {
		diffs = append(diffs, fmt.Sprintf("\n   node: %d %s\n     go: %d %s", want.Status, normalize(want.Answer), have.Status, normalize(have.Answer)))
	}
	for name, value := range want.Header {
		if have.Header[name] != value {
			diffs = append(diffs, fmt.Sprintf("header %s: node %q, go %q", name, value, have.Header[name]))
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

// saveFixed writes the fixed steps in the order of the recording, and
// removes the file when there are none.
func saveFixed(t *testing.T, dialect string, fixed map[string]fixedStep, steps []recordedStep) {
	t.Helper()
	var list []fixedEntry
	for _, step := range steps {
		if f, ok := fixed[step.Name]; ok {
			list = append(list, fixedEntry{step.Name, f})
		}
	}
	if len(list) == 0 {
		if err := os.Remove(fixedFile(dialect)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		return
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
// step answers now, its whole answer, or text of what Node answered or
// the rows it stored replaced by what the Go console writes since the
// fix. Fixes apply in their order, so a later one edits what an earlier
// one wrote.
type fix struct {
	bug string
	// dialect limits the fix to one database's recording
	dialect string
	// steps are the names of the steps the fix changes; a name ending in
	// "..." stands for the step with the name before it and every step
	// after it, for a fix whose changed rows later steps read
	steps []string
	// status is the step's status now; 0 keeps Node's
	status int
	// answer is the step's whole answer now; "" keeps Node's
	answer string
	// edits are pairs of the text Node wrote and the text Go writes
	edits []string
	// like names the other database, whose recording has the status and
	// the answer Go gives now: a fix that makes both databases answer
	// alike
	like string
}

// fixes are the bug fixes that changed answers of the recording.
var fixes = []fix{
	// The replay sends from 127.0.0.1 without TRUST_PROXY: the address in
	// X-Forwarded-For is not taken, so the renamed device's stored address
	// does not match and its row stays as it was
	{bug: "BD3", dialect: "sqlite", steps: []string{"41 renamed, same address POST /api/nd/register"}, status: http.StatusConflict,
		answer: `{"error":"Device ID has been changed","new_id":"NEWID1","message":"This device ID was renamed to NEWID1. Please update your configuration."}`},
	{bug: "BD3", dialect: "sqlite", steps: []string{"46 renamed devices after"}, edits: []string{
		`{"id":"NEWID1","uuid":"","pk":null,"info":"{\"hostname\":\"\",\"os\":\"\",\"version\":\"\",\"ip\":\"203.0.113.9\"}","ip":"203.0.113.9","status_online":1,`,
		`{"id":"NEWID1","uuid":"uuid-n","pk":null,"info":"{}","ip":"203.0.113.9","status_online":0,`,
	}},
	{bug: "BD3", dialect: "sqlite", steps: []string{"132 GET /api/nd/operator/devices", "135 token with spaces GET /api/nd/operator/devices"}, edits: []string{
		`{"id":"NEWID1","hostname":"","platform":"","online":true,"last_online":"2026-11-18 17:40:53"}`,
		`{"id":"NEWID1","hostname":"","platform":"","online":false,"last_online":""}`,
	}},
	// A banned device is no target, and the lookup says it is banned
	{bug: "BD4", steps: []string{"87 banned target POST /api/nd/connect"}, status: http.StatusForbidden, answer: `{"error":"Target device is banned"}`},
	{bug: "BD4", steps: []string{"101 GET /api/nd/peer/BANNED1"}, edits: []string{`"banned":false`, `"banned":true`}},
	// A key stored as bytes, as PostgreSQL keeps every key, is the
	// device's key: a key or uuid change needs the proof there too
	{bug: "BD6", dialect: "postgres", like: "sqlite", steps: []string{
		"27 key swap without proof POST /api/nd/register",
		"28 key swap, proof by another key POST /api/nd/register",
		"29 key swap, proof too old POST /api/nd/register",
		"30 key swap, proof from the future POST /api/nd/register",
		"31 key swap, unreadable time POST /api/nd/register",
		"32 key swap, nonce too long POST /api/nd/register",
		"33 key swap, signature too short POST /api/nd/register",
		"34 key swap, signature of another path POST /api/nd/register",
		"35 key swap, uuid change with proof POST /api/nd/register",
		"36 the same proof again POST /api/nd/register",
	}},
	{bug: "BD6", dialect: "postgres", steps: []string{"38 known devices after"}, edits: []string{
		`{"id":"KEYDEV","uuid":"uuid-k",`, `{"id":"KEYDEV","uuid":"uuid-k2",`,
	}},
	// A registration without a uuid keeps the stored one on SQLite too
	{bug: "BD7", dialect: "sqlite", steps: []string{"38 known devices after"}, edits: []string{
		`{"id":"DEV3","uuid":"",`, `{"id":"DEV3","uuid":"uuid-3",`,
		`{"id":"KEYDEV","uuid":"",`, `{"id":"KEYDEV","uuid":"uuid-k2",`,
	}},
	{bug: "BD7", dialect: "sqlite", steps: []string{"46 renamed devices after"}, edits: []string{
		`{"id":"NEWID2","uuid":"",`, `{"id":"NEWID2","uuid":"uuid-n2",`,
	}},
	{bug: "BD7", dialect: "sqlite", steps: []string{"128 odd values after"}, edits: []string{
		`{"id":"DEV3","uuid":"",`, `{"id":"DEV3","uuid":"uuid-3",`,
	}},
	// An approval whose request carried a key admits only that key: the
	// device that sent none is refused and never registered
	{bug: "BD8", steps: []string{"49 approved, no key sent POST /api/nd/register"}, status: http.StatusForbidden,
		answer: `{"error":"enrollment_proof_required","message":"Registration requires operator approval, an enrollment token, or an Ed25519 proof of possession"}`},
	{bug: "BD8", dialect: "sqlite", steps: []string{"73 new devices after"}, edits: []string{
		`,{"id":"APPROVED2","uuid":"","pk":null,"info":"{\"hostname\":\"\",\"os\":\"\",\"version\":\"\",\"ip\":\"127.0.0.1\"}","ip":"127.0.0.1","status_online":1,"is_deleted":0}`, ``,
	}},
	{bug: "BD8", dialect: "postgres", steps: []string{"73 new devices after"}, edits: []string{
		`,{"id":"APPROVED2","uuid":"","pk":null,"info":{"ip":"127.0.0.1","os":"","version":"","hostname":""},"ip":"127.0.0.1","status_online":true,"is_deleted":false}`, ``,
	}},
	{bug: "BD8", dialect: "sqlite", steps: []string{"132 GET /api/nd/operator/devices", "135 token with spaces GET /api/nd/operator/devices"}, edits: []string{
		`,{"id":"APPROVED2","hostname":"","platform":"","online":true,"last_online":"2026-11-18 17:40:53"}`, ``,
	}},
	{bug: "BD8", dialect: "postgres", steps: []string{"132 GET /api/nd/operator/devices", "135 token with spaces GET /api/nd/operator/devices"}, edits: []string{
		`,{"id":"APPROVED2","hostname":"","platform":"","online":true,"last_online":"2026-11-18T17:41:22.789Z"}`, ``,
	}},
}

// fixedSteps returns the steps as the Go console answers them since the
// fixes. A fix that changes no step, and a step a fix names that it does
// not change, are errors, so the list cannot go stale.
func fixedSteps(dialect string, steps []recordedStep) ([]recordedStep, []error) {
	out := append([]recordedStep(nil), steps...)
	var errs []error
	others := map[string]map[string]recordedStep{}
	for _, f := range fixes {
		if f.dialect != "" && f.dialect != dialect {
			continue
		}
		if f.like != "" && others[f.like] == nil {
			other, err := otherRecording(f.like)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			others[f.like] = other
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
				if f.like != "" {
					like, ok := others[f.like][out[i].Name]
					if !ok {
						errs = append(errs, fmt.Errorf("the %s fix names a step the %s recording does not have: %q", f.bug, f.like, out[i].Name))
						continue
					}
					f.status, f.answer = like.Status, like.Answer
				}
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

// otherRecording is the steps of another database's recording by name.
func otherRecording(dialect string) (map[string]recordedStep, error) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_"+dialect+".json"))
	if err != nil {
		return nil, err
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	out := map[string]recordedStep{}
	for _, step := range rec.Steps {
		out[step.Name] = step
	}
	return out, nil
}

// apply changes the step and reports whether anything changed.
func (f fix) apply(step *recordedStep) bool {
	changed := false
	for i := 0; i+1 < len(f.edits); i += 2 {
		for _, text := range []*string{&step.Answer, &step.Rows} {
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

// firstSessionHistory is the first step that reads the operator sessions
// the replay recorded.
const firstSessionHistory = "159 GET /api/nd/operator/sessions"

// sameSecond gives the connection rows the replay wrote the times they
// had in the recordings: one second, on PostgreSQL in the order they were
// written. The history orders sessions and counts their length by the
// second, so rows that a slow run spreads over two seconds would change
// both.
func (p *replayer) sameSecond() {
	query := `UPDATE audit_connections SET created_at = (SELECT MIN(created_at) FROM audit_connections WHERE created_at > '2020-01-01')
		WHERE created_at > '2020-01-01'`
	if p.db.IsPostgres() {
		query = `UPDATE audit_connections
			SET created_at = date_trunc('second', (SELECT MIN(created_at) FROM audit_connections WHERE created_at > '2020-01-01')) + id * interval '1 millisecond'
			WHERE created_at > '2020-01-01'`
	}
	if _, err := p.db.Exec(context.Background(), query); err != nil {
		p.t.Fatalf("connection times: %v", err)
	}
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
// axios did not make (BUGS RG14): the API server reads both alike.
var axiosQuery = strings.NewReplacer("%2C", ",", "%3A", ":", "%24", "$")

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)\}`)

// proofHeaders are the headers the oracle made for the step, made now.
func (p *replayer) proofHeaders(step recordedStep, path string) map[string]string {
	spec := step.Proof
	timestamp := time.Now().Add(time.Duration(spec.Offset) * time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z")
	if spec.Timestamp != nil {
		timestamp = *spec.Timestamp
	}
	public := base64.StdEncoding.EncodeToString(p.keys[spec.Key].Public().(ed25519.PublicKey))
	if spec.PayloadKey != nil {
		public = *spec.PayloadKey
	}
	method := spec.Method
	if method == "" {
		method = step.Method
	}
	target := spec.Path
	if target == "" {
		target, _, _ = strings.Cut(path, "?")
	}
	payload := strings.Join([]string{"nd-enrollment-v1", strings.ToUpper(method), target, spec.Device, public, timestamp, spec.Nonce}, "\n")
	signer := spec.Sign
	if signer == "" {
		signer = spec.Key
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(p.keys[signer], []byte(payload)))
	if spec.Signature != nil {
		signature = *spec.Signature
	}
	return map[string]string{
		"X-ND-Enrollment-Timestamp": timestamp,
		"X-ND-Enrollment-Nonce":     spec.Nonce,
		"X-ND-Enrollment-Signature": signature,
	}
}

func (p *replayer) send(step recordedStep) (int, http.Header, string) {
	target := placeholder.ReplaceAllStringFunc(step.Path, func(m string) string {
		if v, ok := p.vars[m[1:len(m)-1]]; ok {
			return url.PathEscape(v)
		}
		return "999999999"
	})
	cookies := p.signIn(step.Role)
	req, err := http.NewRequest(step.Method, "http://"+p.world.Addr()+"/", nil)
	if err != nil {
		p.t.Fatal(err)
	}
	req.URL.Opaque = target
	req.Header.Set("Accept", "application/json")
	if step.Method != http.MethodGet && step.Method != http.MethodHead {
		req.Header.Set("X-CSRF-Token", p.tokens[step.Role])
	}
	for name, value := range step.Headers {
		switch v := value.(type) {
		case string:
			req.Header[http.CanonicalHeaderKey(name)] = []string{v}
		case []any:
			for _, item := range v {
				req.Header.Add(name, item.(string))
			}
		}
	}
	if step.Proof != nil {
		for name, value := range p.proofHeaders(step, target) {
			req.Header.Set(name, value)
		}
	}
	if step.Body != nil {
		req.Body = io.NopCloser(strings.NewReader(*step.Body))
		req.ContentLength = int64(len(*step.Body))
		contentType := step.Type
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if c := cookieHeader(cookies); c != "" {
		req.Header.Set("Cookie", c)
	}
	res, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", step.Method, target, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, string(raw)
}

// signIn returns the cookies of a role, signing it in and taking its
// CSRF token on first use. Nobody has a CSRF token too.
func (p *replayer) signIn(role string) map[string]string {
	if c, ok := p.cookies[role]; ok {
		return c
	}
	cookies := map[string]string{}
	p.cookies[role] = cookies
	get := func(path string) string {
		req, _ := http.NewRequest(http.MethodGet, "http://"+p.world.Addr()+path, nil)
		if c := cookieHeader(cookies); c != "" {
			req.Header.Set("Cookie", c)
		}
		res, err := p.client.Do(req)
		if err != nil {
			p.t.Fatal(err)
		}
		defer res.Body.Close()
		for _, c := range res.Cookies() {
			cookies[c.Name] = c.Value
		}
		raw, _ := io.ReadAll(res.Body)
		return string(raw)
	}
	if role != "" {
		get("/__test/signin/" + role)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(get("/__test/token")), &out); err != nil {
		p.t.Fatal(err)
	}
	p.tokens[role] = out.Token
	return cookies
}

func cookieHeader(cookies map[string]string) string {
	var pairs []string
	for name, value := range cookies {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, "; ")
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
		if s, ok := goapi.Dig(v, path).(string); ok {
			p.vars[name] = s
		}
	}
}

// rows runs a SELECT of the recording and returns its rows as the JSON
// the Node drivers' rows had: a PostgreSQL JSON column as its value.
func (p *replayer) rows(query string, params []any) string {
	rows, err := p.db.Query(context.Background(), query, args(params)...)
	if err != nil {
		p.t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	columns, _ := rows.Columns()
	types, _ := rows.ColumnTypes()
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
			out.Write(name)
			out.WriteByte(':')
			if strings.HasPrefix(types[i].DatabaseTypeName(), "JSON") && cells[i].Value().IsString() {
				var compact bytes.Buffer
				if err := json.Compact(&compact, []byte(cells[i].Value().Str())); err != nil {
					p.t.Fatal(err)
				}
				out.Write(compact.Bytes())
				continue
			}
			value, _ := cells[i].MarshalJSON()
			out.Write(value)
		}
		out.WriteByte('}')
	}
	out.WriteByte(']')
	return out.String()
}

var (
	serverTime  = regexp.MustCompile(`"server_time":[0-9]{13}`)
	uuidText    = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
	hexToken    = regexp.MustCompile(`[0-9a-f]{64}`)
	createdAt   = regexp.MustCompile(`"(created_at|createdAt)":1[7-9][0-9]{11}`)
	dateText    = regexp.MustCompile(`[A-Z][a-z]{2} [A-Z][a-z]{2} [0-9]{2} 20[2-9][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2} GMT[+-][0-9]{4} \([^)]*\)`)
	openSession = regexp.MustCompile(`"ended_at":"","duration_secs":[0-9]+`)
)

// normalize replaces what differs from run to run: the times the
// databases filled in and the clock gave (also as Date.prototype.toString
// wrote a PostgreSQL time), random session ids and tokens,
// and the length of the sessions that have not ended.
func normalize(s string) string {
	s = replay.Normalize(s)
	s = serverTime.ReplaceAllString(s, `"server_time":"<now>"`)
	s = uuidText.ReplaceAllString(s, "<uuid>")
	s = hexToken.ReplaceAllString(s, "<token>")
	s = createdAt.ReplaceAllString(s, `"$1":"<now>"`)
	s = dateText.ReplaceAllString(s, "<now, Date text>")
	return openSession.ReplaceAllString(s, `"ended_at":"","duration_secs":"<open>"`)
}
