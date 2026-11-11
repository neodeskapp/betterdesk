package smtpwire

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// The scripted SMTP server of the session tests. It plays the server side a
// recorded scenario describes and records what the client sends, exactly as
// testdata/nodeoracle/sink.js did when the scenario was recorded with
// nodemailer. Both read a script the same way; the format is described in
// sink.js.

type sinkScript struct {
	TLS      bool                  `json:"tls"`
	Cert     string                `json:"cert"`
	Greeting []sinkOp              `json:"greeting"`
	Rules    map[string][][]sinkOp `json:"rules"`
	Listen   *bool                 `json:"listen"`
}

type sinkOp struct {
	W    *string `json:"w"`
	Wrep *struct {
		S string `json:"s"`
		N int    `json:"n"`
	} `json:"wrep"`
	Sleep int    `json:"sleep"`
	TLS   int    `json:"tls"`
	Data  int    `json:"data"`
	Cont  string `json:"cont"`
	Close int    `json:"close"`
	Stall int    `json:"stall"`
}

type sinkEvent struct {
	E      string  `json:"e"`
	S      string  `json:"s,omitempty"`
	SNI    *string `json:"sni,omitempty"`
	B64    *string `json:"b64,omitempty"`
	Len    int     `json:"len,omitempty"`
	SHA256 string  `json:"sha256,omitempty"`
}

// latin1Bytes turns a string that holds bytes as characters back into bytes.
func latin1Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}

func latin1String(b []byte) string {
	var out strings.Builder
	for _, c := range b {
		out.WriteRune(rune(c))
	}
	return out.String()
}

type sink struct {
	listener net.Listener
	script   sinkScript

	mu        sync.Mutex
	events    []sinkEvent
	connected bool
	done      chan struct{}
}

// startSink listens on loopback and serves the first connection.
func startSink(t testing.TB, script sinkScript) *sink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &sink{listener: listener, script: script, done: make(chan struct{})}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.connected = true
		s.mu.Unlock()
		s.serve(conn)
		close(s.done)
	}()
	return s
}

func (s *sink) addr() string { return s.listener.Addr().String() }

func (s *sink) record(event sinkEvent) {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
}

