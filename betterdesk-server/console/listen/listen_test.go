package listen

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

type certFiles struct {
	dir                      string
	leaf, leafKey, int, root string
	selfSigned, selfKey      string
	bundle, otherKey         string
}

func writePEM(t *testing.T, path, kind string, der ...[]byte) string {
	t.Helper()
	var out []byte
	for _, d := range der {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: d})...)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeCerts(t *testing.T) certFiles {
	t.Helper()
	dir := t.TempDir()
	serial := int64(0)
	issue := func(cn string, ca bool, key crypto.Signer, parent *x509.Certificate, parentKey crypto.Signer) (*x509.Certificate, []byte) {
		serial++
		tpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: ca}
		if ca {
			tpl.KeyUsage = x509.KeyUsageCertSign
		}
		if parent == nil {
			parent, parentKey = tpl, key
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, parent, key.Public(), parentKey)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := x509.ParseCertificate(der)
		return c, der
	}
	key := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	keyDER := func(k *rsa.PrivateKey) []byte {
		der, _ := x509.MarshalPKCS8PrivateKey(k)
		return der
	}
	rootKey, intKey, leafKey, selfKey, other := key(), key(), key(), key(), key()
	root, rootDER := issue("Root", true, rootKey, nil, nil)
	inter, intDER := issue("Intermediate", true, intKey, root, rootKey)
	_, leafDER := issue("localhost", false, leafKey, inter, intKey)
	_, selfDER := issue("localhost", false, selfKey, nil, nil)
	f := certFiles{dir: dir}
	f.leaf = writePEM(t, filepath.Join(dir, "leaf.crt"), "CERTIFICATE", leafDER)
	f.leafKey = writePEM(t, filepath.Join(dir, "leaf.key"), "PRIVATE KEY", keyDER(leafKey))
	f.int = writePEM(t, filepath.Join(dir, "int.crt"), "CERTIFICATE", intDER)
	f.root = writePEM(t, filepath.Join(dir, "root.crt"), "CERTIFICATE", rootDER)
	f.bundle = writePEM(t, filepath.Join(dir, "bundle.crt"), "CERTIFICATE", rootDER, intDER)
	f.selfSigned = writePEM(t, filepath.Join(dir, "self.crt"), "CERTIFICATE", selfDER)
	f.selfKey = writePEM(t, filepath.Join(dir, "self.key"), "PRIVATE KEY", keyDER(selfKey))
	f.otherKey = writePEM(t, filepath.Join(dir, "other.key"), "PRIVATE KEY", keyDER(other))
	return f
}

type logLines []string

func (l *logLines) logf(format string, args ...any) { *l = append(*l, fmt.Sprintf(format, args...)) }

func TestReadFilesAsTheOldConsole(t *testing.T) {
	c := makeCerts(t)
	missing := filepath.Join(c.dir, "missing")
	cases := []struct {
		name          string
		cert, key, ca string
		ok            bool
		log           string
		caLoaded      bool
	}{
		{"no paths", "", "", "", false, "", false},
		{"only the certificate path", c.leaf, "", "", false, "", false},
		{"certificate missing", missing, c.leafKey, "", false, "SSL certificate not found: " + missing, false},
		{"key missing", c.leaf, missing, "", false, "SSL private key not found: " + missing, false},
		{"certificate is a folder", c.dir, c.leafKey, "", false, "Failed to load SSL certificates: ", false},
		{"bundle is a folder", c.leaf, c.leafKey, c.dir, false, "Failed to load SSL certificates: ", false},
		{"bundle missing", c.leaf, c.leafKey, missing, true, "", false},
		{"with bundle", c.leaf, c.leafKey, c.bundle, true, "", true},
	}
	for _, tc := range cases {
		var lines logLines
		f, ok := ReadFiles(tc.cert, tc.key, tc.ca, lines.logf)
		if ok != tc.ok {
			t.Errorf("%s: ok %v", tc.name, ok)
		}
		joined := strings.Join(lines, "\n")
		if (tc.log == "") != (joined == "") || !strings.HasPrefix(joined, tc.log) {
			t.Errorf("%s: logged %q, want %q", tc.name, joined, tc.log)
		}
		if ok && (f.CA != nil) != tc.caLoaded {
			t.Errorf("%s: bundle loaded %v", tc.name, f.CA != nil)
		}
	}
}

