package ssrfguard

import (
	"context"
	"errors"
	"testing"
)

// noLookup fails the test when a check that must not need DNS asks it.
func noLookup(t *testing.T) *Guard {
	t.Helper()
	return &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
		t.Errorf("lookup of %q: this check must not resolve anything", host)
		return nil, errors.New("no lookup in this test")
	}}
}

func fixed(records ...string) *Guard {
	return &Guard{Lookup: func(context.Context, string) ([]string, error) { return records, nil }}
}

func message(err error) string {
	var refused *BlockedError
	if !errors.As(err, &refused) {
		return "<not a BlockedError>"
	}
	if refused.Code != "SSRF_BLOCKED" {
		return "<code " + refused.Code + ">"
	}
	return refused.Message
}

// The cases of web-nodejs/tests/ssrfGuard.test.js.

func TestIsBlockedIPBlocksLoopbackAndPrivateRanges(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254"} {
		if !IsBlockedIP(ip, Options{}) {
			t.Errorf("%s is not blocked", ip)
		}
	}
}

func TestIsBlockedIPAllowsPublicIPv4(t *testing.T) {
	for _, ip := range []string{"8.8.8.8", "1.1.1.1"} {
		if IsBlockedIP(ip, Options{}) {
			t.Errorf("%s is blocked", ip)
		}
	}
}

func TestIsBlockedIPAllowsPrivateRangesInMonitoringMode(t *testing.T) {
	private := Options{AllowPrivate: true}
	if IsBlockedIP("192.168.1.1", private) || IsBlockedIP("10.0.0.1", private) {
		t.Error("a private address is blocked although AllowPrivate is set")
	}
	if !IsBlockedIP("127.0.0.1", private) {
		t.Error("loopback is let through with AllowPrivate")
	}
}

func TestSafeHostnameRejectsLocalhostAndPrivateAddresses(t *testing.T) {
	if _, err := SafeHostname("localhost", Options{}); message(err) != "Blocked hostname: localhost" {
		t.Errorf("localhost: %v", err)
	}
	if _, err := SafeHostname("10.0.0.5", Options{}); message(err) != "Blocked IP address: 10.0.0.5" {
		t.Errorf("10.0.0.5: %v", err)
	}
}

func TestSafeHTTPURLRejectsOtherSchemesAndLoopback(t *testing.T) {
	g := noLookup(t)
	if _, err := g.SafeHTTPURL(context.Background(), "file:///etc/passwd", Options{}); message(err) != "Only http and https URLs are allowed" {
		t.Errorf("file URL: %v", err)
	}
	if _, err := g.SafeHTTPURL(context.Background(), "http://127.0.0.1/", Options{}); message(err) != "Blocked IP address: 127.0.0.1" {
		t.Errorf("loopback URL: %v", err)
	}
}

func TestSafeHTTPURLAllowsAPublicAddressWithoutDNS(t *testing.T) {
	parsed, err := noLookup(t).SafeHTTPURL(context.Background(), "http://1.1.1.1/", Options{})
	if err != nil || parsed.Hostname != "1.1.1.1" || parsed.Href != "http://1.1.1.1/" {
		t.Errorf("got %+v, %v", parsed, err)
	}
}

func TestSafeResolvedHostAcceptsAPublicAddressWithoutDNS(t *testing.T) {
	clean, err := noLookup(t).SafeResolvedHost(context.Background(), "1.1.1.1", Options{})
	if err != nil || clean != "1.1.1.1" {
		t.Errorf("got %q, %v", clean, err)
	}
}

// What the checks answer beyond the Jest cases, each message once.

