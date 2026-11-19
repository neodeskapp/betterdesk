// Package ssrfguard decides whether the console may open a connection to
// a host or fetch a URL that a signed-in person named: the checks of
// lib/ssrfGuard.js, with its messages.
//
// An address is parsed and refused by its range (IsBlockedIP); a name is
// resolved once and every address it resolves to is checked the same way.
// Nothing here opens a connection, and nothing ties a later connection to
// the addresses that were checked: a caller that dials the returned name
// resolves it a second time.
package ssrfguard

import (
	"context"
	"net/netip"
	"strings"
)

// Options say how strict a check is.
type Options struct {
	// AllowPrivate lets the private ranges through (10.0.0.0/8,
	// 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 198.18.0.0/15 and the
	// unique local IPv6 addresses), for the network tools of an
	// administrator. Loopback, link-local, 0.0.0.0/8 and :: stay blocked:
	// a connection to 0.0.0.0 or :: reaches the machine itself.
	AllowPrivate bool
}

// Code is the code of every BlockedError.
const Code = "SSRF_BLOCKED"

// BlockedError is the refusal of a check (SsrfBlockedError). Its message
// goes to the client as it is.
type BlockedError struct {
	Message string
	Code    string
}

func (e *BlockedError) Error() string { return e.Message }

func blocked(message string) *BlockedError {
	return &BlockedError{Message: message, Code: Code}
}

// blockedHostnames are refused by name, before any lookup.
var blockedHostnames = map[string]bool{
	"localhost":                true,
	"localhost.localdomain":    true,
	"metadata.google.internal": true,
	"metadata.goog":            true,
}

// The ranges the guard refuses. alwaysBlocked holds even for the network
// tools of an administrator.
var (
	alwaysBlocked = []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fe80::/10"),
	}
	privateRanges = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("fc00::/7"),
	}
)

// IsBlockedIP reports whether an address, as text, must not be reached
// (isBlockedIp). The address is parsed and its range decides, however it
// is written: "0:0:0:0:0:0:0:1" is ::1, and an IPv4 address inside an
// IPv6 one, mapped (::ffff:7f00:1, ::ffff:127.0.0.1) or compatible
// (::127.0.0.1), counts as that IPv4 address. A zone does not matter.
// Text that is no address is blocked.
//
//	0.0.0.0/8, 127.0.0.0/8, 169.254.0.0/16,
//	::, ::1, fe80::/10                                 always
//	10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
//	100.64.0.0/10, 198.18.0.0/15, fc00::/7          unless AllowPrivate
func IsBlockedIP(ip string, opts Options) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return true
	}
	addr = embeddedIPv4(addr.WithZone(""))
	for _, r := range alwaysBlocked {
		if r.Contains(addr) {
			return true
		}
	}
	if opts.AllowPrivate {
		return false
	}
	for _, r := range privateRanges {
		if r.Contains(addr) {
			return true
		}
	}
	return false
}

// embeddedIPv4 is the IPv4 address an IPv6 address carries in its last 32
// bits when the bits before them are those of a mapped address
// (::ffff:0:0/96) or of a compatible one (::/96, without :: and ::1), and
// the address itself otherwise.
func embeddedIPv4(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}
	if !addr.Is6() || addr.IsUnspecified() || addr.IsLoopback() {
		return addr
	}
	b := addr.As16()
	for _, octet := range b[:12] {
		if octet != 0 {
			return addr
		}
	}
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
}

// normalizeHostname trims a host name, lowers its letters and takes the
// square brackets off an IPv6 literal.
func normalizeHostname(host string) string {
	h := toLowerCase(trim(host))
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") && len(h) >= 2 {
		h = h[1 : len(h)-1]
	}
	return h
}

// SafeHostname checks a host name or address without resolving it
// (assertSafeHostname) and returns it normalized: trimmed, in lower case,
// without the brackets of an IPv6 literal.
//
//	""                         Missing hostname
//	localhost and the like     Blocked hostname: <name>
//	a blocked address          Blocked IP address: <address>
func SafeHostname(host string, opts Options) (string, error) {
	clean := normalizeHostname(host)
	if clean == "" {
		return "", blocked("Missing hostname")
	}
	if blockedHostnames[clean] {
		return "", blocked("Blocked hostname: " + clean)
	}
	if IsIP(clean) != 0 && IsBlockedIP(clean, opts) {
		return "", blocked("Blocked IP address: " + clean)
	}
	return clean, nil
}

// LookupFunc resolves a host name to all of its addresses, as text and in
// the order the system gave them: dns.lookup(host, { all: true, verbatim:
// true }). The text of its error ends the message of a failed lookup.
type LookupFunc func(ctx context.Context, host string) ([]string, error)

// Guard checks names that need a lookup.
type Guard struct {
	// Lookup resolves names. nil means SystemLookup.
	Lookup LookupFunc
}

func (g *Guard) lookup(ctx context.Context, host string) ([]string, error) {
	if g != nil && g.Lookup != nil {
		return g.Lookup(ctx, host)
	}
	return SystemLookup(ctx, host)
}

// SafeResolvedHost is SafeHostname followed, for a name, by one lookup
// (assertSafeResolvedHost): every address the name resolves to must pass
// IsBlockedIP. An address literal is not looked up.
//
//	the lookup fails           DNS lookup failed for <name>: <error>
//	no addresses               No DNS records for <name>
//	a blocked address          Hostname <name> resolves to blocked address <address>
//
// The addresses are checked and then forgotten. Whoever connects to the
// returned name resolves it again and may get other addresses.
func (g *Guard) SafeResolvedHost(ctx context.Context, host string, opts Options) (string, error) {
	clean, err := SafeHostname(host, opts)
	if err != nil {
		return "", err
	}
	if IsIP(clean) != 0 {
		return clean, nil
	}
	records, err := g.lookup(ctx, clean)
	if err != nil {
		return "", blocked("DNS lookup failed for " + clean + ": " + err.Error())
	}
	if len(records) == 0 {
		return "", blocked("No DNS records for " + clean)
	}
	for _, address := range records {
		if IsBlockedIP(address, opts) {
			return "", blocked("Hostname " + clean + " resolves to blocked address " + address)
		}
	}
	return clean, nil
}

// SafeHTTPURL checks a URL before the console fetches it
// (assertSafeHttpUrl) and returns it parsed. Fetch URL.Href, not the
// text that came in: the two can differ in what a host is.
//
//	""                         URL is required
//	does not parse             Invalid URL
//	not http or https          Only http and https URLs are allowed
//	user name or password      Credentials in URL are not allowed
//
// and then the refusals of SafeResolvedHost for the host.
func (g *Guard) SafeHTTPURL(ctx context.Context, raw string, opts Options) (*URL, error) {
	if raw == "" {
		return nil, blocked("URL is required")
	}
	parsed, ok := ParseURL(raw)
	if !ok {
		return nil, blocked("Invalid URL")
	}
	if parsed.Protocol != "http:" && parsed.Protocol != "https:" {
		return nil, blocked("Only http and https URLs are allowed")
	}
	if parsed.Username != "" || parsed.Password != "" {
		return nil, blocked("Credentials in URL are not allowed")
	}
	if _, err := g.SafeResolvedHost(ctx, parsed.Hostname, opts); err != nil {
		return nil, err
	}
	return parsed, nil
}

// SafeMonitoringURL is SafeHTTPURL for the network tools of an
// administrator: private addresses are allowed (assertSafeMonitoringUrl).
func (g *Guard) SafeMonitoringURL(ctx context.Context, raw string) (*URL, error) {
	return g.SafeHTTPURL(ctx, raw, Options{AllowPrivate: true})
}
