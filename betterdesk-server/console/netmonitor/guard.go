package netmonitor

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Guard decides which hosts the server may reach for someone who uses the
// network tools. Every check asks it before anything leaves the server.
// It is what the checks need of lib/ssrfGuard.js: private networks are
// allowed, loopback, link-local and the cloud metadata names are not.
//
// A refusal is a *BlockedError; its message goes to the client.
type Guard interface {
	// SafeResolvedHost is assertSafeResolvedHost(host, {allowPrivate:
	// true}): it refuses a blocked name or address, looks a name up and
	// refuses it when one of its addresses is blocked. It returns the
	// host trimmed, in lower case and without brackets.
	SafeResolvedHost(ctx context.Context, host string) (string, error)
	// SafeMonitoringURL is assertSafeMonitoringUrl(url): the URL must be
	// http or https without a user name or password, and its host must
	// pass SafeResolvedHost. It returns the URL's parts.
	SafeMonitoringURL(ctx context.Context, rawURL string) (URL, error)
}

// URL holds the parts of a checked URL as JavaScript's URL class names
// them.
type URL struct {
	// Protocol is "http:" or "https:".
	Protocol string
	// Hostname is the host in lower case, an IPv4 address in its usual
	// form, an IPv6 address in brackets.
	Hostname string
	// Port is the port's digits, empty for the scheme's own port.
	Port string
	// Pathname starts with "/".
	Pathname string
	// Search is "?" and the query, or empty.
	Search string
}

// BlockedError is the guard's refusal (SsrfBlockedError).
type BlockedError struct {
	Message string
}

func (e *BlockedError) Error() string { return e.Message }

func blocked(message string) error { return &BlockedError{Message: message} }

// Resolver looks host names up. *net.Resolver is one.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// blockedHostnames are refused by name, before any lookup.
var blockedHostnames = map[string]bool{
	"localhost":                true,
	"localhost.localdomain":    true,
	"metadata.google.internal": true,
	"metadata.goog":            true,
}

// The address patterns of Node's net.isIPv4 and net.isIPv6.
const (
	v4Seg = `(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])`
	v4Str = `(?:` + v4Seg + `\.){3}` + v4Seg
	v6Seg = `(?:[0-9a-fA-F]{1,4})`
)

var (
	ipv4Pattern = regexp.MustCompile(`^` + v4Str + `$`)
	ipv6Pattern = regexp.MustCompile(`^(?:` +
		`(?:` + v6Seg + `:){7}(?:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){6}(?:` + v4Str + `|:` + v6Seg + `|:)|` +
		`(?:` + v6Seg + `:){5}(?::` + v4Str + `|(?::` + v6Seg + `){1,2}|:)|` +
		`(?:` + v6Seg + `:){4}(?:(?::` + v6Seg + `){0,1}:` + v4Str + `|(?::` + v6Seg + `){1,3}|:)|` +
		`(?:` + v6Seg + `:){3}(?:(?::` + v6Seg + `){0,2}:` + v4Str + `|(?::` + v6Seg + `){1,4}|:)|` +
		`(?:` + v6Seg + `:){2}(?:(?::` + v6Seg + `){0,3}:` + v4Str + `|(?::` + v6Seg + `){1,5}|:)|` +
		`(?:` + v6Seg + `:){1}(?:(?::` + v6Seg + `){0,4}:` + v4Str + `|(?::` + v6Seg + `){1,6}|:)|` +
		`(?::(?:(?::` + v6Seg + `){0,5}:` + v4Str + `|(?::` + v6Seg + `){1,7}|:))` +
		`)(?:%[0-9a-zA-Z-.:]{1,})?$`)
)

// isIPv4 is net.isIPv4: four decimal numbers up to 255 without leading
// zeros.
func isIPv4(s string) bool { return ipv4Pattern.MatchString(s) }

// isIPv6 is net.isIPv6: an IPv6 address, with a zone after "%" or not.
func isIPv6(s string) bool { return ipv6Pattern.MatchString(s) }

// isIP is net.isIP(s) != 0.
func isIP(s string) bool { return isIPv4(s) || isIPv6(s) }

