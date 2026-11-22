package smtpwire

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// FuzzServer runs a whole call against a server made of the fuzz input. The
// input is cut into pieces at every 0xFF byte: the first piece is sent when
// the client has connected, each following piece when the client has written
// something, and after the last piece the server closes (or, by a bit of
// mode, goes silent, so that the client runs into its timeouts).
//
// The bits of mode: 1 Verify instead of Send, 2 credentials, 4 implicit TLS,
// 8 the server really starts TLS after a 2xx reply to STARTTLS, 16 silence
// instead of closing at the end, 32 the client checks the certificate,
// 64 the server's certificate is not trusted.
//
// Checked for every input:
//   - the call returns (no panic, no hang: every wait has a deadline);
//   - the result is a success or an *Error, and its text never holds the
//     password;
//   - a failed Send returns no info, a successful one at least one accepted
//     recipient;
//   - the rule for credentials: once the client has written STARTTLS, the
//     only thing it ever writes in plain text again is a TLS handshake.
//     Neither the login nor the message appear in plain text after that
//     line. (nodemailer's rule: STARTTLS is sent whenever the 2xx reply to
//     EHLO offers it on a connection that is not secure, and the session
//     ends when the upgrade is refused or fails. A server that does not
//     offer it gets the login in plain text.)
//
// Memory is bounded by the reply queue (TestReplySizeLimit and
// TestReplyQueueBound): the client holds at most about 1 MiB of replies plus
// one read of 64 KiB.

// fuzzCallLimit is how long a call may take. With timeouts of 40ms a call
// of 40 steps stays far below it.
const fuzzCallLimit = 10 * time.Second

const (
	fuzzUser     = "fuzz-user@example.test"
	fuzzPass     = "Fuzz-Password-9f3a7c21"
	fuzzBodyMark = "FUZZ-MESSAGE-BODY-5d81e0"
)

type fuzzServer struct {
	plain net.Conn
	mode  byte

	mu     sync.Mutex
	inTLS  bool
	clear  []byte // what the client wrote in plain text
	secure []byte // what the client wrote over TLS
	chunks chan struct{}
}

func (s *fuzzServer) add(data []byte) {
	s.mu.Lock()
	if s.inTLS {
		s.secure = append(s.secure, data...)
	} else {
		s.clear = append(s.clear, data...)
	}
	s.mu.Unlock()
	select {
	case s.chunks <- struct{}{}:
	default:
	}
}

