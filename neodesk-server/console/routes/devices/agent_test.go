package devices_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ndrelay/fakeagent"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// agentRecording is what testdata/node_agent_<db>.json recorded: the
// routes that reach into an agent, with a made-up agent of A1001 that
// answered each request the way the step says.
type agentRecording struct {
	RawSeed [][]json.RawMessage `json:"seed"`
	Table   map[string]reply    `json:"table"`
	Steps   []struct {
		Name   string           `json:"name"`
		Role   string           `json:"role"`
		Method string           `json:"method"`
		Path   string           `json:"path"`
		Body   *string          `json:"body"`
		Agent  json.RawMessage  `json:"agent"`
		API    map[string]reply `json:"api"`
		SQL    string           `json:"sql"`
		Status int              `json:"status"`
		Answer string           `json:"answer"`
		Rows   string           `json:"rows"`
		Extra  struct {
			Calls []string `json:"calls"`
			Agent []string `json:"agent"`
		} `json:"extra"`
	} `json:"steps"`
}

// agentToken is the made-up agent's access token; the test gives it to
// A1001 as the stack gives device tokens.
const agentToken = "agent-token-A1001"

// TestNodeAgentAnswers replays what the Node console's agent routes
// answered with a connected agent and without one, on SQLite and
// PostgreSQL (testdata/node_agent_*.json): every route with answers of
// every kind (data of every JSON type, errors, an agent that never
// answers), the snapshot and the queued collection of an offline agent,
// not found and out of scope, odd bodies and queries, the audit entries.
// The Go routes must answer the same bytes, make the same calls to the API
// server, send the agent the same requests and leave the same audit rows,
// except for the steps a bug fix changed (fixed_test.go): those must be
// the Go console's answers in testdata/node_agent_<db>_fixed.json.
func TestNodeAgentAnswers(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		// Checked after the console of the test has stopped
		t.Cleanup(wstest.Goroutines(t))
		file := "node_agent_" + db.Dialect().String()
		raw, err := os.ReadFile(filepath.Join("testdata", file+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec agentRecording
		if err := json.Unmarshal(wstest.Expand(raw), &rec); err != nil {
			t.Fatal(err)
		}
		recordFixed, fixes := recordingFixed(), fixesOf(file)
		var fixed map[string]fixedAnswer
		if !recordFixed {
			fixed = loadFixed(t, file)
		}
		answers := map[string]fixedAnswer{}
		names := make([]string, len(rec.Steps))
		api := &fakeAPI{table: rec.Table}
		w := routetest.New(t, routetest.Options{DB: db, API: api, Env: raisedLimits, Config: nodeLanguages})
		ctx := context.Background()
		for _, s := range rec.RawSeed {
			var query string
			var params []any
			_ = json.Unmarshal(s[0], &query)
			_ = json.Unmarshal(s[1], &params)
			for i, p := range params {
				if f, ok := p.(float64); ok && f == float64(int64(f)) {
					params[i] = int64(f)
				}
			}
			if strings.Contains(query, "INSERT INTO users") || strings.Contains(query, "DELETE FROM users") {
				// The world has its accounts already, with the same ids.
				continue
			}
			if _, err := db.Exec(ctx, query, params...); err != nil {
				t.Fatalf("seed %s: %v", query, err)
			}
		}
		// The server's enrollment tokens, which the token check reads first;
		// on PostgreSQL a missing table fails the check.
		if _, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS device_tokens (token_hash TEXT, peer_id TEXT, status TEXT,
			max_uses INTEGER, use_count INTEGER, expires_at TEXT, kind TEXT)`); err != nil {
			t.Fatalf("device_tokens: %v", err)
		}
		sum := sha256.Sum256([]byte(agentToken))
		if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked, token_kind)
			VALUES (?, ?, 1, 'A1001', '', '2999-01-01 00:00:00', ?, 'device')`, agentToken, hex.EncodeToString(sum[:]), false); err != nil {
			t.Fatalf("token: %v", err)
		}
		agent, err := fakeagent.Connect(w.Addr(), "A1001", agentToken)
		if err != nil {
			t.Fatal(err)
		}
		defer agent.Close()

		browsers := map[string]*routetest.Agent{}
		failed := 0
		for i, step := range rec.Steps {
			names[i] = step.Name
			key := stepKey(i, step.Name)
			_, isFixed := fixes[key]
			if isFixed && !recordFixed {
				f := fixed[key]
				step.Status, step.Answer, step.Rows, step.Extra.Calls, step.Extra.Agent = f.Status, f.Answer, f.Rows, f.Calls, f.Agent
			}
			if step.SQL != "" {
				got := rows(t, db, step.SQL)
				if isFixed && recordFixed {
					answers[key] = fixedAnswer{Rows: normalize(got)}
					continue
				}
				if normalize(got) != normalize(step.Rows) {
					t.Errorf("step %d %s:\n node: %s\n   go: %s", i, step.Name, step.Rows, got)
				}
				continue
			}
			b := browsers[step.Role]
			if b == nil {
				b = w.As(step.Role)
				browsers[step.Role] = b
			}
			api.step(step.API)
			var answer any
			silent := false
			if len(step.Agent) > 0 {
				var s string
				if json.Unmarshal(step.Agent, &s) == nil && s == "silent" {
					silent = true
				} else {
					answer = fakeagent.JSON(string(step.Agent))
				}
			}
			agent.Answer(func(fakeagent.Request) any {
				if silent {
					return nil
				}
				return answer
			})
			agent.Take()
			body := ""
			if step.Body != nil {
				body = *step.Body
			}
			got := b.Do(step.Method, step.Path, body, "Accept", "application/json")
			calls := api.taken()
			var sent []string
			for _, req := range agent.Take() {
				sent = append(sent, strings.Replace(req.Raw, req.RequestID, "<id>", 1))
			}
			if isFixed && recordFixed {
				answers[key] = fixedAnswer{Status: got.Status, Answer: normalize(got.Body), Calls: calls, Agent: normalizeAll(sent)}
				continue
			}
			// The oracle's app ends with a stand-in for the console's error
			// handler (a body it cannot parse, one over the size limit);
			// that answer is the console's own and is pinned elsewhere, so
			// only its status counts here.
			sameBody := normalize(got.Body) == normalize(sinceFO1(step.Answer)) || strings.Contains(step.Answer, `"oracle":"error handler"`)
			same := got.Status == step.Status && sameBody &&
				strings.Join(calls, "\n") == strings.Join(step.Extra.Calls, "\n") &&
				normalize(strings.Join(sent, "\n")) == normalize(strings.Join(step.Extra.Agent, "\n"))
			if same {
				continue
			}
			if failed++; failed <= 30 {
				t.Errorf("step %d %s\n node: %d %s\n       calls %q\n       agent %q\n   go: %d %s\n       calls %q\n       agent %q", i, step.Name,
					step.Status, step.Answer, step.Extra.Calls, step.Extra.Agent, got.Status, got.Body, calls, sent)
			}
		}
		if failed > 30 {
			t.Errorf("%d more steps differ", failed-30)
		}
		checkListed(t, file, names)
		if recordFixed {
			writeFixed(t, file, answers)
		}
	})
}

