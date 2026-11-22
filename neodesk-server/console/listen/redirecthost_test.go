package listen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// redirectRecording is testdata/node_redirect.json: lib/redirectHost.js
// of upstream 3.5.264 on generated input (testdata/README.md).
// normalize is [raw (null: undefined), host or null], resolve
// [configured, listen, host, source], build [host, port, target, URL].
type redirectRecording struct {
	Normalize [][2]*string         `json:"normalize"`
	Resolve   [][4]string          `json:"resolve"`
	Build     [][4]json.RawMessage `json:"build"`
}

type buildVector struct {
	host, target, want string
	port               int
}

func loadRedirectRecording(t *testing.T) (*redirectRecording, []buildVector) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "node_redirect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec redirectRecording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	var build []buildVector
	for _, b := range rec.Build {
		var c buildVector
		if json.Unmarshal(b[0], &c.host) != nil || json.Unmarshal(b[1], &c.port) != nil || json.Unmarshal(b[2], &c.target) != nil || json.Unmarshal(b[3], &c.want) != nil {
			t.Fatalf("build vector %s", b)
		}
		build = append(build, c)
	}
	return &rec, build
}

// needsIDNA reports a configured value written as a URL whose host has
// letters outside ASCII or a Punycode label: new URL() maps such a host,
// the Go parser refuses it (DECISIONS A21), and the value then names no
// host.
func needsIDNA(raw string) bool {
	value := jsTrim(raw)
	if !schemePattern.MatchString(value) {
		return false
	}
	return strings.Contains(strings.ToLower(value), "xn--") || !isASCIIText(value)
}

func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// bracketed is the address Node built for an IPv6 host with the host put
// in brackets and without its zone, as the redirect writes it: Node put
// the address in as it was, which no browser can follow (BUGS UM40).
func bracketed(nodeURL, host string) (string, bool) {
	normal, ok := normalizeConfiguredHost(host)
	if !ok || ssrfguard.IsIP(normal) != 6 {
		return nodeURL, false
	}
	address, _, _ := strings.Cut(normal, "%")
	return strings.Replace(nodeURL, "https://"+normal, "https://["+address+"]", 1), true
}

// TestRedirectHostMatchesNode replays lib/redirectHost.js: the host each
// configured value names, the host chosen from a configured value and a
// listen address, and the address built from a host, a port and a request
// target. The differences are URLs with international host names, which
// name no host here, and IPv6 hosts, which go into the address in
// brackets.
func TestRedirectHostMatchesNode(t *testing.T) {
	rec, build := loadRedirectRecording(t)
	if len(rec.Normalize) < 4000 || len(rec.Resolve) < 1000 || len(build) < 3000 {
		t.Fatalf("vectors: %d, %d, %d", len(rec.Normalize), len(rec.Resolve), len(build))
	}
	idna, ipv6, zones, failed := 0, 0, 0, 0
	for _, c := range rec.Normalize {
		raw := ""
		if c[0] != nil {
			raw = *c[0]
		}
		got, ok := normalizeConfiguredHost(raw)
		switch {
		case c[1] == nil && !ok:
		case c[1] != nil && ok && got == *c[1]:
		case c[1] != nil && !ok && needsIDNA(raw):
			idna++
		default:
			failed++
			want := "<null>"
			if c[1] != nil {
				want = *c[1]
			}
			t.Errorf("normalizeConfiguredHost(%q) = %q %v, Node %q", raw, got, ok, want)
		}
	}
	for _, c := range rec.Resolve {
		host, source := RedirectHost(c[0], c[1])
		if host == c[2] && source == c[3] {
			continue
		}
		if c[3] == "config" && needsIDNA(c[0]) {
			idna++
			continue
		}
		failed++
		t.Errorf("RedirectHost(%q, %q) = %q %q, Node %q %q", c[0], c[1], host, source, c[2], c[3])
	}
	for _, c := range build {
		want, isIPv6 := bracketed(c.want, c.host)
		if isIPv6 {
			ipv6++
			if normal, _ := normalizeConfiguredHost(c.host); strings.Contains(normal, "%") {
				zones++
			}
		}
		if got := SafeRedirectURL(c.host, c.port, c.target); got != want {
			failed++
			t.Errorf("SafeRedirectURL(%q, %d, %q) = %q, want %q (Node %q)", c.host, c.port, c.target, got, want, c.want)
		}
		if failed > 20 {
			t.FailNow()
		}
	}
	if ipv6 < 100 || zones == 0 {
		t.Errorf("only %d build vectors with an IPv6 host, %d with a zone", ipv6, zones)
	}
	t.Logf("%d normalize, %d resolve, %d build vectors; %d differ for international host names, %d for IPv6 hosts", len(rec.Normalize), len(rec.Resolve), len(build), idna, ipv6)
}

