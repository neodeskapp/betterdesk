package netmonitor

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// probeVectors is testdata/probes.json (testdata/nodeoracle/probes.js):
// what the checks of the Node console answered against the script's own
// servers and its own ping command.
type probeVectors struct {
	DNS   map[string]json.RawMessage `json:"dns"`
	Pings map[string]struct {
		Stdout string `json:"stdout"`
		Code   int    `json:"code"`
		Sleep  int    `json:"sleep"`
	} `json:"pings"`
	Records []probeRecord       `json:"records"`
	DueNow  int64               `json:"due_now"`
	Due     [][]json.RawMessage `json:"due"`
	Checks  []struct {
		Target   map[string]json.RawMessage `json:"target"`
		Result   map[string]json.RawMessage `json:"result"`
		Keys     []string                   `json:"keys"`
		Calls    []map[string]json.RawMessage
		PingArgs []string `json:"ping_args"`
	} `json:"checks"`
}

type probeRecord struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Args       []json.RawMessage `json:"args"`
	SelfSigned bool              `json:"self_signed"`
	Inject     *string           `json:"inject"`
	Never      bool              `json:"never"`
	Thrown     *string           `json:"thrown"`
	Result     json.RawMessage   `json:"result"`
	Requests   []struct {
		Method  string   `json:"method"`
		URL     string   `json:"url"`
		Headers []string `json:"headers"`
	} `json:"requests"`
	Lookups  []string `json:"lookups"`
	PingArgs []string `json:"ping_args"`
}

// The servers of the oracle script listened on 0.0.0.0, which the guard
// refuses since BUGS.md SC3. The replay moves them to standIn, a private
// address the guard lets through, and the dialers of the tests connect
// that address to the local servers. Only the local address Node names in
// an error ("Local (0.0.0.0:0)") stays.
const standIn = "10.9.9.9"

var thisNetwork = regexp.MustCompile(`(^|[^0-9.])0\.0\.0\.0([^0-9.]|$)`)

// moveToStandIn replaces 0.0.0.0 in a text of the vectors by standIn.
func moveToStandIn(text string) string {
	for range 2 {
		// Twice, because two addresses in a row share the character between them
		text = thisNetwork.ReplaceAllString(text, "${1}"+standIn+"${2}")
	}
	return strings.ReplaceAll(text, "Local ("+standIn+":0)", "Local (0.0.0.0:0)")
}

// local is where a dialer of the tests connects for address: the stand-in
// is the local servers.
func local(address string) string {
	if host, port, err := net.SplitHostPort(address); err == nil && host == standIn {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return address
}

func loadProbeVectors(t *testing.T) *probeVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/probes.json")
	if err != nil {
		t.Fatal(err)
	}
	var v probeVectors
	if err := json.Unmarshal([]byte(moveToStandIn(string(raw))), &v); err != nil {
		t.Fatal(err)
	}
	return &v
}

// seenRequest is a request one of the test servers got.
type seenRequest struct {
	method, uri, host string
	headers           http.Header
}

// world is what the checks of a test can reach: the servers of the oracle
// script at standIn, a table of names, and a ping command that prints what
// the script's did.
type world struct {
	t        *testing.T
	v        *probeVectors
	ports    map[string]int
	resolver *tableResolver
	roots    *x509.CertPool

	mu       sync.Mutex
	requests []seenRequest
	pingArgs []string
	inject   syscall.Errno
}

func (w *world) handle(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	w.requests = append(w.requests, seenRequest{method: r.Method, uri: r.RequestURI, host: r.Host, headers: r.Header.Clone()})
	w.mu.Unlock()
	path, _, _ := strings.Cut(r.RequestURI, "?")
	if rest, ok := strings.CutPrefix(path, "/status/"); ok {
		status, _ := strconv.Atoi(rest)
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(status)
		if status != 204 && status != 304 {
			_, _ = rw.Write([]byte("status"))
		}
		return
	}
	hijack := func() net.Conn {
		conn, _, err := rw.(http.Hijacker).Hijack()
		if err != nil {
			w.t.Error(err)
		}
		return conn
	}
	switch path {
	case "/ok":
		rw.Header().Set("Content-Type", "text/plain")
		_, _ = rw.Write([]byte("ok"))
	case "/redirect":
		rw.Header().Set("Location", "/ok")
		rw.WriteHeader(http.StatusFound)
	case "/big":
		_, _ = rw.Write(make([]byte, 1<<20))
	case "/trickle":
		for i := 0; i < 6; i++ {
			time.Sleep(100 * time.Millisecond)
			_, _ = rw.Write([]byte("part\n"))
			rw.(http.Flusher).Flush()
		}
	case "/slow-headers":
		<-r.Context().Done()
	case "/slow-body":
		_, _ = rw.Write([]byte("part"))
		rw.(http.Flusher).Flush()
		<-r.Context().Done()
	case "/hangup":
		_ = hijack().Close()
	case "/reset-body":
		conn := hijack()
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: 100\r\n\r\npart"))
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close()
	default:
		http.Error(rw, "not found", http.StatusNotFound)
	}
}

