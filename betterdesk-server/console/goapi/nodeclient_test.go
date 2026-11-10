//go:build nodeclient

package goapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi/internal/apitest"
)

// The comparison with the Node client. testdata/nodeclient/vectors.js runs
// the steps of calls.json through the Node console's code against a Go
// server and writes every result to the file GOAPI_NODE_VECTORS names. This
// test runs the same steps through this package against a server in the
// process and compares the results, key order included.

type vectorFile struct {
	HangTimeoutMs int            `json:"hangTimeoutMs"`
	Down          []vectorRecord `json:"down"`
	Up            []vectorRecord `json:"up"`
	Hang          []vectorRecord `json:"hang"`
	Collation     struct {
		Items  []string `json:"items"`
		Rows   []string `json:"rows"`
		Locale string   `json:"locale"`
	} `json:"collation"`
}

type vectorRecord struct {
	Step   int             `json:"step"`
	SQL    string          `json:"sql"`
	HTTP   string          `json:"http"`
	Fn     string          `json:"fn"`
	Result json.RawMessage `json:"result"`
	Thrown json.RawMessage `json:"thrown"`
}

type callStep struct {
	SQL  string            `json:"sql"`
	HTTP []json.RawMessage `json:"http"`
	Fn   string            `json:"fn"`
	Args json.RawMessage   `json:"args"`
	Save map[string]string `json:"save"`
	Hang bool              `json:"hang"`
	Only []string          `json:"only"`
}

func loadVectors(t *testing.T) *vectorFile {
	t.Helper()
	path := os.Getenv("GOAPI_NODE_VECTORS")
	if path == "" {
		t.Fatal("GOAPI_NODE_VECTORS is not set: run testdata/nodeclient/run.sh")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return &v
}

func loadSteps(t *testing.T, fixtures string) []callStep {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, "calls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var steps []callStep
	if err := json.Unmarshal(raw, &steps); err != nil {
		t.Fatalf("calls.json: %v", err)
	}
	return steps
}

var placeholder = regexp.MustCompile(`\{\{(\w+)\}\}`)

// substitute fills {{name}} from saved; a name not saved yet becomes 0.
func substitute(v any, saved map[string]any) any {
	switch t := v.(type) {
	case string:
		if m := placeholder.FindStringSubmatch(t); m != nil && m[0] == t {
			if val, ok := saved[m[1]]; ok {
				return val
			}
			return float64(0)
		}
		return placeholder.ReplaceAllStringFunc(t, func(match string) string {
			if val, ok := saved[match[2:len(match)-2]]; ok {
				return jsString(val)
			}
			return "0"
		})
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = substitute(el, saved)
		}
		return out
	case *Object:
		out := NewObject()
		for _, k := range t.Keys() {
			out.Set(k, substitute(t.Get(k), saved))
		}
		return out
	}
	return v
}

// failingStore is the Node console's own database in the comparison: the
// script loads the client code without the console's tables, so every
// query fails there.
func failingStore() *fakeStore {
	return &fakeStore{failFolders: true, failSysinfo: true, failUpdate: true, failStats: true}
}

