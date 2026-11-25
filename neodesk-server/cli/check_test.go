package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

type checkVectors struct {
	ParseArgs []struct {
		Args      []string  `json:"args"`
		APIURL    *string   `json:"apiUrl"`
		PanelURL  *string   `json:"panelUrl"`
		ProxyURL  *string   `json:"proxyUrl"`
		Ports     []*string `json:"ports"`
		TimeoutMs float64   `json:"timeoutMs"`
		Insecure  bool      `json:"insecure"`
		Help      bool      `json:"help"`
		Error     string    `json:"error"`
	} `json:"parseArgs"`
	Hostname []struct {
		SAN   string `json:"san"`
		Host  string `json:"host"`
		Match bool   `json:"match"`
	} `json:"hostname"`
	Identity []struct {
		SAN   string  `json:"san"`
		Host  string  `json:"host"`
		CN    *string `json:"cn"`
		Error *string `json:"error"`
	} `json:"identity"`
	Runs []struct {
		Args   []string `json:"args"`
		Status int      `json:"status"`
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
	} `json:"runs"`
}

func loadCheckVectors(t *testing.T) checkVectors {
	t.Helper()
	var v checkVectors
	readVectors(t, "protocolcheck", &v)
	return v
}

// The arguments are read as the script's parseArgs read them; a URL that
// is missing or empty is left out, as filter(Boolean) left it out. The
// script knew no --api, --panel and --proxy: lists it refused for them
// are not compared.
func TestCheckArgumentsLikeNode(t *testing.T) {
	v := loadCheckVectors(t)
	if len(v.ParseArgs) < 1000 {
		t.Fatalf("only %d vectors", len(v.ParseArgs))
	}
	compared := 0
	for _, c := range v.ParseArgs {
		switch c.Error {
		case "Unknown option: --api", "Unknown option: --panel", "Unknown option: --proxy":
			continue
		}
		compared++
		cfg, err := parseCheckArgs(c.Args)
		if c.Error != "" || err != nil {
			if err == nil || err.Error() != c.Error {
				t.Errorf("%q: error %v, Node %q", c.Args, err, c.Error)
			}
			continue
		}
		var want []string
		for _, u := range []*string{c.APIURL, c.PanelURL, c.ProxyURL} {
			if u != nil && *u != "" {
				// The one difference: the console's default port is 3375
				// since 3.6.0-rc.5 (DECISIONS A250), 5000 in the script
				want = append(want, strings.Replace(*u, "http://127.0.0.1:5000/health", "http://127.0.0.1:3375/health", 1))
			}
		}
		if strings.Join(cfg.urls, "\n") != strings.Join(want, "\n") {
			t.Errorf("%q: URLs %q, Node %q", c.Args, cfg.urls, want)
		}
		if len(cfg.ports) != len(c.Ports) {
			t.Errorf("%q: ports %+v, Node %v", c.Args, cfg.ports, c.Ports)
		} else {
			for i, p := range c.Ports {
				if (p == nil) == cfg.ports[i].given || (p != nil && *p != cfg.ports[i].text) {
					t.Errorf("%q: port %d %+v, Node %v", c.Args, i, cfg.ports[i], p)
				}
			}
		}
		if cfg.timeoutMs != c.TimeoutMs || cfg.insecure != c.Insecure || cfg.help != c.Help {
			t.Errorf("%q: timeout %v insecure %v help %v, Node %v %v %v", c.Args, cfg.timeoutMs, cfg.insecure, cfg.help, c.TimeoutMs, c.Insecure, c.Help)
		}
		if cfg.timeout != time.Duration(c.TimeoutMs*float64(time.Millisecond)) {
			t.Errorf("%q: timeout %v", c.Args, cfg.timeout)
		}
	}
	if compared < 1000 {
		t.Fatalf("only %d vectors compared", compared)
	}
}

// ipv6Matches are the IPv6 hosts (as URL.hostname gives them, and
// without the brackets) with the certificate entry that is the same
// address. The script compared the bracketed host with the names as text
// and never matched them; the check compares the address (BN9).
var ipv6Matches = map[string]string{
	"[::1]":            "IP Address:0:0:0:0:0:0:0:1",
	"::1":              "IP Address:0:0:0:0:0:0:0:1",
	"::ffff:127.0.0.1": "IP Address:0:0:0:0:0:FFFF:7F00:1",
}

func TestCertificateHostnameLikeNode(t *testing.T) {
	v := loadCheckVectors(t)
	changed := 0
	for _, c := range v.Hostname {
		want := c.Match
		if entry, ok := ipv6Matches[c.Host]; ok && strings.Contains(c.SAN, entry) {
			want = true
			changed++
		}
		if got := certificateHostname(c.SAN, c.Host); got != want {
			t.Errorf("%q for %q: %v, want %v (Node %v)", c.SAN, c.Host, got, want, c.Match)
		}
	}
	if len(v.Hostname) < 500 {
		t.Fatalf("only %d vectors", len(v.Hostname))
	}
	if changed == 0 {
		t.Error("no IPv6 vector with its own address: BN9 is not covered")
	}
	t.Logf("%d IPv6 vectors match their address where Node's script did not (BN9)", changed)
}

