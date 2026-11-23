package rustdeskapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// apiStub is the scripted API server the recordings were made with: six
// devices, no remote targets, tag writes taken. It keeps the writes.
type apiStub struct {
	mu    sync.Mutex
	calls []apiCall
}

type apiCall struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	Body   string `json:"body"`
}

const stubPeers = `[{"id":"900200001","hostname":"host-1","user":"alice","os":"Windows 11","version":"1.4.6","note":"","live_online":true,"tags":"office,lab","uuid":"u1"},` +
	`{"id":"900200002","hostname":"","user":"","os":"macOS","note":"Note 2","live_online":false,"tags":"[\"vip\",\"x\"]","display_name":"Boss Mac"},` +
	`{"id":"900200003","hostname":"host-3","banned":true,"tags":"office"},` +
	`{"id":"900200004","hostname":"host-4","soft_deleted":true,"tags":""},` +
	`{"id":"900200005","hostname":"host-5","live_status":"degraded","tags":["lab"," vip ","Ünïcode"]},` +
	`{"id":"bad id","hostname":"weird","tags":"x"}]`

var tagPath = regexp.MustCompile(`^/api/peers/[^/]+/tags$`)

func (s *apiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodGet {
		s.mu.Lock()
		s.calls = append(s.calls, apiCall{Method: r.Method, URL: r.URL.RequestURI(), Body: string(body)})
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/peers":
		_, _ = io.WriteString(w, stubPeers)
	case r.Method == http.MethodGet && r.URL.Path == "/api/remote-targets":
		_, _ = io.WriteString(w, "[]")
	case r.Method == http.MethodPut && tagPath.MatchString(r.URL.Path):
		_, _ = io.WriteString(w, `{"success":true}`)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	}
}

func (s *apiStub) take() []apiCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := s.calls
	s.calls = nil
	if calls == nil {
		calls = []apiCall{}
	}
	return calls
}

// Access tokens and second-step secrets are random: they are compared by
// their form.
var (
	accessToken = regexp.MustCompile(`"access_token":"[0-9a-f]{64}"`)
	tfaSecret   = regexp.MustCompile(`"secret":"[0-9a-f]{32}"`)
	randomGUID  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)
)

// maskSecrets replaces what is random on both sides: tokens, secrets and
// the guids crypto.randomUUID made.
func maskSecrets(body string) string {
	body = accessToken.ReplaceAllString(body, `"access_token":"<64 hex>"`)
	body = tfaSecret.ReplaceAllString(body, `"secret":"<32 hex>"`)
	return randomGUID.ReplaceAllString(body, `<uuid>`)
}

// TestNodeAnswers replays what the Node console's client API answered
// with its real services and database adapter, on SQLite and PostgreSQL
// (testdata/node_sqlite.json, testdata/node_postgres.json): the address
// books a client reads back after storing one, tags, heartbeats, system
// information, audit uploads and lists, sign-ins and sign-outs, each with
// the rows it leaves. Status, Content-Type, body (byte for byte) and the
// writes to the API server must be Node's, except for the steps a bug fix
// changed (differences_test.go): those must be the Go console's answers in
// testdata/node_<db>_fixed.json.
func TestNodeAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, func(cfg *config.Config) { cfg.APIEnabled = true })
		stub := &apiStub{}
		d := w.Server.Deps()
		d.GoAPI = goapi.New(stub)
		d.Backend = goapi.NewBackend(d.GoAPI, devicegroups.DeviceStore{Groups: d.DeviceGroups})
		rec, file := replay.Load(t, db, "node"), "node_"+db.Dialect().String()
		recording := recordingFixed()
		fixes, fixed, queries := fixesOf(file), map[string]fixedEntry{}, map[string]replay.Step{}
		if !recording {
			fixed = loadFixed(t, file)
		}
		names := make([]string, len(rec.Steps))
		for i := range rec.Steps {
			step := &rec.Steps[i]
			names[i] = step.Name
			if _, ok := fixes[step.Name]; !ok {
				continue
			}
			switch {
			case recording && step.SQL != "":
				// The check below runs the query and keeps its rows
				queries[step.Name] = *step
				step.SQL = ""
			case !recording:
				var answer replay.Step
				if err := json.Unmarshal(fixed[step.Name].Answer, &answer); err != nil {
					t.Fatalf("the fixed answer of %s: %v", step.Name, err)
				}
				step.Status, step.Answer, step.Rows, step.Extra = answer.Status, answer.Answer, answer.Rows, answer.Extra
			}
		}
		checkListed(t, file, names)
		answers := map[string]any{}
		failed := 0
		w.Run(rec, func(t *testing.T, step replay.Step, got replay.Answer) bool {
			if _, ok := fixes[step.Name]; ok && recording {
				if query, ok := queries[step.Name]; ok {
					answers[step.Name] = map[string]any{"rows": w.Rows(query.SQL, query.Params)}
					return true
				}
				answers[step.Name] = map[string]any{"status": got.Status, "answer": maskSecrets(got.Body),
					"extra": map[string]any{"type": got.Header.Get("Content-Type"), "calls": stub.take()}}
				return true
			}
			var extra struct {
				Type  string    `json:"type"`
				Calls []apiCall `json:"calls"`
			}
			if raw, ok := step.Extra["type"]; ok {
				_ = json.Unmarshal(raw, &extra.Type)
			}
			if raw, ok := step.Extra["calls"]; ok {
				_ = json.Unmarshal(raw, &extra.Calls)
			}
			// The sign-in service asks the API server about accounts it
			// does not know (console/authn checks that); Node's went to
			// the same stub, the Go console's has an API of its own here.
			kept := []apiCall{}
			for _, c := range extra.Calls {
				if !strings.HasPrefix(c.URL, "/api/auth/") {
					kept = append(kept, c)
				}
			}
			extra.Calls = kept
			calls := stub.take()
			var problems []string
			if got.Status != step.Status {
				problems = append(problems, "status")
			}
			if want, have := replay.Normalize(maskSecrets(step.Answer)), replay.Normalize(maskSecrets(got.Body)); want != have {
				problems = append(problems, "body\n   node: "+want+"\n     go: "+have)
			}
			if have := got.Header.Get("Content-Type"); have != extra.Type {
				problems = append(problems, "Content-Type "+have+", want "+extra.Type)
			}
			wantCalls, _ := json.Marshal(extra.Calls)
			haveCalls, _ := json.Marshal(calls)
			if string(wantCalls) != string(haveCalls) {
				problems = append(problems, "API writes "+string(haveCalls)+", want "+string(wantCalls))
			}
			if len(problems) > 0 {
				failed++
				if failed <= 25 {
					t.Errorf("step %q (%s %s, status %d, want %d): %s", step.Name, step.Method, step.Path, got.Status, step.Status, strings.Join(problems, "; "))
				}
			}
			return true
		})
		if recording {
			writeFixed(t, file, answers)
		}
	})
}