func TestSafeHostnameNormalizes(t *testing.T) {
	cases := []struct{ in, want, refusal string }{
		{" Example.COM ", "example.com", ""},
		{"[2001:DB8::1]", "2001:db8::1", ""},
		{"", "", "Missing hostname"},
		{" \t", "", "Missing hostname"},
		{"[]", "", "Missing hostname"},
		{"LOCALHOST", "", "Blocked hostname: localhost"},
		{"metadata.google.internal", "", "Blocked hostname: metadata.google.internal"},
		{"Metadata.Goog", "", "Blocked hostname: metadata.goog"},
		{"localhost.localdomain", "", "Blocked hostname: localhost.localdomain"},
		{"[::1]", "", "Blocked IP address: ::1"},
		{"169.254.169.254", "", "Blocked IP address: 169.254.169.254"},
		// A name with a dot at its end is another name to the list
		{"localhost.", "localhost.", ""},
	}
	for _, c := range cases {
		got, err := SafeHostname(c.in, Options{})
		if c.refusal != "" {
			if message(err) != c.refusal {
				t.Errorf("%q: %v, want %q", c.in, err, c.refusal)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: %q, %v, want %q", c.in, got, err, c.want)
		}
	}
}

func TestSafeResolvedHostChecksEveryRecord(t *testing.T) {
	ctx := context.Background()
	asked := ""
	g := &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
		asked = host
		return []string{"93.184.216.34", "127.0.0.1"}, nil
	}}
	_, err := g.SafeResolvedHost(ctx, " Rebind.Example ", Options{})
	if message(err) != "Hostname rebind.example resolves to blocked address 127.0.0.1" {
		t.Errorf("public then loopback: %v", err)
	}
	if asked != "rebind.example" {
		t.Errorf("the resolver was asked for %q", asked)
	}
	if _, err := fixed("10.0.0.5").SafeResolvedHost(ctx, "nas.example", Options{}); message(err) != "Hostname nas.example resolves to blocked address 10.0.0.5" {
		t.Errorf("private record: %v", err)
	}
	if clean, err := fixed("10.0.0.5").SafeResolvedHost(ctx, "nas.example", Options{AllowPrivate: true}); err != nil || clean != "nas.example" {
		t.Errorf("private record with AllowPrivate: %q, %v", clean, err)
	}
	if _, err := fixed("::ffff:127.0.0.1").SafeResolvedHost(ctx, "mapped.example", Options{AllowPrivate: true}); message(err) != "Hostname mapped.example resolves to blocked address ::ffff:127.0.0.1" {
		t.Errorf("mapped loopback: %v", err)
	}
	if _, err := fixed().SafeResolvedHost(ctx, "empty.example", Options{}); message(err) != "No DNS records for empty.example" {
		t.Errorf("no records: %v", err)
	}
	failing := &Guard{Lookup: func(_ context.Context, host string) ([]string, error) {
		return nil, &LookupError{Code: "ENOTFOUND", Host: host}
	}}
	if _, err := failing.SafeResolvedHost(ctx, "gone.example", Options{}); message(err) != "DNS lookup failed for gone.example: getaddrinfo ENOTFOUND gone.example" {
		t.Errorf("failed lookup: %v", err)
	}
}

func TestSafeHTTPURLRefusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ url, want string }{
		{"", "URL is required"},
		{"not a url", "Invalid URL"},
		{"//example.com/", "Invalid URL"},
		{"http://", "Invalid URL"},
		{"ftp://example.com/", "Only http and https URLs are allowed"},
		{"javascript:alert(1)", "Only http and https URLs are allowed"},
		{"http://user@example.com/", "Credentials in URL are not allowed"},
		{"http://user:pw@example.com/", "Credentials in URL are not allowed"},
		{"http://:pw@example.com/", "Credentials in URL are not allowed"},
		{"http://localhost/", "Blocked hostname: localhost"},
		{"http://2130706433/", "Blocked IP address: 127.0.0.1"},
		{"http://0x7f.1/", "Blocked IP address: 127.0.0.1"},
		{"http://0177.0.0.1/", "Blocked IP address: 127.0.0.1"},
		{"http://127.1/", "Blocked IP address: 127.0.0.1"},
		{"http://%31%32%37.0.0.1/", "Blocked IP address: 127.0.0.1"},
		{"http://[::1]/", "Blocked IP address: ::1"},
		{"http://[0:0:0:0:0:0:0:1]/", "Blocked IP address: ::1"},
		{"http://169.254.169.254/latest/meta-data/", "Blocked IP address: 169.254.169.254"},
		{"http://127.0.0.1\\@example.com/", "Blocked IP address: 127.0.0.1"},
		{"http://192.168.1.1/", "Blocked IP address: 192.168.1.1"},
	}
	for _, c := range cases {
		if _, err := fixed("93.184.216.34").SafeHTTPURL(ctx, c.url, Options{}); message(err) != c.want {
			t.Errorf("%q: %v, want %q", c.url, err, c.want)
		}
	}
	// A backslash ends the host like a slash does: what follows is path
	if parsed, err := fixed("93.184.216.34").SafeHTTPURL(ctx, "http://example.com\\@127.0.0.1/", Options{}); err != nil || parsed.Href != "http://example.com/@127.0.0.1/" {
		t.Errorf("backslash after the host: %+v, %v", parsed, err)
	}
	// "@" with nothing in front of it is no credential
	if parsed, err := fixed("93.184.216.34").SafeHTTPURL(ctx, "http://@example.com/", Options{}); err != nil || parsed.Href != "http://example.com/" {
		t.Errorf("empty user name: %+v, %v", parsed, err)
	}
	if parsed, err := fixed("192.168.1.9").SafeMonitoringURL(ctx, "HTTP://Printer.LAN:80/status?x=1#top"); err != nil || parsed.Href != "http://printer.lan/status?x=1#top" || parsed.Hostname != "printer.lan" {
		t.Errorf("monitoring URL: %+v, %v", parsed, err)
	}
	if _, err := fixed("192.168.1.9").SafeHTTPURL(ctx, "http://printer.lan/", Options{}); message(err) != "Hostname printer.lan resolves to blocked address 192.168.1.9" {
		t.Errorf("private record without AllowPrivate: %v", err)
	}
}

