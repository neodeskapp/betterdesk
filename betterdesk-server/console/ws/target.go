package ws

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// target is a request target read the way the old console read it on an
// upgrade: new URL(req.url, "http://" + host), which follows the URL
// Standard. Path is the URL's pathname and Query its search without the
// "?", both percent-encoded as the standard leaves them.
type target struct {
	Path  string
	Query string
}

// parseTarget reads a request target against the base http://host. ok is
// false where new URL throws: a host or port the standard refuses after
// "//". Request targets reach the console in two forms, a path
// ("/ws/x?y") and an absolute URL ("http://host/ws/x"); a path that
// starts with two slashes names a host, as it did in Node.
func parseTarget(raw string) (target, bool) {
	input := cleanInput(raw)
	if scheme, rest, ok := cutScheme(input); ok {
		special := isSpecialScheme(scheme)
		switch {
		case scheme == "http":
			// The base's scheme: relative unless "//" follows.
			if startsWithTwoSlashes(rest, true) {
				return afterAuthority(skipSlashes(rest, true), true)
			}
			return relative(rest)
		case special:
			return afterAuthority(skipSlashes(rest, true), true)
		case strings.HasPrefix(rest, "//"):
			return afterAuthority(rest[2:], false)
		default:
			return opaque(rest), true
		}
	}
	return relative(input)
}

// relative resolves input against the base http://host/.
func relative(input string) (target, bool) {
	if input == "" {
		return target{Path: "/"}, true
	}
	if isSlash(input[0], true) {
		if len(input) > 1 && isSlash(input[1], true) {
			return afterAuthority(skipSlashes(input, true), true)
		}
		return pathAndQuery(input, true), true
	}
	if input[0] == '?' || input[0] == '#' {
		return pathAndQuery("/"+input, true), true
	}
	// A path relative to the base's empty path.
	return pathAndQuery("/"+input, true), true
}

// afterAuthority skips "user@host:port" and reads what follows. The host
// must be one the standard accepts for the scheme.
func afterAuthority(rest string, special bool) (target, bool) {
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '/' || c == '?' || c == '#' || (special && c == '\\') {
			end = i
			break
		}
	}
	authority := rest[:end]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	if !validHostPort(authority, special) {
		return target{}, false
	}
	remainder := rest[end:]
	if remainder == "" || remainder[0] == '?' || remainder[0] == '#' {
		if special {
			remainder = "/" + remainder
		} else {
			return pathAndQuery(remainder, false), true
		}
	}
	return pathAndQuery(remainder, special), true
}

// opaque is a URL of a scheme other than the special ones without "//":
// its path is the text up to the query, encoded with the C0 control set.
func opaque(rest string) target {
	path, query := rest, ""
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		path = rest[:i]
		if rest[i] == '?' {
			query = rest[i+1:]
			if j := strings.IndexByte(query, '#'); j >= 0 {
				query = query[:j]
			}
			query = percentEncode(query, inQuerySet(false))
		}
	}
	return target{Path: percentEncode(path, inC0ControlSet), Query: query}
}

// pathAndQuery reads a path that starts at a slash (or is empty, for a
// URL that is not special), then the query; the fragment is dropped.
func pathAndQuery(input string, special bool) target {
	pathEnd := len(input)
	for i := 0; i < len(input); i++ {
		if input[i] == '?' || input[i] == '#' {
			pathEnd = i
			break
		}
	}
	t := target{Path: normalizePath(input[:pathEnd], special)}
	if pathEnd < len(input) && input[pathEnd] == '?' {
		query := input[pathEnd+1:]
		if j := strings.IndexByte(query, '#'); j >= 0 {
			query = query[:j]
		}
		t.Query = percentEncode(query, inQuerySet(special))
	}
	return t
}

// normalizePath is the path state of the standard: segments split at "/"
// (and "\" for special URLs), "." and ".." in any spelling the standard
// knows resolved, every other segment percent-encoded with the path set.
func normalizePath(p string, special bool) string {
	if p == "" {
		if special {
			return "/"
		}
		return ""
	}
	// The leading slash is the path start; what follows are segments.
	if isSlash(p[0], special) {
		p = p[1:]
	}
	var segments []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i < len(p) && !isSlash(p[i], special) {
			continue
		}
		segment := p[start:i]
		last := i == len(p)
		switch {
		case isDoubleDot(segment):
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
			if last {
				segments = append(segments, "")
			}
		case isSingleDot(segment):
			if last {
				segments = append(segments, "")
			}
		default:
			segments = append(segments, percentEncode(segment, inPathSet))
		}
		start = i + 1
	}
	return "/" + strings.Join(segments, "/")
}

func isSingleDot(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func isSlash(c byte, special bool) bool {
	return c == '/' || (special && c == '\\')
}

func startsWithTwoSlashes(s string, special bool) bool {
	return len(s) >= 2 && isSlash(s[0], special) && isSlash(s[1], special)
}

func skipSlashes(s string, special bool) string {
	i := 0
	for i < len(s) && isSlash(s[i], special) {
		i++
	}
	return s[i:]
}

// cleanInput drops what the standard removes before parsing: C0 controls
// and spaces at both ends, and tabs and line breaks anywhere.
func cleanInput(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	if strings.ContainsAny(s, "\t\n\r") {
		s = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
	}
	return s
}

// cutScheme splits "scheme:rest". ok is false when the input has no
// scheme, and the input is then read as a path.
func cutScheme(s string) (scheme, rest string, ok bool) {
	if s == "" || !isASCIIAlpha(s[0]) {
		return "", "", false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case isASCIIAlpha(c) || isASCIIDigit(c) || c == '+' || c == '-' || c == '.':
		case c == ':':
			return strings.ToLower(s[:i]), s[i+1:], true
		default:
			return "", "", false
		}
	}
	return "", "", false
}

