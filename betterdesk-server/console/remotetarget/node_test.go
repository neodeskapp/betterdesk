package remotetarget

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// The oracle's world: the targets the made-up API server knows, the
// account "scoped" sees rt_echo-0001 only, "scopethrow" makes the scope
// check fail, admin and operator have remote_target.connect, the role
// "throw" makes the lookup fail.
var knownTargets = map[string]bool{
	"rt_echo-0001": true, "rt_scoped-01": true, "rt_refuse-01": true, "rt_slow-0001": true, "rt_close1000": true,
	"rt_close1011": true, "rt_drop-0001": true, "rt_big-00001": true, "rt_hello-001": true, "rt_end-00001": true,
}

// world records the gateway's outside calls and the made-up API server's
// side of every tunnel, as the oracle did.
type world struct {
	mu     sync.Mutex
	calls  []string
	events []event
	seq    int
}

func (w *world) call(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, fmt.Sprintf(format, args...))
}

func (w *world) note(e event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, e)
}

func (w *world) take() ([]string, []event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	calls, events := w.calls, w.events
	w.calls, w.events = nil, nil
	return calls, events
}

func (w *world) permission(_ context.Context, role, permission string) (bool, error) {
	w.call("permission %q %q", role, permission)
	if role == "throw" {
		return false, errors.New("roles down")
	}
	return (role == "admin" || role == "operator") && permission == "remote_target.connect", nil
}

func (w *world) options() Options {
	return Options{
		RemoteTarget: func(_ context.Context, id string) *goapi.Object {
			w.call("getRemoteTarget %q", id)
			if !knownTargets[id] {
				return nil
			}
			return goapi.NewObject("id", id, "hostname", id, "remote_target", true)
		},
		CanAccess: func(_ context.Context, user *session.User, target *goapi.Object) (bool, error) {
			w.call("scope %s", target.Str("id"))
			name := ""
			if user != nil {
				name = user.Username
			}
			switch name {
			case "scopethrow":
				return false, errors.New("scope down")
			case "scoped":
				return target.Str("id") == "rt_echo-0001", nil
			}
			return true, nil
		},
		API: http.HandlerFunc(w.api),
		// The oracle had no session check; an hour keeps its lookups out of
		// the recorded calls (TestRemoteTargetEndsWithItsSession covers it)
		SessionCheck: time.Hour,
	}
}

// pickedHeaders are the tunnel handshake's headers the oracle noted.
var pickedHeaders = []string{"x-api-key", "x-username", "x-role", "origin", "sec-websocket-protocol", "sec-websocket-version", "cookie", "authorization"}

