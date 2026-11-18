package remoterelay

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// testRelay is a service on the oracle's world behind a test server.
func testRelay(t *testing.T, opts func(*Options)) (*Service, string, func()) {
	t.Helper()
	world := &oracleWorld{}
	o := world.options(nil, nil)
	if opts != nil {
		opts(&o)
	}
	svc := New(o)
	reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: world.permission})
	svc.Register(reg)
	srv := httptest.NewServer(reg)
	stop := func() {
		srv.Close()
		reg.Close()
	}
	t.Cleanup(stop)
	return svc, strings.TrimPrefix(srv.URL, "http://"), stop
}

var operator = http.Header{"X-Oracle-Session": {`{"userId":7,"user":{"id":7,"username":"olga","role":"operator"}}`}}

func dial(t *testing.T, addr, target string, header http.Header) *wstest.Client {
	t.Helper()
	c, res, err := wstest.Dial(addr, target, header)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatalf("%s: refused with %d", target, res.StatusCode)
	}
	return c
}

func texts(frames []wstest.Frame) []string {
	var out []string
	for _, f := range frames {
		out = append(out, f.Text())
	}
	return out
}

// The recording's deepest status message is one level below the limit
// here (the message's own object is the extra level), so the constant
// follows the recording.
func TestStringifyDepthIsTheRecordedOne(t *testing.T) {
	rec := loadRecording(t)
	if got := rec.Crash.DeepestStatus + 1; got != stringifyDepth {
		t.Fatalf("the recording allows %d levels, stringifyDepth is %d", got, stringifyDepth)
	}
	nested := func(n int) string {
		return `{"type":"status","a":` + strings.Repeat("[", n-1) + strings.Repeat("]", n-1) + `}`
	}
	if v, out := agentMessage(nested(stringifyDepth)); v != forward || out != nested(stringifyDepth) {
		t.Errorf("a message at the limit: %v %q", v, out)
	}
	if v, _ := agentMessage(nested(stringifyDepth + 1)); v != drop {
		t.Errorf("a message one over the limit: %v, want drop", v)
	}
	if v, _ := agentMessage(`{"type":"other","a":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`); v != ignore {
		t.Errorf("a deep message of another type: %v, want ignore", v)
	}
	if v, _ := agentMessage(`{"a":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `,"type":"pong"}`); v != drop {
		t.Errorf("a deep pong: %v, want drop", v)
	}
	if v, _ := viewerMessage(`{"type":"stop","a":` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`); v != stop {
		t.Errorf("a deep stop: %v, want stop", v)
	}
	if v, _ := viewerMessage(`{"type":"input","a":` + strings.Repeat("[", 5000) + strings.Repeat("]", 5000) + `}`); v != tooDeep {
		t.Errorf("a deep input: %v, want tooDeep", v)
	}
	input := func(n int) string {
		return `{"type":"input","a":` + strings.Repeat("[", n-1) + strings.Repeat("]", n-1) + `}`
	}
	if v, out := viewerMessage(input(stringifyDepth)); v != forward || out != `{"type":"unknown","a":`+strings.Repeat("[", stringifyDepth-1)+strings.Repeat("]", stringifyDepth-1)+`}` {
		t.Errorf("an input at the limit: %v", v)
	}
	if v, _ := viewerMessage(input(stringifyDepth + 1)); v != tooDeep {
		t.Errorf("an input one over the limit: %v, want tooDeep", v)
	}
}

