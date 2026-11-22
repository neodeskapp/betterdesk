//go:build linux

package servermgmt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// nodeTerminal is testdata/node_terminal_<mode>.json, recorded from the
// old serverTerminalProxy.js (testdata/README.md).
type nodeTerminal struct {
	Mode     string `json:"mode"`
	Refusals []struct {
		Name    string     `json:"name"`
		Headers []string   `json:"headers"`
		Answer  string     `json:"answer"`
		Lookups [][]string `json:"lookups"`
		Audits  [][]any    `json:"audits"`
	} `json:"refusals"`
	Conversations []struct {
		Name  string            `json:"name"`
		Env   map[string]string `json:"env"`
		Steps []struct {
			Step struct {
				Send        any     `json:"send"`
				Binary      any     `json:"binary"`
				CloseClient bool    `json:"closeClient"`
				Marker      string  `json:"marker"`
				Count       int     `json:"count"`
				Quiet       int     `json:"quiet"`
				Limit       int     `json:"limit"`
				UntilClosed bool    `json:"untilClosed"`
				Alive       float64 `json:"alive"`
			} `json:"step"`
			Events []map[string]any `json:"events"`
			Output string           `json:"output"`
			Closed *struct {
				Code   int    `json:"code"`
				Reason string `json:"reason"`
			} `json:"closed"`
			Alive map[string]bool `json:"alive"`
		} `json:"steps"`
		Audits  [][]any    `json:"audits"`
		Lookups [][]string `json:"lookups"`
	} `json:"conversations"`
}

// oracleWorld is the Go terminal behind a test server with the stand-ins
// the oracle had: the session in X-Oracle-Session, the permission table,
// the audit log, the clock.
type oracleWorld struct {
	srv     *httptest.Server
	reg     *ws.Registry
	svc     *Service
	env     map[string]string
	mu      sync.Mutex
	audits  [][]any
	lookups [][]string
}

func newOracleWorld(t *testing.T, noPty bool) *oracleWorld {
	t.Helper()
	w := &oracleWorld{env: map[string]string{}}
	w.reg = ws.New(ws.Options{
		Session: wstest.OracleSession,
		Permission: func(_ context.Context, role, permission string) (bool, error) {
			w.mu.Lock()
			w.lookups = append(w.lookups, []string{role, permission})
			w.mu.Unlock()
			if role == "throws" {
				return false, errors.New("lookup failed")
			}
			allowed := map[string]bool{"admin": true, "server_admin": true, "super_admin": true, "operator-with-override": true}
			return allowed[role] && permission == "server.config", nil
		},
		// the oracle had no panel access policy: every account reaches
		// every section
		SectionAccess: func(context.Context, access.PolicyUser, string, access.Level) (bool, error) { return true, nil },
	})
	w.svc = New(Options{
		GOOS: "linux",
		Getenv: func(k string) string {
			w.mu.Lock()
			defer w.mu.Unlock()
			if v, ok := w.env[k]; ok {
				return v
			}
			return os.Getenv(k)
		},
		Now: func() time.Time { return time.UnixMilli(1700000000000) },
		Terminal: TerminalOptions{
			NoPty: noPty,
			// the old terminal looked at the session and the permission
			// once; the checks of an open terminal (terminal_test.go) would
			// add lookups to a long conversation
			SessionCheck: time.Hour,
			Audit: func(_ context.Context, userID int64, action, details, ip string) {
				w.mu.Lock()
				w.audits = append(w.audits, []any{float64(userID), action, details, ip})
				w.mu.Unlock()
			},
		},
	})
	w.svc.Register(w.reg)
	w.srv = httptest.NewServer(w.reg)
	t.Cleanup(func() {
		w.srv.Close()
		w.reg.Close()
	})
	return w
}

func (w *oracleWorld) addr() string { return strings.TrimPrefix(w.srv.URL, "http://") }

// take returns and clears the audit entries and lookups so far.
func (w *oracleWorld) take() (audits [][]any, lookups [][]string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	audits, lookups = w.audits, w.lookups
	w.audits, w.lookups = nil, nil
	return audits, lookups
}

