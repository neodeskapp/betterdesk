package chat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// relayRecording is testdata/node_relay.json, the Node relay's recording
// (testdata/README.md).
type relayRecording struct {
	Scenarios []struct {
		Name    string                     `json:"name"`
		Clock   int64                      `json:"clock"`
		Answers map[string]json.RawMessage `json:"answers"`
		Crash   bool                       `json:"crash"`
		Steps   []wstest.Step              `json:"steps"`
	} `json:"scenarios"`
}

type relayStep struct {
	Answers map[string]json.RawMessage `json:"answers"`
	Open    string                     `json:"open"`
	Path    string                     `json:"path"`
	Method  string                     `json:"method"`
	Headers map[string]string          `json:"headers"`
	Send    string                     `json:"send"`
	Text    *string                    `json:"text"`
	Texts   []string                   `json:"texts"`
	Binary  *string                    `json:"binary"`
	Bytes   *string                    `json:"bytes"`
	Repeat  int                        `json:"repeat"`
	Opcode  byte                       `json:"opcode"`
	Close   *int                       `json:"close"`
	Drop    bool                       `json:"drop"`
	Advance int64                      `json:"advance"`
}

// apiAnswer is one answer of the made-up API server.
type apiAnswer struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// fakeChatAPI is the oracle's API server: the scenario's answers by
// "METHOD path" (a list is used up in order, its last answer stays), 500
// for anything else, and every call noted.
type fakeChatAPI struct {
	mu      sync.Mutex
	answers map[string][]apiAnswer
	note    func(wstest.Event)
}

func (f *fakeChatAPI) set(t *testing.T, raw map[string]json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, r := range raw {
		var list []apiAnswer
		if bytes.HasPrefix(bytes.TrimSpace(r), []byte("[")) {
			if err := json.Unmarshal(r, &list); err != nil {
				t.Fatal(err)
			}
		} else {
			var one apiAnswer
			if err := json.Unmarshal(r, &one); err != nil {
				t.Fatal(err)
			}
			list = []apiAnswer{one}
		}
		f.answers[key] = list
	}
}

func (f *fakeChatAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.RequestURI, "/api")
	call := map[string]any{"method": r.Method, "path": path}
	if r.Method != http.MethodGet {
		body, _ := io.ReadAll(r.Body)
		call["body"] = string(body)
	}
	f.note(wstest.Event{"api": call})
	f.mu.Lock()
	answer := apiAnswer{Status: 500, Body: `{"error":"internal"}`}
	if list := f.answers[r.Method+" "+path]; len(list) > 0 {
		answer = list[0]
		if len(list) > 1 {
			f.answers[r.Method+" "+path] = list[1:]
		}
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(answer.Status)
	_, _ = io.WriteString(w, answer.Body)
}

// oracleChat are the roles with chat.access in the oracle.
var oracleChat = map[string]bool{"admin": true, "super_admin": true, "operator": true}

// TestNodeScenarios plays every scenario the Node oracle recorded
// (testdata/node_relay.json) on the Go relay with the same clock and the
// same answers of the API server, and compares each step: what every
// socket saw (the answer to its handshake, each message, the close, the
// end), every call to the API server (path and body), every token check
// and permission lookup. Where Node's process ended ({crash}), the socket
// that sent the message ends without an answer and nothing else ends
// (DECISIONS A19).
func TestNodeScenarios(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_relay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec relayRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			p := wstest.NewPlayer(t, "")
			note := p.Note
			api := &fakeChatAPI{answers: map[string][]apiAnswer{}, note: note}
			api.set(t, sc.Answers)
			var clockMu sync.Mutex
			clock := sc.Clock
			now := func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				return time.UnixMilli(clock)
			}
			relay := NewRelay(RelayOptions{
				API: goapi.New(api, goapi.WithTimeout(5*time.Second)),
				VerifyDevice: func(_ context.Context, deviceID, token string) (bool, error) {
					note(wstest.Event{"verify": []any{deviceID, token}})
					if token == "tok-throw" {
						return false, errors.New("database down")
					}
					return token == "tok-"+deviceID+"-good", nil
				},
				Now: now,
			})
			reg := ws.New(ws.Options{
				Session: wstest.OracleSession,
				Permission: func(_ context.Context, role, permission string) (bool, error) {
					note(wstest.Event{"permission": []any{role, permission}})
					if role == "throw" {
						return false, errors.New("lookup failed")
					}
					return permission == "chat.access" && oracleChat[role], nil
				},
			})
			relay.Register(reg)
			srv := httptest.NewServer(reg)
			p.SetAddr(strings.TrimPrefix(srv.URL, "http://"))
			for i, s := range sc.Steps {
				var st relayStep
				if err := json.Unmarshal(s.Step, &st); err != nil {
					t.Fatal(err)
				}
				if st.Answers != nil {
					api.set(t, st.Answers)
				}
				if st.Open != "" {
					p.Open(st.Open, st.Method, st.Path, st.Headers)
				}
				if st.Send != "" {
					sendStep(p, st)
				}
				if st.Advance > 0 {
					clockMu.Lock()
					clock += st.Advance
					clockMu.Unlock()
				}
				if len(st.Texts) > 0 {
					p.Quiet(50)
				} else {
					p.Quiet(10)
				}
				got := p.Take()
				node := wstest.RecordedEvents(t, s.Events)
				if fixed, ok := fixedEvents(t, sc.Name, i, node); ok {
					node = fixed
				}
				if sc.Crash && hasCrash(node) {
					checkDropped(t, i, st.Send, got)
					continue
				}
				if diffs := wstest.SameStreams(node, got); diffs != nil {
					t.Errorf("step %d %s:\n%s", i, s.Step, strings.Join(diffs, "\n"))
				}
			}
			p.CloseAll()
			srv.Close()
			reg.Close()
			check()
		})
	}
}

