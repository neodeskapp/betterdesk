package authn

import (
	"math/big"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// A reader for absolute URLs that follows the URL Standard of browsers
// (https://url.spec.whatwg.org), as far as the single sign-on
// settings need it: whether the text is a URL at all, its scheme, and for
// http and https its origin and path.
//
// One part is approximate: a host with letters outside ASCII. The standard
// maps such a host with the IDNA tables and refuses some characters. Here
// it is folded to lower case, normalised and written in its "xn--" form,
// which gives the same result for ordinary names, and an "xn--" label is
// taken as written.

// absoluteURL is a parsed URL.
type absoluteURL struct {
	scheme string // lower case, without the colon
	// host, port and path are set for http, https, ftp, ws and wss.
	host string
	port string // empty for the scheme's own port
	path string
}

// origin is "scheme://host[:port]".
func (u absoluteURL) origin() string {
	if u.port == "" {
		return u.scheme + "://" + u.host
	}
	return u.scheme + "://" + u.host + ":" + u.port
}

var defaultPorts = map[string]string{"http": "80", "https": "443", "ftp": "21", "ws": "80", "wss": "443"}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// parseAbsoluteURL reads input as a URL with a scheme. ok is false when
// the standard's parser fails on it.
func parseAbsoluteURL(input string) (u absoluteURL, ok bool) {
	s := toWellFormed(input)
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	s = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)

	if s == "" || !isASCIIAlpha(s[0]) {
		return u, false
	}
	i := 1
	for i < len(s) && (isASCIIAlpha(s[i]) || isASCIIDigit(s[i]) || s[i] == '+' || s[i] == '-' || s[i] == '.') {
		i++
	}
	if i == len(s) || s[i] != ':' {
		return u, false
	}
	u.scheme = asciiLower(s[:i])
	rest := s[i+1:]

	if u.scheme == "file" {
		return u, validFileURL(rest)
	}
	_, special := defaultPorts[u.scheme]
	if !special {
		if strings.HasPrefix(rest, "//") {
			_, _, _, valid := parseAuthority(rest[2:], false, "")
			return u, valid
		}
		return u, true
	}
	rest = strings.TrimLeft(rest, `/\`)
	host, port, remainder, valid := parseAuthority(rest, true, defaultPorts[u.scheme])
	if !valid {
		return u, false
	}
	u.host, u.port = host, port
	if end := strings.IndexAny(remainder, "?#"); end >= 0 {
		remainder = remainder[:end]
	}
	u.path = specialPath(remainder)
	return u, true
}

// parseAuthority reads "user:password@host:port" up to the path. It
// returns the host and port as the standard writes them and what follows
// the authority.
func parseAuthority(rest string, special bool, defaultPort string) (host, port, remainder string, ok bool) {
	delimiters := "/?#"
	if special {
		delimiters = `/\?#`
	}
	authority := rest
	if end := strings.IndexAny(rest, delimiters); end >= 0 {
		authority, remainder = rest[:end], rest[end:]
	}
	hostPort := authority
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		hostPort = authority[at+1:]
		if hostPort == "" {
			return "", "", "", false
		}
	}

	rawHost, rawPort, hasPort := hostPort, "", false
	inBrackets := false
	for i := 0; i < len(hostPort); i++ {
		switch hostPort[i] {
		case '[':
			inBrackets = true
		case ']':
			inBrackets = false
		case ':':
			if !inBrackets {
				rawHost, rawPort, hasPort = hostPort[:i], hostPort[i+1:], true
			}
		}
		if hasPort {
			break
		}
	}
	if rawHost == "" && (special || hasPort) {
		return "", "", "", false
	}
	if host, ok = parseHost(rawHost, !special); !ok {
		return "", "", "", false
	}
	if rawPort != "" {
		value := 0
		for i := 0; i < len(rawPort); i++ {
			if !isASCIIDigit(rawPort[i]) {
				return "", "", "", false
			}
			if value = value*10 + int(rawPort[i]-'0'); value > 65535 {
				return "", "", "", false
			}
		}
		if port = strconv.Itoa(value); port == defaultPort {
			port = ""
		}
	}
	return host, port, remainder, true
}

// validFileURL reports whether what follows "file:" parses.
func validFileURL(rest string) bool {
	isSlash := func(c byte) bool { return c == '/' || c == '\\' }
	if len(rest) < 2 || !isSlash(rest[0]) || !isSlash(rest[1]) {
		return true
	}
	host := rest[2:]
	if end := strings.IndexAny(host, `/\?#`); end >= 0 {
		host = host[:end]
	}
	if host == "" || len(host) == 2 && isASCIIAlpha(host[0]) && (host[1] == ':' || host[1] == '|') {
		return true
	}
	_, ok := parseHost(host, false)
	return ok
}

const forbiddenHostChars = "\x00\t\n\r #/:<>?@[\\]^|"

