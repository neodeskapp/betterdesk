package serverinfo

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// jsURL holds the parts of a URL as "new URL(text)" reports them in
// JavaScript.
type jsURL struct {
	// protocol is the scheme in lower case with its colon: "https:".
	protocol string
	// hostname is the host without the port, an IPv6 address in brackets.
	hostname string
	// port is empty for the scheme's default port.
	port string
	// pathname is "/" or longer for http and https.
	pathname string
	// search is "?" and the query, or empty when there is no query or an
	// empty one.
	search string
}

// The characters a URL parser percent-encodes in each part, besides the
// control characters and everything outside ASCII.
const (
	queryEncodeSet        = " \"#<>"
	specialQueryEncodeSet = queryEncodeSet + "'"
	pathEncodeSet         = queryEncodeSet + "?`{}"

	forbiddenHostChars = "\x00\t\n\r #/:<>?@[\\]^|"
)

var defaultPorts = map[string]string{"ftp": "21", "http": "80", "https": "443", "ws": "80", "wss": "443"}

func isSpecialScheme(scheme string) bool {
	_, ok := defaultPorts[scheme]
	return ok || scheme == "file"
}

// parseJSURL parses an absolute URL the way the URL standard does (the
// parser of browsers and of Node), with the deviations of the parser in
// Node 22 that keepsDotSegments describes. It reports false where "new
// URL" throws.
//
// A host with letters outside ASCII is converted to its "xn--" form by
// domainToASCII, which follows Node closely but not in every case.
func parseJSURL(input string) (u jsURL, ok bool) {
	input = validText(input)
	input = strings.TrimFunc(input, func(r rune) bool { return r <= ' ' })
	input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)

	end := 0
	for end < len(input) && (isASCIIAlphanumeric(input[end]) || strings.IndexByte("+-.", input[end]) >= 0) {
		end++
	}
	if end == 0 || !isASCIIAlpha(input[0]) || end == len(input) || input[end] != ':' {
		return u, false
	}
	scheme := lowerASCII(input[:end])
	u.protocol = scheme + ":"
	rest := input[end+1:]
	special := isSpecialScheme(scheme)

	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		set := queryEncodeSet
		if special {
			set = specialQueryEncodeSet
		}
		if query := percentEncode(rest[i+1:], set); query != "" {
			u.search = "?" + query
		}
		rest = rest[:i]
	}

	p := urlParser{url: &u, scheme: scheme}
	switch {
	case scheme == "file":
		ok = p.parseFile(rest)
	case special:
		ok = p.parseAuthority(strings.TrimLeft(rest, `/\`), true)
	case strings.HasPrefix(rest, "//"):
		ok = p.parseAuthority(rest[2:], false)
	case strings.HasPrefix(rest, "/"):
		p.parsePath(rest[1:], false)
		ok = true
	default:
		u.pathname = percentEncode(rest, "")
		ok = true
	}
	return u, ok
}

type urlParser struct {
	url     *jsURL
	scheme  string
	hasHost bool
}

func indexSlash(s string, special bool) int {
	if special {
		return strings.IndexAny(s, `/\`)
	}
	return strings.IndexByte(s, '/')
}

// parseAuthority reads "user:password@host:port" and the path after it.
func (p *urlParser) parseAuthority(rest string, special bool) bool {
	authority, path, hasPath := rest, "", false
	if i := indexSlash(rest, special); i >= 0 {
		authority, path, hasPath = rest[:i], rest[i+1:], true
	}
	hostPort := authority
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		hostPort = authority[at+1:]
		if hostPort == "" {
			return false
		}
	}

	name, port, hasPort := hostPort, "", false
	bracketed := false
scan:
	for i := 0; i < len(hostPort); i++ {
		switch hostPort[i] {
		case '[':
			bracketed = true
		case ']':
			bracketed = false
		case ':':
			if !bracketed {
				name, port, hasPort = hostPort[:i], hostPort[i+1:], true
				break scan
			}
		}
	}
	if name == "" && (special || hasPort) {
		return false
	}
	if !p.parseHost(name, !special) {
		return false
	}
	if hasPort && !p.parsePort(port) {
		return false
	}
	if special || hasPath {
		p.parsePath(path, special)
	}
	return true
}

func (p *urlParser) parsePort(port string) bool {
	if port == "" {
		return true
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	port = strings.TrimLeft(port, "0")
	if len(port) > 5 || len(port) == 5 && port > "65535" {
		return false
	}
	if port == "" {
		port = "0"
	}
	if port != defaultPorts[p.scheme] {
		p.url.port = port
	}
	return true
}

// parseFile reads what follows "file:". A file URL has no user name and no
// port; a drive letter where the host would be is the start of the path.
func (p *urlParser) parseFile(rest string) bool {
	p.hasHost = true
	isSlash := func(s string) bool { return s != "" && (s[0] == '/' || s[0] == '\\') }
	if !isSlash(rest) {
		p.parsePath(rest, true)
		return true
	}
	rest = rest[1:]
	if !isSlash(rest) {
		p.parsePath(rest, true)
		return true
	}
	rest = rest[1:]

	name, path := rest, ""
	if i := indexSlash(rest, true); i >= 0 {
		name, path = rest[:i], rest[i:]
	}
	switch {
	case isWindowsDriveLetter(name):
		p.parsePath(rest, true)
		return true
	case name != "":
		if !p.parseHost(name, false) {
			return false
		}
		if p.url.hostname == "localhost" {
			p.url.hostname = ""
		}
	}
	if isSlash(path) {
		path = path[1:]
	}
	p.parsePath(path, true)
	return true
}

// parsePath reads a path given without its first slash: "." and ".."
// segments are resolved and each segment is percent-encoded.
//
// Two things are done as Node 22 does them, not as the standard says:
//
//   - Where the scheme is not one the standard knows, a ".." at the very
//     end that has nothing left to remove adds no slash: "x://h/.." is
//     "x://h" and "x:/a/../.." is "x:" (standard: "x://h/" and "x:/").
//   - In a file URL ".." never removes a first segment that starts with a
//     drive letter: "file:///h:80/../x" is "file:///h:80/x" (standard: only
//     a segment that is nothing but the drive letter stays).
func (p *urlParser) parsePath(path string, special bool) {
	isFile := p.scheme == "file"
	if keepsDotSegments(path, isFile) {
		p.url.pathname = "/" + path
		return
	}
	var segments []string
	for rest, last := path, false; !last; {
		segment := rest
		if i := indexSlash(rest, special); i >= 0 {
			segment, rest = rest[:i], rest[i+1:]
		} else {
			last = true
		}
		switch {
		case isDoubleDot(segment):
			shortened := false
			if n := len(segments); n > 0 && !(isFile && n == 1 && startsWithDrive(segments[0])) {
				segments, shortened = segments[:n-1], true
			}
			if last && (shortened || special) {
				segments = append(segments, "")
			}
		case isSingleDot(segment):
			if last {
				segments = append(segments, "")
			}
		default:
			if isFile && len(segments) == 0 && isWindowsDriveLetter(segment) {
				segment = segment[:1] + ":"
			}
			segments = append(segments, percentEncode(segment, pathEncodeSet))
		}
	}
	if len(segments) > 0 {
		p.url.pathname = "/" + strings.Join(segments, "/")
	}
}

// keepsDotSegments reports whether the URL parser of Node 22 (ada 2.9.2)
// leaves a path, given without its first slash, exactly as written. It
// does when the path has a dot but no "%", no backslash and nothing to
// percent-encode, does not start with a dot, and the first "/." in it
// starts an ordinary name such as ".well-known": every "." and ".."
// segment after that name is then overlooked. A file path that starts with
// a drive letter is always read in full.
func keepsDotSegments(path string, isFile bool) bool {
	if path == "" || path[0] == '.' {
		return false
	}
	if isFile && len(path) >= 2 && isWindowsDriveLetter(path[:2]) && (len(path) == 2 || path[2] == '/') {
		return false
	}
	for i := 0; i < len(path); i++ {
		if c := path[i]; c <= ' ' || c >= 0x7F || c == '%' || c == '\\' || strings.IndexByte(pathEncodeSet, c) >= 0 {
			return false
		}
	}
	i := strings.Index(path, "/.")
	return i >= 0 && i+2 < len(path) && path[i+2] != '.' && path[i+2] != '/'
}

func isSingleDot(segment string) bool {
	return segment == "." || strings.EqualFold(segment, "%2e")
}

func isDoubleDot(segment string) bool {
	switch len(segment) {
	case 2:
		return segment == ".."
	case 4:
		return strings.EqualFold(segment, ".%2e") || strings.EqualFold(segment, "%2e.")
	case 6:
		return strings.EqualFold(segment, "%2e%2e")
	}
	return false
}

// isWindowsDriveLetter reports whether s is a letter and ":" or "|".
func isWindowsDriveLetter(s string) bool {
	return len(s) == 2 && isASCIIAlpha(s[0]) && (s[1] == ':' || s[1] == '|')
}

// startsWithDrive reports whether s starts with a letter and ":".
func startsWithDrive(s string) bool {
	return len(s) >= 2 && isASCIIAlpha(s[0]) && s[1] == ':'
}

// parseHost reads a host: an IPv6 address in brackets, the host of a scheme
// the standard does not know (kept as written, percent-encoded), or a
// domain or IPv4 address.
func (p *urlParser) parseHost(name string, opaque bool) bool {
	p.hasHost = true
	if strings.HasPrefix(name, "[") {
		if !strings.HasSuffix(name, "]") {
			return false
		}
		address, ok := parseIPv6(name[1 : len(name)-1])
		if !ok {
			return false
		}
		p.url.hostname = "[" + formatIPv6(address) + "]"
		return true
	}
	if opaque {
		if strings.ContainsAny(name, forbiddenHostChars) {
			return false
		}
		p.url.hostname = percentEncode(name, "")
		return true
	}

	decoded := percentDecode(name)
	if !utf8.ValidString(decoded) {
		return false
	}
	domain, ok := domainToASCII(decoded)
	if !ok || domain == "" {
		return false
	}
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if c <= ' ' || c >= 0x7F || c == '%' || strings.IndexByte(forbiddenHostChars, c) >= 0 {
			return false
		}
	}
	if endsInNumber(domain) {
		address, ok := parseIPv4(domain)
		if !ok {
			return false
		}
		domain = address
	}
	p.url.hostname = domain
	return true
}

func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHexDigit(s[i+1]) && isHexDigit(s[i+2]) {
			b.WriteByte(hexValue(s[i+1])<<4 | hexValue(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// endsInNumber reports whether the last label of a domain is a number, so
// the domain has to be an IPv4 address.
func endsInNumber(domain string) bool {
	parts := strings.Split(domain, ".")
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
// octal (leading 0) or hexadecimal (leading 0x), and writes it as four
// decimal numbers.
func parseIPv4(domain string) (string, bool) {
	parts := strings.Split(domain, ".")
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	var address uint64
	for i, part := range parts {
		n, ok := parseIPv4Number(part)
		if !ok {
			return "", false
		}
		if i < len(parts)-1 {
			if n > 255 {
				return "", false
			}
			address |= n << (8 * (3 - i))
			continue
		}
		if n >= 1<<(8*(5-len(parts))) {
			return "", false
		}
		address += n
	}
	return strconv.FormatUint(address>>24, 10) + "." + strconv.FormatUint(address>>16&0xFF, 10) + "." +
		strconv.FormatUint(address>>8&0xFF, 10) + "." + strconv.FormatUint(address&0xFF, 10), true
}

// parseIPv4Number reads one number of an IPv4 address. Numbers too large
// for an address come back as 1<<40.
func parseIPv4Number(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	base := uint64(10)
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, base = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, base = s[1:], 8
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return 0, false
		}
		digit := uint64(hexValue(s[i]))
		if digit >= base {
			return 0, false
		}
		if n = n*base + digit; n > 1<<40 {
			n = 1 << 40
		}
	}
	return n, true
}

// parseIPv6 reads the inside of a bracketed IPv6 address.
func parseIPv6(s string) (address [8]uint16, ok bool) {
	piece, compress, i := 0, -1, 0
	if strings.HasPrefix(s, ":") {
		if !strings.HasPrefix(s, "::") {
			return address, false
		}
		i, piece, compress = 2, 1, 1
	}
	for i < len(s) {
		if piece == 8 {
			return address, false
		}
		if s[i] == ':' {
			if compress >= 0 {
				return address, false
			}
			i++
			piece++
			compress = piece
			continue
		}
		value, length := uint16(0), 0
		for length < 4 && i < len(s) && isHexDigit(s[i]) {
			value = value<<4 | uint16(hexValue(s[i]))
			i++
			length++
		}
		switch {
		case i < len(s) && s[i] == '.':
			if length == 0 || piece > 6 {
				return address, false
			}
			if !parseIPv4InIPv6(s[i-length:], &address, &piece) {
				return address, false
			}
			i = len(s)
			continue
		case i < len(s) && s[i] == ':':
			i++
			if i == len(s) {
				return address, false
			}
		case i < len(s):
			return address, false
		}
		address[piece] = value
		piece++
	}
	if compress >= 0 {
		for swaps, at := piece-compress, 7; at != 0 && swaps > 0; swaps, at = swaps-1, at-1 {
			address[at], address[compress+swaps-1] = address[compress+swaps-1], address[at]
		}
	} else if piece != 8 {
		return address, false
	}
	return address, true
}

// parseIPv4InIPv6 reads the dotted end of an IPv6 address ("1.2.3.4") into
// two pieces.
func parseIPv4InIPv6(s string, address *[8]uint16, piece *int) bool {
	seen, i := 0, 0
	for i < len(s) {
		if seen > 0 {
			if s[i] != '.' || seen >= 4 {
				return false
			}
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return false
		}
		number := -1
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			switch {
			case number < 0:
				number = int(s[i] - '0')
			case number == 0:
				return false
			default:
				number = number*10 + int(s[i]-'0')
			}
			if number > 255 {
				return false
			}
			i++
		}
		address[*piece] = address[*piece]<<8 | uint16(number)
		seen++
		if seen == 2 || seen == 4 {
			*piece++
		}
	}
	return seen == 4
}

// formatIPv6 writes an address with its first longest run of two or more
// zero pieces as "::".
func formatIPv6(address [8]uint16) string {
	compress, longest := -1, 1
	for i := 0; i < 8; {
		if address[i] != 0 {
			i++
			continue
		}
		start := i
		for i < 8 && address[i] == 0 {
			i++
		}
		if i-start > longest {
			compress, longest = start, i-start
		}
	}
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == compress {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteByte(':')
			}
			i += longest - 1
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(address[i]), 16))
		if i != 7 {
			b.WriteByte(':')
		}
	}
	return b.String()
}

const upperHex = "0123456789ABCDEF"

// percentEncode writes control characters, bytes outside ASCII and the
// characters of set as %XX.
func percentEncode(s, set string) string {
	needs := func(c byte) bool { return c < ' ' || c > '~' || strings.IndexByte(set, c) >= 0 }
	i := 0
	for i < len(s) && !needs(s[i]) {
		i++
	}
	if i == len(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	b.WriteString(s[:i])
	for ; i < len(s); i++ {
		c := s[i]
		if needs(c) {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isASCIIAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isASCIIAlphanumeric(c byte) bool {
	return isASCIIAlpha(c) || c >= '0' && c <= '9'
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
