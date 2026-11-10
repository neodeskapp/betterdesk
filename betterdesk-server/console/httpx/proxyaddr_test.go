package httpx

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func request(remote string, headers map[string]string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "http://console.test/", nil)
	r.RemoteAddr = remote
	r.Host = "console.test"
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	xff := map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.2, 127.0.0.1"}
	cases := []struct {
		name    string
		setting string
		remote  string
		headers map[string]string
		want    string
	}{
		{"off ignores the header", "", "198.51.100.1:4000", xff, "198.51.100.1"},
		{"false ignores the header", "false", "198.51.100.1:4000", xff, "198.51.100.1"},
		{"one hop takes the last forwarded address", "1", "198.51.100.1:4000", xff, "127.0.0.1"},
		{"true means one hop", "true", "198.51.100.1:4000", xff, "127.0.0.1"},
		{"two hops", "2", "198.51.100.1:4000", xff, "10.0.0.2"},
		{"more hops than addresses gives the leftmost", "9", "198.51.100.1:4000", xff, "203.0.113.9"},
		{"loopback socket is trusted", "loopback", "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"loopback does not trust a public socket", "loopback", "198.51.100.1:4000", xff, "198.51.100.1"},
		{"range list walks until the first untrusted hop", "loopback, 10.0.0.0/8", "127.0.0.1:4000", xff, "203.0.113.9"},
		{"range list stops at an untrusted hop", "loopback", "127.0.0.1:4000", map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.2"}, "10.0.0.2"},
		{"single address", "10.0.0.2", "10.0.0.2:4000", map[string]string{"X-Forwarded-For": "203.0.113.9"}, "203.0.113.9"},
		{"no header", "1", "198.51.100.1:4000", nil, "198.51.100.1"},
		{"ipv6 socket", "loopback", "[::1]:4000", map[string]string{"X-Forwarded-For": "2001:db8::7"}, "2001:db8::7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp, err := ParseTrustProxy(c.setting)
			if err != nil {
				t.Fatalf("ParseTrustProxy(%q): %v", c.setting, err)
			}
			if got := tp.ClientIP(request(c.remote, c.headers)); got != c.want {
				t.Errorf("ClientIP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestParseTrustProxyRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not-an-address", "10.0.0.0/99", "loopback, nope"} {
		if _, err := ParseTrustProxy(raw); err == nil {
			t.Errorf("ParseTrustProxy(%q) accepted an invalid value", raw)
		}
	}
}

func TestSchemeAndHost(t *testing.T) {
	forwarded := map[string]string{"X-Forwarded-Proto": "https, http", "X-Forwarded-Host": "panel.example, inner"}
	off, _ := ParseTrustProxy("")
	loop, _ := ParseTrustProxy("loopback")

	if got := off.Scheme(request("127.0.0.1:1", forwarded)); got != "http" {
		t.Errorf("untrusted proxy: scheme = %q, want http", got)
	}
	if got := loop.Scheme(request("127.0.0.1:1", forwarded)); got != "https" {
		t.Errorf("trusted proxy: scheme = %q, want https", got)
	}
	if got := loop.Scheme(request("198.51.100.1:1", forwarded)); got != "http" {
		t.Errorf("public socket: scheme = %q, want http", got)
	}
	secure := request("198.51.100.1:1", nil)
	secure.TLS = &tls.ConnectionState{}
	if got := off.Scheme(secure); got != "https" {
		t.Errorf("TLS connection: scheme = %q, want https", got)
	}

	if got := off.Host(request("127.0.0.1:1", forwarded)); got != "console.test" {
		t.Errorf("untrusted proxy: host = %q, want console.test", got)
	}
	if got := loop.Host(request("127.0.0.1:1", forwarded)); got != "panel.example" {
		t.Errorf("trusted proxy: host = %q, want panel.example", got)
	}
}
