package ssrfguard

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/ssrfguard/guardtest"
)

// plainlyDangerous is the rule the guard exists for, written without the
// guard's own code: an IPv4 address in its usual spelling that is
// loopback or link-local, or private while private addresses are not
// allowed.
func plainlyDangerous(address string, opts Options) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || ip.String() != address {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return true
	}
	if opts.AllowPrivate {
		return false
	}
	return ip.IsPrivate() || ip.As4()[0] == 0 ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("198.18.0.0/15").Contains(ip)
}

// FuzzSSRFGuard feeds hosts, URLs and resolver answers to the checks and
// holds them to the plain rule: no check passes a host that is, or
// resolves to, an IPv4 address that is plainly dangerous. What the Node
// guard answered for the recorded inputs is pinned by the replay of
// testdata/vectors.jsonl.gz.
func FuzzSSRFGuard(f *testing.F) {
	seeds := []struct {
		kind         uint8
		input        string
		records      string
		allowPrivate bool
	}{
		{0, "127.0.0.1", "", false}, {0, "::ffff:127.0.0.1", "", true}, {0, "fe80::1%eth0", "", false}, {0, "0:0:0:0:0:0:0:1", "", false},
		{1, "localhost", "", false}, {1, " [::1] ", "", true}, {1, "LOCALHOST.", "", false}, {1, "10.0.0.1", "", true},
		{2, "example.com", "93.184.216.34", false}, {2, "rebind.example", "8.8.8.8,127.0.0.1", true}, {2, "2130706433", "127.0.0.1", false},
		{2, "nas.lan", "192.168.1.5", true}, {2, "v6.example", "::1", false}, {2, "gone.example", "!getaddrinfo ENOTFOUND gone.example", false},
		{3, "http://example.com/", "93.184.216.34", false}, {3, "http://0x7f.1/", "", false}, {3, "http://[::ffff:127.0.0.1]/", "", false},
		{3, "http://user:pw@example.com/", "1.1.1.1", false}, {3, "http://example.com\\@127.0.0.1/", "1.1.1.1", false}, {3, "file:///etc/passwd", "", false},
		{3, "http://%31%32%37.0.0.1/", "", true}, {3, "https://EXAMPLE.com:443/a/../b?c#d", "10.0.0.1", true}, {3, "http://127.1:80/", "", false},
		{3, "ht\ttp://169.254.169.254/latest", "", true}, {3, "http://[fe80::1]/", "", true}, {3, "http://1.1.1.1.1/", "", false},
	}
	for _, s := range seeds {
		f.Add(s.kind, s.input, s.records, s.allowPrivate)
	}
	f.Fuzz(func(t *testing.T, kind uint8, input, recordList string, allowPrivate bool) {
		opts := Options{AllowPrivate: allowPrivate}
		var records []string
		lookupErr := ""
		if strings.HasPrefix(recordList, "!") {
			lookupErr = recordList[1:]
			if lookupErr == "" {
				lookupErr = "getaddrinfo ENOTFOUND host"
			}
		} else if recordList != "" {
			records = strings.Split(recordList, ",")
		}
		var asked []string
		g := &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
			asked = append(asked, host)
			if lookupErr != "" {
				return nil, errors.New(lookupErr)
			}
			return records, nil
		}}
		ctx := context.Background()

		switch kind % 4 {
		case 0:
			blockedHere := IsBlockedIP(input, opts)
			if !blockedHere && plainlyDangerous(input, opts) {
				t.Fatalf("IsBlockedIP(%q, %+v) lets a dangerous address through", input, opts)
			}
			if blockedHere != guardtest.Blocked(input, allowPrivate) {
				t.Fatalf("IsBlockedIP(%q, %+v) = %v, the ranges say %v", input, opts, blockedHere, !blockedHere)
			}
		case 1:
			if clean, err := SafeHostname(input, opts); err == nil && plainlyDangerous(clean, opts) {
				t.Fatalf("SafeHostname(%q, %+v) = %q, a dangerous address", input, opts, clean)
			}
		case 2:
			clean, err := g.SafeResolvedHost(ctx, input, opts)
			if err != nil {
				return
			}
			for _, address := range append([]string{clean}, recordsIfAsked(asked, records)...) {
				if plainlyDangerous(address, opts) {
					t.Fatalf("SafeResolvedHost(%q, %+v, records %q) = %q although %s is dangerous", input, opts, records, clean, address)
				}
			}
		case 3:
			parsed, err := g.SafeHTTPURL(ctx, input, opts)
			if err != nil {
				return
			}
			host := strings.TrimSuffix(strings.TrimPrefix(parsed.Hostname, "["), "]")
			for _, address := range append([]string{host}, recordsIfAsked(asked, records)...) {
				if plainlyDangerous(address, opts) {
					t.Fatalf("SafeHTTPURL(%q, %+v, records %q) passes although %s is dangerous", input, opts, records, address)
				}
			}
			if parsed.Protocol != "http:" && parsed.Protocol != "https:" {
				t.Fatalf("SafeHTTPURL(%q) passes the scheme %s", input, parsed.Protocol)
			}
			if again, ok := ParseURL(parsed.Href); !ok || again.Href != parsed.Href || again.Hostname != parsed.Hostname {
				t.Fatalf("SafeHTTPURL(%q): the normal form %q does not parse to itself: %+v", input, parsed.Href, again)
			}
		}
	})
}

// The spellings of an IPv4 address the system resolver reads give an
// address in the usual form, and the usual form gives itself.
func FuzzLegacyIPv4(f *testing.F) {
	for _, seed := range []string{"127.1", "2130706433", "0x7f.0.0.1", "0177.0.0.1", "1.2.3.4", "256", "0x", "08.1.1.1", "1.2.3.4.5", "4294967296", "", ".", "1..2"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, host string) {
		address, ok := legacyIPv4(host)
		if !ok {
			if IsIPv4(host) {
				t.Fatalf("the address %q is not read", host)
			}
			return
		}
		if !IsIPv4(address) {
			t.Fatalf("%q gives %q, which is no address", host, address)
		}
		if IsIPv4(host) && address != host {
			t.Fatalf("the address %q gives %q", host, address)
		}
	})
}

// recordsIfAsked is the resolver's answer when the check used it.
func recordsIfAsked(asked, records []string) []string {
	if len(asked) == 0 {
		return nil
	}
	return records
}
