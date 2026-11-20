package remoterelay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// fakeClock is the clock of the Node oracle: time moves only when a test
// moves it, and due timers fire in the order of their time, then of their
// creation, in the goroutine that moves the clock.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Time
	seq     int
	f       func()
	stopped bool
	fired   bool
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d < time.Millisecond {
		d = time.Millisecond
	}
	c.seq++
	t := &fakeTimer{c: c, at: c.now.Add(d), seq: c.seq, f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

// Advance moves the clock by d and fires the timers that come due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	until := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if t.stopped || t.fired || t.at.After(until) {
				continue
			}
			if next == nil || t.at.Before(next.at) || (t.at.Equal(next.at) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now = until
			c.mu.Unlock()
			return
		}
		c.now = next.at
		next.fired = true
		c.mu.Unlock()
		next.f()
	}
}

// xorshift is the oracle's random source.
type xorshift struct{ state uint32 }

func (x *xorshift) Read(p []byte) (int, error) {
	for i := range p {
		x.state ^= x.state << 13
		x.state ^= x.state >> 17
		x.state ^= x.state << 5
		p[i] = byte(x.state)
	}
	return len(p), nil
}

// The oracle's world: three devices, the account "scoped" sees dev1 only,
// "scopethrow" makes the scope check fail, dev-throw makes the lookup
// fail, tok-<id>-good is the device token of <id>, tok-throw makes the
// token check fail, admin and operator have device.connect, the role
// "throw" makes the lookup fail.
var oracleDevices = map[string]*goapi.Object{
	"dev1":  goapi.NewObject("id", "dev1", "hostname", "one", "tags", []any{}, "folder_id", nil),
	"dev2":  goapi.NewObject("id", "dev2", "hostname", "two", "tags", []any{"lab"}, "folder_id", float64(3)),
	"dev-3": goapi.NewObject("id", "dev-3", "hostname", "three", "tags", []any{}, "folder_id", nil),
}

// oracleWorld records the outside calls of the service as the oracle did.
type oracleWorld struct {
	mu    sync.Mutex
	calls []string
}

func (w *oracleWorld) note(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, fmt.Sprintf(format, args...))
}

func (w *oracleWorld) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.calls
	w.calls = nil
	return out
}

func (w *oracleWorld) options(clock Clock, random io.Reader) Options {
	return Options{
		VerifyDevice: func(_ context.Context, deviceID, token string) (bool, error) {
			w.note("verify %q %q", deviceID, token)
			if token == "tok-throw" {
				return false, errors.New("database down")
			}
			return token == "tok-"+deviceID+"-good", nil
		},
		Devices: func(_ context.Context, id string) (*goapi.Object, error) {
			w.note("getPeerById %q", id)
			if id == "dev-throw" {
				return nil, errors.New("database down")
			}
			return oracleDevices[id], nil
		},
		CanAccess: func(_ context.Context, user *session.User, device *goapi.Object) (bool, error) {
			w.note("scope %s", device.Str("id"))
			name := ""
			if user != nil {
				name = user.Username
			}
			switch name {
			case "scopethrow":
				return false, errors.New("scope down")
			case "scoped":
				return device.Str("id") == "dev1", nil
			}
			return true, nil
		},
		Clock:  clock,
		Random: random,
		// The oracle had no session check; an hour keeps its lookups out of
		// the recorded calls (TestViewerEndsWithItsSession covers it)
		SessionCheck: time.Hour,
	}
}

func (w *oracleWorld) permission(_ context.Context, role, permission string) (bool, error) {
	if role == "" {
		role = "<undefined>"
	}
	w.note("permission %q %q", role, permission)
	if role == "throw" {
		return false, errors.New("roles down")
	}
	return (role == "admin" || role == "operator") && permission == "device.connect", nil
}

