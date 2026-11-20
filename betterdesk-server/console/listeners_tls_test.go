package console

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/discovery"
	"github.com/unitronix/betterdesk-server/console/listen"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// goSuites are the OpenSSL names of the probed TLS 1.2 suites in Go.
var goSuites = map[string]uint16{
	"ECDHE-RSA-AES128-GCM-SHA256":   tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-RSA-AES256-GCM-SHA384":   tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-RSA-CHACHA20-POLY1305":   tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-RSA-AES128-SHA256":       tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-RSA-AES128-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
	"ECDHE-RSA-AES256-SHA":          tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
	"ECDHE-RSA-DES-CBC3-SHA":        tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
	"ECDHE-RSA-RC4-SHA":             tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
	"ECDHE-ECDSA-AES128-GCM-SHA256": tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	"ECDHE-ECDSA-AES256-GCM-SHA384": tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	"ECDHE-ECDSA-CHACHA20-POLY1305": tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	"ECDHE-ECDSA-AES128-SHA256":     tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
	"ECDHE-ECDSA-AES128-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
	"ECDHE-ECDSA-AES256-SHA":        tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	"ECDHE-ECDSA-RC4-SHA":           tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
	"AES128-GCM-SHA256":             tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
	"AES256-GCM-SHA384":             tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	"AES128-SHA256":                 tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
	"AES128-SHA":                    tls.TLS_RSA_WITH_AES_128_CBC_SHA,
	"AES256-SHA":                    tls.TLS_RSA_WITH_AES_256_CBC_SHA,
	"DES-CBC3-SHA":                  tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
	"RC4-SHA":                       tls.TLS_RSA_WITH_RC4_128_SHA,
}

var goVersions = map[string]uint16{"TLSv1": tls.VersionTLS10, "TLSv1.1": tls.VersionTLS11, "TLSv1.2": tls.VersionTLS12, "TLSv1.3": tls.VersionTLS13}

// probeTLS reports whether a handshake with one suite or version works.
func probeTLS(addr string, c oracleCipher) bool {
	cfg := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"} //nolint:gosec // a test server's own certificate
	switch c.Version {
	case "TLSv1.2":
		cfg.MinVersion, cfg.MaxVersion, cfg.CipherSuites = tls.VersionTLS12, tls.VersionTLS12, []uint16{goSuites[c.Suite]}
	case "TLSv1.3":
		cfg.MinVersion, cfg.MaxVersion = tls.VersionTLS13, tls.VersionTLS13
	case "default":
	default:
		cfg.MinVersion, cfg.MaxVersion = goVersions[c.Version], goVersions[c.Version]
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, cfg)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// checkCiphers probes what Node's TLS listener accepted and refused. A
// suite the recording's client could not even offer (no 3DES, no RC4 in
// its OpenSSL, nor in Node's default list) has to be refused by Go.
func checkCiphers(t *testing.T, addr string, ciphers []oracleCipher, cert string) int {
	t.Helper()
	n := 0
	for _, c := range ciphers {
		if c.Cert != cert {
			continue
		}
		if got := probeTLS(addr, c); got != c.OK {
			t.Errorf("%s %s %s: handshake %v, Node %v (%s)", cert, c.Version, c.Suite, got, c.OK, c.Error)
		}
		n++
	}
	return n
}

// chainOf lists the subjects of the certificates a TLS listener sends.
func chainOf(t *testing.T, addr string) []string {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}) //nolint:gosec // a test server's own certificate
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var out []string
	for _, c := range conn.ConnectionState().PeerCertificates {
		out = append(out, "CN = "+c.Subject.CommonName)
	}
	return out
}