// isBlockedIP is isBlockedIp(ip, {allowPrivate}): true for an address the
// server must not reach. Loopback (127.0.0.0/8, ::1) and link-local
// (169.254.0.0/16, fe80:) are always blocked; the private ranges only
// without allowPrivate.
//
// The IPv6 rules compare text, as the old guard did, so they only see the
// usual spelling of an address (BUGS.md SC1).
func isBlockedIP(ip string, allowPrivate bool) bool {
	if ip == "" {
		return true
	}
	if isIPv4(ip) {
		parts := strings.Split(ip, ".")
		a, _ := strconv.Atoi(parts[0])
		b, _ := strconv.Atoi(parts[1])
		switch {
		case a == 127:
			return true
		case a == 169 && b == 254:
			return true
		case allowPrivate:
			return false
		case a == 0 || a == 10:
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
	if isIPv6(ip) {
		normalized := strings.ToLower(ip)
		switch {
		case normalized == "::1":
			return true
		case strings.HasPrefix(normalized, "fe80:"):
			return true
		case !allowPrivate && (strings.HasPrefix(normalized, "fc") || strings.HasPrefix(normalized, "fd")):
			return true
		}
		if strings.HasPrefix(normalized, "::ffff:") {
			if mapped := normalized[7:]; isIPv4(mapped) {
				return isBlockedIP(mapped, allowPrivate)
			}
		}
	}
	return false
}

// normalizeHostname trims a host, lowers it and takes the brackets off an
// IPv6 address.
func normalizeHostname(host string) string {
	h := strings.ToLower(strings.TrimFunc(host, isJSSpace))
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	return h
}

// assertSafeHostname refuses an empty host, a blocked name and a blocked
// address, and returns the normalized host.
func assertSafeHostname(host string, allowPrivate bool) (string, error) {
	clean := normalizeHostname(host)
	switch {
	case clean == "":
		return "", blocked("Missing hostname")
	case blockedHostnames[clean]:
		return "", blocked("Blocked hostname: " + clean)
	case isIP(clean) && isBlockedIP(clean, allowPrivate):
		return "", blocked("Blocked IP address: " + clean)
	}
	return clean, nil
}

// ssrfGuard is the guard of lib/ssrfGuard.js for the network tools.
type ssrfGuard struct {
	resolver Resolver
}

// SafeResolvedHost implements Guard.
func (g ssrfGuard) SafeResolvedHost(ctx context.Context, host string) (string, error) {
	return g.safeResolvedHost(ctx, host, true)
}

func (g ssrfGuard) safeResolvedHost(ctx context.Context, host string, allowPrivate bool) (string, error) {
	clean, err := assertSafeHostname(host, allowPrivate)
	if err != nil {
		return "", err
	}
	if isIP(clean) {
		return clean, nil
	}
	addresses, err := lookupAll(ctx, g.resolver, clean)
	if err != nil {
		return "", blocked("DNS lookup failed for " + clean + ": " + err.Error())
	}
	if len(addresses) == 0 {
		return "", blocked("No DNS records for " + clean)
	}
	for _, address := range addresses {
		if isBlockedIP(address, allowPrivate) {
			return "", blocked("Hostname " + clean + " resolves to blocked address " + address)
		}
	}
	return clean, nil
}

// SafeMonitoringURL implements Guard.
func (g ssrfGuard) SafeMonitoringURL(ctx context.Context, rawURL string) (URL, error) {
	if rawURL == "" {
		return URL{}, blocked("URL is required")
	}
	parsed, ok := parseJSURL(rawURL)
	if !ok {
		return URL{}, blocked("Invalid URL")
	}
	if parsed.protocol != "http:" && parsed.protocol != "https:" {
		return URL{}, blocked("Only http and https URLs are allowed")
	}
	if parsed.credentials {
		return URL{}, blocked("Credentials in URL are not allowed")
	}
	if _, err := g.safeResolvedHost(ctx, parsed.hostname, true); err != nil {
		return URL{}, err
	}
	return URL{
		Protocol: parsed.protocol,
		Hostname: parsed.hostname,
		Port:     parsed.port,
		Pathname: parsed.pathname,
		Search:   parsed.search,
	}, nil
}

// lookupError is a failed lookup with the message Node gave it:
// "getaddrinfo ENOTFOUND host".
type lookupError struct {
	code string
	host string
	err  error
}

func (e *lookupError) Error() string { return "getaddrinfo " + e.code + " " + e.host }
func (e *lookupError) Unwrap() error { return e.err }

// lookupAll is dns.lookup(host, {all: true}): the addresses of a name as
// text, in the order the system's resolver gives them. An address is its
// own answer, and so are the spellings of an IPv4 address the system
// resolver reads without asking anyone: "2130706433", "0x7f.1", "127.1".
func lookupAll(ctx context.Context, resolver Resolver, host string) ([]string, error) {
	if isIP(host) {
		return []string{host}, nil
	}
	if address, ok := numericIPv4(host); ok {
		return []string{address}, nil
	}
	found, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		code := "ENOTFOUND"
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && !dnsErr.IsNotFound && (dnsErr.IsTemporary || dnsErr.IsTimeout) {
			code = "EAI_AGAIN"
		}
		if ctx.Err() != nil {
			code = "EAI_AGAIN"
		}
		return nil, &lookupError{code: code, host: host, err: err}
	}
	addresses := make([]string, 0, len(found))
	for _, a := range found {
		addr, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if a.Zone != "" {
			addr = addr.WithZone(a.Zone)
		}
		addresses = append(addresses, addr.String())
	}
	return addresses, nil
}

// lookupFirst is dns.lookup(host): the first address of a name.
func lookupFirst(ctx context.Context, resolver Resolver, host string) (string, error) {
	addresses, err := lookupAll(ctx, resolver, host)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", &lookupError{code: "ENOTFOUND", host: host}
	}
	return addresses[0], nil
}

// numericIPv4 reads the spellings of an IPv4 address that the system
// resolver takes besides the usual one: one to four numbers, each decimal,
// octal (leading 0) or hexadecimal (leading 0x), the last one filling the
// bytes that are left.
func numericIPv4(host string) (string, bool) {
	if host == "" || strings.HasSuffix(host, ".") {
		return "", false
	}
	for _, part := range strings.Split(host, ".") {
		if len(part) == 2 && (part == "0x" || part == "0X") {
			return "", false
		}
	}
	return parseIPv4(host)
}