// An HTTPS check of an IPv6 URL passes when the certificate names the
// address (BN9).
func TestCertificateHostnameOfIPv6(t *testing.T) {
	for _, c := range []struct {
		san, host string
		want      bool
	}{
		{"IP Address:0:0:0:0:0:0:0:1", "[::1]", true},
		{"DNS:localhost, IP Address:0:0:0:0:0:0:0:1", "[::1]", true},
		{"IP Address:2001:DB8:0:0:0:0:0:7", "[2001:db8::7]", true},
		{"IP Address:2001:DB8:0:0:0:0:0:8", "[2001:db8::7]", false},
		{"IP Address:127.0.0.1", "[::1]", false},
		{"DNS:localhost", "[::1]", false},
		{"IP Address:127.0.0.1", "127.0.0.1", true},
		{"IP Address:127.0.0.2", "127.0.0.1", false},
	} {
		if got := certificateHostname(c.san, c.host); got != c.want {
			t.Errorf("%q for %q: %v, want %v", c.san, c.host, got, c.want)
		}
	}
}

// Node's check of the certificate's names, messages and all. A host in
// Punycode or with letters beyond ASCII is not compared: Node maps it with
// the IDNA tables, which the check does not carry.
func TestServerIdentityLikeNode(t *testing.T) {
	v := loadCheckVectors(t)
	for _, c := range v.Identity {
		if strings.HasPrefix(c.Host, "xn--") || strings.Contains(c.Host, ".xn--") || !isASCII(c.Host) {
			continue
		}
		cn := ""
		if c.CN != nil {
			cn = *c.CN
		}
		got := serverIdentity(c.Host, c.SAN, cn)
		want := ""
		if c.Error != nil {
			want = strings.TrimPrefix(*c.Error, "Hostname/IP does not match certificate's altnames: ")
		}
		if got != want {
			t.Errorf("%q (CN %q) for %q:\n got %q\nwant %q", c.SAN, cn, c.Host, got, want)
		}
	}
	if len(v.Identity) < 2000 {
		t.Fatalf("only %d vectors", len(v.Identity))
	}
}

// checkRunDifferences are recorded runs whose answer depends on the server
// the oracle ran, not on the check.
var checkRunDifferences = map[string]string{
	"--api-url https://127.0.0.1:{HTTP}/health --panel-url http://127.0.0.1:{GOOD}/health": "TLS to a plain HTTP server fails with OpenSSL's text, " +
		"which names a thread; a Go HTTPS server answers plain HTTP with 400 where Node's closed",
}

// checkRunFixes are the recorded runs that stopped at a check the script
// could not start and printed only "FAIL <message>" on standard error.
// The check prints that as the check's own FAIL line and goes on with the
// others (BN10): the standard output it gives instead, with exit 1 and
// nothing on standard error.
var checkRunFixes = map[string]string{
	"--api-url not a url --panel-url http://127.0.0.1:{HTTP}/health": "FAIL not a url: Invalid URL\nPASS http://127.0.0.1:{HTTP}/health (200)\n",
	"--api-url http://127.0.0.1:{HTTP}/health --panel-url http://":   "PASS http://127.0.0.1:{HTTP}/health (200)\nFAIL http://: Invalid URL\n",
	"--api-url  --panel-url  --port abc":                             "FAIL abc: Port should be >= 0 and < 65536. Received type number (NaN).\n",
	"--api-url  --panel-url  --port 127.0.0.1:":                      "FAIL 127.0.0.1:: Port should be >= 0 and < 65536. Received type number (NaN).\n",
	"--api-url  --panel-url  --port 127.0.0.1:70000":                 "FAIL 127.0.0.1:70000: Port should be >= 0 and < 65536. Received type number (70000).\n",
	"--api-url  --panel-url  --port 127.0.0.1:1.5":                   "FAIL 127.0.0.1:1.5: Port should be >= 0 and < 65536. Received type number (1.5).\n",
	"--api-url  --panel-url  --port 127.0.0.1:{TCP} --port":          "PASS 127.0.0.1:{TCP}\nFAIL undefined: Port should be >= 0 and < 65536. Received type number (NaN).\n",
}