func isSpecialScheme(s string) bool {
	switch s {
	case "http", "https", "ws", "wss", "ftp", "file":
		return true
	}
	return false
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// validHostPort checks "host[:port]" the way the standard's host and
// port states do, as far as a console sees it: a special URL needs a
// host, an IPv6 address needs its brackets, a port is digits up to 65535,
// and a host has no forbidden character.
func validHostPort(s string, special bool) bool {
	host, port := s, ""
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return false
		}
		host, port = s[:end+1], s[end+1:]
		if port != "" {
			if port[0] != ':' {
				return false
			}
			port = port[1:]
		}
		if !validIPv6(host[1 : len(host)-1]) {
			return false
		}
	} else if i := strings.IndexByte(s, ':'); i >= 0 {
		host, port = s[:i], s[i+1:]
	}
	if port != "" {
		for i := 0; i < len(port); i++ {
			if !isASCIIDigit(port[i]) {
				return false
			}
		}
		if n, err := strconv.Atoi(strings.TrimLeft(port, "0")); err == nil && n > 65535 {
			return false
		} else if err != nil && strings.TrimLeft(port, "0") != "" {
			return false
		}
	}
	if strings.HasPrefix(host, "[") {
		return true
	}
	if host == "" {
		return !special
	}
	decoded := host
	if special {
		decoded = string(percentDecode(host))
		if !utf8.ValidString(decoded) {
			return false
		}
	}
	for i := 0; i < len(decoded); i++ {
		c := decoded[i]
		if c < 0x20 || c == 0x7f || strings.IndexByte(" #/:<>?@[\\]^|", c) >= 0 {
			return false
		}
		if special && c == '%' {
			return false
		}
	}
	return true
}

// validIPv6 accepts the hex groups and :: of an IPv6 address, with an
// optional dotted IPv4 tail.
func validIPv6(s string) bool {
	if s == "" {
		return false
	}
	groups := strings.Split(s, ":")
	if len(groups) < 3 && !strings.Contains(s, "::") {
		return false
	}
	compressed := strings.Count(s, "::")
	if compressed > 1 {
		return false
	}
	for i, g := range groups {
		if g == "" {
			continue
		}
		if i == len(groups)-1 && strings.Contains(g, ".") {
			parts := strings.Split(g, ".")
			if len(parts) != 4 {
				return false
			}
			for _, p := range parts {
				n, err := strconv.Atoi(p)
				if err != nil || n > 255 || (len(p) > 1 && p[0] == '0') {
					return false
				}
			}
			continue
		}
		if len(g) > 4 {
			return false
		}
		for j := 0; j < len(g); j++ {
			if !isHex(g[j]) {
				return false
			}
		}
	}
	return true
}

func isHex(c byte) bool {
	return isASCIIDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func inC0ControlSet(c byte) bool { return c < 0x20 || c > 0x7e }

func inPathSet(c byte) bool {
	return inC0ControlSet(c) || strings.IndexByte(" \"#<>?`{}", c) >= 0
}

func inQuerySet(special bool) func(byte) bool {
	return func(c byte) bool {
		return inC0ControlSet(c) || strings.IndexByte(" \"#<>", c) >= 0 || (special && c == '\'')
	}
}

const upperHex = "0123456789ABCDEF"

// percentEncode writes every byte of the set as %XX.
func percentEncode(s string, in func(byte) bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if in(c) {
			if b.Len() == 0 && i > 0 {
				b.WriteString(s[:i])
			}
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&15])
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(c)
		}
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}

func unhex(c byte) byte {
	switch {
	case isASCIIDigit(c):
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// percentDecode turns %XX into the byte and leaves a "%" that starts no
// escape as it is.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

// searchParam is URLSearchParams.get(name) on an encoded query: the
// value of the first pair named name, "+" read as a space, escapes
// decoded and the bytes read as UTF-8 with U+FFFD for what is not. ok is
// false without such a pair (get returned null).
func searchParam(query, name string) (string, bool) {
	for _, pair := range strings.Split(query, "&") {
		if pair == "" {
			continue
		}
		key, value, _ := strings.Cut(pair, "=")
		if formDecode(key) == name {
			return formDecode(value), true
		}
	}
	return "", false
}

// formDecode decodes one name or value of
// application/x-www-form-urlencoded.
func formDecode(s string) string {
	return decodeUTF8(percentDecode(strings.ReplaceAll(s, "+", " ")))
}

// decodeUTF8 is the Encoding Standard's UTF-8 decoder: each maximal part
// of a sequence that is not UTF-8 becomes one U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for i := 0; i < len(b); {
		c := b[i]
		need, lower, upper := 0, byte(0x80), byte(0xBF)
		switch {
		case c < 0x80:
			out.WriteByte(c)
			i++
			continue
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			out.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(b) || b[j] < lower || b[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			out.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		out.Write(b[i:j])
		i = j
	}
	return out.String()
}