func isForbiddenDomainChar(c byte) bool {
	return c <= 0x1f || c == 0x7f || c == '%' || strings.IndexByte(forbiddenHostChars, c) >= 0
}

// parseHost reads a host and returns it as the standard writes it.
func parseHost(input string, opaque bool) (string, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", false
		}
		return parseIPv6(input[1 : len(input)-1])
	}
	if opaque {
		if strings.ContainsAny(input, forbiddenHostChars) {
			return "", false
		}
		return input, true
	}
	domain, ok := domainToASCII(toWellFormed(percentDecode(input)))
	if !ok || domain == "" {
		return "", false
	}
	for i := 0; i < len(domain); i++ {
		if isForbiddenDomainChar(domain[i]) {
			return "", false
		}
	}
	if endsInNumber(domain) {
		return parseIPv4(domain)
	}
	return domain, true
}

func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, ok1 := hexValue(s[i+1])
			lo, ok2 := hexValue(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ignoredInHosts turns the ideographic full stop into a dot and drops the
// characters a host name ignores: the soft hyphen, the zero width space,
// the word joiner and the byte order mark.
var ignoredInHosts = strings.NewReplacer(
	string(rune(0x3002)), ".",
	string(rune(0x00AD)), "",
	string(rune(0x200B)), "",
	string(rune(0x2060)), "",
	string(rune(0xFEFF)), "",
)

// domainToASCII lowers an ASCII name, and writes a name with other letters
// in its "xn--" form.
func domainToASCII(domain string) (string, bool) {
	ascii := true
	for i := 0; i < len(domain); i++ {
		if domain[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	mapped := asciiLower(domain)
	if !ascii {
		mapped = norm.NFKC.String(cases.Lower(language.Und).String(norm.NFKC.String(domain)))
		mapped = ignoredInHosts.Replace(mapped)
	}
	labels := strings.Split(mapped, ".")
	for i, label := range labels {
		if rest, coded := strings.CutPrefix(label, "xn--"); coded {
			if !validPunycode(rest) {
				return "", false
			}
			continue
		}
		encoded, ok := punycodeLabel(label)
		if !ok {
			return "", false
		}
		labels[i] = encoded
	}
	return strings.Join(labels, "."), true
}

// validPunycode reports whether the part of a label after "xn--" decodes
// to a name with at least one letter outside ASCII and is the way that
// name is written.
func validPunycode(encoded string) bool {
	const base, tMin, tMax, skew, damp = 36, 1, 26, 38, 700
	var out []rune
	rest := encoded
	if cut := strings.LastIndexByte(encoded, '-'); cut >= 0 {
		out, rest = []rune(encoded[:cut]), encoded[cut+1:]
	}
	if rest == "" {
		return false
	}
	n, i, bias := 128, 0, 72
	for pos := 0; pos < len(rest); {
		oldI, w := i, 1
		for k := base; ; k += base {
			if pos >= len(rest) {
				return false
			}
			c := rest[pos]
			pos++
			var digit int
			switch {
			case c >= 'a' && c <= 'z':
				digit = int(c - 'a')
			case c >= '0' && c <= '9':
				digit = int(c-'0') + 26
			default:
				return false
			}
			if digit > (1<<31-1-i)/w {
				return false
			}
			i += digit * w
			t := k - bias
			if t < tMin {
				t = tMin
			} else if t > tMax {
				t = tMax
			}
			if digit < t {
				break
			}
			if w > (1<<31-1)/(base-t) {
				return false
			}
			w *= base - t
		}
		points := len(out) + 1
		delta := i - oldI
		if oldI == 0 {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > (base-tMin)*tMax/2 {
			delta /= base - tMin
			k += base
		}
		bias = k + (base-tMin+1)*delta/(delta+skew)
		if i/points > 1<<31-1-n {
			return false
		}
		n += i / points
		i %= points
		if n > utf8.MaxRune || n >= 0xD800 && n <= 0xDFFF {
			return false
		}
		out = append(out, 0)
		copy(out[i+1:], out[i:])
		out[i] = rune(n)
		i++
	}
	again, ok := punycodeLabel(string(out))
	return ok && again == "xn--"+encoded
}

// punycodeLabel writes a label with letters outside ASCII as "xn--" and
// its Punycode (RFC 3492). An ASCII label is returned in lower case.
func punycodeLabel(label string) (string, bool) {
	runes := []rune(label)
	var out strings.Builder
	basic := 0
	for _, r := range runes {
		if r < utf8.RuneSelf {
			out.WriteByte(byte(r))
			basic++
		}
	}
	if basic == len(runes) {
		return asciiLower(label), true
	}
	const base, tMin, tMax, skew, damp = 36, 1, 26, 38, 700
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	adapt := func(delta, points int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > (base-tMin)*tMax/2 {
			delta /= base - tMin
			k += base
		}
		return k + (base-tMin+1)*delta/(delta+skew)
	}
	prefix := asciiLower(out.String())
	out.Reset()
	out.WriteString("xn--")
	out.WriteString(prefix)
	if basic > 0 {
		out.WriteByte('-')
	}
	n, delta, bias, handled := rune(128), 0, 72, basic
	for handled < len(runes) {
		next := rune(utf8.MaxRune)
		for _, r := range runes {
			if r >= n && r < next {
				next = r
			}
		}
		if int(next-n) > (1<<31-1-delta)/(handled+1) {
			return "", false
		}
		delta += int(next-n) * (handled + 1)
		n = next
		for _, r := range runes {
			if r < n {
				delta++
			}
			if r != n {
				continue
			}
			q := delta
			for k := base; ; k += base {
				t := k - bias
				if t < tMin {
					t = tMin
				} else if t > tMax {
					t = tMax
				}
				if q < t {
					break
				}
				out.WriteByte(digit(t + (q-t)%(base-t)))
				q = (q - t) / (base - t)
			}
			out.WriteByte(digit(q))
			bias = adapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return out.String(), true
}

// ipv4Number reads one part of a dotted address: decimal, octal with a
// leading 0, or hex with a leading 0x.
func ipv4Number(part string) (*big.Int, bool) {
	if part == "" {
		return nil, false
	}
	radix := 10
	switch {
	case len(part) >= 2 && (part[:2] == "0x" || part[:2] == "0X"):
		part, radix = part[2:], 16
	case len(part) >= 2 && part[0] == '0':
		part, radix = part[1:], 8
	}
	if part == "" {
		return new(big.Int), true
	}
	for i := 0; i < len(part); i++ {
		v, isHex := hexValue(part[i])
		if !isHex || int(v) >= radix {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(part, radix)
	return n, ok
}

// endsInNumber reports whether the last label of a name is a number, which
// makes the name an IPv4 address or nothing.
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
	_, ok := ipv4Number(last)
	return ok
}

func parseIPv4(input string) (string, bool) {
	parts := strings.Split(input, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	numbers := make([]*big.Int, len(parts))
	for i, part := range parts {
		n, ok := ipv4Number(part)
		if !ok {
			return "", false
		}
		numbers[i] = n
	}
	limit := big.NewInt(255)
	for _, n := range numbers[:len(numbers)-1] {
		if n.Cmp(limit) > 0 {
			return "", false
		}
	}
	last := numbers[len(numbers)-1]
	if last.Cmp(new(big.Int).Exp(big.NewInt(256), big.NewInt(int64(5-len(numbers))), nil)) >= 0 {
		return "", false
	}
	address := last.Uint64()
	for i, n := range numbers[:len(numbers)-1] {
		address += n.Uint64() << (8 * uint(3-i))
	}
	return strconv.Itoa(int(address>>24)) + "." + strconv.Itoa(int(address>>16&255)) + "." +
		strconv.Itoa(int(address>>8&255)) + "." + strconv.Itoa(int(address&255)), true
}

// parseIPv6 reads the inside of a bracketed address and writes it in the
// standard's short form, brackets included.
func parseIPv6(input string) (string, bool) {
	if strings.ContainsAny(input, "%") {
		return "", false
	}
	addr, err := netip.ParseAddr(input)
	if err != nil || !addr.Is6() {
		return "", false
	}
	raw := addr.As16()
	var pieces [8]uint16
	for i := range pieces {
		pieces[i] = uint16(raw[2*i])<<8 | uint16(raw[2*i+1])
	}
	// The first longest run of two or more zero pieces is written "::".
	start, length := -1, 0
	for i := 0; i < 8; {
		if pieces[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && pieces[j] == 0 {
			j++
		}
		if j-i > length && j-i >= 2 {
			start, length = i, j-i
		}
		i = j
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 8; i++ {
		if i == start {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteByte(':')
			}
			i += length - 1
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(pieces[i]), 16))
		if i != 7 {
			b.WriteByte(':')
		}
	}
	b.WriteByte(']')
	return b.String(), true
}

func isSingleDot(segment string) bool {
	return segment == "." || strings.EqualFold(segment, "%2e")
}

func isDoubleDot(segment string) bool {
	switch asciiLower(segment) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

// specialPath resolves the path of an http-like URL: backslashes are
// slashes, "." and ".." segments are taken out, and characters a path
// cannot carry are percent-encoded.
func specialPath(raw string) string {
	raw = strings.ReplaceAll(raw, `\`, "/")
	raw = strings.TrimPrefix(raw, "/")
	parts := strings.Split(raw, "/")
	var segments []string
	for i, part := range parts {
		last := i == len(parts)-1
		switch {
		case isDoubleDot(part):
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
			if last {
				segments = append(segments, "")
			}
		case isSingleDot(part):
			if last {
				segments = append(segments, "")
			}
		default:
			segments = append(segments, pathEncode(part))
		}
	}
	return "/" + strings.Join(segments, "/")
}

func pathEncode(segment string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte("\"#<>?^`{}", c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
