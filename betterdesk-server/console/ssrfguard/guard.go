// Package ssrfguard decides whether the console may open a connection to
// a host or fetch a URL that a signed-in person named: the checks of
// lib/ssrfGuard.js, with the same answers and the same messages.
//
// The guard looks at text. An address counts as blocked when its text
// has one of the forms listed at IsBlockedIP; a name is resolved once and
// every address it resolves to is checked the same way. Nothing here
// opens a connection, and nothing ties a later connection to the
// addresses that were checked: a caller that dials the returned name
// resolves it a second time.
package ssrfguard

import (
	"context"
	"strings"
)

// Options say how strict a check is.
type Options struct {
	// AllowPrivate lets the private ranges through (10.0.0.0/8,
	// 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10, 198.18.0.0/15,
	// 0.0.0.0/8 and the unique local IPv6 addresses), for the network
	// tools of an administrator. Loopback and link-local stay blocked.
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

// IsBlockedIP reports whether an address, as text, must not be reached
// (isBlockedIp). Text that is no address is blocked when it is empty and
// let through otherwise.
//
// IPv4, written as four decimal numbers:
//
//	127.x.x.x, 169.254.x.x                              always
//	0.x.x.x, 10.x.x.x, 172.16-31.x.x, 192.168.x.x,
//	100.64-127.x.x, 198.18-19.x.x                       unless AllowPrivate
//
// IPv6, compared as lower-case text:
//
//	"::1", text that starts with "fe80:"                always
//	text that starts with "fc" or "fd"                  unless AllowPrivate
//	"::ffff:" followed by an IPv4 address               as that IPv4 address
//
// Other spellings of the same addresses are not recognized:
// "0:0:0:0:0:0:0:1", "::ffff:7f00:1" and "::127.0.0.1" pass.
func IsBlockedIP(ip string, opts Options) bool {
	if ip == "" {
		return true
	}
	if IsIPv4(ip) {
		a, b := leadingOctets(ip)
		if a == 127 {
			return true
		}
		if a == 169 && b == 254 {
			return true
		}
		if opts.AllowPrivate {
			return false
		}
		switch {
		case a == 0 || a == 10 || a == 127:
			return true
		case a == 172 && b >= 16 && b <= 31:
			return true
		case a == 192 && b == 168:
			return true
		case a == 100 && b >= 64 && b <= 127:
			return true
		case a == 198 && (b == 18 || b == 19):
			return true
		}
		return false
	}
	if IsIPv6(ip) {
		normalized := toLowerCase(ip)
		if normalized == "::1" {
			return true
		}
		if strings.HasPrefix(normalized, "fe80:") {
			return true
		}
		if !opts.AllowPrivate && (strings.HasPrefix(normalized, "fc") || strings.HasPrefix(normalized, "fd")) {
			return true
		}
		if mapped, ok := strings.CutPrefix(normalized, "::ffff:"); ok && IsIPv4(mapped) {
			return IsBlockedIP(mapped, opts)
		}
	}
	return false
}

// leadingOctets reads the first two numbers of text that IsIPv4 accepted.
func leadingOctets(ip string) (a, b int) {
	parts := strings.SplitN(ip, ".", 3)
	return decimal(parts[0]), decimal(parts[1])
}

func decimal(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n
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
