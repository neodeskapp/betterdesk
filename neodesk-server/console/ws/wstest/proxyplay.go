package wstest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ws"
)

// ProxyRecording is what the oracles of the proxies to the API server
// wrote (cdapproxy, meshproxy): scenarios of steps, each step with what
// the browser sockets, the API server's side and the permission lookups
// saw.
type ProxyRecording struct {
	Scenarios []struct {
		Name  string `json:"name"`
		Crash bool   `json:"crash"`
		Steps []Step `json:"steps"`
	} `json:"scenarios"`
}

// ProxyStep is one step: set the API server's plan, open a socket, send
// on it (text, binary, bytes as text, a close, a drop), let held
// handshakes go, or act on the API server's side (Up).
type ProxyStep struct {
	Plan    *Plan             `json:"plan"`
	Open    string            `json:"open"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Send    string            `json:"send"`
	Text    *string           `json:"text"`
	Binary  *string           `json:"binary"`
	Bytes   *string           `json:"bytes"`
	Close   *int              `json:"close"`
	Reason  string            `json:"reason"`
	Drop    bool              `json:"drop"`
	Release bool              `json:"release"`
	Up      string            `json:"up"`
}

// ProxySetup builds the proxy for one scenario: register its sockets on
// a registry whose sockets reach api as the API server.
type ProxySetup struct {
	// Register adds the proxy's sockets.
	Register func(reg *ws.Registry, api http.Handler)
	// Permissions are the roles' permissions; the role "throw" makes the
	// lookup fail.
	Permissions map[string][]string
	// Describe and GreetBinary configure the made-up API server.
	Describe    func(r *http.Request) Event
	GreetBinary bool
}

// PlayProxy plays every scenario of a proxy recording on the Go proxy and
// compares each step: what every socket saw, in order per socket. A
// scenario where Node's process ended ({crash}) is checked for what this
// console does instead (DECISIONS A19): the socket that caused it ends
// without an answer and nothing else ends.
func PlayProxy(t *testing.T, file string, setup ProxySetup) {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var rec ProxyRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			check := Goroutines(t)
			p := NewPlayer(t, "")
			note := p.Note
			fake := &FakeAPI{Describe: setup.Describe, GreetBinary: setup.GreetBinary, Note: note}
			fake.SetPlan(Plan{})
			reg := ws.New(ws.Options{
				Session: OracleSession,
				Permission: func(_ context.Context, role, permission string) (bool, error) {
					note(Event{"permission": []any{role, permission}})
					if role == "throw" {
						return false, errLookup
					}
					for _, have := range setup.Permissions[role] {
						if have == permission {
							return true, nil
						}
					}
					return false, nil
				},
			})
			setup.Register(reg, fake)
			srv := httptest.NewServer(reg)
			p.SetAddr(strings.TrimPrefix(srv.URL, "http://"))
			for i, s := range sc.Steps {
				var st ProxyStep
				if err := json.Unmarshal(s.Step, &st); err != nil {
					t.Fatal(err)
				}
				playProxyStep(p, fake, st)
				p.Quiet(10)
				got := p.Take()
				node := RecordedEvents(t, s.Events)
				if sc.Crash && hasCrash(node) {
					checkDropped(t, i, st.Open, got)
					continue
				}
				if diffs := SameStreams(node, got); diffs != nil {
					t.Errorf("step %d %s:\n%s", i, s.Step, strings.Join(diffs, "\n"))
				}
			}
			p.CloseAll()
			fake.Wait()
			srv.Close()
			reg.Close()
			check()
		})
	}
}

type lookupError struct{}

func (lookupError) Error() string { return "lookup failed" }

var errLookup = lookupError{}

func playProxyStep(p *Player, fake *FakeAPI, st ProxyStep) {
	if st.Plan != nil {
		fake.SetPlan(*st.Plan)
	}
	if st.Open != "" {
		p.Open(st.Open, "", st.Path, st.Headers)
	}
	if st.Send != "" {
		switch {
		case st.Text != nil:
			p.Write(st.Send, 1, []byte(*st.Text))
		case st.Binary != nil:
			p.Write(st.Send, 2, decode64(*st.Binary))
		case st.Bytes != nil:
			p.Write(st.Send, 1, decode64(*st.Bytes))
		case st.Close != nil:
			p.Write(st.Send, 8, []byte{byte(*st.Close >> 8), byte(*st.Close)})
		case st.Drop:
			p.Drop(st.Send)
		}
	}
	if st.Release {
		fake.Release()
	}
	if st.Up != "" {
		closeCode := 0
		if st.Close != nil {
			closeCode = *st.Close
		}
		fake.Up(st.Up, st.Text, st.Binary, st.Bytes, closeCode, st.Reason, st.Drop)
	}
}

func hasCrash(events []Event) bool {
	for _, e := range events {
		if _, ok := e["crash"]; ok {
			return true
		}
	}
	return false
}

// checkDropped checks a step where Node's process ended: the socket the
// step opened got no answer and its connection ended; no other socket
// ended and nothing reached the API server.
func checkDropped(t *testing.T, step int, opened string, got []Event) {
	t.Helper()
	ended := false
	for _, e := range got {
		switch {
		case e["socket"] == opened && e["end"] == true:
			ended = true
		case e["socket"] == opened:
			t.Errorf("step %d: the socket that ended Node got %v", step, e)
		case e["end"] == true:
			t.Errorf("step %d: another socket ended: %v", step, e)
		case e["upstream"] != nil && e["open"] != nil:
			t.Errorf("step %d: the API server got a socket: %v", step, e)
		}
	}
	if !ended {
		t.Errorf("step %d: the connection of %s did not end", step, opened)
	}
}

func decode64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}