// checkServers starts what the oracle's runs talked to and answers the
// placeholders' ports.
func checkServers(t *testing.T) map[string]int {
	t.Helper()
	ports := map[string]int{}
	listen := func(name string) net.Listener {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = l.Close() })
		ports[name] = l.Addr().(*net.TCPAddr).Port
		return l
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/health")
			w.WriteHeader(302)
		case "/redirect-nolocation":
			w.WriteHeader(301)
		case "/error":
			w.WriteHeader(500)
		case "/notfound":
			w.WriteHeader(404)
		case "/created":
			w.WriteHeader(201)
		case "/nocontent":
			w.WriteHeader(204)
		case "/hang":
			<-r.Context().Done()
		case "/close":
			conn, _, err := http.NewResponseController(w).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	})
	serve := func(l net.Listener) {
		srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		go func() { _ = srv.Serve(l) }()
		t.Cleanup(func() { _ = srv.Close() })
	}
	serve(listen("HTTP"))
	for _, name := range []string{"good", "wronghost", "expired", "self", "cnonly"} {
		cert, err := tls.LoadX509KeyPair(filepath.Join("testdata", "certs", name+".pem"), filepath.Join("testdata", "certs", name+"-key.pem"))
		if err != nil {
			t.Fatal(err)
		}
		serve(tls.NewListener(listen(strings.ToUpper(name)), &tls.Config{Certificates: []tls.Certificate{cert}}))
	}
	// A server that is no HTTP server: it answers once the request is
	// there (Go's client takes bytes before its request was sent as a
	// broken connection, where Node's parsed them), and closes
	accept := func(l net.Listener, answer string) {
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					return
				}
				if answer != "" {
					_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					_, _ = conn.Read(make([]byte, 4096))
					_, _ = conn.Write([]byte(answer))
				}
				_ = conn.Close()
			}
		}()
	}
	accept(listen("TCP"), "")
	accept(listen("GARBAGE"), "NOT HTTP\r\n\r\n")
	closed := listen("CLOSED")
	_ = closed.Close()
	return ports
}

// Whole runs of the script against servers like the oracle's: the same
// lines, the same exit status.
func TestCheckRunsLikeNode(t *testing.T) {
	v := loadCheckVectors(t)
	if len(v.Runs) < 35 {
		t.Fatalf("only %d runs", len(v.Runs))
	}
	ports := checkServers(t)
	ca, err := os.ReadFile(filepath.Join("testdata", "certs", "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	placeholder := regexp.MustCompile(`\{([A-Z]+)\}`)
	fill := func(s string) string {
		return placeholder.ReplaceAllStringFunc(s, func(m string) string {
			name := m[1 : len(m)-1]
			if name == "TCPHEX" {
				return strconv.FormatInt(int64(ports["TCP"]), 16)
			}
			return strconv.Itoa(ports[name])
		})
	}
	hide := func(s string) string {
		s = strings.ReplaceAll(s, "0x"+strconv.FormatInt(int64(ports["TCP"]), 16), "0x{TCPHEX}")
		for name, port := range ports {
			p := strconv.Itoa(port)
			s = strings.ReplaceAll(s, ":"+p, ":{"+name+"}")
			s = strings.ReplaceAll(s, ": "+p+" ", ": {"+name+"} ")
			s = strings.ReplaceAll(s, " "+p+"\n", " {"+name+"}\n")
		}
		return s
	}
	fixed := 0
	for _, run := range v.Runs {
		name := strings.Join(run.Args, " ")
		if reason, ok := checkRunDifferences[name]; ok {
			t.Logf("%s differs on purpose: %s", name, reason)
			continue
		}
		if stdout, ok := checkRunFixes[name]; ok {
			run.Stdout, run.Stderr = stdout, ""
			fixed++
		}
		// The console's default port is 3375 since 3.6.0-rc.5 (DECISIONS
		// A250), 5000 in the script
		run.Stdout = strings.ReplaceAll(run.Stdout, "127.0.0.1:5000", "127.0.0.1:3375")
		run.Stderr = strings.ReplaceAll(run.Stderr, "127.0.0.1:5000", "127.0.0.1:3375")
		args := make([]string, len(run.Args))
		for i, a := range run.Args {
			args[i] = fill(a)
		}
		env, out, errOut := testEnv(nil)
		status := check(args, env, checkOptions{roots: roots, now: time.Now, resolve: resolveLikeTheOracle})
		if status != run.Status {
			t.Errorf("%s: exit %d, Node %d", name, status, run.Status)
		}
		if strings.HasPrefix(run.Stdout, "Usage:") {
			if !strings.HasPrefix(out.String(), "Usage: neodesk-server check") {
				t.Errorf("%s: %q", name, out.String())
			}
			continue
		}
		if got := hide(out.String()); got != run.Stdout {
			t.Errorf("%s: stdout\n got %q\nwant %q", name, got, run.Stdout)
		}
		if got := hide(errOut.String()); got != run.Stderr {
			t.Errorf("%s: stderr\n got %q\nwant %q", name, got, run.Stderr)
		}
	}
	if fixed != len(checkRunFixes) {
		t.Errorf("%d of the %d runs of BN10 were recorded", fixed, len(checkRunFixes))
	}
}

func TestJSInspectNumber(t *testing.T) {
	for _, c := range []struct {
		f    float64
		want string
	}{{math.NaN(), "NaN"}, {70000, "70000"}, {1.5, "1.5"}, {math.Copysign(0, -1), "-0"}, {math.Inf(1), "Infinity"}, {1e21, "1e+21"}} {
		if got := jsInspectNumber(c.f); got != c.want {
			t.Errorf("%v: %q, want %q", c.f, got, c.want)
		}
	}
}

// resolveLikeTheOracle has the name the oracle put into /etc/hosts with
// two addresses; other names go to the system's resolver.
func resolveLikeTheOracle(ctx context.Context, host string) ([]string, error) {
	if host == "multi.test" {
		return []string{"127.0.0.2", "127.0.0.3"}, nil
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}