// certificate makes a key and a certificate for the names, signed by
// parent, or by itself when parent is nil.
func certificate(t *testing.T, cn string, names []string, ips []net.IP, isCA bool, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  isCA,
		DNSNames:              names,
		IPAddresses:           ips,
	}
	signer, signerKey := template, key
	if parent != nil {
		signer, signerKey = parent, parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, template, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func newWorld(t *testing.T) *world {
	t.Helper()
	v := loadProbeVectors(t)
	w := &world{t: t, v: v, ports: map[string]int{}, resolver: newTableResolver(t, v.DNS), roots: x509.NewCertPool()}
	listen := func(name string) net.Listener {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		w.ports[name] = l.Addr().(*net.TCPAddr).Port
		t.Cleanup(func() { _ = l.Close() })
		return l
	}
	serve := func(l net.Listener) {
		server := &http.Server{Handler: http.HandlerFunc(w.handle), ErrorLog: log.New(io.Discard, "", 0)}
		go func() { _ = server.Serve(l) }()
		t.Cleanup(func() { _ = server.Close() })
	}
	serve(listen("http"))

	names := []string{"oracle.test", "*.oracle.test"}
	_, _, selfSignedCert := certificate(t, "oracle.test", names, []net.IP{net.ParseIP("192.0.2.1")}, false, nil, nil)
	serve(tls.NewListener(listen("tls"), &tls.Config{Certificates: []tls.Certificate{selfSignedCert}}))

	ca, caKey, _ := certificate(t, "Oracle CA", nil, nil, true, nil, nil)
	w.roots.AddCert(ca)
	_, _, signed := certificate(t, "oracle.test", []string{"zero.test", "*.oracle.test"}, []net.IP{net.ParseIP("192.0.2.1")}, false, ca, caKey)
	serve(tls.NewListener(listen("tlsca"), &tls.Config{Certificates: []tls.Certificate{signed}}))

	accept := func(l net.Listener, each func(net.Conn)) {
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				each(conn)
			}
		}()
	}
	accept(listen("garbage"), func(conn net.Conn) {
		// Take the request first: closing with unread data would reset
		// the connection, and the client might see that before the answer
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Read(make([]byte, 4096))
		_, _ = conn.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
		_ = conn.Close()
	})
	accept(listen("silent"), func(conn net.Conn) { t.Cleanup(func() { _ = conn.Close() }) })
	closed := listen("closed")
	_ = closed.Close()
	return w
}

// DialContext implements Dialer. It fails with the system error a record
// names, for the addresses that only a machine without a network refuses
// at once.
func (w *world) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	w.mu.Lock()
	inject := w.inject
	w.mu.Unlock()
	if inject != 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", inject)}
	}
	var d net.Dialer
	return d.DialContext(ctx, network, local(address))
}

// ping implements PingRunner with the table of the oracle script.
func (w *world) ping(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	if name != "ping" {
		w.t.Errorf("command %q, want ping", name)
	}
	// A process that is stopped a millisecond after its start has not run
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 5*time.Millisecond {
		<-ctx.Done()
		return nil, -1, nil
	}
	w.mu.Lock()
	w.pingArgs = append(w.pingArgs, strings.Join(args, " "))
	w.mu.Unlock()
	p, ok := w.v.Pings[args[len(args)-1]]
	if !ok && ssrfguard.IsIP(args[len(args)-1]) != 0 {
		// The ping of a name goes to the address the guard checked (SC4);
		// the script's command knew names, so this one answers for the
		// name the guard looked up
		w.resolver.mu.Lock()
		if n := len(w.resolver.lookups); n > 0 {
			p, ok = w.v.Pings[w.resolver.lookups[n-1]]
		}
		w.resolver.mu.Unlock()
	}
	if !ok {
		return nil, 2, nil
	}
	if p.Sleep > 0 {
		select {
		case <-ctx.Done():
			return nil, -1, nil
		case <-time.After(time.Duration(p.Sleep) * time.Second):
		}
	}
	return []byte(p.Stdout), p.Code, nil
}

func (w *world) prober(allowSelfSigned bool) *prober {
	no := false
	return w.proberFor(allowSelfSigned, &no)
}

func (w *world) proberFor(allowSelfSigned bool, windows *bool) *prober {
	transport := newTransport(&connector{dialer: w, roots: w.roots, allowSelfSigned: allowSelfSigned, now: time.Now})
	w.t.Cleanup(transport.CloseIdleConnections)
	return &prober{
		guard:     newGuard(w.resolver),
		dialer:    w,
		transport: transport,
		ping:      w.ping,
		windows:   *windows,
		now:       time.Now,
	}
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// fill puts the ports of this run into a text of the vectors.
func (w *world) fill(s string) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		if port, ok := w.ports[m[1:len(m)-1]]; ok {
			return strconv.Itoa(port)
		}
		return m
	})
}

// mask is fill the other way round.
func (w *world) mask(s string) string {
	for name, port := range w.ports {
		s = strings.ReplaceAll(s, strconv.Itoa(port), "{"+name+"}")
	}
	return s
}

// number reads an argument of the vectors: a number, a number as text
// (a port), or "Infinity" and "NaN".
func (w *world) number(t *testing.T, raw json.RawMessage) Number {
	t.Helper()
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return Number(f)
	}
	text := w.fill(decode[string](t, raw))
	switch text {
	case "Infinity":
		return Number(math.Inf(1))
	case "NaN":
		return NaN()
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("argument %s is no number", raw)
	}
	return Number(f)
}