// nodeCall is a call the oracle noted, in the form oracleWorld notes it.
func nodeCall(raw json.RawMessage) string {
	var c struct {
		GetPeerByID *string           `json:"getPeerById"`
		Scope       []json.RawMessage `json:"scope"`
		Verify      []string          `json:"verify"`
		Permission  []string          `json:"permission"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return "?"
	}
	switch {
	case c.GetPeerByID != nil:
		return fmt.Sprintf("getPeerById %q", *c.GetPeerByID)
	case c.Scope != nil:
		var id string
		_ = json.Unmarshal(c.Scope[1], &id)
		return "scope " + id
	case c.Verify != nil:
		return fmt.Sprintf("verify %q %q", c.Verify[0], c.Verify[1])
	case c.Permission != nil:
		return fmt.Sprintf("permission %q %q", c.Permission[0], c.Permission[1])
	}
	return "?"
}

type remoteRecording struct {
	Start     int64 `json:"start"`
	Scenarios []struct {
		Name  string `json:"name"`
		Clock int64  `json:"clock"`
		Steps []struct {
			Step   step              `json:"step"`
			Result json.RawMessage   `json:"result"`
			Events []json.RawMessage `json:"events"`
			Calls  []json.RawMessage `json:"calls"`
		} `json:"steps"`
	} `json:"scenarios"`
	Crash struct {
		Cases []struct {
			Name  string `json:"name"`
			Ended bool   `json:"ended"`
		} `json:"cases"`
		DeepestStatus int `json:"deepestStatus"`
	} `json:"crash"`
}

type step struct {
	Open    string            `json:"open"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Send    string            `json:"send"`
	Text    *string           `json:"text"`
	Binary  *string           `json:"binary"`
	Repeat  int               `json:"repeat"`
	Opcode  byte              `json:"opcode"`
	Close   *int              `json:"close"`
	Drop    bool              `json:"drop"`
	Advance int64             `json:"advance"`
	Call    string            `json:"call"`
	Args    []json.RawMessage `json:"args"`
	As      string            `json:"as"`
}

// event is one thing a socket saw, in the oracle's JSON.
type event struct {
	Socket string  `json:"socket"`
	Head   *string `json:"head,omitempty"`
	Body   *string `json:"body,omitempty"`
	Text   *string `json:"text,omitempty"`
	Binary *string `json:"binary,omitempty"`
	Close  *int    `json:"close,omitempty"`
	Reason *string `json:"reason,omitempty"`
	End    bool    `json:"end,omitempty"`
}

func (e event) key() string {
	switch {
	case e.Head != nil:
		if strings.HasPrefix(*e.Head, "HTTP/1.1 101") {
			return "head " + wstest.HeadKey(*e.Head)
		}
		return "head " + *e.Head
	case e.Body != nil:
		return "body " + *e.Body
	case e.Text != nil:
		return "text " + *e.Text
	case e.Binary != nil:
		return "binary " + *e.Binary
	case e.Close != nil:
		return fmt.Sprintf("close %d %q", *e.Close, *e.Reason)
	case e.End:
		return "end"
	}
	return "?"
}

// player plays a scenario's steps on the Go service.
type player struct {
	t       *testing.T
	svc     *Service
	addr    string
	mu      sync.Mutex
	events  []event
	sockets map[string]*rawSocket
	vars    map[string]map[string]string
	world   *oracleWorld
}

var placeholder = regexp.MustCompile(`\{([a-z0-9_]+)\.([A-Za-z0-9_]+)\}`)

func (p *player) fill(s string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		parts := placeholder.FindStringSubmatch(m)
		return p.vars[parts[1]][parts[2]]
	})
}

