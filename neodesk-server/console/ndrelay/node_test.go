package ndrelay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws"
	"github.com/neodeskapp/neodesk/neodesk-server/console/ws/wstest"
)

// fakeClock is the clock of the Node oracle: time moves only when a test
// moves it, and due timers fire in the order of their time, then of their
// creation, in the goroutine that moves the clock. With panicNext set the
// next Now panics, as the oracle's Date.now throws after a throwNow step.
type fakeClock struct {
	mu        sync.Mutex
	now       time.Time
	seq       int
	timers    []*fakeTimer
	panicNext bool
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
	if c.panicNext {
		c.panicNext = false
		panic("clock failed")
	}
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

// oracleVerify is the token check of the oracle.
func oracleVerify(_ context.Context, deviceID, token string) (bool, error) {
	if token == "tok-throw" {
		return false, fmt.Errorf("database down")
	}
	return token == "tok-"+deviceID+"-good", nil
}

type relayRecording struct {
	Start     int64 `json:"start"`
	Scenarios []struct {
		Name  string `json:"name"`
		Clock int64  `json:"clock"`
		Steps []struct {
			Step   step              `json:"step"`
			Result json.RawMessage   `json:"result"`
			Events []json.RawMessage `json:"events"`
		} `json:"steps"`
	} `json:"scenarios"`
}

type step struct {
	Open    string            `json:"open"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Send    string            `json:"send"`
	Text    *string           `json:"text"`
	Binary  *string           `json:"binary"`
	Bytes   *string           `json:"bytes"`
	Repeat  int               `json:"repeat"`
	Opcode  byte              `json:"opcode"`
	Close   *int              `json:"close"`
	Drop    bool              `json:"drop"`
	Advance int64             `json:"advance"`
	Call    string            `json:"call"`
	Args    []json.RawMessage `json:"args"`
	As      string            `json:"as"`
	// ThrowNow makes the clock fail once, at the next read
	ThrowNow bool `json:"throwNow"`
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
		return "head " + headKey(*e.Head)
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

func headKey(head string) string {
	if strings.HasPrefix(head, "HTTP/1.1 101") {
		return wstest.HeadKey(head)
	}
	return head
}

// player plays a scenario's steps on the Go service.
type player struct {
	t       *testing.T
	svc     *Service
	clock   *fakeClock
	addr    string
	mu      sync.Mutex
	events  []event
	sockets map[string]*rawSocket
	vars    map[string]map[string]string
	reqs    map[string]*pendingCall
}

type pendingCall struct {
	done   chan struct{}
	result string
}

var placeholder = regexp.MustCompile(`\{([a-z0-9_]+)\.([A-Za-z0-9]+)\}`)

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
	if e.Text != nil {
		var msg struct {
			RequestID *string `json:"request_id"`
		}
		if json.Unmarshal([]byte(*e.Text), &msg) == nil && msg.RequestID != nil {
			req := p.vars["req"]
			req[fmt.Sprint(len(req))] = *msg.RequestID
		}
	}
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
			if !sent && len(payload) >= 2 {
				p.write(s, 8, payload[:2])
			} else if !sent {
				p.write(s, 8, nil)
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

// quiet waits until no event came for 100 ms.
func (p *player) quiet() { p.quietFor(10) }

// quietFor waits until no event came for n times 10 ms.
func (p *player) quietFor(n int) {
	last, still := -1, 0
	for still < n {
		time.Sleep(10 * time.Millisecond)
		p.mu.Lock()
		n := len(p.events)
		p.mu.Unlock()
		if n != last {
			last, still = n, 0
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

// call runs a step's call on the service and returns its result as the
// oracle wrote it.
func (p *player) call(st step) (string, bool) {
	arg := func(i int) string { return p.fill(rawString(st.Args[i])) }
	out := func(v any) string {
		text, err := stringify(v)
		if err != nil {
			p.t.Fatal(err)
		}
		return text
	}
	switch st.Call {
	case "create":
		id, it, tt := p.svc.CreateRelaySession(arg(0), arg(1))
		p.mu.Lock()
		p.vars[st.As] = map[string]string{"sessionId": id, "initiatorToken": it, "targetToken": tt}
		p.mu.Unlock()
		return out(goapi.NewObject("sessionId", id, "initiatorToken", it, "targetToken", tt)), true
	case "get":
		if info := p.svc.GetRelaySession(arg(0)); info != nil {
			return out(info), true
		}
		return "null", true
	case "delete":
		return out(p.svc.DeleteSession(arg(0))), true
	case "notify":
		payload, _ := goapi.Parse(st.Args[1])
		return out(p.svc.NotifyTarget(arg(0), payload)), true
	case "online":
		return out(p.svc.OnlineDeviceIDs()), true
	case "isOnline":
		online, known := p.svc.IsDeviceOnline(arg(0))
		if !known {
			return `"<undefined>"`, true
		}
		return out(online), true
	case "pending":
		return out(p.svc.PendingFor(arg(0))), true
	case "request":
		payload, _ := goapi.Parse(st.Args[2])
		var ms float64
		_ = json.Unmarshal(st.Args[3], &ms)
		pc := &pendingCall{done: make(chan struct{})}
		p.reqs[st.As] = pc
		device, typ := arg(0), arg(1)
		go func() {
			data, err := p.svc.RequestFromDevice(context.Background(), device, typ, payload, time.Duration(ms)*time.Millisecond)
			if err != nil {
				pc.result = out(goapi.NewObject("error", err.Error()))
			} else {
				pc.result = out(goapi.NewObject("data", data))
			}
			close(pc.done)
		}()
		return "", false
	case "await":
		pc := p.reqs[rawString(st.Args[0])]
		p.quiet()
		select {
		case <-pc.done:
			return pc.result, true
		default:
			return `{"pending":true}`, true
		}
	}
	p.t.Fatalf("unknown call %q", st.Call)
	return "", false
}

func compactJSON(raw string) string {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(raw)); err != nil {
		return raw
	}
	return b.String()
}

// TestNodeScenarios plays every scenario the Node oracle recorded on the
// Go service, with the same clock and random source, and compares each
// step: what every socket saw (in order per socket) and what each call
// returned.
func TestNodeScenarios(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_relay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec relayRecording
	if err := json.Unmarshal(wstest.Expand(raw), &rec); err != nil {
		t.Fatal(err)
	}
	random := &xorshift{state: 0x2545f491}
	clock := &fakeClock{now: time.UnixMilli(rec.Start)}
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			clock.mu.Lock()
			clock.now = time.UnixMilli(sc.Clock)
			clock.timers = nil
			clock.mu.Unlock()
			svc := New(Options{VerifyDevice: oracleVerify, Clock: clock, Random: random})
			reg := ws.New(ws.Options{})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			ctx, cancel := context.WithCancel(context.Background())
			ran := make(chan struct{})
			go func() { svc.Run(ctx); close(ran) }()
			p := &player{t: t, svc: svc, clock: clock, addr: strings.TrimPrefix(srv.URL, "http://"),
				sockets: map[string]*rawSocket{}, vars: map[string]map[string]string{"req": {}}, reqs: map[string]*pendingCall{}}
			for i, s := range sc.Steps {
				st := s.Step
				nodeResult, nodeEvents := s.Result, s.Events
				if f, ok := fixFor(sc.Name, i); ok {
					nodeResult, nodeEvents = f.apply(t, nodeResult, nodeEvents)
				}
				var result string
				hasResult := false
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
					case st.Bytes != nil:
						b, _ := base64.StdEncoding.DecodeString(*st.Bytes)
						p.write(sock, 1, b)
					case st.Repeat > 0:
						op := st.Opcode
						if op == 0 {
							op = 1
						}
						p.write(sock, op, bytes.Repeat([]byte{'x'}, st.Repeat))
					case st.Close != nil:
						p.write(sock, 8, []byte{byte(*st.Close >> 8), byte(*st.Close)})
					case st.Drop:
						sock.conn.Close()
					}
				}
				if st.ThrowNow {
					clock.mu.Lock()
					clock.panicNext = true
					clock.mu.Unlock()
				}
				if st.Advance > 0 {
					p.quiet()
					clock.Advance(time.Duration(st.Advance) * time.Millisecond)
				}
				if st.Call != "" {
					result, hasResult = p.call(st)
				}
				if st.Repeat >= 1<<20 {
					// Megabytes take a while through the race detector.
					p.quietFor(200)
				} else {
					p.quiet()
				}
				got := p.take()
				if hasResult && compactJSON(result) != compactJSON(string(nodeResult)) {
					t.Errorf("step %d %s: result\n node: %s\n   go: %s", i, st.Call, nodeResult, result)
				}
				want := map[string][]string{}
				for _, raw := range nodeEvents {
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
			}
			for _, s := range p.sockets {
				s.conn.Close()
			}
			p.quiet()
			cancel()
			<-ran
			srv.Close()
			reg.Close()
			for name, pc := range p.reqs {
				select {
				case <-pc.done:
				case <-time.After(5 * time.Second):
					t.Fatalf("request %s never settled", name)
				}
			}
			check()
		})
	}
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

// head101 is the answer to a handshake of the oracle's client, as a JSON
// string.
const head101 = `"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"`

// fixes are the steps the bug fixes changed. The tests of each fix pin
// what it does; here the steps say only what the replay sees instead.
var fixes = []fix{
	// The address is the connection's (no proxy is trusted here), so an
	// X-Forwarded-For of 10.0.0.2 buys no budget of its own
	{bug: "WS3", scenario: "per-address limit", step: 11, events: `[{"socket":"other","head":` + head101 + `},
		{"socket":"other","close":4429,"reason":"Too many connections"},{"socket":"other","end":true}]`},
	// The socket closed with 1009 is counted down once: after it, one more
	// socket fits, not two
	{bug: "WS4", scenario: "an error counts down twice", step: 12, events: `[{"socket":"x2","head":` + head101 + `},
		{"socket":"x2","close":4429,"reason":"Too many connections"},{"socket":"x2","end":true}]`},
	// The delete closes the side that joined; its own close later finds
	// the socket gone
	{bug: "WS6", scenario: "relay cleanup and delete", step: 4, events: `[{"socket":"i2","close":1000,"reason":"Session deleted"},{"socket":"i2","end":true}]`},
	{bug: "WS6", scenario: "relay cleanup and delete", step: 11, events: `[]`},
	// The request whose socket closed is answered at once
	{bug: "WS7", scenario: "requests", step: 45, result: `{"error":"agent_offline"}`},
	{bug: "WS7", scenario: "requests", step: 47, result: `{"error":"agent_offline"}`},
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
		changed = compactJSON(f.result) != compactJSON(string(result))
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
	raw, err := os.ReadFile(filepath.Join("testdata", "node_relay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec relayRecording
	if err := json.Unmarshal(wstest.Expand(raw), &rec); err != nil {
		t.Fatal(err)
	}
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