// TestAgentRequestThatWouldEndNode: a body value String() throws on ended
// the old console's process in the asynchronous agent routes (BUGS WS9);
// the Go console drops that one connection and goes on.
func TestAgentRequestThatWouldEndNode(t *testing.T) {
	w := routetest.New(t, routetest.Options{Env: raisedLimits, Config: nodeLanguages})
	admin := w.As("admin")
	for _, path := range []string{"files/write", "files/delete", "files/rename", "files/mkdir", "terminal/execute"} {
		got, err := admin.Try(http.MethodPost, "/api/devices/A1001/"+path, `{"path":{"toString":1},"from":{"toString":1},"command":{"toString":1}}`)
		if err == nil {
			t.Errorf("%s: answered %d %s, want the connection dropped", path, got.Status, got.Body)
		}
	}
	// The synchronous routes answered with the console's 500.
	for _, path := range []string{"input/key", "input/text", "input/mouse"} {
		got := admin.Do(http.MethodPost, "/api/devices/A1001/"+path, `{"key":{"toString":1},"text":{"toString":1},"action":{"toString":1}}`)
		if got.Status != http.StatusInternalServerError {
			t.Errorf("%s: answered %d %s, want 500", path, got.Status, got.Body)
		}
	}
	if got := admin.Do(http.MethodGet, "/health", ""); got.Status != http.StatusOK {
		t.Errorf("the console stopped answering: %d", got.Status)
	}
}