// api is the made-up API server of the oracle: the tunnel of a target
// echoes, closes, drops or refuses as gateway.js has it.
func (w *world) api(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	w.seq++
	name := fmt.Sprintf("up%d", w.seq)
	w.mu.Unlock()
	headers := map[string]string{}
	for _, h := range pickedHeaders {
		if v := r.Header.Values(h); len(v) > 0 {
			headers[h] = strings.Join(v, ", ")
		}
	}
	hs, _ := json.Marshal(map[string]any{"method": r.Method, "url": r.URL.RequestURI(), "headers": headers})
	text := string(hs)
	w.note(event{Socket: name, Handshake: &text})
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/remote-targets/"), "/tunnel")
	if strings.HasPrefix(id, "rt_refuse") {
		rw.Header().Set("Content-Type", "application/json")
		rw.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(rw, `{"error":"RDP/VNC gateway is not configured"}`)
		return
	}
	if strings.HasPrefix(id, "rt_slow") {
		time.Sleep(300 * time.Millisecond)
	}
	c, err := websocket.Accept(rw, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(64 << 20)
	ctx := context.Background()
	closed := func(code int, reason string) {
		w.note(event{Socket: name, Close: &code, Reason: &reason})
	}
	serverClose := func(code websocket.StatusCode, reason string) {
		if c.Close(code, reason) == nil {
			closed(int(code), reason)
		}
	}
	switch {
	case strings.HasPrefix(id, "rt_hello"):
		_ = c.Write(ctx, websocket.MessageText, []byte("hello"))
	case strings.HasPrefix(id, "rt_close1000"):
		serverClose(1000, "session ended")
		return
	case strings.HasPrefix(id, "rt_close1011"):
		serverClose(1011, "gateway unavailable")
		return
	case strings.HasPrefix(id, "rt_drop"):
		_ = c.CloseNow()
		closed(1006, "")
		return
	}
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			code := int(websocket.CloseStatus(err))
			reason := ""
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				reason = ce.Reason
			}
			if code == -1 {
				code = 1006
			}
			closed(code, reason)
			return
		}
		if typ == websocket.MessageBinary {
			b := summary(data, true)
			w.note(event{Socket: name, Binary: &b})
			_ = c.Write(ctx, websocket.MessageBinary, data)
			continue
		}
		t := summary(data, false)
		w.note(event{Socket: name, Text: &t})
		switch string(data) {
		case "close-1000":
			serverClose(1000, "session ended")
			return
		case "close-1011":
			serverClose(1011, "gateway unavailable")
			return
		case "drop":
			_ = c.CloseNow()
			closed(1006, "")
			return
		case "big":
			_ = c.Write(ctx, websocket.MessageBinary, bytes.Repeat([]byte{'y'}, MaxPayload+1))
		case "at-limit":
			_ = c.Write(ctx, websocket.MessageBinary, bytes.Repeat([]byte{'y'}, MaxPayload))
		default:
			_ = c.Write(ctx, websocket.MessageText, append([]byte("echo:"), data...))
		}
	}
}

// summary is a message as the oracle noted it: a long one by its length
// and SHA-256, a binary one in base64.
func summary(data []byte, binary bool) string {
	if len(data) > 4096 {
		sum := sha256.Sum256(data)
		return fmt.Sprintf("big %d %s", len(data), hex.EncodeToString(sum[:]))
	}
	if binary {
		return base64.StdEncoding.EncodeToString(data)
	}
	return string(data)
}