// resultJSON writes a result as the JSON the routes send, with the round
// trip time as "number" and ports as their names.
func (w *world) resultJSON(res Result) string {
	fields := []string{fmt.Sprintf(`"success":%v`, res.Success)}
	if res.RTT != nil {
		fields = append(fields, `"rtt_ms":"number"`)
	} else {
		fields = append(fields, `"rtt_ms":null`)
	}
	if res.HTTP {
		if res.StatusCode != nil {
			fields = append(fields, fmt.Sprintf(`"status_code":%d`, *res.StatusCode))
		} else {
			fields = append(fields, `"status_code":null`)
		}
	}
	if res.Error != nil {
		fields = append(fields, `"error":`+quote(w.mask(*res.Error)))
	} else {
		fields = append(fields, `"error":null`)
	}
	return "{" + strings.Join(fields, ",") + "}"
}

// quote writes s as a JSON string without escaping "<" and ">".
func quote(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(b.String())
}

// nobody is a resolver that knows no name.
type nobody struct{}

func (nobody) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// namesOnly leaves out what no resolver is asked for: addresses, and the
// numbers the system reads as one.
func namesOnly(lookups []string) []string {
	names := []string{}
	for _, host := range lookups {
		if _, err := ssrfguard.ResolverLookup(nobody{})(context.Background(), host); err != nil && ssrfguard.IsIP(host) == 0 && !strings.HasPrefix(host, "[") {
			names = append(names, host)
		}
	}
	return names
}

// once is the lookups of Node with a name asked again right after itself
// left out: Node looked a name up again to connect or ping, the checks
// here go to the addresses the guard checked (BUGS.md SC4).
func once(names []string) []string {
	out := []string{}
	for i, name := range names {
		if i == 0 || name != names[i-1] {
			out = append(out, name)
		}
	}
	return out
}

// atAddress is the arguments Node gave the ping command with the host, a
// name, replaced by its first address: the ping goes to the address the
// guard checked (BUGS.md SC4).
func (w *world) atAddress(pings []string) []string {
	if pings == nil {
		return nil
	}
	out := make([]string, len(pings))
	for i, line := range pings {
		fields := strings.Fields(line)
		if addresses := w.resolver.names[strings.ToLower(fields[len(fields)-1])]; len(addresses) > 0 {
			fields[len(fields)-1] = addresses[0]
		}
		out[i] = strings.Join(fields, " ")
	}
	return out
}

// Where the Go checks answer something else than Node's, and why.
var probeDifferences = map[string]string{
	// Node never answers: the request has no listener for a connection
	// that breaks inside the body (BUGS.md NM4).
	"http connection closed in the body": `{"success":false,"rtt_ms":null,"status_code":null,"error":"aborted"}`,
	// Node's message goes on with a line of its TLS library's source.
	"https to a plain server": `{"success":false,"rtt_ms":null,"status_code":null,"error":"write EPROTO"}`,
	// Go's TLS server answers a request without TLS with status 400,
	// Node's closes the connection. The check itself does the same.
	"http to a TLS server": `{"success":false,"rtt_ms":"number","status_code":400,"error":null}`,
	// SC3: :: and 0.0.0.0/8 reach the server itself, and the guard refuses
	// them. Node looked :: up as a name (NM5), and 256 is 0.0.1.0.
	"http v6 any address": `{"success":false,"rtt_ms":null,"status_code":null,"error":"Blocked IP address: ::"}`,
	"resolve 256":         "thrown: Hostname 256 resolves to blocked address 0.0.1.0",
	// SC4: the ping goes to the address the guard checked for the name, so
	// the command no longer sees the name in capitals, which the script's
	// command did not know
	"ping in capitals": `{"success":true,"rtt_ms":"number","error":null}`,
	// NM5: the check connects to an IPv6 address the guard passed; Node
	// looked it up as a name, brackets included
	"http v6 address": `{"success":false,"rtt_ms":null,"status_code":null,"error":"connect ENETUNREACH 2001:db8::1:8080 - Local (:::0)"}`,
	// NM9: when every address of a name fails, the message is the first one's
	"http name with a v6 and a v4 address": `{"success":false,"rtt_ms":null,"status_code":null,"error":"connect ENETUNREACH 2001:db8::1:8080 - Local (:::0)"}`,
	// Go's server refuses a request line with a "%" that starts no escape
	// before the handler sees it; Node's server passes it on. The check
	// sends the same line to both.
	"http path with percent signs": `{"success":false,"rtt_ms":"number","status_code":400,"error":null}`,
}

