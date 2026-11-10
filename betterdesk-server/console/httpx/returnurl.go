package httpx

import (
	"net/http"
	"strings"
	"unicode/utf8"
)

// maxReturnLength is the longest return path taken, in UTF-16 code units
// (the length a browser's JavaScript sees).
const maxReturnLength = 2048

// sessionCookies are the names the console's session cookie has had.
var sessionCookies = []string{"bd.sid", "betterdesk.sid"}

// SafeReturnPath checks a path the sign-in page is asked to send someone to
// afterwards. It answers the path as a browser would normalise it (with
// query and fragment), and false when the path is not one on this console:
//
//   - it starts with one "/" and still does after the browser's own
//     normalisation (dot segments): no scheme, no host, no "//";
//   - it has no backslash and no control character, typed or
//     percent-encoded (browsers drop tabs and newlines, so "/\t/evil.example"
//     would become "//evil.example");
//   - it is not a sign-in page, sign-out, the API or a socket.
//
// The answer is what the URL parser of Node 22 makes of the path: dot
// segments (also written %2e) are resolved, spaces at the end are dropped,
// and the characters a URL cannot carry are percent-encoded. That parser
// overlooks dot segments in one case (keepsDotSegments), and so does this.
func SafeReturnPath(raw string) (string, bool) {
	if raw == "" || len(raw) > 4*maxReturnLength {
		return "", false
	}
	if !utf8.ValidString(raw) {
		raw = string([]rune(raw))
	}
	if utf16Length(raw) > maxReturnLength {
		return "", false
	}
	if hasControlOrBackslash(raw) || hasEncodedControlOrBackslash(raw) {
		return "", false
	}
	if raw[0] != '/' || strings.HasPrefix(raw, "//") {
		return "", false
	}
	path, search, hash := normalizeReturnPath(raw)
	if strings.HasPrefix(path, "//") || blockedReturnPath(path) {
		return "", false
	}
	return path + search + hash, true
}

// HadSessionCookie reports whether the request carried a session cookie.
// On a request without a session that means the session it named has ended.
func HadSessionCookie(r *http.Request) bool {
	for _, part := range strings.Split(strings.Join(r.Header.Values("Cookie"), "; "), ";") {
		name, _, _ := strings.Cut(part, "=")
		name = trimHeaderSpace(name)
		for _, known := range sessionCookies {
			if name == known {
				return true
			}
		}
	}
	return false
}

// LoginRedirectURL is the sign-in page for a request that needs a session:
// with the way back to the page, and with the "session expired" notice only
// when the browser still sent a session cookie.
//
// returnTo is the way back. nil means the request's own address for GET and
// HEAD and nothing for other methods. A way back that SafeReturnPath
// refuses is left out.
func LoginRedirectURL(r *http.Request, returnTo *string) string {
	var params []string
	if HadSessionCookie(r) {
		params = append(params, "expired=1")
	}
	wanted := ""
	switch {
	case returnTo != nil:
		wanted = *returnTo
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		wanted = OriginalURL(r)
	}
	if back, ok := SafeReturnPath(wanted); ok {
		params = append(params, "return="+EncodeURIComponent(back))
	}
	if len(params) == 0 {
		return "/login"
	}
	return "/login?" + strings.Join(params, "&")
}

// OriginalURL is the request target as the client sent it (path and query,
// not decoded). A byte above 0x7F counts as the Latin-1 character with that
// number, the way the console has always read request lines.
func OriginalURL(r *http.Request) string {
	target := r.RequestURI
	if target == "" && r.URL != nil {
		target = r.URL.RequestURI()
	}
	return Latin1(target)
}