// TestRedirectIgnoresTheRequestHost: the Host header, an absolute request
// target and a target that names a host never move the redirect off the
// host it was made with (audit H5).
func TestRedirectIgnoresTheRequestHost(t *testing.T) {
	h := Redirect("Desk.Example.COM", 5443)
	for _, c := range []struct {
		host, target, want string
	}{
		{"attacker.example", "/login?x=1", "https://desk.example.com:5443/login?x=1"},
		{"attacker.example:5443", "/", "https://desk.example.com:5443/"},
		{"desk.example.com", "//evil.example/login", "https://desk.example.com:5443/"},
		{"desk.example.com", "http://evil.example/login", "https://desk.example.com:5443/"},
		{"evil.example", "https://evil.example:444/y?z", "https://desk.example.com:5443/"},
		{"desk.example.com", "*", "https://desk.example.com:5443/"},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://placeholder/", nil)
		r.Host, r.RequestURI = c.host, c.target
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != c.want {
			t.Errorf("Host %q target %q: %d %q, want %q", c.host, c.target, w.Code, w.Header().Get("Location"), c.want)
		}
	}
	if got := w443(t); got != "https://desk.example.com/x" {
		t.Errorf("port 443: %q", got)
	}
}

// The redirect names no server: no X-Powered-By, as on every other answer
// of the console.
func TestRedirectHasNoPoweredBy(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		r := httptest.NewRequest(method, "/x", nil)
		w := httptest.NewRecorder()
		Redirect("desk.example.com", 5443).ServeHTTP(w, r)
		if _, ok := w.Header()["X-Powered-By"]; ok || w.Code != http.StatusTemporaryRedirect {
			t.Errorf("%s: %d, X-Powered-By %q", method, w.Code, w.Header().Values("X-Powered-By"))
		}
	}
}

// PublicHostPage answers every method with the same short page: no
// redirect, no script, nothing of the request, the HTTPS port and the
// setting to set (BUGS UM41, DECISIONS A69).
func TestPublicHostPage(t *testing.T) {
	h := PublicHostPage(5443)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead, http.MethodDelete} {
		r := httptest.NewRequest(method, "http://placeholder/", nil)
		r.Host, r.RequestURI = "attacker.example", "/login?next=%3Cscript%3E"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		body := w.Body.String()
		switch {
		case w.Code != http.StatusOK || w.Header().Get("Location") != "":
			t.Errorf("%s: %d, Location %q; want 200 and no redirect", method, w.Code, w.Header().Get("Location"))
		case w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" ||
			w.Header().Get("Content-Security-Policy") != "default-src 'none'":
			t.Errorf("%s: headers %v", method, w.Header())
		case w.Header().Get("Content-Length") != strconv.Itoa(len(publicHostPage(5443))):
			t.Errorf("%s: Content-Length %q", method, w.Header().Get("Content-Length"))
		case method == http.MethodHead && body != "":
			t.Errorf("HEAD has a body: %q", body)
		case method != http.MethodHead && (!strings.Contains(body, "PANEL_PUBLIC_HOST") || !strings.Contains(body, "port 5443") ||
			strings.Contains(body, "<script") || strings.Contains(body, "attacker") || strings.Contains(body, "login")):
			t.Errorf("%s: page %q", method, body)
		}
	}
	if page := publicHostPage(443); strings.Contains(page, "port") && strings.Contains(page, "443") {
		t.Errorf("the page for port 443 names the port: %q", page)
	}
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ProtoMajor = 2
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("HTTP/2 request: %d, want the redirect's 400", w.Code)
	}
}