// plainOnTLS sends plain bytes to a TLS port. Node never answered them in
// HTTP; where Node closed the connection, Go closes it too. The console's
// HTTPS port does not answer them either; the client API port answers a
// plain HTTP request with the hint Node meant to send and never did
// (BUGS LS4).
func plainOnTLS(t *testing.T, g *goStack, scenario oracleScenario, listener string) int {
	t.Helper()
	n, hints := 0, 0
	for _, e := range scenario.Exchanges {
		if e.Secure || e.Listener != listener {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		if bytes.HasPrefix(raw, []byte("HTTP/")) {
			t.Fatalf("%s: Node answered in plain HTTP: %q", e.ID, raw)
		}
		got, closed, _ := send(g.ports[listener], false, e.Parts, 4*time.Second)
		switch {
		case !bytes.HasPrefix(got, []byte("HTTP/")):
		case listener == "api":
			// The hint has its body also for HEAD
			if msg := plainHTTPHint(t, parseReplies(got, http.MethodGet), closed); msg != "" {
				t.Errorf("%s: %s", e.ID, msg)
			}
			hints++
		default:
			t.Errorf("%s: answered in plain HTTP: %q", e.ID, got)
		}
		if e.Closed && !closed {
			t.Errorf("%s: Node closed the connection, Go kept it open", e.ID)
		}
		n++
	}
	if listener == "api" && hints < 8 {
		t.Errorf("%d plain HTTP requests got the https hint", hints)
	}
	return n
}

var plainHTTPHintBody = regexp.MustCompile(`^\{"error":"RustDesk Client API on port ([0-9]+) requires HTTPS\. Use https://<server>:([0-9]+)\."\}$`)

// plainHTTPHint checks the client API port's answer to plain HTTP on TLS:
// 400 with the error that asks for https on its port, and a close.
func plainHTTPHint(_ *testing.T, got []reply, closed bool) string {
	if len(got) != 1 || got[0].status != http.StatusBadRequest || !closed {
		return fmt.Sprintf("want one 400 and a close, got %v closed %v", statuses(got), closed)
	}
	m := plainHTTPHintBody.FindStringSubmatch(string(got[0].body))
	if m == nil || m[1] != m[2] || got[0].header.Get("Content-Type") != "application/json; charset=utf-8" {
		return fmt.Sprintf("want the https hint, got %q %v", got[0].body, got[0].header)
	}
	return ""
}

func secureOnly(s oracleScenario) oracleScenario {
	return oracleScenario{Name: s.Name, Env: s.Env, Exchanges: filter(s.Exchanges, func(e oracleExchange) bool { return e.Secure })}
}

func filter(list []oracleExchange, keep func(oracleExchange) bool) []oracleExchange {
	var out []oracleExchange
	for _, e := range list {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func TestClientAPITLSAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	scenario := oracleScenarioNamed(t, "api tls")
	g, err := newGoStack(t, db, scenario, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkReplay(t, "api tls", g.replayExchanges(t, secureOnly(scenario), nil))
	n := plainOnTLS(t, g, scenario, "api")
	n += checkCiphers(t, g.ports["api"], scenario.Ciphers, "rsa")
	if got := chainOf(t, g.ports["api"]); strings.Join(got, "|") != strings.Join(scenario.Chain, "|") {
		t.Errorf("chain %q, Node %q", got, scenario.Chain)
	}
	ecdsa := oracleScenarioNamed(t, "api tls ecdsa")
	g, err = newGoStack(t, db, ecdsa, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	n += checkCiphers(t, g.ports["api"], ecdsa.Ciphers, "ecdsa")
	if n < 70 {
		t.Errorf("only %d plain and handshake probes", n)
	}
}

// TestClientAPITLSModes: RUSTDESK_API_TLS. A plain request gets 204 where
// the port stayed on HTTP, nothing where it had TLS.
func TestClientAPITLSModes(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	f, err := loadOracle()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, scenario := range f.Scenarios {
		if !strings.HasPrefix(scenario.Name, "api tls mode") {
			continue
		}
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatalf("%s: %v", scenario.Name, err)
		}
		known := map[string]func(t *testing.T, got []reply, closed bool) string{}
		if g.s.net.apiCert != nil {
			// Plain HTTP on the port with TLS gets the https hint (BUGS LS4)
			for _, e := range scenario.Exchanges {
				if !e.Secure {
					known[e.ID] = plainHTTPHint
				}
			}
		}
		checkReplay(t, scenario.Name, g.replayExchanges(t, scenario, known))
		n++
	}
	if n != 11 {
		t.Errorf("%d modes replayed", n)
	}
}

// csrfCookie is the CSRF cookie of an answer without its value.
func csrfCookie(rs []reply) string {
	for _, r := range rs {
		for _, line := range r.header.Values("Set-Cookie") {
			if name, rest, ok := strings.Cut(line, "="); ok && strings.HasPrefix(name, "__csrf") {
				_, attrs, _ := strings.Cut(rest, ";")
				return name + ";" + attrs
			}
		}
	}
	return ""
}

// checkConsoleCookies compares the CSRF cookie of the console's answers:
// its name and its attributes (Secure among them). plain says the console
// fell back to plain HTTP in HTTPS mode: Node kept Secure on the cookie
// there, which browsers then drop, and Go leaves it off (BUGS LS3).
func checkConsoleCookies(t *testing.T, g *goStack, name string, exchanges []oracleExchange, listener string, plain bool) {
	t.Helper()
	if len(exchanges) == 0 {
		t.Fatalf("%s: no console exchanges", name)
	}
	for _, e := range exchanges {
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		want := csrfCookie(parseReplies(raw, methodOf(e.Parts)))
		if plain {
			want = strings.Replace(want, "; Secure", "", 1)
		}
		got, _, _ := send(g.ports[listener], e.Secure, e.Parts, 6*time.Second)
		if have := csrfCookie(parseReplies(got, methodOf(e.Parts))); have != want || want == "" {
			t.Errorf("%s %s: CSRF cookie %q, Node %q", name, e.ID, have, want)
		}
	}
}

// knownRedirect are the redirect requests where Go answers otherwise.
var knownRedirect = map[string]func(t *testing.T, got []reply, closed bool) string{
	// A Host header that is no host name or two Host headers: net/http
	// refuses them with 400 where Node redirected (to its own host)
	`redirect host "evil.com@good.com"`: statusIs(http.StatusBadRequest),
	`redirect host "good.com/evil"`:     statusIs(http.StatusBadRequest),
	`redirect host "good.com?x"`:        statusIs(http.StatusBadRequest),
	`redirect host "good.com#x"`:        statusIs(http.StatusBadRequest),
	`redirect host "good.com\\evil"`:    statusIs(http.StatusBadRequest),
	`redirect host "good.com evil"`:     statusIs(http.StatusBadRequest),
	"redirect host \"gööd.com\"":        statusIs(http.StatusBadRequest),
	`redirect host "a\"b"`:              statusIs(http.StatusBadRequest),
	`redirect host "a<b>"`:              statusIs(http.StatusBadRequest),
	"redirect two hosts":                statusIs(http.StatusBadRequest),
	// Targets net/http refuses: a space (Node refused it too, without a
	// body) and a broken escape (Node redirected to it encoded)
	`redirect target "/a b"`: statusIs(http.StatusBadRequest),
	`redirect target "/%zz"`: statusIs(http.StatusBadRequest),
}

// loadRedirectOracle is listen/testdata/node_redirect.json: the plain
// port of upstream 3.5.264's server.js (audit H5) with different public
// hosts, recorded by listen/testdata/nodeoracle/redirect.js.
var loadRedirectOracle = sync.OnceValues(func() (*oracleFile, error) {
	raw, err := os.ReadFile(filepath.Join("listen", "testdata", "node_redirect.json"))
	if err != nil {
		return nil, err
	}
	var f oracleFile
	err = json.Unmarshal(raw, &f)
	return &f, err
})

// configuredPublicHost is the PANEL_PUBLIC_HOST a redirect scenario gives
// the console: from the environment, else from the public endpoints file.
func configuredPublicHost(scenario oracleScenario) string {
	if value := scenario.Env["PANEL_PUBLIC_HOST"]; value != "" || scenario.Durable == nil {
		return value
	}
	for _, line := range strings.Split(*scenario.Durable, "\n") {
		if value, ok := strings.CutPrefix(line, "PANEL_PUBLIC_HOST="); ok {
			return value
		}
	}
	return ""
}

// publicHostPage is the known difference of a redirect scenario whose
// PANEL_PUBLIC_HOST names no host and whose console binds every address:
// where Node redirected to localhost, the plain port answers the page that
// asks to set PANEL_PUBLIC_HOST (BUGS UM41, DECISIONS A69).
func publicHostPage(method string) func(t *testing.T, got []reply, closed bool) string {
	return func(t *testing.T, got []reply, closed bool) string {
		if len(got) != 1 || got[0].status != http.StatusOK || got[0].header.Get("Location") != "" ||
			got[0].header.Get("Content-Type") != "text/html; charset=utf-8" ||
			method != http.MethodHead && !bytes.Contains(got[0].body, []byte("set PANEL_PUBLIC_HOST")) {
			return fmt.Sprintf("want the page that asks to set PANEL_PUBLIC_HOST, got %d answers (closed %v)", len(got), closed)
		}
		return ""
	}
}

// TestRedirectAgainstNode replays the redirect recording: the plain port
// sends every request to the host chosen at the start (PANEL_PUBLIC_HOST
// from the environment, else from the public endpoints file), whatever
// Host header and target the request had. Without a PANEL_PUBLIC_HOST
// that names a host it redirects to the address the console binds, as
// Node did; a console that binds every address, where Node used
// localhost, answers a page that asks to set it.
func TestRedirectAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	f, err := loadRedirectOracle()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Scenarios) < 11 {
		t.Fatalf("only %d redirect scenarios", len(f.Scenarios))
	}
	ipv6, pages := 0, 0
	for _, scenario := range f.Scenarios {
		known, page := knownRedirect, false
		if _, source := listen.RedirectHost(configuredPublicHost(scenario), scenario.Env["HOST"]); source == "fallback" {
			pages, page = pages+1, true
			known = map[string]func(t *testing.T, got []reply, closed bool) string{}
			for _, e := range scenario.Exchanges {
				raw, _ := base64.StdEncoding.DecodeString(e.Got)
				method := methodOf(e.Parts)
				if check, ok := knownRedirect[e.ID]; ok {
					known[e.ID] = check
				} else if replies := parseReplies(raw, method); len(replies) == 1 && replies[0].status == http.StatusTemporaryRedirect {
					known[e.ID] = publicHostPage(method)
				}
			}
		}
		// An IPv6 host goes into the redirect in brackets, where Node put it
		// in bare (BUGS UM40)
		if host, _ := listen.RedirectHost(scenario.Env["PANEL_PUBLIC_HOST"], scenario.Env["HOST"]); strings.Contains(host, ":") {
			ipv6++
			exchanges := make([]oracleExchange, len(scenario.Exchanges))
			for i, e := range scenario.Exchanges {
				raw, _ := base64.StdEncoding.DecodeString(e.Got)
				e.Got = base64.StdEncoding.EncodeToString(replaceInAnswers(raw, "https://"+host+":", "https://["+host+"]:"))
				exchanges[i] = e
			}
			scenario.Exchanges = exchanges
		}
		t.Run(scenario.Name, func(t *testing.T) {
			var logged bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logged)
			g, err := newGoStack(t, db, scenario, certs, nil)
			log.SetOutput(previous)
			if err != nil {
				t.Fatal(err)
			}
			if g.ports["redirect"] == "" || g.ports["https"] == "" {
				t.Fatalf("listeners %v", g.ports)
			}
			// The start log warns when the plain port answers the page
			if warned := strings.Contains(logged.String(), "the plain HTTP port"); warned != page {
				t.Errorf("warned about PANEL_PUBLIC_HOST %v, want %v; log:\n%s", warned, page, logged.String())
			}
			// The redirects name the recording's HTTPS port
			_, httpsPort, _ := net.SplitHostPort(g.ports["https"])
			g.ports["redirect"] = portRewriter(t, g.ports["redirect"], ":"+httpsPort, ":25443")
			res := g.replayExchanges(t, scenario, known)
			checkReplay(t, scenario.Name, res)
			compared := res.same
			if page {
				compared += res.differ
			}
			if compared < len(scenario.Exchanges)*3/4 {
				t.Errorf("only %d of %d redirects compared", compared, len(scenario.Exchanges))
			}
		})
	}
	if ipv6 == 0 {
		t.Error("no redirect scenario with an IPv6 host")
	}
	if pages == 0 {
		t.Error("no redirect scenario without a public host")
	}
}