func TestNodeProbes(t *testing.T) {
	w := newWorld(t)
	probers := map[bool]*prober{false: w.prober(false), true: w.prober(true)}
	if len(w.v.Records) < 160 {
		t.Fatalf("only %d records", len(w.v.Records))
	}
	for _, rec := range w.v.Records {
		w.mu.Lock()
		w.requests, w.pingArgs, w.inject = nil, nil, 0
		if rec.Inject != nil {
			w.inject = map[string]syscall.Errno{"ENETUNREACH": syscall.ENETUNREACH}[*rec.Inject]
		}
		w.resolver.lookups = nil
		w.mu.Unlock()

		p := probers[rec.SelfSigned]
		ctx := context.Background()
		var got string
		switch rec.Kind {
		case "http":
			got = w.resultJSON(p.checkHTTP(ctx, w.fill(decode[string](t, rec.Args[0])), w.number(t, rec.Args[1])))
		case "tcp":
			got = w.resultJSON(p.checkTCPPort(ctx, decode[string](t, rec.Args[0]), w.number(t, rec.Args[1]), w.number(t, rec.Args[2])))
		case "ping":
			got = w.resultJSON(p.pingHost(ctx, decode[string](t, rec.Args[0]), w.number(t, rec.Args[1])))
		case "resolve":
			address, err := p.resolveHost(ctx, decode[string](t, rec.Args[0]))
			if err != nil {
				got = "thrown: " + err.Error()
			} else {
				quoted, _ := json.Marshal(address)
				got = string(quoted)
			}
		default:
			t.Fatalf("%s: unknown kind %s", rec.Name, rec.Kind)
		}

		var compact bytes.Buffer
		_ = json.Compact(&compact, rec.Result)
		want := compact.String()
		if rec.Thrown != nil {
			want = "thrown: " + *rec.Thrown
		}
		if other, ok := probeDifferences[rec.Name]; ok {
			want = other
		} else if rec.Never {
			t.Errorf("%s: Node never answered, and the test does not say what Go answers", rec.Name)
		}
		if got != want {
			t.Errorf("%s:\n got  %s\n Node %s", rec.Name, got, want)
		}

		w.mu.Lock()
		requests, pingArgs := w.requests, w.pingArgs
		w.mu.Unlock()
		if rec.Name == "http path with percent signs" {
			requests = nil
			rec.Requests = nil
		}
		if len(requests) != len(rec.Requests) {
			t.Errorf("%s: the servers got %d requests, Node's got %d", rec.Name, len(requests), len(rec.Requests))
		} else {
			for i, req := range requests {
				wantReq := rec.Requests[i]
				if req.method != wantReq.Method || req.uri != wantReq.URL {
					t.Errorf("%s: request %s %s, Node sent %s %s", rec.Name, req.method, req.uri, wantReq.Method, wantReq.URL)
				}
				// Node sent Host and Connection and nothing else
				wantHeaders := map[string]string{}
				for j := 0; j+1 < len(wantReq.Headers); j += 2 {
					wantHeaders[wantReq.Headers[j]] = w.fill(wantReq.Headers[j+1])
				}
				gotHeaders := map[string]string{"Host": req.host}
				for name, values := range req.headers {
					gotHeaders[name] = strings.Join(values, ", ")
				}
				if fmt.Sprint(gotHeaders) != fmt.Sprint(wantHeaders) {
					t.Errorf("%s: request headers %v, Node sent %v", rec.Name, gotHeaders, wantHeaders)
				}
			}
		}
		if got, want := fmt.Sprint(namesOnly(w.resolver.lookups)), fmt.Sprint(once(namesOnly(rec.Lookups))); got != want {
			t.Errorf("%s: lookups %s, Node's %s", rec.Name, got, want)
		}
		if fmt.Sprint(pingArgs) != fmt.Sprint(w.atAddress(rec.PingArgs)) {
			t.Errorf("%s: ping got %q, Node's got %q", rec.Name, pingArgs, rec.PingArgs)
		}
	}
}

// A dialer that does not answer makes a TCP check end as a timeout.
type stuckDialer struct{}

