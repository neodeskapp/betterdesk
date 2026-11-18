package wsrelay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
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

// oraclePermission is the permission lookup of the oracle: admin and
// operator have device.connect, "throw" makes the lookup fail.
func oraclePermission(_ context.Context, role, permission string) (bool, error) {
	if role == "throw" {
		return false, fmt.Errorf("lookup failed")
	}
	return permission == "device.connect" && (role == "admin" || role == "operator"), nil
}

type relayRecording struct {
	Start     int64  `json:"start"`
	RelayHost string `json:"relayHost"`
	Hosts     []struct {
		Host   string          `json:"host"`
		Ifaces json.RawMessage `json:"ifaces"`
		Lines  []string        `json:"lines"`
	} `json:"hosts"`
	Scenarios []struct {
		Name         string `json:"name"`
		Differs      bool   `json:"differs"`
		SignalClosed bool   `json:"signalClosed"`
		Steps        []struct {
			Step   relayStep         `json:"step"`
			Result json.RawMessage   `json:"result"`
			Events []json.RawMessage `json:"events"`
		} `json:"steps"`
	} `json:"scenarios"`
}

type relayStep struct {
	Open    string                     `json:"open"`
	Path    string                     `json:"path"`
	Session json.RawMessage            `json:"session"`
	Headers map[string]json.RawMessage `json:"headers"`
	Send    string                     `json:"send"`
	TCP     string                     `json:"tcp"`
	Conn    int                        `json:"conn"`
	End     bool                       `json:"end"`
	Destroy bool                       `json:"destroy"`
	Hex     *string                    `json:"hex"`
	Text    *string                    `json:"text"`
	Repeat  int                        `json:"repeat"`
	Byte    *int                       `json:"byte"`
	Close   *int                       `json:"close"`
	Drop    bool                       `json:"drop"`
	Advance int64                      `json:"advance"`
	Call    string                     `json:"call"`
	Args    []string                   `json:"args"`
}

func (st relayStep) payload() []byte {
	switch {
	case st.Hex != nil:
		b, _ := hex.DecodeString(*st.Hex)
		return b
	case st.Repeat > 0:
		c := byte(0x78)
		if st.Byte != nil {
			c = byte(*st.Byte)
		}
		return bytes.Repeat([]byte{c}, st.Repeat)
	case st.Text != nil:
		return []byte(*st.Text)
	}
	return nil
}

// relayEvent is one thing the oracle noted.
type relayEvent struct {
	Socket string          `json:"socket"`
	Head   *string         `json:"head"`
	Body   *string         `json:"body"`
	Text   *string         `json:"text"`
	Binary json.RawMessage `json:"binary"`
	Close  *int            `json:"close"`
	Reason *string         `json:"reason"`
	End    bool            `json:"end"`

	TCP    string          `json:"tcp"`
	Accept bool            `json:"accept"`
	Data   json.RawMessage `json:"data"`
	Closed bool            `json:"closed"`
	// received holds the bytes of a data entry while its step runs.
	received []byte
	open     bool

	API    string          `json:"api"`
	Params string          `json:"params"`
	BodyJS string          `json:"-"`
	Answer json.RawMessage `json:"answer"`
}

// channel and key: events are compared in order per socket, per TCP
// connection and for the API calls.
func (e relayEvent) channel() string {
	switch {
	case e.Socket != "":
		return "socket " + e.Socket
	case e.TCP != "":
		return "tcp " + e.TCP
	}
	return "api"
}

func (e relayEvent) key() string {
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
		return "binary " + string(e.Binary)
	case e.Close != nil:
		return fmt.Sprintf("close %d %q", *e.Close, *e.Reason)
	case e.End:
		return "end"
	case e.Accept:
		return "accept"
	case e.Data != nil:
		return "data " + string(e.Data)
	case e.Closed:
		return "closed"
	case e.API != "":
		return e.API + " " + e.Params + e.BodyJS
	}
	return "?"
}

func parseEvent(t *testing.T, raw json.RawMessage) relayEvent {
	t.Helper()
	var e relayEvent
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	var extra struct {
		Body *string `json:"body"`
	}
	_ = json.Unmarshal(raw, &extra)
	if e.API != "" && extra.Body != nil {
		e.BodyJS, e.Body = *extra.Body, nil
	}
	if e.Data != nil {
		e.Data = json.RawMessage(compact(e.Data))
	}
	if e.Binary != nil {
		e.Binary = json.RawMessage(compact(e.Binary))
	}
	return e
}