var contentLength = regexp.MustCompile(`(?mi)^Content-Length: *([0-9]+)\r$`)

// replaceInAnswers replaces from with to in the head and the body of each
// HTTP answer in raw, with the Content-Length of the new body.
func replaceInAnswers(raw []byte, from, to string) []byte {
	var out []byte
	for len(raw) > 0 {
		end := bytes.Index(raw, []byte("\r\n\r\n"))
		if end < 0 {
			return append(out, raw...)
		}
		head, rest := string(raw[:end+4]), raw[end+4:]
		n := 0
		if m := contentLength.FindStringSubmatch(head); m != nil {
			n, _ = strconv.Atoi(m[1])
		}
		n = min(n, len(rest))
		body := bytes.ReplaceAll(rest[:n], []byte(from), []byte(to))
		head = contentLength.ReplaceAllString(strings.ReplaceAll(head, from, to), "Content-Length: "+strconv.Itoa(len(body))+"\r")
		out = append(append(out, head...), body...)
		raw = rest[n:]
	}
	return out
}

func TestConsoleHTTPSAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	scenario := oracleScenarioNamed(t, "https")
	g, err := newGoStack(t, db, scenario, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.ports["redirect"] == "" || g.ports["https"] == "" {
		t.Fatalf("listeners %v", g.ports)
	}
	checkConsoleCookies(t, g, "https", filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "https" && e.Secure }), "https", false)
	plainOnTLS(t, g, scenario, "https")
	checkCiphers(t, g.ports["https"], scenario.Ciphers, "rsa")
	if got := chainOf(t, g.ports["https"]); strings.Join(got, "|") != strings.Join(scenario.Chain, "|") {
		t.Errorf("chain %q, Node %q", got, scenario.Chain)
	}
	if got := chainOf(t, g.ports["api"]); strings.Join(got, "|") != strings.Join(scenario.APIChain, "|") {
		t.Errorf("client API chain %q, Node %q", got, scenario.APIChain)
	}
}