// wait returns the recorded events once the client is gone. A client that
// never connected leaves none.
func (s *sink) wait(t testing.TB, limit time.Duration) []sinkEvent {
	t.Helper()
	s.mu.Lock()
	connected := s.connected
	s.mu.Unlock()
	if connected {
		select {
		case <-s.done:
		case <-time.After(limit):
			t.Errorf("the client was still connected after %v", limit)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkEvent(nil), s.events...)
}

type sinkSession struct {
	sink    *sink
	plain   net.Conn
	conn    net.Conn
	buf     []byte
	ended   bool
	mode    string
	cont    string
	stalled bool
	counts  map[string]int
}

func (s *sink) serve(conn net.Conn) {
	sess := &sinkSession{sink: s, plain: conn, conn: conn, mode: "line", counts: map[string]int{}}
	defer conn.Close()
	if s.script.TLS && !sess.startTLS() {
		s.record(sinkEvent{E: "eof"})
		return
	}
	greeting := s.script.Greeting
	if greeting == nil {
		text := "220 sink.example.test ESMTP\r\n"
		greeting = []sinkOp{{W: &text}}
	}
	sess.exec(greeting)
	for {
		if sess.mode == "data" {
			data, ok := sess.readData()
			if !ok {
				break
			}
			sess.mode = "line"
			if len(data) > 4096 {
				sum := sha256.Sum256(data)
				s.record(sinkEvent{E: "data", Len: len(data), SHA256: hex.EncodeToString(sum[:])})
			} else {
				encoded := base64.StdEncoding.EncodeToString(data)
				s.record(sinkEvent{E: "data", B64: &encoded})
			}
			if !sess.stalled {
				sess.exec(sess.ops("ENDDATA"))
			}
			continue
		}
		line, ok := sess.readLine()
		if !ok {
			break
		}
		text := latin1String(line)
		s.record(sinkEvent{E: "line", S: text})
		verb := sinkVerb(text)
		if sess.mode == "cont" {
			verb = sess.cont
			sess.mode = "line"
		}
		if !sess.stalled {
			sess.exec(sess.ops(verb))
		}
	}
	if len(sess.buf) > 0 {
		s.record(sinkEvent{E: "partial", S: latin1String(sess.buf)})
	}
	s.record(sinkEvent{E: "eof"})
}

func sinkVerb(text string) string {
	line := strings.TrimSuffix(strings.TrimSuffix(text, "\n"), "\r")
	switch line {
	case "":
		return "EMPTY"
	case ".":
		return "DOT"
	}
	words := strings.Split(strings.ReplaceAll(line, ":", " "), " ")
	first := strings.ToUpper(words[0])
	if first == "AUTH" {
		second := ""
		if len(words) > 1 {
			second = words[1]
		}
		return "AUTH " + strings.ToUpper(second)
	}
	return first
}

func (s *sinkSession) fill() bool {
	if s.ended {
		return false
	}
	chunk := make([]byte, 64*1024)
	n, err := s.conn.Read(chunk)
	s.buf = append(s.buf, chunk[:n]...)
	if err != nil {
		s.ended = true
	}
	return n > 0
}

func (s *sinkSession) readLine() ([]byte, bool) {
	for {
		if end := bytes.IndexByte(s.buf, '\n'); end >= 0 {
			line := append([]byte(nil), s.buf[:end+1]...)
			s.buf = s.buf[end+1:]
			return line, true
		}
		if !s.fill() && s.ended {
			return nil, false
		}
	}
}

func (s *sinkSession) readData() ([]byte, bool) {
	end := []byte("\r\n.\r\n")
	for {
		pos := -1
		if bytes.HasPrefix(s.buf, []byte(".\r\n")) {
			pos = 3
		} else if found := bytes.Index(s.buf, end); found >= 0 {
			pos = found + len(end)
		}
		if pos >= 0 {
			data := append([]byte(nil), s.buf[:pos]...)
			s.buf = s.buf[pos:]
			return data, true
		}
		if !s.fill() && s.ended {
			return nil, false
		}
	}
}

func (s *sinkSession) startTLS() bool {
	name := s.sink.script.Cert
	if name == "" {
		name = "localhost"
	}
	cert, err := tls.LoadX509KeyPair("testdata/certs/"+name+".chain.pem", "testdata/certs/"+name+".key")
	if err != nil {
		s.sink.record(sinkEvent{E: "sink-error", S: err.Error()})
		s.ended = true
		return false
	}
	secure := tls.Server(s.plain, &tls.Config{Certificates: []tls.Certificate{cert}})
	if err := secure.Handshake(); err != nil {
		s.sink.record(sinkEvent{E: "tlsfail"})
		s.ended = true
		return false
	}
	event := sinkEvent{E: "tls"}
	if sni := secure.ConnectionState().ServerName; sni != "" {
		event.SNI = &sni
	}
	s.sink.record(event)
	s.conn = secure
	s.buf = nil
	return true
}

func (s *sinkSession) exec(ops []sinkOp) {
	for _, op := range ops {
		switch {
		case op.W != nil:
			_, _ = s.conn.Write(latin1Bytes(*op.W))
		case op.Wrep != nil:
			_, _ = s.conn.Write(bytes.Repeat(latin1Bytes(op.Wrep.S), op.Wrep.N))
		case op.Sleep > 0:
			time.Sleep(time.Duration(op.Sleep) * time.Millisecond)
		case op.TLS != 0:
			if !s.startTLS() {
				return
			}
		case op.Data != 0:
			s.mode = "data"
		case op.Cont != "":
			s.mode = "cont"
			s.cont = op.Cont
		case op.Close != 0:
			if secure, ok := s.conn.(*tls.Conn); ok {
				_ = secure.CloseWrite()
			}
			if tcp, ok := s.plain.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
		case op.Stall != 0:
			s.stalled = true
		}
	}
}

func (s *sinkSession) ops(verb string) []sinkOp {
	rules := s.sink.script.Rules
	list, key := rules[verb], verb
	if list == nil && strings.HasPrefix(verb, "AUTH ") {
		list, key = rules["AUTH"], "AUTH"
	}
	if list == nil {
		text := "250 OK\r\n"
		return []sinkOp{{W: &text}}
	}
	n := s.counts[key]
	s.counts[key] = n + 1
	if n >= len(list) {
		n = len(list) - 1
	}
	return list[n]
}