// fix is a step of the recording that a bug fix changed on purpose: what
// the Go relay does there since the fix, as the events the oracle would
// have noted.
type fix struct {
	bug, scenario string
	step          int
	events        string
}

// fixes are the steps the bug fixes changed. The tests of each fix pin
// what it does; here the steps say only what the replay sees instead.
var fixes = []fix{
	// An agent gets the contacts of its own device only, and reads the
	// history of its own conversations only: its device's and the groups
	// it is in, which it asks the API server for
	{"SK4", "agent session", 27, `[]`},
	{"SK4", "agent session", 30, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	{"SK4", "fallbacks and odd answers", 17, `[]`},
	{"SK4", "conversation ids the API path cannot take", 13, `[]`},
	{"SK4", "conversation ids the API path cannot take", 14, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	// No device socket comes in under the inbox's id "panel": it is refused
	// before its token is looked at, sends nothing, and the inbox sees no
	// agent
	{"SK8", "agent replaced and gone", 14, `[{"socket":"a5","head":"HTTP/1.1 403 Forbidden\r\n\r\n"},{"socket":"a5","end":true}]`},
	{"SK8", "agent replaced and gone", 15, `[]`},
	{"SK8", "agent replaced and gone", 16, `[{"permission":["operator","chat.access"]},` +
		`{"socket":"p2","head":"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"},` +
		`{"api":{"method":"GET","path":"/chat/contacts/panel"}},{"api":{"method":"GET","path":"/chat/groups/panel"}},` +
		`{"socket":"p2","text":"{\"type\":\"status\",\"agent_connected\":false}"},` +
		`{"socket":"p2","text":"{\"type\":\"contacts\",\"contacts\":[{\"id\":\"dev1\",\"name\":\"one\",\"online\":true},{\"id\":\"operator:admin\",\"name\":\"admin\",\"role\":\"operator\"}]}"},` +
		`{"socket":"p2","text":"{\"type\":\"groups\",\"groups\":[]}"}]`},
	{"SK8", "agent replaced and gone", 17, `[]`},
	// An agent's messages carry its device's name (here its id, the replay
	// has no device rows), and an agent writes only into its device's
	// conversation, the operators' and its groups': anything else waits for
	// the group lookup and goes nowhere
	{"RV163", "agent session", 5, `[{"api":{"method":"POST","path":"/chat/messages","body":"{\"conversation_id\":\"operator\",\"from_id\":\"dev1\",\"from_name\":\"dev1\",\"to_id\":\"\",\"text\":\"no conversation\"}"}},` +
		`{"socket":"op","text":"{\"type\":\"message\",\"id\":1700000002000,\"from\":\"dev1\",\"from_name\":\"dev1\",\"conversation_id\":\"operator\",\"text\":\"no conversation\",\"timestamp\":\"yesterday\"}"},` +
		`{"socket":"panel","text":"{\"type\":\"message\",\"id\":1700000002000,\"from\":\"dev1\",\"from_name\":\"dev1\",\"conversation_id\":\"operator\",\"text\":\"no conversation\",\"timestamp\":\"yesterday\"}"}]`},
	{"RV163", "agent session", 7, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	{"RV163", "agent session", 14, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	{"RV163", "agent session", 19, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	{"RV163", "agent session", 23, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	{"RV163", "agent session", 42, `[{"api":{"method":"GET","path":"/chat/groups/dev1"}}]`},
	// A socket reads no message over MaxTextBytes: the agent that sends one
	// is closed with 1009 and leaves its room, so its next message goes
	// nowhere
	{"RV166", "agent replaced and gone", 10, `[{"socket":"a4","close":1009,"reason":""},{"socket":"a4","end":true},` +
		`{"socket":"o","text":"{\"type\":\"status\",\"agent_connected\":false}"},` +
		`{"socket":"o","text":"{\"type\":\"presence\",\"device_id\":\"dev1\",\"online\":false}"},` +
		`{"socket":"p","text":"{\"type\":\"status\",\"device_id\":\"dev1\",\"agent_connected\":false}"},` +
		`{"socket":"p","text":"{\"type\":\"presence\",\"device_id\":\"dev1\",\"online\":false}"}]`},
	{"RV166", "agent replaced and gone", 12, `[]`},
	{"RV166", "agent session", 45, `[{"socket":"a","close":1009,"reason":""},{"socket":"a","end":true},` +
		`{"socket":"op","text":"{\"type\":\"status\",\"agent_connected\":false}"},` +
		`{"socket":"op","text":"{\"type\":\"presence\",\"device_id\":\"dev1\",\"online\":false}"},` +
		`{"socket":"panel","text":"{\"type\":\"status\",\"device_id\":\"dev1\",\"agent_connected\":false}"},` +
		`{"socket":"panel","text":"{\"type\":\"presence\",\"device_id\":\"dev1\",\"online\":false}"}]`},
	{"RV166", "agent session", 46, `[]`},
	// An agent makes groups of its own device and operators only: a group
	// with another device is not made, and the operator's group that
	// follows gets the answer the made-up API server held for the agent's.
	// An agent socket's 41st message at once is dropped
	{"L56", "agent session", 31, `[]`},
	{"L56", "agent session", 44, `[]`},
	{"L56", "fallbacks and odd answers", 13, `[]`},
	{"L56", "fallbacks and odd answers", 14, `[{"api":{"method":"POST","path":"/chat/groups","body":"{\"name\":\"G\",\"members\":[\"x\"],\"created_by\":\"anna\"}"}},` +
		`{"api":{"method":"GET","path":"/chat/groups/dev1"}},` +
		`{"socket":"o1","text":"{\"0\":\"m\",\"1\":\"a\",\"2\":\"d\",\"3\":\"e\",\"type\":\"group_created\"}"}]`},
}

// fixedEvents is what a step of the recording looks like since the fixes:
// ok is false for a step no fix changed. A fix whose events are the ones
// Node noted is stale and fails the test.
func fixedEvents(t *testing.T, scenario string, step int, node []wstest.Event) ([]wstest.Event, bool) {
	t.Helper()
	for _, f := range fixes {
		if f.scenario != scenario || f.step != step {
			continue
		}
		var events []wstest.Event
		if err := json.Unmarshal([]byte(f.events), &events); err != nil {
			t.Fatalf("the %s fix of %q step %d: %v", f.bug, scenario, step, err)
		}
		if wstest.SameStreams(node, events) == nil {
			t.Errorf("the %s fix of %q step %d changes nothing", f.bug, scenario, step)
		}
		return events, true
	}
	return nil, false
}

// TestFixesNameRecordedSteps: every fix names a step of the recording.
func TestFixesNameRecordedSteps(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_relay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec relayRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	steps := map[string]int{}
	for _, sc := range rec.Scenarios {
		steps[sc.Name] = len(sc.Steps)
	}
	for _, f := range fixes {
		if n, ok := steps[f.scenario]; !ok || f.step < 0 || f.step >= n {
			t.Errorf("the %s fix names step %d of %q, which the recording does not have", f.bug, f.step, f.scenario)
		}
	}
}

func sendStep(p *wstest.Player, st relayStep) {
	switch {
	case st.Text != nil:
		p.Write(st.Send, 1, []byte(*st.Text))
	case len(st.Texts) > 0:
		for _, text := range st.Texts {
			p.Write(st.Send, 1, []byte(text))
		}
	case st.Binary != nil:
		b, _ := base64.StdEncoding.DecodeString(*st.Binary)
		p.Write(st.Send, 2, b)
	case st.Bytes != nil:
		b, _ := base64.StdEncoding.DecodeString(*st.Bytes)
		p.Write(st.Send, 1, b)
	case st.Repeat > 0:
		op := st.Opcode
		if op == 0 {
			op = 1
		}
		p.Write(st.Send, op, bytes.Repeat([]byte{'x'}, st.Repeat))
	case st.Close != nil:
		p.Write(st.Send, 8, []byte{byte(*st.Close >> 8), byte(*st.Close)})
	case st.Drop:
		p.Drop(st.Send)
	}
}

func hasCrash(events []wstest.Event) bool {
	for _, e := range events {
		if _, ok := e["crash"]; ok {
			return true
		}
	}
	return false
}

// checkDropped checks a step whose message ended Node's process: the
// sender's connection ended without a close frame or any message, and no
// other socket ended.
func checkDropped(t *testing.T, step int, sender string, got []wstest.Event) {
	t.Helper()
	ended := false
	for _, e := range got {
		switch {
		case e["socket"] == sender && e["end"] == true:
			ended = true
		case e["socket"] == sender:
			t.Errorf("step %d: the sender got %v", step, e)
		case e["end"] == true:
			t.Errorf("step %d: another socket ended: %v", step, e)
		}
	}
	if !ended {
		t.Errorf("step %d: the connection of %s did not end", step, sender)
	}
}
