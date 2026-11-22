package wstest

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Event is one thing a Node oracle noted, as its JSON object: what a
// socket got ({"socket": "a", "text": "..."}), a call it saw
// ({"api": {...}}), the end of the process ({"crash": "..."}).
type Event map[string]any

// Player plays the steps of a Node recording against a Go service, the
// way the oracle scripts played them against Node: raw sockets opened
// with the bytes the oracle wrote, frames written by hand, and everything
// the sockets got noted as the oracle noted it.
type Player struct {
	t    testing.TB
	addr string

	mu      sync.Mutex
	events  []Event
	sockets map[string]*rawSocket
	readers sync.WaitGroup
}

type rawSocket struct {
	conn      net.Conn
	mu        sync.Mutex
	closeSent bool
}

// NewPlayer returns a player for the service listening on addr. A player
// made before the service listens takes its address with SetAddr.
func NewPlayer(t testing.TB, addr string) *Player {
	return &Player{t: t, addr: addr, sockets: map[string]*rawSocket{}}
}

// SetAddr sets the address sockets are opened at.
func (p *Player) SetAddr(addr string) { p.addr = addr }

// Note adds an event.
func (p *Player) Note(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

// Open opens socket name with a raw upgrade request: the request line,
// the oracle's fixed headers, then headers sorted by name. Each character
// of a header value below 256 is one byte, as the oracle wrote them.
func (p *Player) Open(name, method, target string, headers map[string]string) {
	conn, err := net.Dial("tcp", p.addr)
	if err != nil {
		p.t.Fatal(err)
	}
	s := &rawSocket{conn: conn}
	p.mu.Lock()
	p.sockets[name] = s
	p.mu.Unlock()
	if method == "" {
		method = "GET"
	}
	lines := []string{method + " " + target + " HTTP/1.1", "Host: console.test", "Upgrade: websocket", "Connection: Upgrade",
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==", "Sec-WebSocket-Version: 13"}
	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, k+": "+headers[k])
	}
	_, _ = conn.Write(Latin1(strings.Join(lines, "\r\n") + "\r\n\r\n"))
	p.readers.Add(1)
	go p.read(name, s)
}

// Latin1 is the bytes of s with one byte per character below 256, as
// Node's Buffer.from(s, "latin1") gave them.
func Latin1(s string) []byte {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return b
}

// FromLatin1 reads bytes as Latin-1, as Node read a header: each byte is
// the character of the same number.
func FromLatin1(b []byte) string {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func (p *Player) read(name string, s *rawSocket) {
	defer p.readers.Done()
	br := bufio.NewReader(s.conn)
	defer p.Note(Event{"socket": name, "end": true})
	var head bytes.Buffer
	for !bytes.HasSuffix(head.Bytes(), []byte("\r\n\r\n")) {
		c, err := br.ReadByte()
		if err != nil {
			return
		}
		head.WriteByte(c)
	}
	h := FromLatin1(head.Bytes())
	p.Note(Event{"socket": name, "head": h})
	if !strings.HasPrefix(h, "HTTP/1.1 101") {
		rest, _ := io.ReadAll(br)
		if len(rest) > 0 {
			p.Note(Event{"socket": name, "body": FromLatin1(rest)})
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
			p.Note(Event{"socket": name, "text": string(payload)})
		case 2:
			p.Note(Event{"socket": name, "binary": base64.StdEncoding.EncodeToString(payload)})
		case 8:
			code, reason := 1005, ""
			if len(payload) >= 2 {
				code, reason = int(binary.BigEndian.Uint16(payload)), string(payload[2:])
			}
			p.Note(Event{"socket": name, "close": float64(code), "reason": reason})
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

func (p *Player) socket(name string) *rawSocket {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sockets[name]
	if s == nil {
		p.t.Fatalf("no socket %q", name)
	}
	return s
}

// Write sends one masked frame on socket name.
func (p *Player) Write(name string, op byte, payload []byte) {
	p.write(p.socket(name), op, payload)
}

func (p *Player) write(s *rawSocket, op byte, payload []byte) {
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

// Drop ends socket name's connection without a close frame.
func (p *Player) Drop(name string) {
	_ = p.socket(name).conn.Close()
}

// Quiet waits until no event came for n times 10 ms.
func (p *Player) Quiet(n int) {
	last, still := -1, 0
	for still < n {
		time.Sleep(10 * time.Millisecond)
		p.mu.Lock()
		count := len(p.events)
		p.mu.Unlock()
		if count != last {
			last, still = count, 0
			continue
		}
		still++
	}
}

// Take returns the events so far and forgets them.
func (p *Player) Take() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.events
	p.events = nil
	return out
}

// CloseAll ends every socket's connection and waits for its reader.
func (p *Player) CloseAll() {
	p.mu.Lock()
	for _, s := range p.sockets {
		_ = s.conn.Close()
	}
	p.mu.Unlock()
	p.readers.Wait()
}

// Step is a step of a recording as the oracles wrote it: the step itself
// and what was noted during it.
type Step struct {
	Step   json.RawMessage   `json:"step"`
	Events []json.RawMessage `json:"events"`
}

// Streams sorts events by where they happened (a socket, an upstream
// socket, a kind of call) and writes each as text that compares: keys
// sorted, a 101 answer's head as HeadKey.
func Streams(events []Event) map[string][]string {
	out := map[string][]string{}
	for _, e := range events {
		stream := streamOf(e)
		e2 := Event{}
		for k, v := range e {
			e2[k] = v
		}
		if head, ok := e2["head"].(string); ok && strings.HasPrefix(head, "HTTP/1.1 101") {
			e2["head"] = HeadKey(head)
		}
		text, err := json.Marshal(e2)
		if err != nil {
			text = []byte(fmt.Sprint(e2))
		}
		out[stream] = append(out[stream], string(text))
	}
	return out
}

func streamOf(e Event) string {
	for _, k := range []string{"socket", "upstream"} {
		if v, ok := e[k].(string); ok {
			return k + " " + v
		}
	}
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// RecordedEvents reads the events of a recorded step.
func RecordedEvents(t testing.TB, raw []json.RawMessage) []Event {
	t.Helper()
	out := make([]Event, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// SameStreams reports the streams that differ between Node's events and
// the Go service's, as lines for an error message; nil when they match.
func SameStreams(node, got []Event) []string {
	want, have := Streams(node), Streams(got)
	names := map[string]bool{}
	for k := range want {
		names[k] = true
	}
	for k := range have {
		names[k] = true
	}
	sorted := make([]string, 0, len(names))
	for k := range names {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	var diffs []string
	for _, k := range sorted {
		if strings.Join(want[k], "\n") != strings.Join(have[k], "\n") {
			diffs = append(diffs, fmt.Sprintf("%s:\n   node: %q\n     go: %q", k, want[k], have[k]))
		}
	}
	return diffs
}