// portRewriter is a TCP relay in front of addr that rewrites from to to in
// what comes back, so an answer that names this run's port reads as the
// recording's. Both ports have five digits, so lengths stay right.
func portRewriter(t *testing.T, addr, from, to string) string {
	t.Helper()
	if len(from) != len(to) {
		t.Fatalf("port %s has not five digits", from)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				server, err := net.Dial("tcp", addr)
				if err != nil {
					return
				}
				defer server.Close()
				go func() {
					_, _ = io.Copy(server, client)
					if tc, ok := server.(*net.TCPConn); ok {
						_ = tc.CloseWrite()
					}
				}()
				all, _ := io.ReadAll(server)
				_, _ = client.Write(bytes.ReplaceAll(all, []byte(from), []byte(to)))
			}()
		}
	}()
	return ln.Addr().String()
}

func TestHTTPSSettingsAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, name := range []string{"https chain from SSL_CA_PATH", "https chain from SSL_CA_PATH intermediate only", "https missing SSL_CA_PATH file"} {
		scenario := oracleScenarioNamed(t, name)
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := chainOf(t, g.ports["https"]); strings.Join(got, "|") != strings.Join(scenario.Chain, "|") {
			t.Errorf("%s: chain %q, Node %q", name, got, scenario.Chain)
		}
		if scenario.APIChain != nil {
			if got := chainOf(t, g.ports["api"]); strings.Join(got, "|") != strings.Join(scenario.APIChain, "|") {
				t.Errorf("%s: client API chain %q, Node %q", name, got, scenario.APIChain)
			}
		}
	}
	for _, name := range []string{"https without redirect", "https redirect yes"} {
		scenario := oracleScenarioNamed(t, name)
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if open := g.s.net.redirectAddr != ""; open != *scenario.PlainPortOpen {
			t.Errorf("%s: redirect listener %v, Node %v", name, open, *scenario.PlainPortOpen)
		}
	}
	// HTTPS_ENABLED=True with HTTP_REDIRECT_HTTPS=TRUE: TestRedirectAgainstNode
}

