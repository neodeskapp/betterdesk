package console

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

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
// HTTP, nor does Go; where Node closed the connection, Go closes it too.
func plainOnTLS(t *testing.T, g *goStack, scenario oracleScenario, listener string) int {
	t.Helper()
	n := 0
	for _, e := range scenario.Exchanges {
		if e.Secure || e.Listener != listener {
			continue
		}
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		if bytes.HasPrefix(raw, []byte("HTTP/")) {
			t.Fatalf("%s: Node answered in plain HTTP: %q", e.ID, raw)
		}
		got, closed, _ := send(g.ports[listener], false, e.Parts, 4*time.Second)
		if bytes.HasPrefix(got, []byte("HTTP/")) {
			t.Errorf("%s: answered in plain HTTP: %q", e.ID, got)
		}
		if e.Closed && !closed {
			t.Errorf("%s: Node closed the connection, Go kept it open", e.ID)
		}
		n++
	}
	return n
}

func secureOnly(s oracleScenario) oracleScenario {
	return oracleScenario{Name: s.Name, Exchanges: filter(s.Exchanges, func(e oracleExchange) bool { return e.Secure })}
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
		checkReplay(t, scenario.Name, g.replayExchanges(t, scenario, nil))
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
// its name and its attributes (Secure among them).
func checkConsoleCookies(t *testing.T, g *goStack, name string, exchanges []oracleExchange, listener string) {
	t.Helper()
	if len(exchanges) == 0 {
		t.Fatalf("%s: no console exchanges", name)
	}
	for _, e := range exchanges {
		raw, _ := base64.StdEncoding.DecodeString(e.Got)
		want := csrfCookie(parseReplies(raw, methodOf(e.Parts)))
		got, _, _ := send(g.ports[listener], e.Secure, e.Parts, 6*time.Second)
		if have := csrfCookie(parseReplies(got, methodOf(e.Parts))); have != want || want == "" {
			t.Errorf("%s %s: CSRF cookie %q, Node %q", name, e.ID, have, want)
		}
	}
}

// knownRedirect are the redirect requests where Go answers otherwise.
var knownRedirect = map[string]func(t *testing.T, got []reply, closed bool) string{
	// A Host header that is no host name or two Host headers: net/http
	// refuses them with 400 where Node redirected to what the client sent
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
	// A whole address as the request target: Node took the host from the
	// Host header, net/http from the address
	`redirect target "http://evil.com/x"`:        locationIs("https://evil.com:25443http://evil.com/x"),
	`redirect target "https://evil.com:444/y?z"`: locationIs("https://evil.com:25443https://evil.com:444/y?z"),
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
	_, httpsPort, _ := net.SplitHostPort(g.ports["https"])
	redirects := oracleScenario{Exchanges: filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "redirect" })}
	// The redirects name the recording's HTTPS port
	g.ports["redirect"] = portRewriter(t, g.ports["redirect"], ":"+httpsPort, ":25443")
	res := g.replayExchanges(t, redirects, knownRedirect)
	checkReplay(t, "https redirects", res)
	if res.same < 60 {
		t.Errorf("only %d redirects compared", res.same)
	}
	checkConsoleCookies(t, g, "https", filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "https" && e.Secure }), "https")
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
	scenario := oracleScenarioNamed(t, "https HTTPS_ENABLED=True")
	g, err := newGoStack(t, db, scenario, certs, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, httpsPort, _ := net.SplitHostPort(g.ports["https"])
	g.ports["redirect"] = portRewriter(t, g.ports["redirect"], ":"+httpsPort, ":25443")
	checkReplay(t, scenario.Name, g.replayExchanges(t, scenario, nil))
}

// TestFallbackAgainstNode: HTTPS mode without usable certificate files
// runs on plain HTTP with the HTTPS cookie names, without Secure.
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
		checkConsoleCookies(t, g, scenario.Name, filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "console" }), "console")
		api := oracleScenario{Exchanges: filter(scenario.Exchanges, func(e oracleExchange) bool { return e.Listener == "api" })}
		checkReplay(t, scenario.Name, g.replayExchanges(t, api, nil))
		n++
	}
	if n != 6 {
		t.Errorf("%d fallback scenarios replayed", n)
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

func locationIs(want string) func(t *testing.T, got []reply, closed bool) string {
	return func(_ *testing.T, got []reply, _ bool) string {
		if len(got) != 1 || got[0].status != http.StatusTemporaryRedirect || got[0].header.Get("Location") != want {
			return fmt.Sprintf("want 307 to %q, got %v", want, statuses(got))
		}
		return ""
	}
}
