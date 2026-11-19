package avatars

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// target is a picture address the console may ask: the parts of an http
// or https URL as a browser's URL parser reads them.
type target struct {
	// scheme is "http" or "https".
	scheme string
	// hostname is the host in lower case: a name, an IPv4 address in its
	// dotted form, or an IPv6 address without brackets.
	hostname string
	// port is empty for the scheme's own port.
	port string
	// path is the escaped path, "/" or longer.
	path string
	// query is the escaped query without its "?", empty when there is none.
	query string
}

// url returns the target as the value an HTTP request is made from. The
// path and the query go out as they are escaped here.
func (t target) url() *url.URL {
	host := t.hostname
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if t.port != "" {
		host += ":" + t.port
	}
	u := &url.URL{Scheme: t.scheme, Host: host, Opaque: t.path, RawQuery: t.query}
	if strings.HasPrefix(t.path, "//") {
		// An opaque part that starts with two slashes would read as a
		// host. Such a path goes out as it is when it is escaped the way
		// net/url escapes, and as part of the whole address otherwise.
		u.Opaque = "//" + host + t.path
		if plain, err := url.PathUnescape(t.path); err == nil {
			exact := &url.URL{Scheme: t.scheme, Host: host, Path: plain, RawPath: t.path, RawQuery: t.query}
			if exact.EscapedPath() == t.path {
				return exact
			}
		}
	}
	return u
}

func invalidURL(message string) *Error { return refuse("invalid_url", message) }

// checkURL reads a picture address and refuses what the console never
// fetches: anything but http and https, credentials in the address, the
// names of this machine and of cloud metadata services, and refused
// addresses written as literals. A name is judged again when it has been
// resolved (guardedDial).
//
// The text is read as a browser reads it: white space around it and line
// breaks inside are dropped, backslashes count as slashes, any number of
// slashes may follow the scheme, and an IPv4 address may be written with
// hex, octal or fewer than four numbers ("0x7f.1" is 127.0.0.1).
func checkURL(text string) (target, error) {
	t, ok := parseTarget(text)
	if !ok {
		return target{}, invalidURL("Not a valid URL")
	}
	if t.scheme != "http" && t.scheme != "https" {
		return target{}, invalidURL("Only http and https URLs")
	}
	if t.credentials {
		return target{}, invalidURL("Credentials in the URL are not allowed")
	}
	host := t.hostname
	if host == "" {
		return target{}, invalidURL("URL has no host")
	}
	if refusedHostnames[host] || strings.HasSuffix(host, ".localhost") {
		return target{}, refuse("address_refused", "Host "+host+" is refused")
	}
	if addr, err := netip.ParseAddr(host); err == nil && refusedAddr(addr) {
		return target{}, refuse("address_refused", "Address "+host+" is refused")
	}
	return t.target, nil
}

// addressForLog is what a log line may show of a picture address: its
// scheme, host and path, cut to 200 characters. The query, the fragment
// and credentials are left out, because identity providers put signed
// tokens there. An http or https address is read as checkURL reads it;
// other text loses what follows the first "?" or "#" and what comes
// before an "@" after "://".
func addressForLog(address string) string {
	if t, ok := parseTarget(address); ok && (t.scheme == "http" || t.scheme == "https") {
		return sliceUnits(t.scheme+"://"+t.url().Host+t.path, 200)
	}
	if i := strings.IndexAny(address, "?#"); i >= 0 {
		address = address[:i]
	}
	if scheme, rest, ok := strings.Cut(address, "://"); ok {
		authority, _, _ := strings.Cut(rest, "/")
		if i := strings.LastIndexByte(authority, '@'); i >= 0 {
			address = scheme + "://" + rest[i+1:]
		}
	}
	return sliceUnits(address, 200)
}

type parsedTarget struct {
	target
	credentials bool
}