// An address is judged by what it is, not by how it is written: every
// spelling of loopback and link-local is refused, also in the mode for
// administrators, and every spelling of a private address without it
// (BUGS.md SC1, SC2, SC5, SC6).
func TestOtherSpellingsOfLoopbackAreBlocked(t *testing.T) {
	ctx := context.Background()
	private := Options{AllowPrivate: true}
	for _, ip := range []string{
		"0:0:0:0:0:0:0:1", "::0001", "0::1", "::ffff:7f00:1", "::FFFF:7F00:1", "0:0:0:0:0:ffff:127.0.0.1", "::127.0.0.1", "::7f00:1",
		"febf::1", "fe81::", "FE80:0:0:0:0:0:0:1", "fe80::1%eth0", "::ffff:a9fe:a9fe", "::ffff:169.254.169.254", "::169.254.0.1",
	} {
		if !IsBlockedIP(ip, Options{}) || !IsBlockedIP(ip, private) {
			t.Errorf("%s is let through", ip)
		}
	}
	for _, ip := range []string{"::ffff:a00:1", "::ffff:10.0.0.1", "::10.0.0.1", "::ffff:c0a8:101", "::ffff:ac10:1", "fd00::1", "fc00::"} {
		if !IsBlockedIP(ip, Options{}) {
			t.Errorf("%s is let through without AllowPrivate", ip)
		}
		if IsBlockedIP(ip, private) {
			t.Errorf("%s is blocked with AllowPrivate", ip)
		}
	}
	// fc00::/7 is a range, not two letters at the start of the text
	for _, ip := range []string{"fc::1", "fd::", "fe::1", "00fc::1", "::ffff:808:808", "2001:db8::1", "64:ff9b::808:808"} {
		if IsBlockedIP(ip, Options{}) {
			t.Errorf("the public address %s is blocked", ip)
		}
	}
	// Text that is no address is refused, the empty text as before
	for _, ip := range []string{"", "not-an-address", "1.2.3", "127.0.0.1.", "[::1]", "::1%"} {
		if !IsBlockedIP(ip, private) {
			t.Errorf("%q, which is no address, is let through", ip)
		}
	}

	if _, err := SafeHostname("0:0:0:0:0:0:0:1", private); message(err) != "Blocked IP address: 0:0:0:0:0:0:0:1" {
		t.Errorf("long loopback: %v", err)
	}
	if _, err := SafeHostname("[::ffff:7f00:1]", Options{}); message(err) != "Blocked IP address: ::ffff:7f00:1" {
		t.Errorf("mapped loopback in hex: %v", err)
	}
	// The URL parser writes a mapped address in hex; it is read all the same
	for url, want := range map[string]string{
		"http://[::ffff:127.0.0.1]/":       "Blocked IP address: ::ffff:7f00:1",
		"http://[::ffff:169.254.169.254]/": "Blocked IP address: ::ffff:a9fe:a9fe",
		"http://[::ffff:10.0.0.1]:8080/":   "Blocked IP address: ::ffff:a00:1",
		"http://[0:0::1]/":                 "Blocked IP address: ::1",
		"http://[febf::1]/":                "Blocked IP address: febf::1",
	} {
		if _, err := noLookup(t).SafeHTTPURL(ctx, url, Options{}); message(err) != want {
			t.Errorf("%s: %v, want %q", url, err, want)
		}
	}
	if parsed, err := noLookup(t).SafeHTTPURL(ctx, "http://[fc::1]/", Options{}); err != nil || parsed.Hostname != "[fc::1]" {
		t.Errorf("public address fc::1 in a URL: %+v, %v", parsed, err)
	}
	if _, err := fixed("not-an-address").SafeResolvedHost(ctx, "odd.example", private); message(err) != "Hostname odd.example resolves to blocked address not-an-address" {
		t.Errorf("a record that is no address: %v", err)
	}
	if _, err := fixed("93.184.216.34", "::ffff:7f00:1").SafeResolvedHost(ctx, "rebind.example", private); message(err) != "Hostname rebind.example resolves to blocked address ::ffff:7f00:1" {
		t.Errorf("a record of mapped loopback in hex: %v", err)
	}
}