// The start log warns when the plain port answers the page, and names a
// PANEL_PUBLIC_HOST that is set but names no host.
func TestPublicHostWarning(t *testing.T) {
	for configured, want := range map[string]string{
		"":                      "PANEL_PUBLIC_HOST is not set: the plain HTTP port 5000 answers a page",
		"   ":                   "PANEL_PUBLIC_HOST is not set: the plain HTTP port 5000 answers a page",
		"desk.example.com:8443": `PANEL_PUBLIC_HOST "desk.example.com:8443" names no host (write the host name or address alone, without a port or path)`,
		"desk.example.com":      "",
		"https://desk.example":  "",
	} {
		got := PublicHostWarning(configured, 5000)
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%q: %q, want %q", configured, got, want)
		}
	}
}

func w443(t *testing.T) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RequestURI = "/x"
	w := httptest.NewRecorder()
	Redirect("desk.example.com", 443).ServeHTTP(w, r)
	return w.Header().Get("Location")
}

// FuzzRedirectHost feeds configured hosts, listen addresses, ports,
// request targets and Host headers: nothing panics, the chosen host is
// "localhost", an IP address or a host name, and the Location of the
// answer is https:// with exactly that host and port, followed by the
// request target when it is a path and "/" otherwise; the Host header
// never matters.
func FuzzRedirectHost(f *testing.F) {
	for _, s := range [][4]string{
		{"desk.example.com", "127.0.0.1", "/x", "evil.example"},
		{"https://Desk.Example.com:8443/x", "0.0.0.0", "//evil/x", "a"},
		{"[2001:db8::1]", "::", "http://evil/x", ""},
		{"evil\r\nX: 1", "[::1]", "*", "x:1"},
		{"", "fe80::1%eth0", "/a?b=<c>", "[::1]:80"},
		{"foo://Host:1/x", " 10.0.0.1 ", "/%zz", "h"},
		{"http://0x7f.1", "localhost", "/\\evil", "h"},
	} {
		f.Add(s[0], s[1], 5443, s[2], s[3])
	}
	f.Fuzz(func(t *testing.T, configured, listenHost string, port int, target, hostHeader string) {
		host, source := RedirectHost(configured, listenHost)
		switch {
		case host == "localhost" && source == "fallback":
		case source == "config" && (ssrfguard.IsIP(host) != 0 || hostnamePattern.MatchString(host)) && len(host) <= 253:
		case source == "listen" && ssrfguard.IsIP(host) != 0:
		default:
			t.Fatalf("RedirectHost(%q, %q) = %q %q", configured, listenHost, host, source)
		}
		if strings.ContainsAny(host, "/?#@\\ \t\r\n\"<>") {
			t.Fatalf("host %q", host)
		}
		normal, ok := normalizeConfiguredHost(host)
		if !ok && host != "localhost" && source != "listen" {
			t.Fatalf("chosen host %q does not read back", host)
		}
		if !ok {
			normal = "localhost"
		}
		if ssrfguard.IsIP(normal) == 6 {
			address, _, _ := strings.Cut(normal, "%")
			normal = "[" + address + "]"
		}
		origin := "https://" + normal
		if port > 0 && port != 443 {
			origin += ":" + strconv.Itoa(port)
		}
		want := origin + "/"
		if strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
			want = origin + target
		}
		if got := SafeRedirectURL(host, port, target); got != want {
			t.Fatalf("SafeRedirectURL(%q, %d, %q) = %q, want %q", host, port, target, got, want)
		}
		if strings.ContainsAny(target, " \x00\r\n") || !utf8.ValidString(hostHeader) {
			return
		}
		r := httptest.NewRequest(http.MethodGet, "http://placeholder/", nil)
		r.Host, r.RequestURI = hostHeader, target
		w := httptest.NewRecorder()
		Redirect(host, port).ServeHTTP(w, r)
		if w.Code == http.StatusBadRequest {
			return
		}
		location := w.Header().Get("Location")
		if location != expressLocation(want) {
			t.Fatalf("Location %q for Host %q, target %q, want %q", location, hostHeader, target, expressLocation(want))
		}
		// What a browser reads as the host: up to the first "/" after
		// the scheme (the path always starts with one)
		rest := strings.TrimPrefix(location, "https://")
		if authority, _, _ := strings.Cut(rest, "/"); authority != strings.TrimPrefix(origin, "https://") {
			t.Fatalf("Location %q names %q, want %q", location, authority, strings.TrimPrefix(origin, "https://"))
		}
	})
}