// Where a message or an upgrade ended the Node process (the recording's
// crash cases), the Go relay drops that one connection without a close
// frame and goes on serving; where Node lived on, the socket stays open. A
// path whose escapes are not even well-formed is refused by the HTTP
// server before the socket sees it.
func TestWhereNodeEndedTheConnectionIsDropped(t *testing.T) {
	// A broken escape never reaches the socket: Go's HTTP server refuses
	// the request target first
	const badRequest = "HTTP/1.1 400 Bad Request"
	deep := func(n int) string { return strings.Repeat("[", n) + strings.Repeat("]", n) }
	type plan struct {
		upgrade string // an agent path that is refused before the handshake
		// refused is the head of Go's answer to the upgrade: "" for a
		// connection dropped without one
		refused string
		agent   bool   // open dev1's agent first
		viewer  string // the device of the viewer, "" for none
		from    string // who sends: agent or viewer
		text    string
	}
	plans := map[string]plan{
		"agent path with a broken escape":         {upgrade: "/ws/remote-agent/%E0%A4%A?token=tok-x-good", refused: badRequest},
		"agent path with a bad escape":            {upgrade: "/ws/remote-agent/dev%ZZ?token=x", refused: badRequest},
		"agent path with a lone surrogate escape": {upgrade: "/ws/remote-agent/%ED%A0%80?token=x"},
		"agent text null":                         {agent: true, from: "agent", text: "null"},
		"agent text 5":                            {agent: true, from: "agent", text: "5"},
		"viewer text null":                        {viewer: "dev1", from: "viewer", text: "null"},
		"viewer text null without agent":          {viewer: "dev2", from: "viewer", text: " null "},
		"agent status nested 5000":                {agent: true, from: "agent", text: `{"type":"status","a":` + deep(5000) + `}`},
		"agent other nested 5000":                 {agent: true, from: "agent", text: `{"type":"other","a":` + deep(5000) + `}`},
		"agent status nested 100000":              {agent: true, from: "agent", text: `{"type":"status","a":` + deep(100000) + `}`},
		"viewer input nested 5000 with agent":     {agent: true, viewer: "dev1", from: "viewer", text: `{"type":"input","a":` + deep(5000) + `}`},
		"viewer input nested 5000 without agent":  {viewer: "dev2", from: "viewer", text: `{"type":"input","a":` + deep(5000) + `}`},
		"viewer stop nested 5000":                 {viewer: "dev1", from: "viewer", text: `{"type":"stop","a":` + deep(5000) + `}`},
	}
	rec := loadRecording(t)
	if len(rec.Crash.Cases) != len(plans) {
		t.Fatalf("%d crash cases recorded, %d planned", len(rec.Crash.Cases), len(plans))
	}
	for _, c := range rec.Crash.Cases {
		t.Run(c.Name, func(t *testing.T) {
			p, ok := plans[c.Name]
			if !ok {
				t.Fatalf("no plan for %q", c.Name)
			}
			check := wstest.Goroutines(t)
			_, addr, stop := testRelay(t, nil)
			if p.upgrade != "" {
				answer, err := wstest.Raw(addr, "GET "+p.upgrade+" HTTP/1.1\r\nHost: console.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
				if err != nil {
					t.Fatal(err)
				}
				if head, _, _ := strings.Cut(answer, "\r\n"); !c.Ended || head != p.refused {
					t.Errorf("answer %q, want %q; Node ended: %v", answer, p.refused, c.Ended)
				}
			} else {
				var agent, viewer *wstest.Client
				if p.agent {
					agent = dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
				}
				if p.viewer != "" {
					viewer = dial(t, addr, "/ws/remote-viewer/"+p.viewer, operator)
				}
				sender := agent
				if p.from == "viewer" {
					sender = viewer
				}
				time.Sleep(50 * time.Millisecond)
				if err := sender.Text(p.text); err != nil {
					t.Fatal(err)
				}
				ended := sender.Wait(time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
				_, code, _ := sender.Closed()
				if ended != c.Ended || ended && code != 1006 {
					t.Errorf("ended %v with %d; Node ended: %v (dropped without a close frame: 1006)", ended, code, c.Ended)
				}
				for _, other := range []*wstest.Client{agent, viewer} {
					if other != nil {
						other.Drop()
					}
				}
			}
			// The relay goes on serving
			again := dial(t, addr, "/ws/remote-viewer/dev-3", operator)
			if !again.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
				t.Errorf("a later viewer got %q", texts(again.Frames()))
			}
			again.Close(1000)
			stop()
			check()
		})
	}
}

// Agents and viewers are pinged at the interval, and an unanswered ping
// is not acted on.
func TestBothSocketsArePinged(t *testing.T) {
	check := wstest.Goroutines(t)
	_, addr, stop := testRelay(t, func(o *Options) { o.PingInterval = 40 * time.Millisecond })
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	for _, c := range []*wstest.Client{agent, viewer} {
		if !c.Wait(2*time.Second, func([]wstest.Frame, bool) bool { return c.Pings() >= 3 }) {
			t.Errorf("%d pings", c.Pings())
		}
		if closed, _, _ := c.Closed(); closed {
			t.Error("closed")
		}
	}
	agent.Close(1000)
	viewer.Close(1000)
	stop()
	check()
}

// A sixth viewer never joins: it gets no session information, its
// messages do nothing, and the five see nothing of it.
func TestTheSixthViewerIsClosed(t *testing.T) {
	check := wstest.Goroutines(t)
	svc, addr, stop := testRelay(t, nil)
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	var five []*wstest.Client
	for i := 0; i < MaxViewers; i++ {
		five = append(five, dial(t, addr, "/ws/remote-viewer/dev1", operator))
	}
	sixth := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	if !sixth.Wait(time.Second, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Fatal("the sixth viewer stays open")
	}
	if _, code, reason := sixth.Closed(); code != 1008 || reason != "Max viewers reached" || len(sixth.Frames()) != 0 {
		t.Errorf("closed with %d %q after %q", code, reason, texts(sixth.Frames()))
	}
	if state := svc.SessionState("dev1"); state.ViewerCount != MaxViewers || !state.Streaming {
		t.Errorf("state %+v", state)
	}
	agent.Close(1000)
	for _, v := range five {
		v.Close(1000)
	}
	stop()
	check()
}

// A viewer that joins while the agent's socket is closing (the idle close
// is out, the agent has not answered it) is told the agent is not ready,
// and no stream starts.
func TestViewerOfAClosingAgent(t *testing.T) {
	check := wstest.Goroutines(t)
	clock := &fakeClock{now: time.UnixMilli(1700000000000)}
	svc, addr, stop := testRelay(t, func(o *Options) { o.Clock = clock })
	// An agent that never reads, so it never answers the close
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(conn, "GET /ws/remote-agent/dev1?token=tok-dev1-good HTTP/1.1\r\nHost: console.test\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n")
	deadline := time.Now().Add(2 * time.Second)
	for svc.SessionState("dev1") == nil || !svc.SessionState("dev1").AgentConnected {
		if time.Now().After(deadline) {
			t.Fatal("the agent did not connect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	clock.Advance(AgentIdleTTL)
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	viewer.Wait(500*time.Millisecond, func(f []wstest.Frame, _ bool) bool { return len(f) > 1 })
	if got := texts(viewer.Frames()); len(got) != 1 || got[0] != `{"type":"session-info","agent_ready":false,"streaming":false}` {
		t.Errorf("the viewer got %q", got)
	}
	viewer.Close(1000)
	conn.Close()
	stop()
	check()
}

// lateClock is a fake clock whose timers cannot be stopped: a timer that
// already fired and waits for the lock while a viewer joins.
type lateClock struct{ *fakeClock }

type lateTimer struct{}

func (lateTimer) Stop() bool { return false }

func (c lateClock) AfterFunc(d time.Duration, f func()) Timer {
	c.fakeClock.AfterFunc(d, f)
	return lateTimer{}
}

// The idle close looks at the viewers when it runs: an agent that got a
// viewer meanwhile stays open.
func TestIdleCloseSparesAnAgentWithViewers(t *testing.T) {
	check := wstest.Goroutines(t)
	clock := lateClock{&fakeClock{now: time.UnixMilli(1700000000000)}}
	_, addr, stop := testRelay(t, func(o *Options) { o.Clock = clock })
	agent := dial(t, addr, "/ws/remote-agent/dev1?token=tok-dev1-good", nil)
	viewer := dial(t, addr, "/ws/remote-viewer/dev1", operator)
	if !agent.Wait(time.Second, func(f []wstest.Frame, _ bool) bool { return len(f) == 1 }) {
		t.Fatalf("the agent got %q", texts(agent.Frames()))
	}
	clock.Advance(AgentIdleTTL)
	if agent.Wait(300*time.Millisecond, func(_ []wstest.Frame, closed bool) bool { return closed }) {
		t.Error("the agent of a viewer was closed as idle")
	}
	agent.Close(1000)
	viewer.Close(1000)
	stop()
	check()
}

// A token is good once, for AgentTokenTTL, for its device only.
func TestAgentTokens(t *testing.T) {
	clock := &fakeClock{now: time.UnixMilli(1700000000000)}
	svc, addr, _ := testRelay(t, func(o *Options) { o.Clock = clock })
	opens := func(id, token string) bool {
		c, _, err := wstest.Dial(addr, "/ws/remote-agent/"+id+"?token="+token, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c != nil {
			c.Close(1000)
		}
		return c != nil
	}
	token, expires := svc.IssueRemoteAgentToken("dev1")
	if len(token) != 64 || expires != 60 {
		t.Fatalf("token %q for %d s", token, expires)
	}
	if opens("dev2", token) {
		t.Error("another device's token opened")
	}
	if !opens("dev1", token) {
		t.Error("the token did not open")
	}
	if opens("dev1", token) {
		t.Error("the token opened twice")
	}
	token, _ = svc.IssueRemoteAgentToken("dev1")
	clock.Advance(AgentTokenTTL + time.Millisecond)
	if opens("dev1", token) {
		t.Error("an expired token opened")
	}
}