// TestFallbackAgainstNode: HTTPS mode without usable certificate files
// runs on plain HTTP with the HTTPS cookie names, without Secure (Node
// kept Secure there: BUGS LS3).
func TestFallbackAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	f, err := loadOracle()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, scenario := range f.Scenarios {
		if !strings.HasPrefix(scenario.Name, "fallback") && scenario.Name != "http with certificates" {
			continue
		}
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatalf("%s: %v", scenario.Name, err)
		}
		if g.ports["console"] == "" || g.s.net.cert != nil || *scenario.HTTPSPortOpen {
			t.Fatalf("%s: listeners %v, Node's HTTPS port open %v", scenario.Name, g.ports, *scenario.HTTPSPortOpen)
		}
		checkConsoleCookies(t, g, scenario.Name, filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "console" }), "console", scenario.Env["HTTPS_ENABLED"] == "true")
		api := oracleScenario{Env: scenario.Env, Exchanges: filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "api" })}
		known := map[string]func(t *testing.T, got []reply, closed bool) string{}
		if g.s.net.apiCert != nil {
			// Plain HTTP on the port with TLS gets the https hint (BUGS LS4)
			for _, e := range api.Exchanges {
				if !e.Secure {
					known[e.ID] = plainHTTPHint
				}
			}
		}
		checkReplay(t, scenario.Name, g.replayExchanges(t, api, known))
		n++
	}
	if n != 6 {
		t.Errorf("%d fallback scenarios replayed", n)
	}
}