// extraDispatch adds what the request table does not go through: the
// device layer, normalisePeer and the callback proxy.
var extraDispatch = map[string]func(c *Client, ctx context.Context, a []any) any{
	"normalisePeer": func(c *Client, ctx context.Context, a []any) any { return NormalisePeer(arg(a, 0)) },
	"proxyOIDCCallback": func(c *Client, ctx context.Context, a []any) any {
		query := url.Values{}
		if o, ok := arg(a, 0).(*Object); ok {
			for _, k := range o.Keys() {
				query.Set(k, jsString(o.Get(k)))
			}
		}
		got := c.ProxyOIDCCallback(ctx, query)
		return NewObject("status", float64(got.Status), "contentType", got.ContentType, "location", got.Location,
			"cacheControl", got.CacheControl, "body", string(got.Body))
	},
	"serverBackend.getActiveBackend": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetActiveBackend()
	},
	"serverBackend.isBetterDesk": func(c *Client, ctx context.Context, a []any) any { return NewBackend(c, failingStore()).IsBetterDesk() },
	"serverBackend.getHealth":    func(c *Client, ctx context.Context, a []any) any { return NewBackend(c, failingStore()).GetHealth(ctx) },
	"serverBackend.getStats": func(c *Client, ctx context.Context, a []any) any {
		stats, err := NewBackend(c, failingStore()).GetStats(ctx)
		if err != nil {
			return thrown{&Error{Message: err.Error()}}
		}
		return stats
	},
	"serverBackend.getServerInfo": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetServerInfo(ctx)
	},
	"serverBackend.getAllDevices": func(c *Client, ctx context.Context, a []any) any {
		f := arg(a, 0)
		text := func(key string) string { s, _ := prop(f, key).(string); return s }
		return NewBackend(c, failingStore()).GetAllDevices(ctx, DeviceFilters{
			IncludeDeleted: Truthy(prop(f, "includeDeleted")), Search: text("search"), Status: text("status"),
			HasNotes: Truthy(prop(f, "hasNotes")), SortBy: text("sortBy"), SortOrder: text("sortOrder"),
		})
	},
	"serverBackend.getDeviceById": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetDeviceByID(ctx, id(a, 0), Truthy(prop(arg(a, 1), "includeDeleted")))
	},
	"serverBackend.deleteDevice": func(c *Client, ctx context.Context, a []any) any {
		o := arg(a, 1)
		return NewBackend(c, failingStore()).DeleteDevice(ctx, str(a, 0), DeleteOptions{
			Revoke: Truthy(prop(o, "revoke")), Cascade: Truthy(prop(o, "cascade")), Hard: Truthy(prop(o, "hard")),
		})
	},
	"serverBackend.restoreDevice": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).RestoreDevice(ctx, str(a, 0))
	},
	"serverBackend.setBanStatus": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).SetBanStatus(ctx, str(a, 0), Truthy(arg(a, 1)), str(a, 2))
	},
	"serverBackend.updateDevice": func(c *Client, ctx context.Context, a []any) any {
		data, _ := arg(a, 1).(*Object)
		return NewBackend(c, failingStore()).UpdateDevice(ctx, str(a, 0), data)
	},
	"serverBackend.changePeerId": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).ChangePeerID(ctx, str(a, 0), str(a, 1))
	},
	"serverBackend.syncOnlineStatus": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).SyncOnlineStatus()
	},
	"serverBackend.getStatusSummary": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetStatusSummary(ctx)
	},
	"serverBackend.getBlocklist": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetBlocklist(ctx)
	},
	"serverBackend.addBlocklistEntry": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).AddBlocklistEntry(ctx, arg(a, 0))
	},
	"serverBackend.removeBlocklistEntry": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).RemoveBlocklistEntry(ctx, str(a, 0))
	},
	"serverBackend.setPeerTags": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).SetPeerTags(ctx, str(a, 0), arg(a, 1))
	},
	"serverBackend.getPeersByTag": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetPeersByTag(ctx, str(a, 0))
	},
	"serverBackend.getAuditEvents": func(c *Client, ctx context.Context, a []any) any {
		return NewBackend(c, failingStore()).GetAuditEvents(ctx, arg(a, 0))
	},
}

// maskRules lists every value that is replaced before two results are
// compared, because it cannot be the same in two runs.
var maskRules = []string{
	"times of the run (RFC 3339 or YYYY-MM-DD HH:MM:SS, within two days of now)",
	"UUIDs: organization ids, member ids, device GUIDs",
	"remote target ids (rt_ and 32 hex digits)",
	"runs of 16 or more hex digits: device tokens, pairing code ids, the mesh server id",
	"by key: token, token_prefix, code, device_token, partial_token, server_key, server_time, revision",
	"by key, the runtime's numbers: uptime, uptime_seconds, goroutines, memory_alloc_mb, memory_sys_mb",
	"by key: clock_synced (the test server does not start the clock check), version when it is the server's own",
	"lists under the key permissions are sorted: the API server builds them in the order of a map",
	"the error text of a call without an answer in the down phase: ECONNREFUSED from Node, a dead handler in process",
	"the error text of serverBackend.getStats when the console's own tables fail too",
}

var (
	isoTime     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
	sqlTime     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	targetID    = regexp.MustCompile(`^rt_[0-9a-f]{32}$`)
	longHex     = regexp.MustCompile(`^[0-9A-Fa-f]{16,}$`)
	noAnswerMsg = regexp.MustCompile(`ECONNREFUSED|^socket hang up$`)
)

var maskedKeys = map[string]bool{
	"token": true, "token_prefix": true, "code": true, "device_token": true, "partial_token": true,
	"server_key": true, "server_time": true, "revision": true,
	"uptime": true, "uptime_seconds": true, "goroutines": true, "memory_alloc_mb": true, "memory_sys_mb": true,
	"clock_synced": true,
}

type masker struct {
	now      time.Time
	versions map[string]bool
	down     bool
}

func (m masker) recent(layout, s string) bool {
	parsed, err := time.Parse(layout, s)
	if err != nil {
		return false
	}
	age := m.now.Sub(parsed)
	return age > -48*time.Hour && age < 48*time.Hour
}