// showBinary is a payload as the oracle shows it: base64 up to 4096 bytes,
// else its length and SHA-256.
func showBinary(b []byte) json.RawMessage {
	if len(b) > 4096 {
		sum := sha256.Sum256(b)
		return json.RawMessage(fmt.Sprintf(`{"length":%d,"sha256":"%s"}`, len(b), hex.EncodeToString(sum[:])))
	}
	out, _ := json.Marshal(base64.StdEncoding.EncodeToString(b))
	return out
}

// player plays a scenario on the Go service.
type player struct {
	t        *testing.T
	addr     string
	mu       sync.Mutex
	events   []relayEvent
	activity atomic.Int64
	sockets  map[string]*rawSocket
	servers  map[string]*fakeServer
	step     int
	// expected are the API calls of the current step, with their answers.
	expected []apiCall
}

type apiCall struct {
	key    string
	answer json.RawMessage
	used   bool
}

func (p *player) note(e relayEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

// serveAPI answers a call of the service with the answer the oracle's call
// got in the same step.
func (p *player) serveAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api")
	e := relayEvent{API: r.Method + " " + path}
	if r.Method == http.MethodGet {
		var parts []string
		for _, pair := range strings.Split(r.URL.RawQuery, "&") {
			k, v, _ := strings.Cut(pair, "=")
			key, _ := urlUnescape(k)
			value, _ := urlUnescape(v)
			kj, _ := json.Marshal(key)
			vj, _ := json.Marshal(value)
			parts = append(parts, string(kj)+":"+string(vj))
		}
		e.Params = "{" + strings.Join(parts, ",") + "}"
	} else {
		body, _ := io.ReadAll(r.Body)
		e.BodyJS = string(body)
	}
	p.note(e)
	p.mu.Lock()
	var answer json.RawMessage
	for i := range p.expected {
		if !p.expected[i].used && p.expected[i].key == e.key() {
			p.expected[i].used = true
			answer = p.expected[i].answer
			break
		}
	}
	p.mu.Unlock()
	if answer == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var a struct {
		Status int             `json:"status"`
		Data   json.RawMessage `json:"data"`
		Error  string          `json:"error"`
	}
	_ = json.Unmarshal(answer, &a)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.Status)
	if a.Error != "" {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": a.Error})
		return
	}
	_, _ = w.Write(a.Data)
}

func urlUnescape(s string) (string, error) {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := unhex(s[i+1])
			lo, ok2 := unhex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

// fakeServer is a signal or relay server that notes connections, the
// bytes it got (one entry per connection and step) and the ends.
type fakeServer struct {
	p     *player
	name  string
	ln    net.Listener
	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

func newFakeServer(t *testing.T, p *player, name, host string) *fakeServer {
	ln, err := net.Listen("tcp", host+":0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{p: p, name: name, ln: ln}
	s.wg.Add(1)
	go s.accept()
	return s
}

func (s *fakeServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeServer) accept() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		id := fmt.Sprintf("%s#%d", s.name, len(s.conns))
		s.mu.Unlock()
		s.p.note(relayEvent{TCP: id, Accept: true})
		s.wg.Add(1)
		go s.read(id, c)
	}
}

// read notes what a connection receives: a data entry per step, filled as
// bytes come.
func (s *fakeServer) read(id string, c net.Conn) {
	defer s.wg.Done()
	buf := make([]byte, 1<<20)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			s.p.activity.Add(int64(n))
			s.p.addData(id, buf[:n])
		}
		if err != nil {
			s.p.note(relayEvent{TCP: id, Closed: true})
			return
		}
	}
}

// addData adds bytes a connection got to its data entry of the current
// step, which the first bytes of the step make.
func (p *player) addData(id string, b []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.events) - 1; i >= 0; i-- {
		e := &p.events[i]
		if e.TCP == id && e.open {
			e.received = append(e.received, b...)
			return
		}
	}
	p.events = append(p.events, relayEvent{TCP: id, open: true, received: append([]byte{}, b...)})
}

func (s *fakeServer) conn(n int) net.Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == 0 {
		n = len(s.conns)
	}
	return s.conns[n-1]
}