func (p *player) note(e event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

// rawSocket is the oracle's client: a raw upgrade, then frames.
type rawSocket struct {
	name      string
	conn      net.Conn
	mu        sync.Mutex
	closeSent bool
}

func (p *player) open(name, path string, headers map[string]string) {
	conn, err := net.Dial("tcp", p.addr)
	if err != nil {
		p.t.Fatal(err)
	}
	s := &rawSocket{name: name, conn: conn}
	p.sockets[name] = s
	lines := []string{"GET " + path + " HTTP/1.1", "Host: console.test", "Upgrade: websocket", "Connection: Upgrade",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version: 13"}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+": "+headers[k])
	}
	_, _ = io.WriteString(conn, strings.Join(lines, "\r\n")+"\r\n\r\n")
	go p.read(s)
}

func (p *player) read(s *rawSocket) {
	br := bufio.NewReader(s.conn)
	defer func() { p.note(event{Socket: s.name, End: true}) }()
	var head bytes.Buffer
	for !bytes.HasSuffix(head.Bytes(), []byte("\r\n\r\n")) {
		c, err := br.ReadByte()
		if err != nil {
			return
		}
		head.WriteByte(c)
	}
	h := head.String()
	p.note(event{Socket: s.name, Head: &h})
	if !strings.HasPrefix(h, "HTTP/1.1 101") {
		rest, _ := io.ReadAll(br)
		if len(rest) > 0 {
			body := string(rest)
			p.note(event{Socket: s.name, Body: &body})
		}
		return
	}
	for {
		var hd [2]byte
		if _, err := io.ReadFull(br, hd[:]); err != nil {
			return
		}
		op, n := hd[0]&0x0f, uint64(hd[1]&0x7f)
		switch n {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(br, ext[:]); err != nil {
				return
			}
			n = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(br, ext[:]); err != nil {
				return
			}
			n = binary.BigEndian.Uint64(ext[:])
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}
		switch op {
		case 1:
			text := string(payload)
			p.note(event{Socket: s.name, Text: &text})
		case 2:
			b := base64.StdEncoding.EncodeToString(payload)
			p.note(event{Socket: s.name, Binary: &b})
		case 8:
			code, reason := 1005, ""
			if len(payload) >= 2 {
				code, reason = int(binary.BigEndian.Uint16(payload)), string(payload[2:])
			}
			p.note(event{Socket: s.name, Close: &code, Reason: &reason})
			s.mu.Lock()
			sent := s.closeSent
			s.mu.Unlock()
			if !sent {
				echo := []byte{}
				if len(payload) >= 2 {
					echo = payload[:2]
				}
				p.write(s, 8, echo)
			}
		}
	}
}

func (p *player) write(s *rawSocket, op byte, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mask := []byte{1, 2, 3, 4}
	var frame []byte
	n := len(payload)
	switch {
	case n < 126:
		frame = []byte{0x80 | op, 0x80 | byte(n)}
	case n <= 0xffff:
		frame = []byte{0x80 | op, 0x80 | 126, byte(n >> 8), byte(n)}
	default:
		frame = append([]byte{0x80 | op, 0x80 | 127}, binary.BigEndian.AppendUint64(nil, uint64(n))...)
	}
	frame = append(frame, mask...)
	start := len(frame)
	frame = append(frame, payload...)
	for i := range payload {
		frame[start+i] ^= mask[i%4]
	}
	if op == 8 {
		s.closeSent = true
	}
	_, _ = s.conn.Write(frame)
}

// quietFor waits until no event and no call came for n times 10 ms.
func (p *player) quietFor(n int) {
	last, still := -1, 0
	for still < n {
		time.Sleep(10 * time.Millisecond)
		p.mu.Lock()
		count := len(p.events)
		p.mu.Unlock()
		p.world.mu.Lock()
		count += len(p.world.calls)
		p.world.mu.Unlock()
		if count != last {
			last, still = count, 0
			continue
		}
		still++
	}
}

func (p *player) take() []event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.events
	p.events = nil
	return out
}

func rawString(r json.RawMessage) string {
	var s string
	_ = json.Unmarshal(r, &s)
	return s
}

// call runs a step's call on the service and returns its result as JSON.
func (p *player) call(st step) string {
	out := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			p.t.Fatal(err)
		}
		return string(b)
	}
	arg := func(i int) string { return p.fill(rawString(st.Args[i])) }
	switch st.Call {
	case "issue":
		token, expiresIn := p.svc.IssueRemoteAgentToken(arg(0))
		p.mu.Lock()
		p.vars[st.As] = map[string]string{"token": token}
		p.mu.Unlock()
		return out(map[string]any{"token": token, "expires_in": expiresIn})
	case "state":
		if state := p.svc.SessionState(arg(0)); state != nil {
			return out(state)
		}
		return "null"
	case "active":
		return out(p.svc.ActiveSessions())
	}
	p.t.Fatalf("unknown call %q", st.Call)
	return ""
}