type recording struct {
	Scenarios []struct {
		Name  string `json:"name"`
		Steps []struct {
			Step   step              `json:"step"`
			Events []json.RawMessage `json:"events"`
			Calls  []json.RawMessage `json:"calls"`
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
	Repeat  int               `json:"repeat"`
	Opcode  byte              `json:"opcode"`
	Close   *int              `json:"close"`
	Drop    bool              `json:"drop"`
	Wait    int               `json:"wait"`
}

// event is one thing a browser socket or a tunnel saw.
type event struct {
	Socket    string          `json:"socket"`
	Head      *string         `json:"head,omitempty"`
	Body      *string         `json:"body,omitempty"`
	Handshake *string         `json:"-"`
	RawHS     json.RawMessage `json:"handshake,omitempty"`
	Text      *string         `json:"text,omitempty"`
	Binary    *string         `json:"binary,omitempty"`
	Close     *int            `json:"close,omitempty"`
	Reason    *string         `json:"reason,omitempty"`
	End       bool            `json:"end,omitempty"`
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
	case e.Handshake != nil:
		return "handshake " + canonical(*e.Handshake)
	case e.RawHS != nil:
		return "handshake " + canonical(string(e.RawHS))
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

// canonical is a tunnel handshake as compared: Node's had the API key in
// X-API-Key, which the in-process tunnel does not need (the handler
// carries the console's identity), so that header is left out.
func canonical(raw string) string {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	if hs, ok := v.(map[string]any); ok {
		if headers, ok := hs["headers"].(map[string]any); ok {
			delete(headers, "x-api-key")
		}
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func nodeCall(raw json.RawMessage) string {
	var c struct {
		GetRemoteTarget *string           `json:"getRemoteTarget"`
		Scope           []json.RawMessage `json:"scope"`
		Permission      []string          `json:"permission"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return "?"
	}
	switch {
	case c.GetRemoteTarget != nil:
		return fmt.Sprintf("getRemoteTarget %q", *c.GetRemoteTarget)
	case c.Scope != nil:
		var id string
		_ = json.Unmarshal(c.Scope[1], &id)
		return "scope " + id
	case c.Permission != nil:
		return fmt.Sprintf("permission %q %q", c.Permission[0], c.Permission[1])
	}
	return "?"
}

// browser is the oracle's raw websocket client.
type browser struct {
	name      string
	conn      net.Conn
	mu        sync.Mutex
	closeSent bool
}

type player struct {
	t       *testing.T
	addr    string
	w       *world
	mu      sync.Mutex
	events  []event
	sockets map[string]*browser
}

func (p *player) note(e event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

func (p *player) open(name, path string, headers map[string]string) {
	conn, err := net.Dial("tcp", p.addr)
	if err != nil {
		p.t.Fatal(err)
	}
	s := &browser{name: name, conn: conn}
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

func (p *player) read(s *browser) {
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
			text := summary(payload, false)
			p.note(event{Socket: s.name, Text: &text})
		case 2:
			b := summary(payload, true)
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

func (p *player) write(s *browser, op byte, payload []byte) {
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

// quietFor waits until nothing happened for n times 20 ms.
func (p *player) quietFor(n int) {
	last, still := -1, 0
	for still < n {
		time.Sleep(20 * time.Millisecond)
		p.mu.Lock()
		count := len(p.events)
		p.mu.Unlock()
		p.w.mu.Lock()
		count += len(p.w.events) + len(p.w.calls)
		p.w.mu.Unlock()
		if count != last {
			last, still = count, 0
			continue
		}
		still++
	}
}

// TestNodeScenarios plays every scenario of testdata/node_gateway.json on
// the Go gateway in front of a made-up API server that behaves as the
// oracle's, and compares each step: what every browser socket saw, what
// every tunnel saw (its handshake, messages and close), and the outside
// calls (permission, target, scope).
func TestNodeScenarios(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "node_gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	for _, sc := range rec.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			check := wstest.Goroutines(t)
			w := &world{}
			g := New(w.options())
			reg := ws.New(ws.Options{Session: wstest.OracleSession, Permission: w.permission})
			g.Register(reg)
			srv := httptest.NewServer(reg)
			p := &player{t: t, addr: strings.TrimPrefix(srv.URL, "http://"), w: w, sockets: map[string]*browser{}}
			for i, s := range sc.Steps {
				st := s.Step
				if st.Open != "" {
					p.open(st.Open, st.Path, st.Headers)
				}
				if st.Send != "" {
					sock := p.sockets[st.Send]
					switch {
					case st.Text != nil:
						p.write(sock, 1, []byte(*st.Text))
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
				wait := st.Wait
				if wait == 0 {
					wait = 15
				}
				if st.Repeat >= 1<<20 || st.Text != nil && (*st.Text == "big" || *st.Text == "at-limit") {
					// Megabytes take a while through the race detector
					wait = 500
				}
				p.quietFor(wait)
				p.mu.Lock()
				got := p.events
				p.events = nil
				p.mu.Unlock()
				calls, upstream := w.take()
				got = append(got, upstream...)
				want := map[string][]string{}
				for _, raw := range s.Events {
					var e event
					if err := json.Unmarshal(raw, &e); err != nil {
						t.Fatal(err)
					}
					if e.RawHS != nil && sc.Name == "checks" && st.Open == "m" {
						// A session whose user has no name: Node sent
						// user#<id>, the Go session reads the name at
						// the session's top (testdata/nodeoracle/README.md)
						e.RawHS = json.RawMessage(strings.Replace(string(e.RawHS), `"user#12"`, `"sso"`, 1))
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
				if fmt.Sprint(wantCalls) != fmt.Sprint(calls) {
					t.Errorf("step %d %+v calls:\n node: %q\n   go: %q", i, st, wantCalls, calls)
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