// TestHTTPSFallbackCookiesAreNotSecure: in HTTPS mode without a usable
// certificate the console serves plain HTTP. Its session, CSRF and guest
// cookies keep their HTTPS names without Secure, so a browser keeps the
// session and passes the CSRF gate, and its pages do not send the browser
// to HTTPS (BUGS LS3, S2).
func TestHTTPSFallbackCookiesAreNotSecure(t *testing.T) {
	w := goOnly(t, setup{https: true, build: true})
	if w.s.net.cert != nil {
		t.Fatal("a certificate loaded")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	// The client keeps the cookies between requests, as a browser does
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	do := func(method, path, token string) (*http.Response, map[string]any) {
		t.Helper()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader("{}")
		}
		req, err := http.NewRequest(method, w.front.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("X-CSRF-Token", token)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res, out
	}
	res, _ := do(http.MethodGet, "/__test/signin/admin", "")
	names := map[string]bool{}
	for _, line := range res.Header.Values("Set-Cookie") {
		name, _, _ := strings.Cut(line, "=")
		names[name] = true
		if strings.Contains(line, "; Secure") {
			t.Errorf("Secure cookie over the plain HTTP fallback: %s", line)
		}
	}
	if !names["betterdesk.sid"] || !names["__csrf"] {
		t.Errorf("cookies %v, want betterdesk.sid and __csrf", names)
	}
	if _, who := do(http.MethodGet, "/api/probe", ""); who["user"] != float64(roleIDs["admin"]) {
		t.Errorf("the next request is signed in as %v", who["user"])
	}
	_, token := do(http.MethodGet, "/__test/token", "")
	if res, _ := do(http.MethodPost, "/api/probe", fmt.Sprint(token["token"])); res.StatusCode != http.StatusOK {
		t.Errorf("a write with the page's token: %d", res.StatusCode)
	}
	guest := httptest.NewRecorder()
	w.s.deps.GuestAccess.SetCookie(guest, "t", nil)
	if line := guest.Header().Get("Set-Cookie"); !strings.HasPrefix(line, "betterdesk.guest=") || strings.Contains(line, "; Secure") {
		t.Errorf("guest cookie %q", line)
	}
	if res := w.agent().get("/login", "Accept", "text/html"); res.status != http.StatusOK || strings.Contains(res.header.Get("Content-Security-Policy"), "upgrade-insecure-requests") {
		t.Errorf("sign-in page %d, policy %q", res.status, res.header.Get("Content-Security-Policy"))
	}
}

// TestBrokenEscapeGetsTheConsolesAnswer: a request whose path has a
// broken percent escape, which net/http refuses before any handler, gets
// the console's 400 with the security headers (JSON under /api/) on the
// console's listener, plain and with TLS, also as the second request of a
// connection, which then closes (BUGS P2). The answer names the path
// segment that does not decode, as the old console's router did, also in
// production (S17).
func TestBrokenEscapeGetsTheConsolesAnswer(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	text := func(s string, delay int) oraclePart {
		data := base64.StdEncoding.EncodeToString([]byte(s))
		return oraclePart{Data: &data, Delay: delay}
	}
	for _, c := range []struct {
		scenario, listener string
		secure             bool
	}{{"proxy", "console", false}, {"https", "https", true}} {
		g, err := newGoStack(t, db, oracleScenarioNamed(t, c.scenario), certs, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, closed, _ := send(g.ports[c.listener], c.secure, []oraclePart{
			text("GET /api/nothing HTTP/1.1\r\nHost: localhost\r\n\r\n", 0),
			text("GET /api/devices/%zz HTTP/1.1\r\nHost: localhost\r\n\r\n", 300),
		}, 5*time.Second)
		answers := parseReplies(got, "GET")
		if len(answers) != 2 || !closed {
			t.Fatalf("%s: %d answers, closed %v: %q", c.scenario, len(answers), closed, got)
		}
		a := answers[1]
		if a.status != http.StatusBadRequest || a.header.Get("Content-Type") != "application/json; charset=utf-8" ||
			string(a.body) != `{"success":false,"error":"Failed to decode param '%zz'"}` {
			t.Errorf("%s: %d %q %s", c.scenario, a.status, a.header.Get("Content-Type"), a.body)
		}
		for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
			if a.header.Get(name) == "" || a.header.Get(name) != answers[0].header.Get(name) {
				t.Errorf("%s: %s %q, the 404 before it %q", c.scenario, name, a.header.Get(name), answers[0].header.Get(name))
			}
		}
	}
	// Outside production the answer says which segment does not decode
	w := goOnly(t, setup{})
	rec := httptest.NewRecorder()
	w.s.brokenEscape().ServeHTTP(rec, &http.Request{Method: "GET", RequestURI: "/api/devices/a%zz/b%yy?x=%zz", URL: &url.URL{Path: "/api/devices/a%zz/b%yy"}, Header: http.Header{}})
	if rec.Code != http.StatusBadRequest || rec.Body.String() != `{"success":false,"error":"Failed to decode param 'a%zz'"}` {
		t.Errorf("development: %d %s", rec.Code, rec.Body)
	}
}

// TestClientAPICertificateWarning: the start says the certificates of the
// client API port are invalid only when they did not load, whatever
// RUSTDESK_API_TLS turns off.
func TestClientAPICertificateWarning(t *testing.T) {
	certs := makeTestCerts(t)
	const invalid = "SSL certs configured but invalid"
	for _, c := range []struct {
		cert, key, mode string
		warns           bool
	}{
		{certs.files["rsa.crt"], certs.files["rsa.key"], "off", false},
		{certs.files["rsa.crt"], certs.files["rsa.key"], "0", false},
		{certs.files["rsa.crt"], certs.files["rsa.key"], "http", false},
		{certs.files["rsa.crt"], certs.files["rsa.key"], "false", false},
		{certs.files["rsa.crt"], certs.files["rsa.key"], "", false},
		{certs.files["rsa.crt"], "/nonexistent/c.key", "", true},
		{certs.files["rsa.crt"], "/nonexistent/c.key", "off", true},
		{certs.files["rsa.crt"], "/nonexistent/c.key", "false", false},
	} {
		var logged bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&logged)
		s := &Server{cfg: &config.Config{APIEnabled: true, APIPort: 21121, CertFile: c.cert, KeyFile: c.key, ClientAPITLS: c.mode}}
		err := s.loadCertificates()
		log.SetOutput(previous)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if warns := strings.Contains(logged.String(), invalid); warns != c.warns {
			t.Errorf("RUSTDESK_API_TLS=%q, key %s: warned %v, want %v; log:\n%s", c.mode, c.key, warns, c.warns, logged.String())
		}
	}
}

