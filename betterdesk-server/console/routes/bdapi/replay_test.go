package bdapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// recording is what testdata/nodeoracle/bdapi.js recorded from the Node
// console's device API on one database.
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
			"BD_REGISTER_RATE_LIMIT_MAX": "1000000",
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
	failed := 0
	report := func(format string, a ...any) {
		failed++
		if failed <= 30 {
			t.Errorf(format, a...)
		}
	}
	steps, errs := fixedSteps(p.db.Dialect().String(), p.rec.Steps)
	for _, err := range errs {
		report("%v", err)
	}
	for i, step := range steps {
		if step.SQL != "" {
			if have := p.rows(step.SQL, step.Params); normalize(have) != normalize(step.Rows) {
				report("step %d %q:\n   node: %s\n     go: %s", i, step.Name, step.Rows, have)
			}
			continue
		}
		p.gate.Lock()
		p.overrides = step.API
		p.gate.Unlock()
		p.api.Calls()
		status, header, body := p.send(step)
		if status != step.Status || normalize(body) != normalize(step.Answer) {
			report("step %d %q:\n   node: %d %s\n     go: %d %s", i, step.Name, step.Status, normalize(step.Answer), status, normalize(body))
		}
		for name, want := range step.Header {
			if got := header.Get(name); got != want {
				report("step %d %q: header %s: node %q, go %q", i, step.Name, name, want, got)
			}
		}
		if got := p.calls(); !sameCalls(got, step.Calls) {
			report("step %d %q: API calls\n   node: %v\n     go: %v", i, step.Name, step.Calls, got)
		}
		p.capture(step, body)
	}
	if failed > 30 {
		t.Errorf("%d more steps differ", failed-30)
	}
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
}

// fixes are the bug fixes that changed answers of the recording.
var fixes = []fix{
	// The replay sends from 127.0.0.1 without TRUST_PROXY: the address in
	// X-Forwarded-For is not taken, so the renamed device's stored address
	// does not match and its row stays as it was
	{bug: "BD3", dialect: "sqlite", steps: []string{"41 renamed, same address POST /api/bd/register"}, status: http.StatusConflict,
		answer: `{"error":"Device ID has been changed","new_id":"NEWID1","message":"This device ID was renamed to NEWID1. Please update your configuration."}`},
	{bug: "BD3", dialect: "sqlite", steps: []string{"46 renamed devices after"}, edits: []string{
		`{"id":"NEWID1","uuid":"","pk":null,"info":"{\"hostname\":\"\",\"os\":\"\",\"version\":\"\",\"ip\":\"203.0.113.9\"}","ip":"203.0.113.9","status_online":1,`,
		`{"id":"NEWID1","uuid":"uuid-n","pk":null,"info":"{}","ip":"203.0.113.9","status_online":0,`,
	}},
	{bug: "BD3", dialect: "sqlite", steps: []string{"132 GET /api/bd/operator/devices", "135 token with spaces GET /api/bd/operator/devices"}, edits: []string{
		`{"id":"NEWID1","hostname":"","platform":"","online":true,"last_online":"2026-11-18 17:40:53"}`,
		`{"id":"NEWID1","hostname":"","platform":"","online":false,"last_online":""}`,
	}},
}

// fixedSteps returns the steps as the Go console answers them since the
// fixes. A fix that changes no step, and a step a fix names that it does
// not change, are errors, so the list cannot go stale.
func fixedSteps(dialect string, steps []recordedStep) ([]recordedStep, []error) {
	out := append([]recordedStep(nil), steps...)
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

// calls are the API server's calls of the step, without the role
// permission lookups, which the Node console made and the Go console
// does not.
func (p *replayer) calls() []apiCall {
	var out []apiCall
	for _, line := range p.api.Calls() {
		method, rest, _ := strings.Cut(line, " ")
		target, body, _ := strings.Cut(rest, " ")
		if strings.HasPrefix(target, "/api/roles/") {
			continue
		}
		out = append(out, apiCall{Method: method, URL: target, Body: body})
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
	payload := strings.Join([]string{"bd-enrollment-v1", strings.ToUpper(method), target, spec.Device, public, timestamp, spec.Nonce}, "\n")
	signer := spec.Sign
	if signer == "" {
		signer = spec.Key
	}
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(p.keys[signer], []byte(payload)))
	if spec.Signature != nil {
		signature = *spec.Signature
	}
	return map[string]string{
		"X-BD-Enrollment-Timestamp": timestamp,
		"X-BD-Enrollment-Nonce":     spec.Nonce,
		"X-BD-Enrollment-Signature": signature,
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