func (m masker) mask(v any, key string) any {
	switch t := v.(type) {
	case *Object:
		if t == nil {
			return v
		}
		out := NewObject()
		for _, k := range t.Keys() {
			val := t.Get(k)
			switch {
			case maskedKeys[k] && val != nil && val != "":
				out.Set(k, "<"+k+">")
			case k == "version" && m.versions[jsString(val)]:
				out.Set(k, "<version>")
			default:
				out.Set(k, m.mask(val, k))
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = m.mask(el, key)
		}
		if key == "permissions" {
			sort.Slice(out, func(i, j int) bool { return jsString(out[i]) < jsString(out[j]) })
		}
		return out
	case string:
		switch {
		case isoTime.MatchString(t) && m.recent(time.RFC3339Nano, t), sqlTime.MatchString(t) && m.recent("2006-01-02 15:04:05", t):
			return "<time>"
		case uuidPattern.MatchString(t):
			return "<uuid>"
		case targetID.MatchString(t):
			return "<target>"
		case longHex.MatchString(t):
			return "<hex>"
		case m.down && key == "error" && noAnswerMsg.MatchString(t):
			return "<no answer>"
		case m.down && key == "message" && noAnswerMsg.MatchString(t):
			return "<no answer>"
		}
	}
	return v
}

func (m masker) encode(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := encodeValue(&buf, m.mask(v, "")); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.String()
}

// outcome is a result in the form the script records it: {"result": ...}
// or {"thrown": {message, status, data}}.
func outcome(v any) *Object {
	if th, ok := v.(thrown); ok {
		shape := NewObject("message", th.err.Message)
		if th.err.Response != nil {
			shape.Set("status", float64(th.err.Response.Status)).Set("data", th.err.Data())
		}
		return NewObject("thrown", shape)
	}
	if o, ok := v.(*Object); ok && o == nil {
		v = nil
	}
	if list, ok := v.([]*Object); ok {
		plain := make([]any, len(list))
		for i, el := range list {
			if el != nil {
				plain[i] = el
			}
		}
		v = plain
	}
	return NewObject("result", v)
}

// runSteps performs the steps of one phase the way the script does and
// returns one outcome per step it ran, by step number.
func runSteps(t *testing.T, phase string, steps []callStep, c *Client, fixtures, dbPath string, saved map[string]any) map[int]*Object {
	t.Helper()
	ctx := context.Background()
	outcomes := map[int]*Object{}
	for i, step := range steps {
		if len(step.Only) > 0 && !strings.Contains(" "+strings.Join(step.Only, " ")+" ", " "+phase+" ") {
			continue
		}
		if phase == "hang" && !step.Hang {
			continue
		}
		var got any
		switch {
		case step.SQL != "":
			if phase != "up" {
				continue
			}
			statements, err := os.ReadFile(filepath.Join(fixtures, step.SQL))
			if err != nil {
				t.Fatal(err)
			}
			conn, err := sql.Open("sqlite", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(string(statements)); err != nil {
				t.Fatalf("%s: %v", step.SQL, err)
			}
			conn.Close()
			outcomes[i] = NewObject()
			continue
		case step.HTTP != nil:
			var method, path string
			if err := json.Unmarshal(step.HTTP[0], &method); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(step.HTTP[1], &path); err != nil {
				t.Fatal(err)
			}
			path, _ = substitute(path, saved).(string)
			var opts []CallOption
			if len(step.HTTP) > 2 {
				body, err := Parse(step.HTTP[2])
				if err != nil {
					t.Fatal(err)
				}
				opts = append(opts, Body(substitute(body, saved)))
			}
			resp, err := c.Request(ctx, method, path, opts...)
			if err != nil {
				got = thrown{AsError(err)}
			} else {
				got = NewObject("status", float64(resp.Status), "data", resp.Data())
			}
		default:
			call := dispatch[step.Fn]
			if call == nil {
				call = extraDispatch[step.Fn]
			}
			if call == nil {
				t.Fatalf("step %d: no method for %s", i, step.Fn)
			}
			var args []any
			if len(step.Args) > 0 {
				parsed, err := Parse(step.Args)
				if err != nil {
					t.Fatal(err)
				}
				args, _ = substitute(parsed, saved).([]any)
			}
			got = call(c, ctx, args)
		}
		out := outcome(got)
		if phase == "up" {
			for name, dotted := range step.Save {
				path := make([]any, 0, 4)
				for _, part := range strings.Split(dotted, ".") {
					path = append(path, part)
				}
				if val := Dig(out.Get("result"), path...); val != nil {
					saved[name] = val
				}
			}
		}
		outcomes[i] = out
	}
	return outcomes
}

func describe(step callStep) string {
	if step.HTTP != nil {
		return "http " + string(step.HTTP[0]) + " " + string(step.HTTP[1])
	}
	return step.Fn + string(step.Args)
}

func comparePhase(t *testing.T, phase string, steps []callStep, records []vectorRecord, outcomes map[int]*Object, m masker) (compared, differing int) {
	t.Helper()
	seen := map[int]bool{}
	for _, rec := range records {
		seen[rec.Step] = true
		if rec.SQL != "" {
			continue
		}
		got, ok := outcomes[rec.Step]
		if !ok {
			t.Errorf("%s step %d (%s): the script ran it, the test did not", phase, rec.Step, describe(steps[rec.Step]))
			continue
		}
		want := NewObject()
		if len(rec.Thrown) > 0 {
			parsed, err := Parse(rec.Thrown)
			if err != nil {
				t.Fatal(err)
			}
			want.Set("thrown", parsed)
		} else {
			parsed, err := Parse(rec.Result)
			if err != nil {
				t.Fatalf("%s step %d: result %s: %v", phase, rec.Step, rec.Result, err)
			}
			want.Set("result", parsed)
		}
		// When the console's own tables fail as well, both sides throw; the
		// text is the database's on one side and wrapped on the other.
		if rec.Fn == "serverBackend.getStats" && want.Has("thrown") && got.Has("thrown") {
			compared++
			continue
		}
		compared++
		if g, w := m.encode(t, got), m.encode(t, want); g != w {
			differing++
			t.Errorf("%s step %d: %s\n  node: %s\n  go:   %s", phase, rec.Step, describe(steps[rec.Step]), w, g)
		}
	}
	for step := range outcomes {
		if !seen[step] {
			t.Errorf("%s step %d (%s): the test ran it, the script did not", phase, step, describe(steps[step]))
		}
	}
	return compared, differing
}

func TestNodeClient(t *testing.T) {
	vectors := loadVectors(t)
	// The test server changes the working directory, so the fixtures are
	// named by their full path.
	fixtures, err := filepath.Abs(filepath.Join("testdata", "nodeclient"))
	if err != nil {
		t.Fatal(err)
	}
	steps := loadSteps(t, fixtures)
	saved := map[string]any{}

	// The version the Go server of the script reported, and this one's.
	versions := map[string]bool{"test": true}
	for _, rec := range vectors.Up {
		if rec.Fn == "getHealth" {
			if parsed, err := Parse(rec.Result); err == nil {
				versions[jsString(prop(parsed, "version"))] = true
			}
		}
	}
	m := masker{now: time.Now(), versions: versions}
	functions := map[string]bool{}
	for _, step := range steps {
		if step.Fn != "" {
			functions[step.Fn] = true
		}
	}
	for _, name := range exportedFunctions {
		if !functions[name] {
			t.Errorf("calls.json has no call of %s", name)
		}
	}
	total, differing := 0, 0
	count := func(compared, differ int) { total, differing = total+compared, differing+differ }

	down := New(&fakeAPI{answer: noAnswer})
	downMasker := m
	downMasker.down = true
	count(comparePhase(t, "down", steps, vectors.Down, runSteps(t, "down", steps, down, fixtures, "", saved), downMasker))

	srv := apitest.New(t)
	up := New(srv.API.InternalHandler(), WithEventBus(srv.Bus), WithTimeout(time.Minute))
	up.signInCap = time.Minute
	count(comparePhase(t, "up", steps, vectors.Up, runSteps(t, "up", steps, up, fixtures, srv.DBPath, saved), m))

	silent := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	hang := New(silent, WithTimeout(time.Duration(vectors.HangTimeoutMs)*time.Millisecond))
	count(comparePhase(t, "hang", steps, vectors.Hang, runSteps(t, "hang", steps, hang, fixtures, "", saved), m))

	t.Logf("%d functions, %d results compared (%d down, %d up, %d hang), %d differ",
		len(functions), total, len(vectors.Down), len(vectors.Up), len(vectors.Hang), differing)
}

// TestNodeClientCollation compares localeCompare with JavaScript's for
// every pair of the texts the script compared.
func TestNodeClientCollation(t *testing.T) {
	vectors := loadVectors(t)
	items := vectors.Collation.Items
	if len(items) == 0 || len(vectors.Collation.Rows) != len(items) {
		t.Fatalf("no collation vectors")
	}
	pairs, differing := 0, 0
	for i, a := range items {
		for j, b := range items {
			pairs++
			want := map[byte]int{'<': -1, '=': 0, '>': 1}[vectors.Collation.Rows[i][j]]
			if got := sign(localeCompare(a, b)); got != want {
				differing++
				if differing <= 40 {
					t.Errorf("localeCompare(%q, %q) = %d, JavaScript (%s) gives %d", a, b, got, vectors.Collation.Locale, want)
				}
			}
		}
	}
	t.Logf("%d pairs of %d texts compared, %d differ", pairs, len(items), differing)
}
