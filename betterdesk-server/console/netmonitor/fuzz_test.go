package netmonitor

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/console/ssrfguard/guardtest"
)

// anywhere resolves every name to a public address.
type anywhere struct{}

func (anywhere) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
}

// A URL that passes the guard is http or https, has no credentials, and
// its host is none of the names and addresses the guard blocks as text.
func FuzzSafeMonitoringURL(f *testing.F) {
	for _, seed := range []string{
		"http://example.com/", "https://user:pw@example.com:8443/a?b#c", "http://127.0.0.1/", "http://2130706433/", "http://0x7f.1/",
		"http://[::1]/", "http://[::ffff:127.0.0.1]/", "http:\\\\localhost\\x", "ftp://example.com/", "http://exa mple.com/", "  HTTP://LOCALHOST  ",
		"http://169.254.169.254/latest", "http://metadata.google.internal/", "http://m\u00fcnchen.de/", "http://%6c%6fcalhost/", "http://a:99999/",
		"x://[zz]/", "file:///c:/x", "http://[fe80::1%25eth0]/", "http://1.2.3.4.5/", "",
	} {
		f.Add(seed)
	}
	g := newGuard(anywhere{})
	f.Fuzz(func(t *testing.T, raw string) {
		parsed, err := g.SafeMonitoringURL(context.Background(), raw)
		if err != nil {
			if _, isRefusal := err.(*ssrfguard.BlockedError); !isRefusal {
				t.Fatalf("%q: error of type %T: %v", raw, err, err)
			}
			return
		}
		if parsed.Protocol != "http:" && parsed.Protocol != "https:" {
			t.Fatalf("%q: protocol %q passed", raw, parsed.Protocol)
		}
		host := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(parsed.Hostname, "["), "]"))
		switch {
		case host == "":
			t.Fatalf("%q: an empty host passed", raw)
		case host == "localhost" || host == "localhost.localdomain" || host == "metadata.google.internal" || host == "metadata.goog":
			t.Fatalf("%q: the blocked name %q passed", raw, host)
		case ssrfguard.IsIP(host) != 0 && guardtest.Blocked(host, true):
			t.Fatalf("%q: the blocked address %q passed", raw, host)
		case ssrfguard.IsIPv4(host) && (strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "169.254.")):
			t.Fatalf("%q: the loopback or link-local address %q passed", raw, host)
		case !strings.HasPrefix(parsed.Pathname, "/"):
			t.Fatalf("%q: path %q", raw, parsed.Pathname)
		case strings.ContainsAny(parsed.Hostname+parsed.Port+parsed.Pathname+parsed.Search, " \t\r\n"):
			t.Fatalf("%q: white space in %+v", raw, parsed)
		}
	})
}

// A host the tools accept has nothing in it a shell or the ping command
// could read as something else than a host.
func FuzzValidHost(f *testing.F) {
	for _, seed := range []string{
		"example.com", "10.0.0.1", "::1", "fe80::1%eth0", "-c5", "a;b", "a b", "$(id)", "`id`", "a|b", "::ffff:1.2.3.4", "999.999.999.999",
		"a-b.c", "-a.b", "a.-b", "0x7f.1", "", "\n", "a\x00b", strings.Repeat("a", 254),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		if !isValidHost(host) {
			return
		}
		if len(host) > 253 || host[0] == '-' {
			t.Fatalf("%q is taken for a host", host)
		}
		for i := 0; i < len(host); i++ {
			c := host[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == ':' || c == '%') {
				t.Fatalf("%q is taken for a host, with %q in it", host, c)
			}
		}
		// A zone is only there after an IPv6 address
		if i := strings.IndexByte(host, '%'); i >= 0 && !ssrfguard.IsIPv6(host) {
			t.Fatalf("%q is taken for a host", host)
		}
	})
}

// parseInt gives NaN or a whole number, and a number's own text gives the
// number back.
func FuzzParseInt(f *testing.F) {
	for _, seed := range []string{"42", " -7x", "+", "", "1e5", "99999999999999999999999", "0x10", "\u00a05", "5,6", "NaN", "Infinity"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		n := ParseInt(text)
		if n.IsNaN() {
			return
		}
		if v := float64(n); !math.IsInf(v, 0) && v != math.Trunc(v) {
			t.Fatalf("ParseInt(%q) = %v", text, v)
		}
		if again := ParseInt(n.String()); !math.IsInf(float64(n), 0) && math.Abs(float64(n)) < 1e21 && again != n {
			t.Fatalf("ParseInt(%q) = %s, and that text gives %s", text, n, again)
		}
		if received := n.received(); strings.ReplaceAll(received, "_", "") != n.inspect() {
			t.Fatalf("%s is written %q in an error", n.inspect(), received)
		}
	})
}
