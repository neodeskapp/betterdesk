package console

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The recording listen/testdata/node_listeners.json holds what the Node
// console's listeners (server.js) answered over real sockets: the client
// API port in proxy and local mode and with TLS, the console on HTTPS, the
// fallbacks and the start failures; listen/testdata/node_redirect.json
// holds the redirect listener of upstream 3.5.264. The tests below start
// the Go console with the same settings and send the same bytes.

type oracleFile struct {
	Ports     map[string]int   `json:"ports"`
	Scenarios []oracleScenario `json:"scenarios"`
}

type oracleScenario struct {
	Name          string            `json:"name"`
	Env           map[string]string `json:"env"`
	Exchanges     []oracleExchange  `json:"exchanges"`
	Ciphers       []oracleCipher    `json:"ciphers"`
	Chain         []string          `json:"chain"`
	APIChain      []string          `json:"apiChain"`
	Exit          *int              `json:"exit"`
	ExitCode      *int              `json:"exitCode"`
	PlainPortOpen *bool             `json:"plainPortOpen"`
	HTTPSPortOpen *bool             `json:"httpsPortOpen"`
	APIPortOpen   *bool             `json:"apiPortOpen"`
	ConsoleOpen   *bool             `json:"consoleOpen"`
	// Durable is the public endpoints file (DATA_DIR/public-endpoints.env)
	// of the redirect recording, written before the start
	Durable        *string `json:"durable"`
	RecordedPrefix string  `json:"-"`
}

type oracleExchange struct {
	ID          string       `json:"id"`
	Listener    string       `json:"listener"`
	Secure      bool         `json:"secure"`
	Parts       []oraclePart `json:"parts"`
	Wait        int          `json:"wait"`
	Got         string       `json:"got"`
	Closed      bool         `json:"closed"`
	ClosedAfter *int         `json:"closedAfter"`
}

type oraclePart struct {
	Data  *string `json:"data"`
	Delay int     `json:"delay"`
	Head  string  `json:"head"`
	Fill  int     `json:"fill"`
	Byte  byte    `json:"byte"`
	Tail  string  `json:"tail"`
}

func (p oraclePart) bytes() []byte {
	if p.Data != nil {
		b, _ := base64.StdEncoding.DecodeString(*p.Data)
		return b
	}
	head, _ := base64.StdEncoding.DecodeString(p.Head)
	tail, _ := base64.StdEncoding.DecodeString(p.Tail)
	return append(append(head, bytes.Repeat([]byte{p.Byte}, p.Fill)...), tail...)
}

type oracleCipher struct {
	Cert    string `json:"cert"`
	Version string `json:"version"`
	Suite   string `json:"suite"`
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
}

var loadOracle = sync.OnceValues(func() (*oracleFile, error) {
	raw, err := os.ReadFile(filepath.Join("listen", "testdata", "node_listeners.json"))
	if err != nil {
		return nil, err
	}
	var f oracleFile
	err = json.Unmarshal(raw, &f)
	return &f, err
})