// read drains what the client writes on conn until it is closed or stopped
// by a deadline.
func (s *fuzzServer) read(conn net.Conn, stopped chan<- struct{}) {
	defer close(stopped)
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			s.add(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

var fuzzCertificates sync.Map

// fuzzCertificate loads a server certificate once per process.
func fuzzCertificate(t testing.TB, name string) tls.Certificate {
	t.Helper()
	if cached, ok := fuzzCertificates.Load(name); ok {
		return cached.(tls.Certificate)
	}
	cert, err := tls.LoadX509KeyPair("testdata/certs/"+name+".chain.pem", "testdata/certs/"+name+".key")
	if err != nil {
		t.Fatal(err)
	}
	fuzzCertificates.Store(name, cert)
	return cert
}

// serve plays the pieces. It returns when the session is over on the
// server's side.
func (s *fuzzServer) serve(pieces [][]byte, cert tls.Certificate) {
	conn := s.plain
	// stopped is closed while no reader runs
	stopped := make(chan struct{})
	close(stopped)
	silent := false
	defer func() {
		// The client sees the end of the connection, and the server reads
		// what the client still writes until the client is gone.
		if !silent {
			_ = s.plain.(*memConn).CloseWrite()
		}
		<-stopped
		_ = s.plain.Close()
	}()
	startTLS := func() bool {
		secure := tls.Server(s.plain, &tls.Config{Certificates: []tls.Certificate{cert}})
		_ = s.plain.SetDeadline(time.Now().Add(2 * time.Second))
		err := secure.Handshake()
		_ = s.plain.SetDeadline(time.Time{})
		if err != nil {
			return false
		}
		s.mu.Lock()
		s.inTLS = true
		s.mu.Unlock()
		conn = secure
		return true
	}
	if s.mode&4 != 0 && !startTLS() {
		return
	}
	stopped = make(chan struct{})
	go s.read(conn, stopped)

	if _, err := conn.Write(pieces[0]); err != nil {
		return
	}
	for _, piece := range pieces[1:] {
		select {
		case <-s.chunks:
		case <-stopped:
			return
		}
		s.mu.Lock()
		upgrade := s.mode&8 != 0 && !s.inTLS && bytes.HasSuffix(s.clear, []byte("STARTTLS\r\n")) && len(piece) > 0 && piece[0] == '2' && bytes.HasSuffix(piece, []byte("\n"))
		s.mu.Unlock()
		if !upgrade {
			if _, err := conn.Write(piece); err != nil {
				return
			}
			continue
		}
		// The client waits for the reply and writes nothing until it has it,
		// so the reader can be stopped before the handshake takes over.
		_ = s.plain.SetReadDeadline(time.Now())
		<-stopped
		_ = s.plain.SetReadDeadline(time.Time{})
		if _, err := conn.Write(piece); err != nil || !startTLS() {
			return
		}
		stopped = make(chan struct{})
		go s.read(conn, stopped)
	}
	// silent: the client has to give up by itself
	silent = s.mode&16 != 0
}

func runFuzzSession(t *testing.T, mode byte, input []byte) {
	pieces := bytes.Split(input, []byte{0xFF})
	if len(pieces) > 40 {
		pieces = pieces[:40]
	}
	clientEnd, serverEnd := memPipe()
	certName := "localhost"
	if mode&64 != 0 {
		certName = "untrusted"
	}
	server := &fuzzServer{plain: serverEnd, mode: mode, chunks: make(chan struct{}, 1)}
	served := make(chan struct{})
	go func() {
		defer close(served)
		server.serve(pieces, fuzzCertificate(t, certName))
	}()

	opts := Options{
		Host:              "127.0.0.1",
		Port:              2525,
		Secure:            mode&4 != 0,
		TLSVerify:         mode&32 != 0,
		RootCAs:           trustedRoots(t),
		Hostname:          testHostname,
		ConnectionTimeout: 3 * time.Second,
		GreetingTimeout:   40 * time.Millisecond,
		SocketTimeout:     40 * time.Millisecond,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return clientEnd, nil
		},
	}
	if mode&2 != 0 {
		opts.User, opts.Pass = fuzzUser, fuzzPass
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	message := []byte("Subject: fuzz\r\n\r\n" + fuzzBodyMark + "\r\n.\r\n.leading dot\r\n")
	started := time.Now()
	var info *Info
	var err error
	if mode&1 != 0 {
		err = Verify(ctx, opts)
	} else {
		info, err = Send(ctx, opts, Envelope{From: "sender@example.test", To: []string{"one@example.test", "two@example.test"}}, message)
	}
	if took := time.Since(started); ctx.Err() != nil || took > fuzzCallLimit {
		t.Fatalf("the call took %v (mode %d)", took, mode)
	}
	// the session is over for the caller: the server side may go away
	_ = clientEnd.Close()
	select {
	case <-served:
	case <-time.After(20 * time.Second):
		t.Fatal("the server side of the pipe did not finish")
	}
	if took := time.Since(started); took > fuzzCallLimit {
		t.Fatalf("the session took %v until the server side was done (mode %d)", took, mode)
	}

	if err != nil {
		var failed *Error
		if !errors.As(err, &failed) {
			t.Fatalf("error %v (%T) is not an *Error", err, err)
		}
		if info != nil {
			t.Fatalf("a failed call returned info %+v", info)
		}
		for _, secret := range fuzzSecrets() {
			// a server that knows the password can put it into its reply
			if strings.Contains(err.Error()+failed.Response, secret) && !bytes.Contains(input, []byte(secret)) {
				t.Fatalf("the error holds the password: %q", err.Error())
			}
		}
	} else if mode&1 == 0 {
		if info == nil || len(info.Accepted) == 0 {
			t.Fatalf("a sent message without an accepted recipient: %+v", info)
		}
		server.mu.Lock()
		sent := bytes.Contains(server.clear, []byte(fuzzBodyMark)) || bytes.Contains(server.secure, []byte(fuzzBodyMark))
		server.mu.Unlock()
		if !sent {
			t.Fatalf("Send succeeded but the message was never written")
		}
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if at := bytes.Index(server.clear, []byte("STARTTLS\r\n")); at >= 0 {
		after := server.clear[at+len("STARTTLS\r\n"):]
		if len(after) > 0 && after[0] != 0x16 {
			t.Fatalf("plain text after STARTTLS: %q", after)
		}
		for _, secret := range append(fuzzSecrets(), fuzzBodyMark, "AUTH ", "MAIL FROM") {
			if bytes.Contains(after, []byte(secret)) {
				t.Fatalf("%q in plain text after STARTTLS: %q", secret, after)
			}
		}
	}
	if mode&4 != 0 && len(server.clear) > 0 && server.clear[0] != 0x16 {
		t.Fatalf("plain text on an implicit TLS connection: %q", server.clear)
	}
	if mode&2 == 0 {
		if bytes.Contains(server.clear, []byte("AUTH ")) || bytes.Contains(server.secure, []byte("AUTH ")) {
			t.Fatalf("a login without credentials")
		}
	}
}

// fuzzSecrets are the forms the password takes on the wire and as text.
func fuzzSecrets() []string {
	return []string{
		fuzzPass,
		base64.StdEncoding.EncodeToString([]byte(fuzzPass)),
		base64.StdEncoding.EncodeToString([]byte("\x00" + fuzzUser + "\x00" + fuzzPass)),
	}
}

func FuzzServer(f *testing.F) {
	join := func(pieces ...string) []byte { return []byte(strings.Join(pieces, "\xff")) }
	ok := "250 ok\r\n"
	seeds := [][]byte{
		join("220 hi\r\n", ok, ok, ok, ok, "354 go\r\n", "250 queued\r\n"),
		join("220 hi\r\n", "250-sink\r\n250-PIPELINING\r\n250 AUTH PLAIN LOGIN\r\n", "235 ok\r\n", "250 ok\r\n250 ok\r\n250 ok\r\n354 go\r\n", "250 queued\r\n"),
		join("220 hi\r\n", "250-sink\r\n250 STARTTLS\r\n", "220 go\r\n", "250-sink\r\n250 AUTH LOGIN\r\n", "334 VXNlcm5hbWU6\r\n", "334 UGFzc3dvcmQ6\r\n", "235 ok\r\n", ok, ok, ok, "354 go\r\n", "250 queued\r\n"),
		join("220 hi\r\n", "250-sink\r\n250 STARTTLS\r\n", "454 no\r\n"),
		join("220 hi\r\n", "250-sink\r\n250 AUTH CRAM-MD5\r\n", "334 PDE4OTYuNjk3MTcwOTUyQHNpbms+\r\n", "235 ok\r\n", ok, "550 no\r\n", "450 later\r\n", "354 go\r\n", ok),
		join("220 hi\r\n", "500 no\r\n", ok, "235 ok\r\n", ok, ok, ok, "354 go\r\n", "250 queued\r\n"),
		join("220-hi\r\n220 there\r\n", "250-sink\r\n250-SIZE 10\r\n250-8BITMIME\r\n250 SMTPUTF8\r\n", "421 bye\r\n"),
		join("554 no\r\n"),
		join("220 hi\r\n250 early\r\n250 early\r\n", "334 \r\n", "334 \r\n", "235 ok\r\n", "354 go\r\n250 early\r\n"),
		join("220 hi\r\n", "250-sink\r\n250-PIPELINING\r\n250 X", "550 no\r\n550 no\r\n550 no\r\n354 go\r\n", ok),
		join("220 hi\r\n", "250-sink\r\n250 STARTTLS\r\n", "220 go\r\n250-injected\r\n", "250 AUTH PLAIN\r\n", "235 ok\r\n"),
		join("", "220 hi", "\r\n", "250 o", "k\r\n"),
		[]byte(strings.Repeat("250-x\r\n", 2000)),
	}
	for _, seed := range seeds {
		for _, mode := range []byte{0, 1, 2, 3, 6, 7, 10, 11, 18, 26, 42, 46, 110} {
			f.Add(mode, seed)
		}
	}
	f.Fuzz(runFuzzSession)
}

// TestFuzzServerSeeds makes sure the harness itself reaches the interesting
// states: a message sent in plain text, after STARTTLS and over implicit
// TLS, and a login in each.
func TestFuzzServerSeeds(t *testing.T) {
	join := func(pieces ...string) []byte { return []byte(strings.Join(pieces, "\xff")) }
	ok := "250 ok\r\n"
	check := func(mode byte, input []byte, clearHas, secureHas string) {
		t.Helper()
		pieces := bytes.Split(input, []byte{0xFF})
		clientEnd, serverEnd := memPipe()
		server := &fuzzServer{plain: serverEnd, mode: mode, chunks: make(chan struct{}, 1)}
		served := make(chan struct{})
		go func() {
			defer close(served)
			server.serve(pieces, fuzzCertificate(t, "localhost"))
		}()
		opts := Options{
			Host: "127.0.0.1", Port: 2525, Secure: mode&4 != 0, Hostname: testHostname, User: fuzzUser, Pass: fuzzPass,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) { return clientEnd, nil },
		}
		info, err := Send(context.Background(), opts, Envelope{From: "sender@example.test", To: []string{"one@example.test"}}, []byte(fuzzBodyMark))
		if err != nil || info == nil {
			t.Fatalf("mode %d: %v", mode, err)
		}
		_ = clientEnd.Close()
		<-served
		server.mu.Lock()
		defer server.mu.Unlock()
		if clearHas != "" && !bytes.Contains(server.clear, []byte(clearHas)) {
			t.Errorf("mode %d: %q is not in the plain text %q", mode, clearHas, server.clear)
		}
		if secureHas != "" && (!bytes.Contains(server.secure, []byte(secureHas)) || bytes.Contains(server.clear, []byte(secureHas))) {
			t.Errorf("mode %d: %q is not only in the TLS part (plain %q, TLS %q)", mode, secureHas, server.clear, server.secure)
		}
	}
	login := "AUTH PLAIN " + fuzzSecrets()[2]
	check(2, join("220 hi\r\n", "250-sink\r\n250 AUTH PLAIN\r\n", "235 ok\r\n", ok, ok, "354 go\r\n", "250 queued\r\n"), login, "")
	check(2|8, join("220 hi\r\n", "250-sink\r\n250 STARTTLS\r\n", "220 go\r\n", "250-sink\r\n250 AUTH PLAIN\r\n", "235 ok\r\n", ok, ok, "354 go\r\n", "250 queued\r\n"), "STARTTLS\r\n", login)
	check(2|8, join("220 hi\r\n", "250-sink\r\n250 STARTTLS\r\n", "220 go\r\n", "250-sink\r\n250 AUTH PLAIN\r\n", "235 ok\r\n", ok, ok, "354 go\r\n", "250 queued\r\n"), "", fuzzBodyMark)
	check(2|4, join("220 hi\r\n", "250-sink\r\n250 AUTH PLAIN\r\n", "235 ok\r\n", ok, ok, "354 go\r\n", "250 queued\r\n"), "", login)
}

// FuzzDataStream checks the encoding of a message for DATA: it does not
// depend on how the message is cut into pieces, it has no line end other
// than CRLF, the only line with a single dot is its last one, and a receiver
// that removes the leading dot of every line gets the message back with its
// line ends normalized.
func FuzzDataStream(f *testing.F) {
	for _, seed := range []string{
		"", ".", "\r", "\n", "\r\n", ".\r\n", "\r\n.\r\n", "a\r\n.\r\nb", "\n.\n", "\r.\r", "\r.\n", "\n.\r", "..\r\n..", "a\rb\nc\r\nd",
		"Subject: x\r\n\r\nbody\r\n", "\r\r\n\n\r", ".\r.\n.\r\n.", "x\r\n.\r\nMAIL FROM:<evil@example.test>\r\n",
	} {
		f.Add([]byte(seed), uint16(1), uint16(3))
	}
	f.Fuzz(func(t *testing.T, message []byte, cutA, cutB uint16) {
		whole := newDataStream()
		encoded := whole.finish(whole.transform(nil, message))
		if whole.in != int64(len(message)) || whole.out != int64(len(encoded)) {
			t.Fatalf("counted %d in and %d out for %d and %d bytes", whole.in, whole.out, len(message), len(encoded))
		}

		// the same in three pieces
		a, b := 0, 0
		if len(message) > 0 {
			a = int(cutA) % (len(message) + 1)
			b = int(cutB) % (len(message) + 1)
		}
		if a > b {
			a, b = b, a
		}
		pieces := newDataStream()
		chunked := pieces.transform(nil, message[:a])
		chunked = pieces.transform(chunked, message[a:b])
		chunked = pieces.transform(chunked, message[b:])
		chunked = pieces.finish(chunked)
		if !bytes.Equal(chunked, encoded) {
			t.Fatalf("cut at %d and %d: %q, whole: %q", a, b, chunked, encoded)
		}

		// only CRLF line ends
		for i, c := range encoded {
			if c == '\r' && (i+1 >= len(encoded) || encoded[i+1] != '\n') {
				t.Fatalf("bare CR at %d in %q", i, encoded)
			}
			if c == '\n' && (i == 0 || encoded[i-1] != '\r') {
				t.Fatalf("bare LF at %d in %q", i, encoded)
			}
		}
		// the end line, and no dot line before it
		if !bytes.HasSuffix(encoded, []byte("\r\n.\r\n")) {
			t.Fatalf("no end of data in %q", encoded)
		}
		body := encoded[:len(encoded)-len(".\r\n")]
		if bytes.HasPrefix(encoded, []byte(".\r\n")) || bytes.Contains(body, []byte("\r\n.\r\n")) {
			t.Fatalf("an end of data inside the message: %q", encoded)
		}

		// what a receiver makes of it
		var received []byte
		for _, line := range bytes.SplitAfter(body, []byte("\r\n")) {
			if len(line) > 0 && line[0] == '.' {
				line = line[1:]
			}
			received = append(received, line...)
		}
		if want := normalizedLineEnds(message); !bytes.Equal(received, want) {
			t.Fatalf("message %q\nencoded  %q\nreceived %q\nwant     %q", message, encoded, received, want)
		}
	})
}

// normalizedLineEnds is the message a receiver should end up with: CRLF for
// every CRLF, lone CR and lone LF, and a line end after the last line.
func normalizedLineEnds(message []byte) []byte {
	text := strings.ReplaceAll(string(message), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(strings.ReplaceAll(text, "\n", "\r\n"))
}