// A connection to 0.0.0.0 or :: reaches the machine itself, so "this
// network" and the unspecified IPv6 address are refused in both modes
// (BUGS.md SC3).
func TestAllowPrivateStillBlocksThisNetwork(t *testing.T) {
	ctx := context.Background()
	private := Options{AllowPrivate: true}
	for _, ip := range []string{"0.0.0.0", "0.1.2.3", "0.255.255.255", "::", "0:0:0:0:0:0:0:0", "::ffff:0.0.0.0", "::ffff:0:0", "::0.0.0.2"} {
		if !IsBlockedIP(ip, private) || !IsBlockedIP(ip, Options{}) {
			t.Errorf("%s is let through", ip)
		}
	}
	if IsBlockedIP("1.0.0.0", private) || IsBlockedIP("10.0.0.0", private) {
		t.Error("an address next to 0.0.0.0/8 is blocked")
	}
	if _, err := SafeHostname("0.0.0.0", private); message(err) != "Blocked IP address: 0.0.0.0" {
		t.Errorf("0.0.0.0: %v", err)
	}
	if _, err := noLookup(t).SafeResolvedHost(ctx, "[::]", private); message(err) != "Blocked IP address: ::" {
		t.Errorf("[::]: %v", err)
	}
	if _, err := fixed("10.0.0.5", "0.0.0.0").SafeResolvedHost(ctx, "zero.example", private); message(err) != "Hostname zero.example resolves to blocked address 0.0.0.0" {
		t.Errorf("a name of 0.0.0.0: %v", err)
	}
	for url, want := range map[string]string{
		"http://0.0.0.0:5000/":   "Blocked IP address: 0.0.0.0",
		"http://0/":              "Blocked IP address: 0.0.0.0",
		"https://[::]:8443/":     "Blocked IP address: ::",
		"http://0x0.0x1.0.0/abc": "Blocked IP address: 0.1.0.0",
	} {
		if _, err := noLookup(t).SafeMonitoringURL(ctx, url); message(err) != want {
			t.Errorf("%s: %v, want %q", url, err, want)
		}
	}
}

func TestSystemLookupReadsOldIPv4Spellings(t *testing.T) {
	cases := map[string]string{
		"2130706433": "127.0.0.1", "0x7f.1": "127.0.0.1", "0177.0.0.1": "127.0.0.1", "127.1": "127.0.0.1",
		"01.2.3.4": "1.2.3.4", "134744072": "8.8.8.8", "10.1.2": "10.1.0.2", "0xa.0x1.0x2.0x3": "10.1.2.3",
	}
	for in, want := range cases {
		got, err := SystemLookup(context.Background(), in)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("%s: %v, %v, want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"1.2.3.4.", "0x", "4294967296", "1.2.3.256", "256.1", "1..2", "08", "1.2.3.4.5", "-1", ""} {
		if got, ok := legacyIPv4(in); ok {
			t.Errorf("%q read as the address %s", in, got)
		}
	}
	// With such a name the guard refuses loopback by the resolved address
	g := &Guard{}
	if _, err := g.SafeResolvedHost(context.Background(), "2130706433", Options{AllowPrivate: true}); message(err) != "Hostname 2130706433 resolves to blocked address 127.0.0.1" {
		t.Errorf("decimal loopback: %v", err)
	}
}

func TestLookupErrorText(t *testing.T) {
	if got := (&LookupError{Code: "ENOTFOUND", Host: "x.invalid"}).Error(); got != "getaddrinfo ENOTFOUND x.invalid" {
		t.Errorf("got %q", got)
	}
	if got := lookupError(context.DeadlineExceeded, "slow.example").Error(); got != "getaddrinfo EAI_AGAIN slow.example" {
		t.Errorf("deadline: %q", got)
	}
}