// Latin1 reads every byte of s as one Latin-1 character.
func Latin1(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

// EncodeURIComponent percent-encodes s as JavaScript's encodeURIComponent
// does: everything but letters, digits and - _ . ! ~ * ' ( ) is written as
// the %XX of its UTF-8 bytes.
func EncodeURIComponent(s string) string {
	if !utf8.ValidString(s) {
		s = string([]rune(s))
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isAlphanumeric(c) || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		writePercent(&b, c)
	}
	return b.String()
}

const upperHex = "0123456789ABCDEF"

func writePercent(b *strings.Builder, c byte) {
	b.WriteByte('%')
	b.WriteByte(upperHex[c>>4])
	b.WriteByte(upperHex[c&0x0F])
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// trimHeaderSpace removes the white space JavaScript's trim removes, as far
// as a header value (Latin-1) can hold it.
func trimHeaderSpace(s string) string {
	isSpace := func(c byte) bool { return c >= '\t' && c <= '\r' || c == ' ' || c == 0xA0 }
	for len(s) > 0 && isSpace(s[0]) {
		s = s[1:]
	}
	for len(s) > 0 && isSpace(s[len(s)-1]) {
		s = s[:len(s)-1]
	}
	return s
}

func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

func hasControlOrBackslash(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7F || c == '\\' {
			return true
		}
	}
	return false
}

// hasEncodedControlOrBackslash finds %00 to %1F, %7F and %5C in any letter
// case.
func hasEncodedControlOrBackslash(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		a, b := s[i+1], s[i+2]|0x20
		switch {
		case (a == '0' || a == '1') && isHex(s[i+2]):
			return true
		case a == '7' && b == 'f', a == '5' && b == 'c':
			return true
		}
	}
	return false
}

// The characters a URL parser percent-encodes in a path, a query and a
// fragment, besides everything outside printable ASCII.
const (
	pathEncodeSet     = " \"<>`{}"
	queryEncodeSet    = " \"'<>"
	fragmentEncodeSet = " \"<>`"
)

// normalizeReturnPath does what a browser's URL parser does with a path
// that starts with one "/" and holds no backslash and no control character.
func normalizeReturnPath(raw string) (path, search, hash string) {
	s := strings.TrimRight(raw, " ")
	rest := s
	fragment, hasFragment := "", false
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest, fragment, hasFragment = rest[:i], rest[i+1:], true
	}
	query, hasQuery := "", false
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest, query, hasQuery = rest[:i], rest[i+1:], true
	}

	if keepsDotSegments(rest[1:]) {
		path = rest
	} else {
		path = resolveDotSegments(rest[1:])
	}
	if hasQuery && query != "" {
		search = "?" + percentEncode(query, queryEncodeSet)
	}
	if hasFragment && fragment != "" {
		hash = "#" + percentEncode(fragment, fragmentEncodeSet)
	}
	return path, search, hash
}

// resolveDotSegments resolves "." and ".." (also written %2e) in a path
// given without its leading slash, and percent-encodes what is left.
func resolveDotSegments(p string) string {
	parts := strings.Split(p, "/")
	segments := make([]string, 0, len(parts))
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
			segments = append(segments, percentEncode(part, pathEncodeSet))
		}
	}
	return "/" + strings.Join(segments, "/")
}

// keepsDotSegments reports whether the URL parser of Node 22 (ada 2.9.2)
// leaves a path, given without its leading slash, exactly as written. It
// does when the path holds no "%" and nothing to percent-encode, does not
// start with a dot, and the first "/." in it starts an ordinary name such
// as ".well-known": it then overlooks every "." and ".." segment after that
// name. SafeReturnPath answers what Node answers, so the same paths pass
// here with their dot segments.
func keepsDotSegments(p string) bool {
	if p == "" || p[0] == '.' {
		return false
	}
	for i := 0; i < len(p); i++ {
		if c := p[i]; c <= 0x20 || c >= 0x7F || c == '%' || strings.IndexByte(pathEncodeSet, c) >= 0 {
			return false
		}
	}
	i := strings.Index(p, "/.")
	return i >= 0 && i+2 < len(p) && p[i+2] != '.' && p[i+2] != '/'
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

func percentEncode(s, set string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x7F || c < 0x20 || strings.IndexByte(set, c) >= 0 {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x7F || c < 0x20 || strings.IndexByte(set, c) >= 0 {
			writePercent(&b, c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// blockedReturnPath reports whether a normalised path is one nobody is sent
// back to: the API, a socket, a sign-in page or sign-out, in any letter
// case.
func blockedReturnPath(path string) bool {
	rest := asciiLower(path[1:])
	if isFirstSegment(rest, "api") || isFirstSegment(rest, "ws") || isFirstSegment(rest, "socket") {
		return true
	}
	rest = strings.TrimPrefix(rest, "app/")
	rest = strings.TrimPrefix(rest, "remote/")
	return isFirstSegment(rest, "login") || isFirstSegment(rest, "logout")
}

func isFirstSegment(path, name string) bool {
	return strings.HasPrefix(path, name) && (len(path) == len(name) || path[len(name)] == '/')
}

func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}