func (stuckDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTCPCheckTimesOut(t *testing.T) {
	w := newWorld(t)
	p := w.prober(false)
	p.dialer = stuckDialer{}
	start := time.Now()
	res := p.checkTCPPort(context.Background(), "10.1.2.3", 80, 150)
	if got := w.resultJSON(res); got != `{"success":false,"rtt_ms":null,"error":"Connection timeout"}` {
		t.Errorf("got %s", got)
	}
	if took := time.Since(start); took < 100*time.Millisecond || took > 3*time.Second {
		t.Errorf("the check took %s, want about 150ms", took)
	}
}

// The system errors of a connection get Node's names; the ones the
// system reports at once also name the local address.
func TestConnectMessages(t *testing.T) {
	cases := []struct {
		errno syscall.Errno
		ip    string
		port  int
		want  string
	}{
		{syscall.ECONNREFUSED, "10.0.0.5", 443, "connect ECONNREFUSED 10.0.0.5:443"},
		{syscall.ECONNREFUSED, "10.0.0.5", 0, "connect ECONNREFUSED 10.0.0.5"},
		{syscall.ETIMEDOUT, "10.0.0.5", 80, "connect ETIMEDOUT 10.0.0.5:80"},
		{syscall.EHOSTUNREACH, "10.0.0.5", 80, "connect EHOSTUNREACH 10.0.0.5:80"},
		{syscall.ECONNRESET, "fd00::1", 80, "connect ECONNRESET fd00::1:80"},
		{syscall.ENETUNREACH, "10.0.0.5", 80, "connect ENETUNREACH 10.0.0.5:80 - Local (0.0.0.0:0)"},
		{syscall.ENETUNREACH, "fd00::1", 80, "connect ENETUNREACH fd00::1:80 - Local (:::0)"},
		{syscall.EADDRNOTAVAIL, "10.0.0.5", 80, "connect EADDRNOTAVAIL 10.0.0.5:80 - Local (0.0.0.0:0)"},
		{syscall.Errno(10061), "10.0.0.5", 80, "connect ECONNREFUSED 10.0.0.5:80"},
	}
	for _, c := range cases {
		err := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", c.errno)}
		if got := connectMessage(err, c.ip, c.port); got != c.want {
			t.Errorf("connectMessage(%v, %s, %d) = %q, want %q", c.errno, c.ip, c.port, got, c.want)
		}
	}
	if got := connectMessage(fmt.Errorf("something else"), "10.0.0.5", 80); got != "something else" {
		t.Errorf("an unknown error: %q", got)
	}
}

// Certificates are refused with Node's words, the chain before the dates
// and the dates before the name.
func TestCertificateMessages(t *testing.T) {
	now := time.Now()
	ca, caKey, _ := certificate(t, "Test CA", nil, nil, true, nil, nil)
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	leaf, _, _ := certificate(t, "host.test", []string{"host.test"}, nil, false, ca, caKey)
	lonely, _, _ := certificate(t, "host.test", []string{"host.test"}, nil, false, nil, nil)
	otherCA, otherKey, _ := certificate(t, "Other CA", nil, nil, true, nil, nil)
	foreign, _, _ := certificate(t, "host.test", []string{"host.test"}, nil, false, otherCA, otherKey)
	cnOnly, _, _ := certificate(t, "host.test", nil, nil, false, ca, caKey)

	c := &connector{roots: roots, now: func() time.Time { return now }}
	later := &connector{roots: roots, now: func() time.Time { return now.Add(48 * time.Hour) }}
	earlier := &connector{roots: roots, now: func() time.Time { return now.Add(-48 * time.Hour) }}
	cases := []struct {
		name  string
		c     *connector
		host  string
		certs []*x509.Certificate
		want  string
	}{
		{"trusted", c, "host.test", []*x509.Certificate{leaf}, ""},
		{"trusted with its authority", c, "host.test", []*x509.Certificate{leaf, ca}, ""},
		{"self-signed", c, "host.test", []*x509.Certificate{lonely}, "self-signed certificate"},
		{"self-signed and for another name", c, "other.test", []*x509.Certificate{lonely}, "self-signed certificate"},
		{"self-signed and expired", later, "host.test", []*x509.Certificate{lonely}, "self-signed certificate"},
		{"unknown authority, sent along", c, "host.test", []*x509.Certificate{foreign, otherCA}, "self-signed certificate in certificate chain"},
		{"unknown authority, not sent", c, "host.test", []*x509.Certificate{foreign}, "unable to verify the first certificate"},
		{"expired", later, "host.test", []*x509.Certificate{leaf}, "certificate has expired"},
		{"expired and for another name", later, "other.test", []*x509.Certificate{leaf}, "certificate has expired"},
		{"not yet valid", earlier, "host.test", []*x509.Certificate{leaf}, "certificate is not yet valid"},
		{"another name", c, "other.test", []*x509.Certificate{leaf}, "Hostname/IP does not match certificate's altnames: Host: other.test. is not in the cert's altnames: DNS:host.test"},
		{"an address", c, "10.0.0.5", []*x509.Certificate{leaf}, "Hostname/IP does not match certificate's altnames: IP: 10.0.0.5 is not in the cert's list: "},
		{"no names but a common name", c, "other.test", []*x509.Certificate{cnOnly}, "Hostname/IP does not match certificate's altnames: Host: other.test. is not cert's CN: host.test"},
		{"no certificate", c, "host.test", nil, "unable to verify the first certificate"},
	}
	for _, tc := range cases {
		err := tc.c.verify(tc.host, tc.certs)
		got := ""
		if err != nil {
			got = requestMessage(err)
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// On Windows ping takes the timeout in milliseconds and prints the time
// without a space.
func TestPingOnWindows(t *testing.T) {
	w := newWorld(t)
	yes := true
	p := w.proberFor(false, &yes)
	res := p.pingHost(context.Background(), "ping-windows.test", 2500)
	if res.RTT == nil || *res.RTT != 14 || !res.Success {
		t.Errorf("result %+v, want a round trip of 14 ms", res)
	}
	if fmt.Sprint(w.pingArgs) != "[-n 1 -w 2500 192.0.2.10]" {
		t.Errorf("arguments %q", w.pingArgs)
	}
	// "time<1ms" is the form for less than a millisecond
	w.v.Pings["ping-less.test"] = struct {
		Stdout string `json:"stdout"`
		Code   int    `json:"code"`
		Sleep  int    `json:"sleep"`
	}{Stdout: "Reply from 192.0.2.10: bytes=32 time<1ms TTL=57\n"}
	if res := p.pingHost(context.Background(), "ping-less.test", 5000); res.RTT == nil || *res.RTT != 1 {
		t.Errorf("time<1ms: %+v, want 1", res)
	}
	// The Linux form has a space and is not read: the time taken stands in
	if res := p.pingHost(context.Background(), "ping-ok.test", 5000); res.RTT == nil || *res.RTT == 12.3 {
		t.Errorf("a Linux answer on Windows: %+v", res)
	}
}

// A command that cannot be started fails with Node's message.
func TestPingCommandMissing(t *testing.T) {
	w := newWorld(t)
	p := w.prober(false)
	p.ping = func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return runCommand(ctx, "bd-no-such-ping-command", args...)
	}
	res := p.pingHost(context.Background(), "10.1.2.3", 5000)
	if res.Error == nil || *res.Error != "spawn bd-no-such-ping-command ENOENT" || res.Success || res.RTT != nil {
		t.Errorf("result %+v", res)
	}
}

// The real runner returns what the command printed and its exit code,
// and stops a command that runs too long.
func TestRunCommand(t *testing.T) {
	out, code, err := runCommand(context.Background(), "sh", "-c", "echo time=1.5 ms; exit 0")
	if err != nil || code != 0 || string(out) != "time=1.5 ms\n" {
		t.Errorf("exit 0: %q %d %v", out, code, err)
	}
	if _, code, err := runCommand(context.Background(), "sh", "-c", "exit 3"); err != nil || code != 3 {
		t.Errorf("exit 3: %d %v", code, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, code, err := runCommand(ctx, "sleep", "20"); err != nil || code == 0 {
		t.Errorf("a stopped command: %d %v", code, err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stopping took %s", took)
	}
}

func TestNodeIsDue(t *testing.T) {
	w := newWorld(t)
	if len(w.v.Due) < 3000 {
		t.Fatalf("only %d vectors", len(w.v.Due))
	}
	db := storetest.Migrated(t)
	ctx := context.Background()
	err := db.InTx(ctx, func(tx *store.Tx) error {
		for i, row := range w.v.Due {
			var interval, last any
			if string(row[1]) != "null" {
				interval = decode[float64](t, row[1])
			}
			if string(row[3]) != "null" {
				last = decode[string](t, row[3])
			}
			if _, err := tx.Exec(ctx, "INSERT INTO network_targets (name, check_type, interval_ms, last_check_at) VALUES (?, 'ping', ?, ?)",
				fmt.Sprintf("%06d", i), interval, last); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := ListTargets(ctx, db, TargetFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != len(w.v.Due) {
		t.Fatalf("%d targets for %d vectors", len(targets), len(w.v.Due))
	}
	m := New(Options{Store: db})
	now := time.UnixMilli(w.v.DueNow)
	for i, row := range w.v.Due {
		m.interval = Number(decode[float64](t, row[0]))
		if got, want := m.IsDue(&targets[i], now), decode[bool](t, row[4]); got != want {
			t.Errorf("poll interval %s, interval %s, last check %s: due %v, in Node %v", row[0], row[1], row[3], got, want)
		}
	}
}

// boundedTimeouts are the checks of targets stored with a timeout above
// MaxTimeoutMs, with the start of the ping command's arguments: the
// console checks them with MaxTimeoutMs (BUGS RV44), where Node stopped
// the ping process after a millisecond for a timeout above its longest
// timer.
var boundedTimeouts = map[string]string{"ping timeout 1e23": "-c 1 -W 60 "}

func TestNodeCheckTarget(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t)
		ctx := context.Background()
		var logged []string
		no := false
		p := w.prober(false)
		m := New(Options{
			Store: db, Resolver: w.resolver, Dialer: w, Transport: p.transport, Ping: w.ping, Windows: &no,
			Log: logFunc(func(line string) { logged = append(logged, line) }),
		})
		if len(w.v.Checks) < 30 {
			t.Fatalf("only %d checks", len(w.v.Checks))
		}
		for _, check := range w.v.Checks {
			name := decode[string](t, check.Target["name"])
			id := decode[int64](t, check.Target["id"])
			column := func(key string) any {
				raw, ok := check.Target[key]
				if !ok || string(raw) == "null" {
					return nil
				}
				var f float64
				if json.Unmarshal(raw, &f) == nil {
					return f
				}
				text := w.fill(decode[string](t, raw))
				if key == "port" {
					n, _ := strconv.ParseFloat(text, 64)
					return n
				}
				return text
			}
			w.mu.Lock()
			w.pingArgs = nil
			w.mu.Unlock()
			started := time.Now()

			var target *Target
			if _, fails := check.Target["store_fails"]; fails {
				// No row has the id, so the check cannot be stored
				target = &Target{ID: store.NewInt64String(id, 0), CheckType: store.NullString{String: "icmp", Valid: true}, Host: store.NullString{String: "x", Valid: true}}
			} else {
				args := []any{id, name, column("host"), column("port"), column("url"), column("check_type"), column("timeout_ms")}
				if db.IsPostgres() {
					for i, a := range args {
						if f, isFloat := a.(float64); isFloat {
							args[i] = Number(f).String()
						}
					}
				}
				if _, err := db.Exec(ctx, "INSERT INTO network_targets (id, name, host, port, url, check_type, timeout_ms) VALUES (?, ?, ?, ?, ?, ?, ?)", args...); err != nil {
					if db.IsPostgres() {
						// An integer column of PostgreSQL does not take what SQLite keeps as a float
						continue
					}
					t.Fatalf("%s: %v", name, err)
				}
				var err error
				if target, err = GetTarget(ctx, db, Num(Number(id))); err != nil || target == nil {
					t.Fatalf("%s: load the target: %v", name, err)
				}
			}

			logged = nil
			got := m.CheckTarget(ctx, target)
			if start, bounded := boundedTimeouts[name]; bounded {
				w.mu.Lock()
				pingArgs := w.pingArgs
				w.mu.Unlock()
				if len(pingArgs) != 1 || !strings.HasPrefix(pingArgs[0], start) {
					t.Errorf("%s: ping got %q, want %q and the address", name, pingArgs, start)
				}
				continue
			}
			wantResult := map[string]any{}
			for key, raw := range check.Result {
				wantResult[key] = decode[any](t, raw)
			}
			gotJSON := strings.TrimSuffix(w.resultJSON(got.Result), "}") + fmt.Sprintf(`,"status":%q,"target_id":%d}`, got.Status, got.TargetID.V)
			var gotResult map[string]any
			if err := json.Unmarshal([]byte(gotJSON), &gotResult); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(gotResult) != fmt.Sprint(wantResult) {
				t.Errorf("%s: result\n got  %v\n Node %v", name, gotResult, wantResult)
			}
			if got.HTTP != slices.Contains(check.Keys, "status_code") {
				t.Errorf("%s: the result has a status code: %v, in Node its keys are %v", name, got.HTTP, check.Keys)
			}
			w.mu.Lock()
			pingArgs := w.pingArgs
			w.mu.Unlock()
			if fmt.Sprint(pingArgs) != fmt.Sprint(w.atAddress(check.PingArgs)) {
				t.Errorf("%s: ping got %q, Node's got %q", name, pingArgs, check.PingArgs)
			}

			if len(check.Calls) == 0 {
				if len(logged) != 1 || !strings.Contains(logged[0], "DB save error for target 99") {
					t.Errorf("%s: log %q, want the line about the failed save", name, logged)
				}
				continue
			}
			if len(logged) != 0 {
				t.Errorf("%s: logged %q", name, logged)
			}
			// What Node handed to insertNetworkCheck and updateNetworkTarget
			// is in the tables
			insert := decode[map[string]any](t, check.Calls[0]["insert"])
			history, err := CheckHistory(ctx, db, Num(Number(id)), HistoryFilter{})
			if err != nil || len(history) != 1 {
				t.Fatalf("%s: history: %d rows, %v", name, len(history), err)
			}
			row := history[0]
			stored := map[string]any{"target_id": float64(row.TargetID.V), "status": row.Status.String, "rtt_ms": nil, "status_code": nil, "error_msg": nil}
			if row.RTT.Valid {
				stored["rtt_ms"] = "number"
			}
			if row.StatusCode.Valid {
				stored["status_code"] = row.StatusCode.V
			}
			if row.ErrorMsg.Valid {
				stored["error_msg"] = w.mask(row.ErrorMsg.String)
			}
			if fmt.Sprint(stored) != fmt.Sprint(insert) {
				t.Errorf("%s: stored check\n got  %v\n Node %v", name, stored, insert)
			}
			update := decode[map[string]any](t, check.Calls[1]["update"])
			after, err := GetTarget(ctx, db, Num(Number(id)))
			if err != nil || after == nil {
				t.Fatalf("%s: load the target again: %v", name, err)
			}
			if after.LastStatus.String != update["last_status"] {
				t.Errorf("%s: last_status %q, Node set %v", name, after.LastStatus.String, update["last_status"])
			}
			if after.LastRTT.Valid != (update["last_rtt_ms"] == "number") {
				t.Errorf("%s: last_rtt_ms set: %v, Node set %v", name, after.LastRTT.Valid, update["last_rtt_ms"])
			}
			checkedAt, ok := after.LastCheckAt.Instant()
			if !ok || checkedAt.Before(started.Add(-time.Second)) || checkedAt.After(time.Now().Add(time.Second)) {
				t.Errorf("%s: last_check_at %s is not the time of the check", name, after.LastCheckAt.String())
			}
			if !db.IsPostgres() && !isoMillis.MatchString(after.LastCheckAt.String()) {
				t.Errorf("%s: last_check_at %q, want the form of toISOString", name, after.LastCheckAt.String())
			}
			if after.UpdatedAt.IsNull() {
				t.Errorf("%s: updated_at is not set", name)
			}
		}
	})
}

var isoMillis = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

// logFunc is a Logger that hands every line to a function.
type logFunc func(line string)

func (f logFunc) Info(args ...any)  { f(fmt.Sprint(args...)) }
func (f logFunc) Error(args ...any) { f(fmt.Sprint(args...)) }

// refusing is a dialer that refuses one address and connects to the rest.
type refusing struct {
	ip string
	mu sync.Mutex
	// dialed are the addresses that were tried, in order.
	dialed []string
}

func (d *refusing) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	d.mu.Unlock()
	if host, _, _ := net.SplitHostPort(address); host == d.ip {
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, local(address))
}

// An HTTP check of a name with several addresses tries them one after
// the other and takes the first that answers.
func TestHTTPCheckTriesEveryAddress(t *testing.T) {
	w := newWorld(t)
	w.resolver.names["two.test"] = []string{"192.0.2.50", standIn}
	dialer := &refusing{ip: "192.0.2.50"}
	transport := newTransport(&connector{dialer: dialer, now: time.Now})
	t.Cleanup(transport.CloseIdleConnections)
	p := w.prober(false)
	p.transport = transport
	res := p.checkHTTP(context.Background(), w.fill("http://two.test:{http}/ok"), 5000)
	if got := w.resultJSON(res); got != `{"success":true,"rtt_ms":"number","status_code":200,"error":null}` {
		t.Errorf("result %s", got)
	}
	want := w.fill("[192.0.2.50:{http} " + standIn + ":{http}]")
	if got := fmt.Sprint(dialer.dialed); got != want {
		t.Errorf("tried %s, want %s", got, want)
	}
}

func TestAddressesTakeTurnsByFamily(t *testing.T) {
	cases := map[string]string{
		"10.0.0.1":                          "[10.0.0.1]",
		"10.0.0.1 10.0.0.2 fd00::1":         "[10.0.0.1 fd00::1 10.0.0.2]",
		"fd00::1 fd00::2 10.0.0.1 10.0.0.2": "[fd00::1 10.0.0.1 fd00::2 10.0.0.2]",
		"10.0.0.1 fd00::1 fd00::2 fd00::3":  "[10.0.0.1 fd00::1 fd00::2 fd00::3]",
		"fd00::1 10.0.0.1 10.0.0.2":         "[fd00::1 10.0.0.1 10.0.0.2]",
	}
	for in, want := range cases {
		if got := fmt.Sprint(takeTurns(strings.Fields(in))); got != want {
			t.Errorf("takeTurns(%s) = %s, want %s", in, got, want)
		}
	}
}

// A server that switches protocols has not answered the check: only a
// status from 200 on can be a success.
func TestHTTPCheckStatusBelow200(t *testing.T) {
	w := newWorld(t)
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: x\r\nConnection: Upgrade\r\n\r\n"))
			_ = conn.Close()
		}
	}()
	res := w.prober(false).checkHTTP(context.Background(), "http://"+standIn+":"+strconv.Itoa(l.Addr().(*net.TCPAddr).Port)+"/", 5000)
	if res.Success || res.StatusCode == nil || *res.StatusCode != 101 {
		t.Errorf("result %s", w.resultJSON(res))
	}
}

// An HTTP check of a server that says nothing ends after the timeout.
func TestHTTPCheckTimesOut(t *testing.T) {
	w := newWorld(t)
	p := w.prober(false)
	done := make(chan Result, 1)
	start := time.Now()
	go func() { done <- p.checkHTTP(context.Background(), w.fill("http://"+standIn+":{silent}/"), 150) }()
	select {
	case res := <-done:
		if got := w.resultJSON(res); got != `{"success":false,"rtt_ms":null,"status_code":null,"error":"Timeout"}` {
			t.Errorf("result %s", got)
		}
		if took := time.Since(start); took < 100*time.Millisecond {
			t.Errorf("the check ended after %s, before its timeout", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check did not end")
	}
}

// The host check refuses every character a shell reads as its own, before
// it looks at the form of the host.
func TestShellCharacters(t *testing.T) {
	for _, c := range ";&|`$(){}[]<>\\!#*?'\"~" {
		if !shellCharacters.MatchString("a" + string(c) + "b") {
			t.Errorf("%q is not among the shell characters", c)
		}
		if isValidHost("a" + string(c) + "b") {
			t.Errorf("a host with %q in it is taken", c)
		}
	}
	if shellCharacters.MatchString("a-b.example:8%z") {
		t.Error("a character of a host is among the shell characters")
	}
}

// The addresses of a name are tried with IPv4 and IPv6 taking turns. When
// none connects the check fails with the message of the first address
// (BUGS.md NM9).
func TestHTTPCheckTriesFamiliesInTurn(t *testing.T) {
	w := newWorld(t)
	w.resolver.names["three.test"] = []string{"192.0.2.50", "192.0.2.51", "fd00::5"}
	dialer := &refusing{}
	w.mu.Lock()
	w.inject = syscall.ECONNREFUSED
	w.mu.Unlock()
	record := dialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		dialer.mu.Lock()
		dialer.dialed = append(dialer.dialed, address)
		dialer.mu.Unlock()
		return w.DialContext(ctx, network, address)
	})
	transport := newTransport(&connector{dialer: record, now: time.Now})
	t.Cleanup(transport.CloseIdleConnections)
	p := w.prober(false)
	p.transport = transport
	res := p.checkHTTP(context.Background(), "http://three.test:8080/", 5000)
	if got := w.resultJSON(res); got != `{"success":false,"rtt_ms":null,"status_code":null,"error":"connect ECONNREFUSED 192.0.2.50:8080"}` {
		t.Errorf("result %s", got)
	}
	if got, want := fmt.Sprint(dialer.dialed), "[192.0.2.50:8080 [fd00::5]:8080 192.0.2.51:8080]"; got != want {
		t.Errorf("tried %s, want %s", got, want)
	}
}

type dialerFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (f dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// An HTTP check of a URL with an IPv6 address connects to that address
// once the guard has passed it (BUGS.md NM5).
func TestHTTPCheckOfAnIPv6Address(t *testing.T) {
	w := newWorld(t)
	var dialed []string
	toLocal := dialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		_, port, _ := net.SplitHostPort(address)
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
	})
	transport := newTransport(&connector{dialer: toLocal, now: time.Now})
	t.Cleanup(transport.CloseIdleConnections)
	p := w.prober(false)
	p.transport = transport
	res := p.checkHTTP(context.Background(), w.fill("http://[2001:DB8::10]:{http}/ok"), 5000)
	if got := w.resultJSON(res); got != `{"success":true,"rtt_ms":"number","status_code":200,"error":null}` {
		t.Errorf("result %s", got)
	}
	if want := w.fill("[[2001:db8::10]:{http}]"); fmt.Sprint(dialed) != want {
		t.Errorf("dialed %v, want %s", dialed, want)
	}
	// What the guard refuses is still refused
	if got := w.resultJSON(p.checkHTTP(context.Background(), "http://[::ffff:127.0.0.1]:80/", 5000)); got != `{"success":false,"rtt_ms":null,"status_code":null,"error":"Blocked IP address: ::ffff:7f00:1"}` {
		t.Errorf("mapped loopback: %s", got)
	}
}