var (
	pidMarker = regexp.MustCompile(`__PID(\w*)__(\d+)`)
	// promptText is a prompt of the recorded accounts; output without a
	// line end may come right before it
	promptText = regexp.MustCompile(`(?:node|root|sync)@\{HOST\}:[^$#\n]*[$#] `)
	// promptTail is the end of a prompt readline shows when it scrolls a
	// line: "<", the end of the host's name, the folder
	promptTail   = regexp.MustCompile(`<[0-9a-zA-Z.-]*:~[$#] `)
	processGroup = regexp.MustCompile(`terminal process group \(-?\d+\)`)
	// rootGroup is the group list (id -G) of a shell that switched
	// accounts under node-pty, which kept root's group 0
	rootGroup = regexp.MustCompile(`\|(\d+) 0\|`)
	// redraws are prompts readline drew again, one after the other
	redraws = regexp.MustCompile(`((?:\r\x1b\[K\r\x1b\]0;[^\a]*\a[^\r]*\$ ))+`)
)

// runeDiff names the characters two texts have a different number of.
func runeDiff(a, b string) string {
	count := map[rune]int{}
	for _, r := range a {
		count[r]++
	}
	for _, r := range b {
		count[r]--
	}
	var out []string
	for r, n := range count {
		if n != 0 {
			out = append(out, fmt.Sprintf("%q:%+d", r, n))
		}
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// pipeRunes is pipe output in a form that compares: standard output and
// standard error come through two pipes, so their pieces may come in
// another order, also inside a line. The prompts are taken out, and so
// are the words readline draws when it scrolls a line that is too short
// for the prompt ("<...", the end of the host's name with ":~$"), and the
// number of the process group bash could not take over (Node's own group
// there, the shell's here). What is left is compared as its characters,
// sorted.
func pipeRunes(s string) string {
	s = processGroup.ReplaceAllString(s, "terminal process group (N)")
	s = promptTail.ReplaceAllString(promptText.ReplaceAllString(s, ""), "<")
	r := []rune(strings.Join(strings.Fields(s), ""))
	sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
	return string(r)
}

// TestNodeTerminalPipe and TestNodeTerminalPty play the oracle's upgrades
// and conversations against the Go terminal, in pipe mode and with a
// pseudo-terminal. Each step must bring the same messages, the same output
// and the same close; each conversation the same audit entries and
// permission lookups. Where the Go terminal differs on purpose the
// expectation is changed here, and only there:
//
//   - A shell that the close message or the socket's end asked to stop is
//     gone afterwards (pipe mode in Node left an interactive bash running:
//     it ignores SIGTERM), and so is the first shell of a second init.
//   - A shell that switched to another account has no supplementary groups
//     (node-pty kept root's group 0 in the shell).
//   - An open terminal looks up server.config again with its session
//     (SM15); the replay checks only once an hour, so the lookups stay the
//     upgrade's.
//   - Pipe output is decoded across reads as a pseudo-terminal's is
//     (SM10): the character split between two reads arrives whole, where
//     the old pipe mode sent two U+FFFD.
func TestNodeTerminalPipe(t *testing.T) { replayTerminal(t, "pipe") }

func TestNodeTerminalPty(t *testing.T) {
	if !ptyWorks() {
		t.Skip("no pseudo-terminals here")
	}
	replayTerminal(t, "pty")
}

func replayTerminal(t *testing.T, mode string) {
	if testing.Short() {
		t.Skip("the conversations take minutes")
	}
	if os.Getuid() != 0 {
		t.Skip("the recording ran as root and switched to the account node")
	}
	if _, err := os.Stat("/home/node"); err != nil {
		t.Skip("the recording's account node is not here")
	}
	raw, err := os.ReadFile("testdata/node_terminal_" + mode + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var rec nodeTerminal
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	w := newOracleWorld(t, mode == "pipe")

	for _, r := range rec.Refusals {
		var b strings.Builder
		b.WriteString("GET /ws/server-management/terminal HTTP/1.1\r\nHost: " + w.addr() + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n")
		hasKey := false
		for _, h := range r.Headers {
			h = strings.ReplaceAll(h, "127.0.0.1:"+portOf(r.Headers), w.addr())
			hasKey = hasKey || strings.HasPrefix(h, "Sec-WebSocket-Key")
			b.WriteString(h + "\r\n")
		}
		if !hasKey {
			b.WriteString("Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")
		}
		b.WriteString("\r\n")
		got, err := wstest.Raw(w.addr(), b.String())
		if err != nil {
			t.Fatal(err)
		}
		got = regexp.MustCompile(`(?i)Sec-WebSocket-Accept: .*\r\n`).ReplaceAllString(got, "Sec-WebSocket-Accept: <accept>\r\n")
		if !wstest.SameAnswer(r.Answer, got) {
			t.Errorf("upgrade %q: got %q, want %q", r.Name, got, r.Answer)
		}
		audits, lookups := settleAudits(w, len(r.Audits))
		if !reflect.DeepEqual(nonNil(audits), nonNil(r.Audits)) || !reflect.DeepEqual(nonNil(lookups), nonNil(r.Lookups)) {
			t.Errorf("upgrade %q: audits %v lookups %v, want %v %v", r.Name, audits, lookups, r.Audits, r.Lookups)
		}
	}

	for _, conv := range rec.Conversations {
		t.Run(conv.Name, func(t *testing.T) {
			w.mu.Lock()
			w.env = map[string]string{}
			for k, v := range conv.Env {
				w.env[k] = v
			}
			w.mu.Unlock()
			header := http.Header{}
			header.Set("X-Oracle-Session", `{"userId":7,"user":{"id":7,"username":"oracle-admin","role":"admin"}}`)
			header.Set("Origin", "http://"+w.addr())
			c, res, err := wstest.Dial(w.addr(), TerminalPath, header)
			if err != nil || c == nil {
				t.Fatalf("dial: %v %v", err, res)
			}
			pids := map[string]int{}
			out := &collected{c: c}
			for i, step := range conv.Steps {
				s := step.Step
				for _, m := range toList(s.Send) {
					if text, ok := m.(string); ok {
						_ = c.Text(text)
					} else {
						b, _ := json.Marshal(m)
						_ = c.Text(string(b))
					}
				}
				if s.Binary != nil {
					b, _ := json.Marshal(s.Binary)
					_ = c.Binary(b)
				}
				if s.CloseClient {
					_ = c.WriteFrame(0x8, nil) // a close without a code, as ws.close() sent it
				}
				count := s.Count
				if count == 0 {
					count = 1
				}
				out.settle(s.Marker, count, s.Quiet, s.Limit, s.UntilClosed)
				events, output, closed := out.take()
				output = strings.ReplaceAll(output, host, "{HOST}")
				for _, m := range pidMarker.FindAllStringSubmatch(output, -1) {
					pids[m[1]], _ = strconv.Atoi(m[2])
				}
				output = pidMarker.ReplaceAllString(output, "__PID${1}__<pid>")
				want := step.Output
				if mode == "pipe" && conv.Name == "split character" {
					want = strings.ReplaceAll(want, "��", "é")
				}
				if mode == "pty" {
					want = rootGroup.ReplaceAllString(want, "|$1|")
					// Two resizes in a row are one redraw or two, as the
					// shell finds the signals.
					want, output = redraws.ReplaceAllString(want, "$1"), redraws.ReplaceAllString(output, "$1")
				}
				where := fmt.Sprintf("step %d %+v", i, s)
				if mode == "pty" && output != want {
					t.Errorf("%s: output\n got %q\nwant %q", where, output, want)
				}
				if mode == "pipe" && pipeRunes(output) != pipeRunes(want) {
					t.Errorf("%s: output differs in %s\n got %.400q\nwant %.400q", where, runeDiff(pipeRunes(output), pipeRunes(want)), output, want)
				}
				if !reflect.DeepEqual(nonNil(events), nonNil(step.Events)) {
					t.Errorf("%s: messages\n got %v\nwant %v", where, events, step.Events)
				}
				if (closed == nil) != (step.Closed == nil) || closed != nil && *closed != *step.Closed {
					t.Errorf("%s: closed %v, want %v", where, closed, step.Closed)
				}
				if step.Alive != nil {
					time.Sleep(time.Duration(s.Alive) * time.Millisecond)
					for name, pid := range pids {
						if alive(pid) {
							t.Errorf("%s: shell %q (%d) still runs (Node: %v)", where, name, pid, step.Alive[name])
						}
					}
				}
			}
			if closed, _, _ := c.Closed(); !closed {
				c.Close(1000)
				c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
			}
			audits, lookups := settleAudits(w, len(conv.Audits))
			if !reflect.DeepEqual(audits, conv.Audits) || !reflect.DeepEqual(lookups, conv.Lookups) {
				t.Errorf("audits %v lookups %v, want %v %v", audits, lookups, conv.Audits, conv.Lookups)
			}
			for name, pid := range pids {
				if alive(pid) {
					t.Errorf("shell %q (%d) still runs after the socket ended", name, pid)
				}
			}
		})
	}
}

// portOf finds the oracle's port in its Origin header.
func portOf(headers []string) string {
	for _, h := range headers {
		if rest, ok := strings.CutPrefix(h, "Origin: http://127.0.0.1:"); ok {
			return rest
		}
	}
	return "-"
}

func toList(v any) []any {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		return x
	}
	return []any{v}
}

// settleAudits waits up to two seconds for n audit entries.
func settleAudits(w *oracleWorld, n int) ([][]any, [][]string) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		got := len(w.audits)
		w.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	return w.take()
}

// alive reports a process that runs and is no zombie.
func alive(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	return !regexp.MustCompile(`^\d+ \(.*\) Z`).Match(stat)
}

// collected gathers what a socket got: messages, output and the close.
type collected struct {
	c      *wstest.Client
	seen   int
	events []map[string]any
	output strings.Builder
}

func (o *collected) pull() {
	frames := o.c.Frames()
	for _, f := range frames[o.seen:] {
		var msg map[string]any
		if err := json.Unmarshal(f.Data, &msg); err != nil {
			o.events = append(o.events, map[string]any{"notJSON": string(f.Data)})
			continue
		}
		if msg["type"] == "output" {
			text, _ := msg["data"].(string)
			o.output.WriteString(text)
			if len(o.events) == 0 || o.events[len(o.events)-1]["type"] != "output" {
				o.events = append(o.events, map[string]any{"type": "output"})
			}
			continue
		}
		o.events = append(o.events, msg)
	}
	o.seen = len(frames)
}

// settle waits as the oracle did: for the marker count times or the
// close, then until the output has been quiet.
func (o *collected) settle(marker string, count, quiet, limit int, untilClosed bool) {
	if quiet == 0 {
		quiet = 300
	}
	if limit == 0 {
		limit = 8000
	}
	start := time.Now()
	elapsed := func() int { return int(time.Since(start).Milliseconds()) }
	for {
		o.pull()
		closed, _, _ := o.c.Closed()
		seen := count
		if marker != "" {
			seen = strings.Count(o.output.String(), marker)
		}
		if closed || !untilClosed && seen >= count || elapsed() > limit {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	last, still := o.output.Len(), 0
	for still < quiet && elapsed() < limit+quiet {
		time.Sleep(20 * time.Millisecond)
		o.pull()
		if o.output.Len() != last {
			last, still = o.output.Len(), 0
		} else {
			still += 20
		}
	}
}

func (o *collected) take() (events []map[string]any, output string, closed *struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}) {
	o.pull()
	events, output = o.events, o.output.String()
	o.events = nil
	o.output.Reset()
	if done, code, reason := o.c.Closed(); done {
		closed = &struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}{code, reason}
	}
	return events, output, closed
}