func subjects(t *testing.T, cert *tls.Certificate) []string {
	t.Helper()
	var out []string
	for _, der := range cert.Certificate {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, c.Subject.CommonName)
	}
	return out
}

func TestCertificateChainAndFailures(t *testing.T) {
	c := makeCerts(t)
	read := func(cert, key, ca string) *Files {
		f, ok := ReadFiles(cert, key, ca, func(string, ...any) {})
		if !ok {
			t.Fatalf("read %s %s %s", cert, key, ca)
		}
		return f
	}
	for _, tc := range []struct {
		name, cert, key, ca string
		want                string
	}{
		{"leaf alone", c.leaf, c.leafKey, "", "localhost"},
		{"bundle in any order", c.leaf, c.leafKey, c.bundle, "localhost Intermediate Root"},
		{"intermediate only", c.leaf, c.leafKey, c.int, "localhost Intermediate"},
		{"unrelated bundle", c.leaf, c.leafKey, c.root, "localhost"},
		{"self-signed with a bundle", c.selfSigned, c.selfKey, c.bundle, "localhost"},
	} {
		cert, err := read(tc.cert, tc.key, tc.ca).Certificate()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := strings.Join(subjects(t, cert), " "); got != tc.want {
			t.Errorf("%s: chain %q, want %q", tc.name, got, tc.want)
		}
	}
	// A certificate file that already holds a chain is sent as it is
	chained := filepath.Join(c.dir, "chained.crt")
	leafPEM, _ := os.ReadFile(c.leaf)
	rootPEM, _ := os.ReadFile(c.root)
	if err := os.WriteFile(chained, append(leafPEM, rootPEM...), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, err := read(chained, c.leafKey, c.bundle).Certificate()
	if err != nil || strings.Join(subjects(t, cert), " ") != "localhost Root" {
		t.Errorf("chain in the file: %v %v", err, cert)
	}
	garbage := filepath.Join(c.dir, "garbage")
	_ = os.WriteFile(garbage, []byte("not a certificate"), 0o600)
	for _, tc := range []struct{ name, cert, key string }{
		{"garbage certificate", garbage, c.leafKey},
		{"garbage key", c.leaf, garbage},
		{"key of another certificate", c.leaf, c.otherKey},
	} {
		if _, err := read(tc.cert, tc.key, "").Certificate(); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	// A bundle that is no certificate is skipped
	if cert, err := read(c.leaf, c.leafKey, garbage).Certificate(); err != nil || len(cert.Certificate) != 1 {
		t.Errorf("garbage bundle: %v", err)
	}
}

func TestServerTLSFloor(t *testing.T) {
	c := makeCerts(t)
	f, _ := ReadFiles(c.selfSigned, c.selfKey, "", func(string, ...any) {})
	cert, err := f.Certificate()
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerTLS(cert)
	if cfg.MinVersion != tls.VersionTLS12 || len(cfg.NextProtos) != 0 || len(cfg.CipherSuites) != 17 {
		t.Errorf("config: min %x, protocols %v, %d suites", cfg.MinVersion, cfg.NextProtos, len(cfg.CipherSuites))
	}
	for _, id := range cfg.CipherSuites {
		for _, insecure := range tls.InsecureCipherSuites() {
			if insecure.ID == id && (strings.Contains(insecure.Name, "RC4") || strings.Contains(insecure.Name, "3DES")) {
				t.Errorf("%s is on", insecure.Name)
			}
		}
	}
}

// expressLocation keeps the address unencoded where encoding would move
// the host url.parse sees: a "$" in the host name before the port.
func TestExpressLocation(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com:5443/x":      "https://example.com:5443/x",
		"https://a$b:5443/x":              "https://a$b:5443/x",
		"https://a$b/x":                   "https://a$b/x",
		"https://a%zz:5443/x":             "https://a%25zz:5443/x",
		"https://h:5443/a\"<>":            "https://h:5443/a%22%3C%3E",
		"https://h:5443/a?x=<script>":     "https://h:5443/a?x=%3Cscript%3E",
		"https://h:5443/%zz":              "https://h:5443/%25zz",
		"https://[::1]:5443/x":            "https://[::1]:5443/x",
		"https://h:5443http://evil.com/x": "https://h:5443http://evil.com/x",
	} {
		if got := expressLocation(in); got != want {
			t.Errorf("expressLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvePort(t *testing.T) {
	no := func() bool { return false }
	yes := func() bool { return true }
	for _, c := range []struct {
		port     int
		canBind  func() bool
		want     int
		warnings int
	}{
		{5000, no, 5000, 0}, {1024, no, 1024, 0}, {1023, no, 5443, 2}, {443, no, 5443, 2}, {443, yes, 443, 0},
		{0, yes, 5443, 0}, {-5, no, 5443, 0},
	} {
		var lines logLines
		if got := ResolvePort(c.port, 5443, "HTTPS", c.canBind, lines.logf); got != c.want || len(lines) != c.warnings {
			t.Errorf("ResolvePort(%d) = %d with %d warnings, want %d with %d", c.port, got, len(lines), c.want, c.warnings)
		}
	}
}

func TestCapabilityBit(t *testing.T) {
	for status, want := range map[string]bool{
		"CapEff:\t0000000000000400\n":                       true,  // CAP_NET_BIND_SERVICE, bit 10
		"CapEff:\t0000000000001000\n":                       false, // CAP_NET_ADMIN, bit 12
		"CapEff:\t0000000000000c00\n":                       true,
		"Name:\tx\nCapEff:\t000001ffffffffff\nCapBnd:\t0\n": true,
		"CapEff:\t0000000000000000\n":                       false,
		"CapInh:\t0000000000000400\n":                       false,
		"":                                                  false,
	} {
		if got := capabilityBit(status); got != want {
			t.Errorf("capabilityBit(%q) = %v", status, got)
		}
	}
	if os.Getuid() != 0 {
		for value, want := range map[string]bool{"1": true, "true": true, " YES ": true, "y": true, "0": false, "": false} {
			getenv := func(string) string { return value }
			if got := CanBindLowPorts(getenv); got != want && !(mayBindAnyway() && !want) {
				t.Errorf("BETTERDESK_HAS_BIND_SERVICE=%q: %v", value, got)
			}
		}
	}
}

// mayBindAnyway reports whether the process may bind low ports anyway (its own
// capabilities say so), which makes the negative cases unprovable here.
func mayBindAnyway() bool { return CanBindLowPorts(func(string) string { return "" }) }

func TestBindErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, err = net.Listen("tcp", ln.Addr().String())
	if !InUse(err) || Denied(err) {
		t.Errorf("taken port: in use %v, denied %v (%v)", InUse(err), Denied(err), err)
	}
	denied := &net.OpError{Op: "listen", Err: os.NewSyscallError("bind", syscall.EACCES)}
	if !Denied(denied) || InUse(denied) {
		t.Errorf("EACCES: denied %v, in use %v", Denied(denied), InUse(denied))
	}
	if InUse(errors.New("other")) || Denied(nil) {
		t.Error("other errors are neither")
	}
}

func TestPlainHTTPOnTLSIsClosedWithoutAnAnswer(t *testing.T) {
	c := makeCerts(t)
	f, _ := ReadFiles(c.selfSigned, c.selfKey, "", func(string, ...any) {})
	cert, _ := f.Certificate()
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "over TLS") }),
		ErrorLog: ErrorLog("test"), ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(TLSListener(inner, ServerTLS(cert))) }()
	defer srv.Close()
	addr := inner.Addr().String()
	for _, text := range []string{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", "POST / HTTP/1.1\r\nHost: x\r\n\r\n", "HEAD / HTTP/1.1\r\n\r\n", "PUT / HTTP/1.1\r\n\r\n", "OPTIONS * HTTP/1.1\r\n\r\n"} {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(conn, text)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		got, err := io.ReadAll(conn)
		conn.Close()
		if len(got) != 0 || err != nil {
			t.Errorf("%q: got %q, %v; want the connection closed without a byte", text, got, err)
		}
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // test certificate
	res, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "over TLS" || res.ProtoMajor != 1 {
		t.Errorf("over TLS: %q %s", body, res.Proto)
	}
}