// TestLANAnnouncesTheConsoleListener: LAN discovery announces the port
// and scheme of the listener that serves the console: the HTTPS port with
// a certificate (not the plain port, which only redirects), and http on
// the plain port in HTTPS mode without a usable certificate.
func TestLANAnnouncesTheConsoleListener(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, c := range []struct {
		scenario string
		https    bool
		listener string
	}{
		{"https", true, "https"},
		{"fallback missing files", false, "console"},
		{"proxy", false, "console"},
	} {
		g, err := newGoStack(t, db, oracleScenarioNamed(t, c.scenario), certs, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := g.s.announced(discovery.Settings{})
		_, port, _ := net.SplitHostPort(g.ports[c.listener])
		if want, _ := strconv.Atoi(port); got.HTTPS != c.https || got.Port != float64(want) {
			t.Errorf("%s: announces https %v port %v, want https %v port %d", c.scenario, got.HTTPS, got.Port, c.https, want)
		}
	}
}

// TestStartFailuresAgainstNode: certificate files that are there but hold
// no certificate stop the start where Node exited; a bundle that is no
// certificate does not.
func TestStartFailuresAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, name := range []string{"fail garbage certificate", "fail key mismatch", "fail garbage certificate http mode",
		"garbage certificate api tls off", "fail garbage ca"} {
		scenario := oracleScenarioNamed(t, name)
		_, err := newGoStack(t, db, scenario, certs, nil)
		nodeFailed := scenario.ExitCode != nil && *scenario.ExitCode != 0
		if (err != nil) != nodeFailed {
			t.Errorf("%s: start error %v, Node exit code %v", name, err, scenario.ExitCode)
		}
	}
}

func TestPortsInUseAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, c := range []struct{ scenario, listener string }{
		{"api port in use", "api"},
		{"console port in use", "console"},
		{"https port in use", "https"},
		{"redirect port in use", "console"},
	} {
		scenario := oracleScenarioNamed(t, c.scenario)
		port, release := holdPort(t)
		g, err := newGoStack(t, db, scenario, certs, map[string]int{c.listener: port})
		release()
		nodeFailed := scenario.ExitCode != nil && *scenario.ExitCode != 0
		if (err != nil) != nodeFailed {
			t.Errorf("%s: start error %v, Node exit code %v", c.scenario, err, scenario.ExitCode)
		}
		if err == nil && c.listener == "api" && (g.ports["api"] != "" || g.ports["console"] == "" || !*scenario.ConsoleOpen) {
			t.Errorf("%s: listeners %v, want the console without the client API port", c.scenario, g.ports)
		}
	}
}

func TestClientAPISwitchesAgainstNode(t *testing.T) {
	certs := makeTestCerts(t)
	db := storetest.Migrated(t)
	for _, name := range []string{"api disabled", "api enabled empty"} {
		scenario := oracleScenarioNamed(t, name)
		g, err := newGoStack(t, db, scenario, certs, nil)
		if err != nil {
			t.Fatal(err)
		}
		if open := g.ports["api"] != ""; open != *scenario.APIPortOpen {
			t.Errorf("%s: client API port %v, Node %v", name, open, *scenario.APIPortOpen)
		}
	}
	scenario := oracleScenarioNamed(t, "api proxy empty")
	g, err := newGoStack(t, db, scenario, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkReplay(t, scenario.Name, g.replayExchanges(t, scenario, nil))
}

// TestTrailerHeaderAgainstNode: a Trailer header on a forwarded request
// that is not chunked ended the whole Node console (BUGS LS1). Go ends
// that one connection and goes on.
func TestTrailerHeaderAgainstNode(t *testing.T) {
	scenario := oracleScenarioNamed(t, "proxy trailer header")
	if scenario.ExitCode == nil || *scenario.ExitCode == 0 {
		t.Fatalf("the recording has Node running on: %v", scenario.ExitCode)
	}
	g, err := newGoStack(t, storetest.Migrated(t), scenario, makeTestCerts(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]func(t *testing.T, got []reply, closed bool) string{"GET with Trailer": closedUnanswered}
	checkReplay(t, scenario.Name, g.replayExchanges(t, scenario, known))
	options := base64.StdEncoding.EncodeToString([]byte("OPTIONS /api/login HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
	got, _, _ := send(g.ports["api"], false, []oraclePart{{Data: &options}}, 3*time.Second)
	if !bytes.HasPrefix(got, []byte("HTTP/1.1 204")) {
		t.Errorf("after the Trailer request the port answered %q", got)
	}
}