// parseTarget splits an absolute URL. For a scheme other than http and
// https only the scheme is filled in. It reports false for text that is
// no absolute URL or whose host or port cannot be read.
func parseTarget(input string) (parsedTarget, bool) {
	var t parsedTarget
	if !utf8.ValidString(input) {
		input = strings.ToValidUTF8(input, "�")
	}
	input = strings.TrimFunc(input, func(r rune) bool { return r <= ' ' })
	input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)

	end := 0
	for end < len(input) && (isAlphanumeric(input[end]) || strings.IndexByte("+-.", input[end]) >= 0) {
		end++
	}
	if end == 0 || !isAlpha(input[0]) || end == len(input) || input[end] != ':' {
		return t, false
	}
	t.scheme = asciiLower(input[:end])
	if t.scheme != "http" && t.scheme != "https" {
		return t, true
	}
	rest := input[end+1:]
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		t.query = percentEncode(rest[i+1:], queryEncodeSet)
		rest = rest[:i]
	}
	rest = strings.ReplaceAll(rest, `\`, "/")
	rest = strings.TrimLeft(rest, "/")
	authority, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	}
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		// "user:password@": either part may be empty and then does not count
		t.credentials = strings.Trim(authority[:i], ":") != "" || strings.Count(authority[:i], ":") > 1
		authority = authority[i+1:]
	}
	host, port := authority, ""
	if strings.HasPrefix(host, "[") {
		closing := strings.IndexByte(host, ']')
		if closing < 0 {
			return t, false
		}
		host, port = host[:closing+1], host[closing+1:]
		if port != "" && port[0] != ':' {
			return t, false
		}
		port = strings.TrimPrefix(port, ":")
	} else if i := strings.IndexByte(host, ':'); i >= 0 {
		host, port = host[:i], host[i+1:]
	}
	if !t.parsePort(port) || !t.parseHost(host) {
		return t, false
	}
	t.path = cleanPath(path)
	return t, true
}

// parsePort keeps a port that is not the scheme's own. Digits only, up to
// 65535.
func (t *parsedTarget) parsePort(port string) bool {
	if port == "" {
		return true
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	port = strings.TrimLeft(port, "0")
	if port == "" {
		port = "0"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n > 65535 {
		return false
	}
	if (t.scheme == "http" && n == 80) || (t.scheme == "https" && n == 443) {
		return true
	}
	t.port = port
	return true
}

// forbiddenHostChars may not be in a host name, besides control
// characters.
const forbiddenHostChars = " #/:<>?@[\\]^|%"

func (t *parsedTarget) parseHost(host string) bool {
	if strings.HasPrefix(host, "[") {
		inner := host[1 : len(host)-1]
		addr, err := netip.ParseAddr(inner)
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return false
		}
		if addr.Is4In6() {
			// In hex, like every other IPv6 address
			b := addr.As16()
			t.hostname = "::ffff:" + strconv.FormatUint(uint64(b[12])<<8|uint64(b[13]), 16) + ":" + strconv.FormatUint(uint64(b[14])<<8|uint64(b[15]), 16)
			return true
		}
		t.hostname = addr.String()
		return true
	}
	host = asciiLower(percentDecode(host))
	if host == "" || !utf8.ValidString(host) {
		return false
	}
	for i := 0; i < len(host); i++ {
		if c := host[i]; c <= ' ' || c == 0x7f || strings.IndexByte(forbiddenHostChars, c) >= 0 {
			return false
		}
	}
	if endsInNumber(host) {
		addr, ok := parseIPv4(host)
		if !ok {
			return false
		}
		host = addr
	}
	t.hostname = host
	return true
}

// endsInNumber reports whether the last label of a host is a number, in
// which case the whole host has to be an IPv4 address.
func endsInNumber(host string) bool {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4 reads an IPv4 address of one to four numbers, each decimal,
// hex ("0x") or octal (a leading 0); the last number fills the bytes the
// others leave.
func parseIPv4(host string) (string, bool) {
	parts := strings.Split(host, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	numbers := make([]uint64, len(parts))
	for i, part := range parts {
		n, ok := parseIPv4Number(part)
		if !ok {
			return "", false
		}
		numbers[i] = n
	}
	for _, n := range numbers[:len(numbers)-1] {
		if n > 255 {
			return "", false
		}
	}
	last := numbers[len(numbers)-1]
	if last >= 1<<(8*uint(5-len(numbers))) {
		return "", false
	}
	ip := last
	for i, n := range numbers[:len(numbers)-1] {
		ip += n << (8 * uint(3-i))
	}
	return netip.AddrFrom4([4]byte{byte(ip >> 24), byte(ip >> 16), byte(ip >> 8), byte(ip)}).String(), true
}

func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		// A number too large for 64 bits is still a number, and too large
		// for an address
		if numErr, ok := err.(*strconv.NumError); ok && numErr.Err == strconv.ErrRange {
			return 1 << 40, true
		}
		return 0, false
	}
	return n, true
}

// The characters a URL parser escapes in a path and in a query, besides
// control characters and everything outside ASCII.
const (
	queryEncodeSet = " \"#<>'"
	pathEncodeSet  = " \"#<>?`{}"
)

// cleanPath escapes a path and takes out its "." and ".." segments, also
// where a dot is written "%2e".
func cleanPath(path string) string {
	var out []string
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, segment := range segments {
		last := i == len(segments)-1
		switch lowered := asciiLower(segment); lowered {
		case "..", ".%2e", "%2e.", "%2e%2e":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		case ".", "%2e":
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, percentEncode(segment, pathEncodeSet))
		}
	}
	return "/" + strings.Join(out, "/")
}

func percentEncode(s, set string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x1f || c >= 0x7f || strings.IndexByte(set, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			n, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b.WriteByte(byte(n))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isAlphanumeric(c byte) bool { return isAlpha(c) || c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// resolve returns the address a redirect's Location names when it is read
// against this one, as text for checkURL.
func (t target) resolve(location string) string {
	ref := strings.TrimFunc(location, func(r rune) bool { return r <= ' ' })
	ref = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(ref)
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		ref = ref[:i]
	}
	if scheme, rest, ok := splitScheme(ref); ok {
		if scheme != t.scheme || len(rest) >= 2 && isSlash(rest[0]) && isSlash(rest[1]) {
			return ref
		}
		// "http:picture.png" under an http address is a relative path
		ref = rest
	}
	path, query, hasQuery := strings.Cut(ref, "?")
	path = strings.ReplaceAll(path, `\`, "/")
	if hasQuery {
		query = "?" + query
	}
	origin := t.scheme + "://" + t.url().Host
	switch {
	case strings.HasPrefix(path, "//"):
		return t.scheme + ":" + path + query
	case strings.HasPrefix(path, "/"):
		return origin + path + query
	case path == "" && hasQuery:
		return origin + t.path + query
	case path == "":
		if t.query != "" {
			return origin + t.path + "?" + t.query
		}
		return origin + t.path
	}
	return origin + t.path[:strings.LastIndexByte(t.path, '/')+1] + path + query
}

// splitScheme cuts "scheme:" off the front of an address.
func splitScheme(s string) (scheme, rest string, ok bool) {
	end := 0
	for end < len(s) && (isAlphanumeric(s[end]) || strings.IndexByte("+-.", s[end]) >= 0) {
		end++
	}
	if end == 0 || !isAlpha(s[0]) || end == len(s) || s[end] != ':' {
		return "", "", false
	}
	return asciiLower(s[:end]), s[end+1:], true
}

func isSlash(c byte) bool { return c == '/' || c == 0x5c }