func (s *fakeServer) close() {
	_ = s.ln.Close()
	s.mu.Lock()
	for _, c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

type rawSocket struct {
	name      string
	conn      net.Conn
	mu        sync.Mutex
	closeSent bool
}

func (p *player) open(name, path string, session json.RawMessage, headers map[string]json.RawMessage) {
	conn, err := net.Dial("tcp", p.addr)
	if err != nil {
		p.t.Fatal(err)
	}
	s := &rawSocket{name: name, conn: conn}
	p.sockets[name] = s
	type header struct{ name, value string }
	all := []header{{"Host", "console.test"}, {"Upgrade", "websocket"}, {"Connection", "Upgrade"},
		{"Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ=="}, {"Sec-WebSocket-Version", "13"}}
	set := func(name string, value *string) {
		for i := range all {
			if all[i].name == name {
				if value == nil {
					all = append(all[:i], all[i+1:]...)
				} else {
					all[i].value = *value
				}
				return
			}
		}
		if value != nil {
			all = append(all, header{name, *value})
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	for _, k := range sortedKeys(names) {
		var v *string
		_ = json.Unmarshal(headers[k], &v)
		set(k, v)
	}
	if session != nil {
		var b bytes.Buffer
		_ = json.Compact(&b, session)
		text := b.String()
		set("X-Oracle-Session", &text)
	}
	lines := []string{"GET " + path + " HTTP/1.1"}
	for _, h := range all {
		lines = append(lines, h.name+": "+h.value)
	}
	_, _ = io.WriteString(conn, strings.Join(lines, "\r\n")+"\r\n\r\n")
	go p.read(s)
}

func sortedKeys(keys []string) []string {
	out := append([]string(nil), keys...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (p *player) read(s *rawSocket) {
	br := bufio.NewReader(s.conn)
	defer func() { p.note(relayEvent{Socket: s.name, End: true}) }()
	var head bytes.Buffer
	for !bytes.HasSuffix(head.Bytes(), []byte("\r\n\r\n")) {
		c, err := br.ReadByte()
		if err != nil {
			return
		}
		head.WriteByte(c)
	}
	h := head.String()
	p.note(relayEvent{Socket: s.name, Head: &h})
	if !strings.HasPrefix(h, "HTTP/1.1 101") {
		rest, _ := io.ReadAll(br)
		if len(rest) > 0 {
			body := string(rest)
			p.note(relayEvent{Socket: s.name, Body: &body})
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
		p.activity.Add(int64(n))
		switch op {
		case 1:
			text := string(payload)
			p.note(relayEvent{Socket: s.name, Text: &text})
		case 2:
			p.note(relayEvent{Socket: s.name, Binary: showBinary(payload)})
		case 8:
			code, reason := 1005, ""
			if len(payload) >= 2 {
				code, reason = int(binary.BigEndian.Uint16(payload)), string(payload[2:])
			}
			p.note(relayEvent{Socket: s.name, Close: &code, Reason: &reason})
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
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, _ = s.conn.Write(frame)
}

// quiet waits until nothing happened for n times 10 ms.
func (p *player) quiet(n int) {
	last, still := int64(-1), 0
	for still < n {
		time.Sleep(10 * time.Millisecond)
		p.mu.Lock()
		now := int64(len(p.events)) + p.activity.Load()
		p.mu.Unlock()
		if now != last {
			last, still = now, 0
			continue
		}
		still++
	}
}

func (p *player) take() []relayEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.events
	p.events = nil
	for i := range out {
		if out[i].open {
			out[i].Data, out[i].open = showBinary(out[i].received), false
		}
	}
	return out
}

// TestNodeScenarios plays every scenario the Node oracle recorded on the
// Go service and compares each step: what every browser socket saw, what
// each fake server accepted, received and saw end, the API calls (in
// order), and what InvalidateGuestGrant returned.
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
		if sc.Differs {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			clock := &fakeClock{now: time.UnixMilli(rec.Start)}
			p := &player{t: t, sockets: map[string]*rawSocket{}}
			hbbs := newFakeServer(t, p, "hbbs", "127.0.0.1")
			hbbr := newFakeServer(t, p, "hbbr", rec.RelayHost)
			p.servers = map[string]*fakeServer{"hbbs": hbbs, "hbbr": hbbr}
			signalPort := hbbs.port()
			if sc.SignalClosed {
				hbbs.close()
				signalPort = closedPort(t)
			}
			svc := New(Options{
				SignalHost: "127.0.0.1", SignalPort: signalPort,
				RelayHost: "127.0.0.1", RelayPort: hbbr.port(),
				GoAPI:       goapi.New(http.HandlerFunc(p.serveAPI)),
				GuestCookie: "bd.guest",
				Clock:       clock,
				Interfaces: func() []InterfaceAddr {
					return []InterfaceAddr{{"IPv4", "127.0.0.1", true}, {"IPv4", rec.RelayHost, false}}
				},
			})
			reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: oraclePermission})
			svc.Register(reg)
			srv := httptest.NewServer(reg)
			p.addr = strings.TrimPrefix(srv.URL, "http://")
			for i, s := range sc.Steps {
				st := s.Step
				var want []relayEvent
				p.mu.Lock()
				p.expected = nil
				for _, raw := range s.Events {
					e := parseEvent(t, raw)
					want = append(want, e)
					if e.API != "" {
						p.expected = append(p.expected, apiCall{key: e.key(), answer: e.Answer})
					}
				}
				p.mu.Unlock()
				var result string
				switch {
				case st.Open != "":
					p.open(st.Open, st.Path, st.Session, st.Headers)
				case st.Send != "":
					sock := p.sockets[st.Send]
					switch {
					case st.Close != nil:
						p.write(sock, 8, []byte{byte(*st.Close >> 8), byte(*st.Close)})
					case st.Drop:
						_ = sock.conn.Close()
					case st.Text != nil:
						p.write(sock, 1, st.payload())
					default:
						p.write(sock, 2, st.payload())
					}
				case st.TCP != "":
					c := p.servers[st.TCP].conn(st.Conn)
					switch {
					case st.End:
						_ = c.(*net.TCPConn).CloseWrite()
					case st.Destroy:
						_ = c.(*net.TCPConn).SetLinger(0)
						_ = c.Close()
					default:
						_, _ = c.Write(st.payload())
					}
				case st.Advance > 0:
					p.quiet(8)
					clock.Advance(time.Duration(st.Advance) * time.Millisecond)
				case st.Call == "invalidate":
					result = fmt.Sprint(svc.InvalidateGuestGrant(st.Args[0]))
				}
				if st.Repeat > 1<<20 {
					// Megabytes take seconds through the race detector.
					p.quiet(800)
				} else {
					p.quiet(8)
				}
				got := p.take()
				if st.Call != "" && result != strings.TrimSpace(string(s.Result)) {
					t.Errorf("step %d %s: result node %s, go %s", i, st.Call, s.Result, result)
				}
				wantBy, gotBy := map[string][]string{}, map[string][]string{}
				for _, e := range want {
					wantBy[e.channel()] = append(wantBy[e.channel()], e.key())
				}
				for _, e := range got {
					gotBy[e.channel()] = append(gotBy[e.channel()], e.key())
				}
				// The calls of different bridges interleave as their answers
				// come: compared in any order.
				sort.Strings(wantBy["api"])
				sort.Strings(gotBy["api"])
				if fmt.Sprint(wantBy) != fmt.Sprint(gotBy) {
					t.Errorf("step %d %s:\n node: %q\n   go: %q", i, stepText(st), wantBy, gotBy)
				}
			}
			for _, s := range p.sockets {
				_ = s.conn.Close()
			}
			p.quiet(8)
			srv.Close()
			reg.Close()
			hbbs.close()
			hbbr.close()
			check()
		})
	}
}

func stepText(st relayStep) string {
	b, _ := json.Marshal(st)
	if len(b) > 300 {
		b = append(b[:300], "..."...)
	}
	return string(b)
}

// closedPort is a TCP port on 127.0.0.1 nothing listens on.
func closedPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestNodeRelayHost compares the relay address New picks with the one the
// old service logged for a loopback, named and empty relay host and the
// interfaces of the machine.
func TestNodeRelayHost(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_relay.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec relayRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Hosts {
		ifaces := interfacesOf(t, c.Ifaces)
		svc := New(Options{RelayHost: c.Host, RelayPort: 21117, Interfaces: func() []InterfaceAddr { return ifaces }})
		var want string
		for _, line := range c.Lines {
			if _, after, ok := strings.Cut(line, "/ws/relay -> "); ok {
				want = after[:strings.LastIndex(after, ":")]
			}
		}
		if svc.relayHost != want {
			t.Errorf("host %q: node %q, go %q", c.Host, want, svc.relayHost)
		}
	}
}

// interfacesOf reads os.networkInterfaces() as the oracle gave it, in
// order.
func interfacesOf(t *testing.T, raw json.RawMessage) []InterfaceAddr {
	t.Helper()
	v, err := goapi.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	obj := v.(*goapi.Object)
	var out []InterfaceAddr
	for _, name := range obj.Keys() {
		for _, item := range obj.Get(name).([]any) {
			a := item.(*goapi.Object)
			out = append(out, InterfaceAddr{Family: a.Str("family"), Address: a.Str("address"), Internal: a.Get("internal") == true})
		}
	}
	return out
}