// fix is a step of the recording that a bug fix changed on purpose: what
// the Go service does there since the fix, as the oracle would have noted
// it. events, when set, replaces what the sockets saw; result, when set,
// replaces what the step's call returned.
type fix struct {
	bug, scenario string
	step          int
	events        string
	result        string
}

// fixes are the steps the bug fixes changed. The tests of each fix pin
// what it does; here the steps say only what the replay sees instead.
var fixes = []fix{
	// A session without agent and viewer is gone: dev1's agent left
	// earlier in the scenario
	{bug: "RM8", scenario: "single-use token", step: 23, result: `["dev2","dev-3"]`},
	{bug: "RM8", scenario: "streaming", step: 45, result: `null`},
	{bug: "RM8", scenario: "streaming", step: 46, result: `[]`},
	{bug: "RM8", scenario: "viewer first", step: 29, result: `null`},
	{bug: "RM8", scenario: "viewer first", step: 30, result: `[]`},
	// Input reaches the agent only as one of its input kinds: these name
	// "key", none ("unknown"), 7, an object, false, "\ud800", "after_stop"
	// and "to_a2"
	{bug: "RM5", scenario: "streaming", step: 21, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 22, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 23, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 24, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 25, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 26, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 27, events: `[]`},
	{bug: "RM5", scenario: "streaming", step: 36, events: `[]`},
	{bug: "RM5", scenario: "viewer first", step: 9, events: `[]`},
}

// fixFor is the fix of a step, ok false for a step no fix changed.
func fixFor(scenario string, step int) (fix, bool) {
	for _, f := range fixes {
		if f.scenario == scenario && f.step == step {
			return f, true
		}
	}
	return fix{}, false
}

// apply returns what the step looks like since the fix. A fix that
// changes nothing is stale and fails the test.
func (f fix) apply(t *testing.T, result json.RawMessage, events []json.RawMessage) (json.RawMessage, []json.RawMessage) {
	t.Helper()
	changed := false
	if f.result != "" {
		changed = canonical(f.result) != canonical(string(result))
		result = json.RawMessage(f.result)
	}
	if f.events != "" {
		var fixed []json.RawMessage
		if err := json.Unmarshal([]byte(f.events), &fixed); err != nil {
			t.Fatalf("the %s fix of %q step %d: %v", f.bug, f.scenario, f.step, err)
		}
		if eventKeys(t, fixed) != eventKeys(t, events) {
			changed = true
		}
		events = fixed
	}
	if !changed {
		t.Errorf("the %s fix of %q step %d changes nothing", f.bug, f.scenario, f.step)
	}
	return result, events
}

// eventKeys is the events of a step per socket, for comparing.
func eventKeys(t *testing.T, events []json.RawMessage) string {
	t.Helper()
	per := map[string][]string{}
	for _, raw := range events {
		var e event
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		per[e.Socket] = append(per[e.Socket], e.key())
	}
	return fmt.Sprint(per)
}

// TestFixesNameRecordedSteps: every fix names a step of the recording.
func TestFixesNameRecordedSteps(t *testing.T) {
	rec := loadRecording(t)
	steps := map[string]int{}
	for _, sc := range rec.Scenarios {
		steps[sc.Name] = len(sc.Steps)
	}
	seen := map[string]bool{}
	for _, f := range fixes {
		if n, ok := steps[f.scenario]; !ok || f.step < 0 || f.step >= n {
			t.Errorf("the %s fix names step %d of %q, which the recording does not have", f.bug, f.step, f.scenario)
		}
		key := fmt.Sprintf("%s/%d", f.scenario, f.step)
		if seen[key] {
			t.Errorf("two fixes name step %d of %q", f.step, f.scenario)
		}
		seen[key] = true
	}
}