func TestRequestTimeoutBeforeTheFirstRequest(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Served(r)
		_, _ = io.WriteString(w, "ok")
	}), ReadHeaderTimeout: 300 * time.Millisecond, IdleTimeout: 300 * time.Millisecond, ConnContext: ConnContext}
	go func() { _ = srv.Serve(PlainListener(inner)) }()
	defer srv.Close()
	exchange := func(text string) string {
		conn, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, text)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		got, _ := io.ReadAll(conn)
		return string(got)
	}
	for _, text := range []string{"", "GET / HT", "GET / HTTP/1.1\r\nHost: x\r\n"} {
		if got := exchange(text); got != requestTimeout {
			t.Errorf("%q: got %q, want the 408", text, got)
		}
	}
	// After a request was served, a timeout closes without a word
	got := exchange("GET / HTTP/1.1\r\nHost: x\r\n\r\nGET / HT")
	if !strings.HasPrefix(got, "HTTP/1.1 200 OK") || strings.Contains(got, "408") {
		t.Errorf("kept-alive connection: %q", got)
	}
}

// The forward names the client by the address the console resolves under
// its own TRUST_PROXY, as X-Real-IP and as the only X-Forwarded-For entry:
// what a client writes into those headers reaches the API only through a
// hop the console trusts.
func TestForwardNamesTheClientTheConsoleResolves(t *testing.T) {
	var seen http.Header
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.Header.Clone() }))
	defer api.Close()
	origin, _ := url.Parse(api.URL)
	for _, c := range []struct {
		trust, remote, xff, xri, want string
	}{
		{"", "203.0.113.9:4000", "198.51.100.1", "", "203.0.113.9"},
		{"", "203.0.113.9:4000", "", "198.51.100.2", "203.0.113.9"},
		{"", "203.0.113.9:4000", "198.51.100.1, 192.0.2.7", "198.51.100.2", "203.0.113.9"},
		{"loopback", "203.0.113.9:4000", "198.51.100.1", "", "203.0.113.9"},
		{"loopback", "127.0.0.1:4000", "198.51.100.1, 192.0.2.7", "", "192.0.2.7"},
		{"loopback", "127.0.0.1:4000", "", "198.51.100.2", "127.0.0.1"},
		{"true", "127.0.0.1:4000", "198.51.100.1, 192.0.2.7", "", "192.0.2.7"},
		{"2", "127.0.0.1:4000", "198.51.100.1, 192.0.2.7", "", "198.51.100.1"},
	} {
		trust, err := httpx.ParseTrustProxy(c.trust)
		if err != nil {
			t.Fatal(err)
		}
		f := newAPIForward(origin, false, trust.ClientIP)
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.RemoteAddr = c.remote
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.xri != "" {
			req.Header.Set("X-Real-IP", c.xri)
		}
		seen = nil
		f.ServeHTTP(httptest.NewRecorder(), req)
		f.close()
		if seen == nil {
			t.Fatalf("%+v: the request did not reach the API", c)
		}
		if xff, xri := seen.Values("X-Forwarded-For"), seen.Values("X-Real-IP"); len(xff) != 1 || xff[0] != c.want || len(xri) != 1 || xri[0] != c.want {
			t.Errorf("TRUST_PROXY=%q from %s with X-Forwarded-For %q and X-Real-IP %q: the API saw X-Forwarded-For %q and X-Real-IP %q, want %q",
				c.trust, c.remote, c.xff, c.xri, xff, xri, c.want)
		}
	}
}

func TestForwardQuery(t *testing.T) {
	for target, want := range map[string]string{
		"/api/health":                 "",
		"/api/health?":                "",
		"/api/health?a=1&b=%41&c=%zz": "a=1&b=%41&c=%zz",
		"/api/health?a=<>\"'`{}|\\^":  "a=%3C%3E%22%27`{}|\\^",
		"/api/health?a=1#frag":        "a=1",
		"/api/health#frag?x=1":        "",
		"/api/health?a=\x7f":          "a=%7F",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RequestURI = target
		if got := forwardQuery(r); got != want {
			t.Errorf("forwardQuery(%q) = %q, want %q", target, got, want)
		}
	}
}