func oracleScenarioNamed(t *testing.T, name string) oracleScenario {
	t.Helper()
	f, err := loadOracle()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range f.Scenarios {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no scenario %q in the recording", name)
	return oracleScenario{}
}

// testCerts are the certificates of the recording, made again for the
// run: rsa, ec, a chain root > intermediate > leaf, a file that is no
// certificate and a key that belongs to no certificate here.
type testCerts struct {
	dir   string
	files map[string]string
}

func makeTestCerts(t *testing.T) *testCerts {
	t.Helper()
	dir := t.TempDir()
	c := &testCerts{dir: dir, files: map[string]string{}}
	write := func(name string, blocks ...*pem.Block) {
		var buf bytes.Buffer
		for _, b := range blocks {
			_ = pem.Encode(&buf, b)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		c.files[name] = path
	}
	serial := int64(1)
	issue := func(cn string, ca bool, key any, parent *x509.Certificate, parentKey any) (*x509.Certificate, []byte) {
		serial++
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
			DNSNames: []string{cn}, BasicConstraintsValid: true, IsCA: ca,
		}
		if ca {
			tpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
		}
		if parent == nil {
			parent, parentKey = tpl, key
		}
		pub := key.(crypto.Signer).Public()
		der, err := x509.CreateCertificate(rand.Reader, tpl, parent, pub, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, _ := x509.ParseCertificate(der)
		return cert, der
	}
	keyBlock := func(key any) *pem.Block {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	}
	certBlock := func(der []byte) *pem.Block { return &pem.Block{Type: "CERTIFICATE", Bytes: der} }
	rsaKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, der := issue("localhost", false, rsaKey, nil, nil)
	write("rsa.crt", certBlock(der))
	write("rsa.key", keyBlock(rsaKey))
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	_, der = issue("localhost", false, ecKey, nil, nil)
	write("ec.crt", certBlock(der))
	write("ec.key", keyBlock(ecKey))
	rootKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	root, rootDER := issue("Oracle Root", true, rootKey, nil, nil)
	intKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	inter, intDER := issue("Oracle Intermediate", true, intKey, root, rootKey)
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	_, leafDER := issue("localhost", false, leafKey, inter, intKey)
	write("leaf.crt", certBlock(leafDER))
	write("leaf.key", keyBlock(leafKey))
	write("int.crt", certBlock(intDER))
	write("cabundle.crt", certBlock(intDER), certBlock(rootDER))
	if err := os.WriteFile(filepath.Join(dir, "garbage.crt"), []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.files["garbage.crt"] = filepath.Join(dir, "garbage.crt")
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	write("other.key", keyBlock(otherKey))
	return c
}

// path turns a certificate path of the recording into this run's.
func (c *testCerts) path(recorded string) string {
	if name, ok := strings.CutPrefix(recorded, "<certs>/"); ok {
		if p, ok := c.files[name]; ok {
			return p
		}
	}
	if strings.HasPrefix(recorded, "/tmp/bd-oracle-certs-") {
		return c.dir
	}
	return recorded
}

// goStack is the Go console started with a scenario's settings.
type goStack struct {
	s     *Server
	api   *echoAPI
	reg   *fakeRegistration
	ports map[string]string
}

// replayEnv are the variables of a scenario the console reads, with the
// paths of this run.
var replayEnvKeys = []string{"HTTPS_ENABLED", "HTTP_REDIRECT_HTTPS", "SSL_CERT_PATH", "SSL_KEY_PATH", "SSL_CA_PATH",
	"API_ENABLED", "RUSTDESK_API_PROXY", "RUSTDESK_API_TLS", "TRUST_PROXY", "PANEL_PUBLIC_HOST"}

// newGoStack makes the console of a scenario. held are ports taken by
// something else, by listener name (console, https, api).
func newGoStack(t *testing.T, db *store.DB, scenario oracleScenario, certs *testCerts, held map[string]int) (*goStack, error) {
	t.Helper()
	for _, key := range replayEnvKeys {
		if value, ok := scenario.Env[key]; ok {
			if strings.HasPrefix(key, "SSL_") {
				value = certs.path(value)
			}
			t.Setenv(key, value)
		} else {
			t.Setenv(key, "")
			_ = os.Unsetenv(key)
		}
	}
	t.Setenv("SESSION_SECRET", testSecret)
	t.Setenv("CONSOLE_DIR", t.TempDir())
	cfg, err := config.LoadEnv()
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if scenario.Durable != nil {
		if err := os.WriteFile(filepath.Join(data, "public-endpoints.env"), []byte(*scenario.Durable), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Host, cfg.Port, cfg.HTTPSPort, cfg.APIPort, cfg.ClientAPIHost = "127.0.0.1", held["console"], held["https"], held["api"], "127.0.0.1"
	if host, ok := scenario.Env["HOST"]; ok {
		cfg.Host = host
	}
	cfg.DataDir, cfg.UploadsDir = data, filepath.Join(data, "uploads")
	cfg.LangDir = filepath.Join("static", "files", "lang")
	cfg.WebUIDir = filepath.Join(data, "no-build")
	cfg.NodeEnv, cfg.Production = "production", true
	g := &goStack{api: newEchoAPI(t), reg: newFakeRegistration(t), ports: map[string]string{}}
	cfg.APIHost, cfg.APIURL = g.api.host(), g.api.URL+"/api"
	cfg.NodeUpstream, _ = url.Parse(g.reg.URL)
	s, err := New(context.Background(), Options{Config: cfg, Store: db, API: http.NotFoundHandler()})
	if err != nil {
		return nil, err
	}
	// The registration stand-in is the Node console for the client API port
	// only: the console port answers everything itself
	s.node = nil
	g.s = s
	if err := s.Start(context.Background()); err != nil {
		s.Stop()
		return nil, err
	}
	t.Cleanup(s.Stop)
	g.ports["api"] = s.ClientAPIAddr()
	if s.net.cert != nil {
		g.ports["https"] = s.net.addr
		g.ports["redirect"] = s.net.redirectAddr
	} else {
		g.ports["console"] = s.net.addr
	}
	return g, nil
}

// send sends the parts over a fresh connection and collects what comes
// back until the server closes or wait passes.
func send(addr string, secure bool, parts []oraclePart, wait time.Duration) (got []byte, closed bool, after time.Duration) {
	start := time.Now()
	var conn net.Conn
	var err error
	if secure {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}) //nolint:gosec // a test server's own certificate
	} else {
		conn, err = net.DialTimeout("tcp", addr, 5*time.Second)
	}
	if err != nil {
		return nil, true, 0
	}
	defer conn.Close()
	go func() {
		for _, p := range parts {
			time.Sleep(time.Duration(p.Delay) * time.Millisecond)
			if _, err := conn.Write(p.bytes()); err != nil {
				return
			}
		}
	}()
	_ = conn.SetReadDeadline(start.Add(wait))
	var buf bytes.Buffer
	_, err = io.Copy(&buf, conn)
	var timeout interface{ Timeout() bool }
	closed = err == nil || !(asTimeout(err, &timeout) && timeout.Timeout())
	return buf.Bytes(), closed, time.Since(start)
}

func asTimeout(err error, target *interface{ Timeout() bool }) bool {
	t, ok := err.(interface{ Timeout() bool })
	if !ok {
		if u, ok2 := err.(interface{ Unwrap() error }); ok2 {
			return asTimeout(u.Unwrap(), target)
		}
		return false
	}
	*target = t
	return true
}

// reply is one HTTP answer.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// parseReplies reads the answers in raw; interim (1xx) answers are left
// out.
func parseReplies(raw []byte, method string) []reply {
	br := bufio.NewReader(bytes.NewReader(raw))
	var out []reply
	for {
		res, err := http.ReadResponse(br, &http.Request{Method: method})
		if err != nil {
			return out
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode >= 200 {
			out = append(out, reply{res.StatusCode, res.Header, body})
		}
	}
}

// requestOf reads the first request of the parts.
func requestOf(parts []oraclePart) *http.Request {
	var all []byte
	for _, p := range parts {
		all = append(all, p.bytes()...)
	}
	r, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(all)))
	if err != nil {
		return nil
	}
	return r
}

func methodOf(parts []oraclePart) string {
	if len(parts) == 0 {
		return "GET"
	}
	line := string(parts[0].bytes())
	method, _, _ := strings.Cut(line, " ")
	return method
}

// ignoredHeaders differ between any two servers: the date, and framing
// (the bodies are compared instead).
var ignoredHeaders = map[string]bool{"date": true, "connection": true, "keep-alive": true, "content-length": true, "transfer-encoding": true}

// headerDiff lists how two header sets differ. The seconds until a rate
// limit window ends may differ by one.
func headerDiff(want, got http.Header) []string {
	lower := func(h http.Header) map[string]string {
		out := map[string]string{}
		for name, values := range h {
			if n := strings.ToLower(name); !ignoredHeaders[n] {
				out[n] = strings.Join(values, "\n")
			}
		}
		return out
	}
	w, g := lower(want), lower(got)
	var diffs []string
	for name, value := range w {
		other, ok := g[name]
		switch {
		case !ok:
			diffs = append(diffs, "missing "+name+": "+value)
		case other != value && (name == "ratelimit-reset" || name == "retry-after"):
			// A window of an address that several exchanges share started
			// at another moment of the run
			a, _ := strconv.Atoi(value)
			b, _ := strconv.Atoi(other)
			if a-b > 10 || b-a > 10 {
				diffs = append(diffs, fmt.Sprintf("%s: got %q want %q", name, other, value))
			}
		case other != value:
			diffs = append(diffs, fmt.Sprintf("%s: got %q want %q", name, other, value))
		}
	}
	for name, value := range g {
		if _, ok := w[name]; !ok {
			diffs = append(diffs, "extra "+name+": "+value)
		}
	}
	sort.Strings(diffs)
	return diffs
}

// compareReplies compares the answers of one exchange.
func compareReplies(want, got []reply) string {
	if len(want) != len(got) {
		return fmt.Sprintf("%d answers, Node gave %d", len(got), len(want))
	}
	for i := range want {
		if want[i].status != got[i].status {
			return fmt.Sprintf("status %d, Node %d", got[i].status, want[i].status)
		}
		if d := headerDiff(want[i].header, got[i].header); len(d) > 0 {
			return "headers: " + strings.Join(d, "; ")
		}
		if !bytes.Equal(want[i].body, got[i].body) {
			return fmt.Sprintf("body %q, Node %q", got[i].body, want[i].body)
		}
	}
	return ""
}

// replayResult counts what a replay compared.
type replayResult struct {
	same, routed, differ int
	diffs                []string
}

// replayExchanges sends the exchanges of a scenario in order, the slow
// ones (wait over 20 s) at the same time at the end, and compares each
// answer with Node's. known lists the exchanges where Go answers
// otherwise on purpose, with what Go answers.
func (g *goStack) replayExchanges(t *testing.T, scenario oracleScenario, known map[string]func(t *testing.T, got []reply, closed bool) string) replayResult {
	t.Helper()
	var res replayResult
	var mu sync.Mutex
	trust, err := httpx.ParseTrustProxy(scenario.Env["TRUST_PROXY"])
	if err != nil {
		t.Fatal(err)
	}
	one := func(e oracleExchange) {
		addr := g.ports[e.Listener]
		if addr == "" {
			t.Errorf("%s: Go has no %s listener", e.ID, e.Listener)
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		wait := 6 * time.Second
		slow := e.Wait > 20000
		if slow {
			wait = 45 * time.Second
		}
		got, closed, after := send(addr, e.Secure, e.Parts, wait)
		method := methodOf(e.Parts)
		want, gotReplies := parseReplies(raw, method), parseReplies(got, method)
		forwardedAs(t, want, e.Parts, trust)
		if e.Listener == "redirect" {
			// The redirect names no server, where Node's said Express (BUGS
			// LS7)
			for _, r := range want {
				r.header.Del("X-Powered-By")
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if check, ok := known[e.ID]; ok {
			if msg := check(t, gotReplies, closed); msg != "" {
				res.diffs = append(res.diffs, e.ID+": "+msg)
			} else {
				res.differ++
			}
			return
		}
		if slow {
			if msg := compareSlow(e, raw, got, closed, after); msg != "" {
				res.diffs = append(res.diffs, e.ID+": "+msg)
			} else {
				res.same++
			}
			return
		}
		if len(want) == 0 && len(gotReplies) == 0 {
			if e.Closed != closed && e.Closed {
				res.diffs = append(res.diffs, fmt.Sprintf("%s: Node closed without an answer, Go kept the connection open", e.ID))
				return
			}
			res.same++
			return
		}
		if msg := compareReplies(want, gotReplies); msg != "" {
			// A client API route answers here where the Node console of
			// the recording, whose client API routes had already moved to
			// Go, answered the empty 404
			if r := requestOf(e.Parts); r != nil && g.s != nil && g.s.ClientAPI() != nil && e.Listener == "api" && g.s.ClientAPI().Router().Match(r) &&
				len(want) == 1 && want[0].status == http.StatusNotFound && len(want[0].body) == 0 {
				res.routed++
				return
			}
			res.diffs = append(res.diffs, e.ID+": "+msg)
			return
		}
		res.same++
	}
	var slow []oracleExchange
	for _, e := range scenario.Exchanges {
		if e.Wait > 20000 {
			slow = append(slow, e)
			continue
		}
		one(e)
	}
	var wg sync.WaitGroup
	for _, e := range slow {
		wg.Add(1)
		go func() {
			defer wg.Done()
			one(e)
		}()
	}
	wg.Wait()
	return res
}

// forwardedAs puts into Node's echo of a forwarded request the client
// address Go sends the API: the address the console resolves under
// TRUST_PROXY, alone in X-Forwarded-For and in X-Real-IP. Node passed the
// first address the client wrote there, whatever TRUST_PROXY said (BUGS
// LS10). The recording's requests come from loopback.
func forwardedAs(t *testing.T, want []reply, parts []oraclePart, trust httpx.TrustProxy) {
	t.Helper()
	if len(want) != 1 || want[0].header.Get("X-Upstream") != "echo" || len(want[0].body) == 0 {
		return
	}
	r := requestOf(parts)
	if r == nil {
		return
	}
	r.RemoteAddr = "127.0.0.1:40000"
	var e echo
	if err := json.Unmarshal(want[0].body, &e); err != nil {
		t.Fatalf("Node's echo %q: %v", want[0].body, err)
	}
	ip := trust.ClientIP(r)
	e.XFF, e.XRI = &ip, &ip
	body, err := httpx.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	want[0].body = body
}

// compareSlow compares a slow exchange: the same answer (or none), and
// the connection closed where Node closed it, no later than Node did.
// Node checked its request timeout every 30 seconds, so it closed between
// 10 and 40 seconds; Go closes at 10.
func compareSlow(e oracleExchange, want, got []byte, closed bool, after time.Duration) string {
	method := methodOf(e.Parts)
	w, g := parseReplies(want, method), parseReplies(got, method)
	if len(w) != len(g) {
		return fmt.Sprintf("%d answers %q, Node %d %q", len(g), got, len(w), want)
	}
	for i := range w {
		if w[i].status != g[i].status {
			return fmt.Sprintf("status %d, Node %d", g[i].status, w[i].status)
		}
	}
	if len(w) == 0 && len(want) > 0 && !bytes.Equal(want, got) {
		return fmt.Sprintf("bytes %q, Node %q", got, want)
	}
	if e.Closed != closed {
		return fmt.Sprintf("closed %v, Node %v", closed, e.Closed)
	}
	if e.Closed && e.ClosedAfter != nil {
		node := time.Duration(*e.ClosedAfter) * time.Millisecond
		if after > node+2*time.Second {
			return fmt.Sprintf("closed after %v, Node after %v", after, node)
		}
		if node >= 9*time.Second && after < 9*time.Second {
			return fmt.Sprintf("closed after %v, before the 10 second timeout", after)
		}
	}
	return ""
}

// status checks a known difference by status alone.
func statusIs(want int) func(t *testing.T, got []reply, closed bool) string {
	return func(_ *testing.T, got []reply, _ bool) string {
		if len(got) != 1 || got[0].status != want {
			return fmt.Sprintf("want one answer %d, got %v", want, statuses(got))
		}
		return ""
	}
}

func statuses(rs []reply) []int {
	var out []int
	for _, r := range rs {
		out = append(out, r.status)
	}
	return out
}

func closedUnanswered(_ *testing.T, got []reply, closed bool) string {
	if len(got) != 0 || !closed {
		return fmt.Sprintf("want the connection closed without an answer, got %v closed %v", statuses(got), closed)
	}
	return ""
}

// answersFromUpstream checks that the answer is the echo of the API server
// and the request went to it, not to the address in the request line.
func answersFromUpstream(_ *testing.T, got []reply, _ bool) string {
	if len(got) != 1 || got[0].status != http.StatusOK || !bytes.Contains(got[0].body, []byte(`"host":"upstream"`)) {
		return fmt.Sprintf("want the API server's echo, got %v %q", statuses(got), firstBody(got))
	}
	return ""
}

func firstBody(rs []reply) string {
	if len(rs) == 0 {
		return ""
	}
	return string(rs[0].body)
}

// knownClientAPI are the exchanges of the client API port where Go
// answers otherwise than Node on purpose.
var knownClientAPI = map[string]func(t *testing.T, got []reply, closed bool) string{
	// The request line names another host: Node's forward went there
	// (BUGS LS2); Go's goes to the API server it is set up with
	"absolute form elsewhere": answersFromUpstream,
	// Two Host headers: Node took the first, net/http refuses the request
	"two hosts": statusIs(http.StatusBadRequest),
	// Requests net/http itself refuses: the same status, but its answer has
	// a text body where Node's had none
	"malformed header line": statusIs(http.StatusBadRequest),
	"two content lengths":   statusIs(http.StatusBadRequest),
	"bad method token":      statusIs(http.StatusBadRequest),
	"not http":              statusIs(http.StatusBadRequest),
	"header size 17000":     statusIs(http.StatusRequestHeaderFieldsTooLarge),
	"header size 70000":     statusIs(http.StatusRequestHeaderFieldsTooLarge),
	"many headers":          statusIs(http.StatusRequestHeaderFieldsTooLarge),
	// Content-Length and chunked together: Node refused the request,
	// net/http reads it chunked and drops the length (RFC 9112 6.3); the
	// forward sends it chunked
	"length and chunked": statusIs(http.StatusOK),
	// net/http drops Content-Type from a 304; the API server, which is
	// net/http too, never sends one
	"upstream status:304": statusIs(http.StatusNotModified),
	// A second request that never completes on a kept-alive connection:
	// Node's keep-alive timer closed it about 6 seconds after its first
	// bytes, net/http's header timeout closes it after 10; neither answers
	// A device id given twice: the status of the first, which names no
	// registration here; Node's SQLite driver refused the list with 500
	// (BUGS LS9)
	"registration status two ids": func(_ *testing.T, got []reply, _ bool) string {
		if len(got) != 1 || got[0].status != http.StatusOK || string(got[0].body) != `{"success":true,"status":"not_found"}` {
			return fmt.Sprintf("want 200 not_found, got %v %q", statuses(got), firstBody(got))
		}
		return ""
	},
	"slow keep-alive then partial": func(_ *testing.T, got []reply, closed bool) string {
		if len(got) != 1 || !closed {
			return fmt.Sprintf("want the first answer and a close, got %v closed %v", statuses(got), closed)
		}
		return ""
	},
}

// proxyRegistration are the exchanges of the registration routes whose
// answer depends on the body. In proxy mode the port reads that body as
// JSON as in local mode, where Node's port read none and refused every
// registration (BUGS I1), so they answer as Node's port did in local mode.
var proxyRegistration = []string{
	"size 1025 POST /api/bd/register-request", "registration request", "registration request again", "registration status",
	"registration request malformed", "registration request ip from x-real-ip", "registration status 2",
}

// knownProxyMode are knownClientAPI with the registration exchanges
// compared against the local mode recording.
func knownProxyMode(t *testing.T) map[string]func(t *testing.T, got []reply, closed bool) string {
	t.Helper()
	known := map[string]func(t *testing.T, got []reply, closed bool) string{}
	for id, check := range knownClientAPI {
		known[id] = check
	}
	// An API answer slower than the port's 10 seconds: Node closed the
	// connection without an answer; Go answers the forward's 504 (BUGS LS8)
	for _, id := range []string{"slow upstream", "slow upstream post"} {
		known[id] = func(_ *testing.T, got []reply, closed bool) string {
			if len(got) != 1 || got[0].status != http.StatusGatewayTimeout || string(got[0].body) != `{"error":"Gateway Timeout"}` || !closed {
				return fmt.Sprintf("want 504 Gateway Timeout and a close, got %v %q closed %v", statuses(got), firstBody(got), closed)
			}
			return ""
		}
	}
	local := oracleScenarioNamed(t, "legacy")
	for _, id := range proxyRegistration {
		var want []reply
		for _, e := range local.Exchanges {
			if e.ID == id {
				raw, _ := base64.StdEncoding.DecodeString(e.Got)
				want = parseReplies(raw, methodOf(e.Parts))
			}
		}
		if len(want) == 0 {
			t.Fatalf("no answer to %q in the local mode recording", id)
		}
		// The budget of the address was spent otherwise in the local mode
		// run, so what remains of it is not compared
		known[id] = func(_ *testing.T, got []reply, _ bool) string {
			return compareReplies(withoutRemaining(want), withoutRemaining(got))
		}
	}
	return known
}

func withoutRemaining(rs []reply) []reply {
	out := make([]reply, len(rs))
	for i, r := range rs {
		out[i] = r
		out[i].header = r.header.Clone()
		out[i].header.Del("Ratelimit-Remaining")
	}
	return out
}

func checkReplay(t *testing.T, name string, res replayResult) {
	t.Helper()
	for _, d := range res.diffs {
		t.Errorf("%s: %s", name, d)
	}
	t.Logf("%s: %d answers as Node's, %d answered by a client API route, %d known differences", name, res.same, res.routed, res.differ)
}

func TestClientAPIProxyModeAgainstNode(t *testing.T) {
	scenario := oracleScenarioNamed(t, "proxy")
	certs := makeTestCerts(t)
	g, err := newGoStack(t, storetest.Migrated(t), scenario, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	res := g.replayExchanges(t, scenario, knownProxyMode(t))
	checkReplay(t, "proxy", res)
	if res.same < 600 {
		t.Errorf("only %d answers compared", res.same)
	}
	// The registration routes are answered by the console's own routes
	// (routes/registration ClientAPI): nothing reaches the Node console
	g.reg.mu.Lock()
	defer g.reg.mu.Unlock()
	if len(g.reg.seen) != 0 {
		t.Errorf("%d registration requests reached the Node console", len(g.reg.seen))
	}
}

// In proxy mode the port reads the JSON body of its own registration
// routes as in local mode, so a LAN device can register through it.
func TestClientAPIProxyModeTakesRegistrations(t *testing.T) {
	db := storetest.Migrated(t)
	g, err := newGoStack(t, db, oracleScenarioNamed(t, "proxy"), makeTestCerts(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	post := func(body string) (int, string) {
		t.Helper()
		res, err := http.Post("http://"+g.ports["api"]+"/api/bd/register-request", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		text, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(text)
	}
	if status, body := post(`{"device_id":"port-dev"}`); status != http.StatusOK || body != `{"success":true,"status":"pending","id":1}` {
		t.Errorf("registration: %d %s", status, body)
	}
	var device string
	if err := db.QueryRow(context.Background(), "SELECT device_id FROM pending_registrations WHERE id = 1").Scan(&device); err != nil || device != "port-dev" {
		t.Errorf("stored device %q %v", device, err)
	}
	if status, body := post(`{"device_id":`); status != http.StatusBadRequest || body != `{"error":"Invalid JSON"}` {
		t.Errorf("a body that does not parse: %d %s", status, body)
	}
}

func TestClientAPILocalModeAgainstNode(t *testing.T) {
	scenario := oracleScenarioNamed(t, "legacy")
	certs := makeTestCerts(t)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatal(err)
		}
		res := g.replayExchanges(t, scenario, knownClientAPI)
		checkReplay(t, "legacy", res)
		if res.same < 500 || res.routed < 100 {
			t.Errorf("only %d answers compared and %d routed", res.same, res.routed)
		}
	})
}

// TestClientAPILive replays the recording's proxy mode against the client
// API port of a running test instance (BD_CLIENT_API_ADDR; an instance
// started with STACK_CLIENT_API=1 TRUST_PROXY=true): every request goes,
// and the answers the port decides itself are compared, with those of the
// registration routes, which come from the instance's real Node console.
// Answers of the real API server are not the recording's echo and are
// left out.
func TestClientAPILive(t *testing.T) {
	addr := os.Getenv("BD_CLIENT_API_ADDR")
	if addr == "" {
		t.Skip("BD_CLIENT_API_ADDR is not set")
	}
	scenario := oracleScenarioNamed(t, "proxy")
	known := knownProxyMode(t)
	var kept []oracleExchange
	forwarded := 0
	for _, e := range scenario.Exchanges {
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		replies := parseReplies(raw, methodOf(e.Parts))
		if e.Wait > 20000 || strings.HasPrefix(e.ID, "upstream ") || strings.HasPrefix(e.ID, "absolute form") {
			continue
		}
		if len(replies) == 1 && replies[0].header.Get("X-Upstream") == "echo" {
			known[e.ID] = func(*testing.T, []reply, bool) string { return "" }
			forwarded++
		}
		kept = append(kept, e)
	}
	g := &goStack{ports: map[string]string{"api": addr}}
	res := g.replayExchanges(t, oracleScenario{Exchanges: kept}, known)
	res.differ -= forwarded
	checkReplay(t, "live proxy", res)
	if res.same < 300 {
		t.Errorf("only %d answers compared", res.same)
	}
}