// canonical writes a JSON text with sorted keys, for comparing results.
func canonical(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func loadRecording(t testing.TB) *remoteRecording {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec remoteRecording
	if err := json.Unmarshal(wstest.Expand(raw), &rec); err != nil {
		t.Fatal(err)
	}
	return &rec
}

// TestNodeScenarios plays every scenario the Node oracle recorded on the
// Go service, with the same clock and random source, and compares each
// step: what every socket saw (in order per socket), what each call
// returned, and the outside calls the service made (the token check, the
// permission lookup, the device lookup, the scope check).
func TestNodeScenarios(t *testing.T) {
	rec := loadRecording(t)
	random := &xorshift{state: 0x2545f491}
	clock := &fakeClock{now: time.UnixMilli(rec.Start)}
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			clock.mu.Lock()
			clock.now = time.UnixMilli(sc.Clock)
			clock.timers = nil
			clock.mu.Unlock()
			world := &oracleWorld{}
			svc := New(world.options(clock, random))
			reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: world.permission})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			p := &player{t: t, svc: svc, addr: strings.TrimPrefix(srv.URL, "http://"), world: world,
				sockets: map[string]*rawSocket{}, vars: map[string]map[string]string{}}
			for i, s := range sc.Steps {
				st := s.Step
				result := ""
				if st.Open != "" {
					p.open(st.Open, p.fill(st.Path), st.Headers)
				}
				if st.Send != "" {
					sock := p.sockets[st.Send]
					switch {
					case st.Text != nil:
						p.write(sock, 1, []byte(p.fill(*st.Text)))
					case st.Binary != nil:
						b, _ := base64.StdEncoding.DecodeString(*st.Binary)
						p.write(sock, 2, b)
					case st.Repeat > 0:
						op := st.Opcode
						if op == 0 {
							op = 2
						}
						p.write(sock, op, bytes.Repeat([]byte{'x'}, st.Repeat))
					case st.Close != nil:
						p.write(sock, 8, []byte{byte(*st.Close >> 8), byte(*st.Close)})
					case st.Drop:
						sock.conn.Close()
					}
				}
				if st.Advance > 0 {
					p.quietFor(10)
					clock.Advance(time.Duration(st.Advance) * time.Millisecond)
				}
				if st.Call != "" {
					result = p.call(st)
				}
				if st.Repeat >= 1<<20 {
					// Megabytes take a while through the race detector.
					p.quietFor(100)
				} else {
					p.quietFor(10)
				}
				got := p.take()
				stepResult, events := s.Result, s.Events
				if f, ok := fixFor(sc.Name, i); ok {
					stepResult, events = f.apply(t, stepResult, events)
				}
				if st.Call != "" && canonical(result) != canonical(string(stepResult)) {
					t.Errorf("step %d %s: result\n node: %s\n   go: %s", i, st.Call, stepResult, result)
				}
				want := map[string][]string{}
				for _, raw := range events {
					var e event
					if err := json.Unmarshal(raw, &e); err != nil {
						t.Fatal(err)
					}
					want[e.Socket] = append(want[e.Socket], e.key())
				}
				have := map[string][]string{}
				for _, e := range got {
					have[e.Socket] = append(have[e.Socket], e.key())
				}
				if fmt.Sprint(want) != fmt.Sprint(have) {
					t.Errorf("step %d %+v:\n node: %q\n   go: %q", i, st, want, have)
				}
				var wantCalls []string
				for _, raw := range s.Calls {
					wantCalls = append(wantCalls, nodeCall(raw))
				}
				if gotCalls := world.take(); fmt.Sprint(wantCalls) != fmt.Sprint(gotCalls) {
					t.Errorf("step %d %+v calls:\n node: %q\n   go: %q", i, st, wantCalls, gotCalls)
				}
			}
			for _, s := range p.sockets {
				s.conn.Close()
			}
			p.quietFor(10)
			srv.Close()
			reg.Close()
			check()
		})
	}
}
