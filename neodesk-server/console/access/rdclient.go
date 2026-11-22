package access

import (
	"strconv"
	"strings"
)

// The remote client pages send people without a session to their own
// sign-in page with the way back. The way back must be a path under /remote
// on this console; it is normalised the way a browser resolves a link, so
// what is checked is what the browser would open.

const (
	rdClientHome  = "/remote"
	rdClientLogin = "/remote/login"
	rdClientHost  = "betterdesk.local"
)

// NormalizeRdClientReturnURL returns the path, query and fragment of a
// return address for the remote client pages after the browser's own
// normalisation, or false when the address is not a path under /remote on
// this console, is the remote sign-in page, or carries a control character
// or backslash, typed or percent-encoded.
func NormalizeRdClientReturnURL(u string) (string, bool) {
	if u == "" || strings.ContainsAny(u, "\r\n\x00\\") || hasEncodedControl(u) {
		return "", false
	}
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
		return "", false
	}
	pathname, search, hash, ok := resolveLocalPath(u)
	if !ok {
		return "", false
	}
	if pathname != rdClientHome && !strings.HasPrefix(pathname, rdClientHome+"/") {
		return "", false
	}
	if pathname == rdClientLogin || strings.HasPrefix(pathname, rdClientLogin+"/") {
		return "", false
	}
	return pathname + search + hash, true
}

// IsSafeRdClientReturnURL reports whether NormalizeRdClientReturnURL
// accepts the address.
func IsSafeRdClientReturnURL(u string) bool {
	_, ok := NormalizeRdClientReturnURL(u)
	return ok
}

// RdClientLoginRedirect is the address of the remote client's sign-in page
// for a request without a session: the way back is the request itself when
// it is a remote client page, else the request's "return" parameter when
// that is one, else the remote client's start page.
func RdClientLoginRedirect(originalURL, returnQuery string) string {
	if originalURL == "" {
		originalURL = rdClientHome
	}
	back, ok := NormalizeRdClientReturnURL(originalURL)
	if !ok {
		back, ok = NormalizeRdClientReturnURL(returnQuery)
	}
	if !ok {
		back = rdClientHome
	}
	return rdClientLogin + "?return=" + encodeURIComponent(back)
}

// hasEncodedControl reports whether s contains %00, %0a, %0d or %5c in any
// letter case.
func hasEncodedControl(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		switch strings.ToLower(s[i+1 : i+3]) {
		case "00", "0a", "0d", "5c":
			return true
		}
	}
	return false
}

// resolveLocalPath resolves a reference that starts with "/" against
// https://betterdesk.local/ as a browser does and returns the resulting
// path, query (with "?", or "" when empty) and fragment (with "#", or ""
// when empty). ok is false when the result points at another origin or
// cannot be parsed. raw holds no backslash, line break or NUL.
func resolveLocalPath(raw string) (pathname, search, hash string, ok bool) {
	// Browsers drop control characters and spaces at both ends, and tabs
	// everywhere.
	s := strings.TrimFunc(raw, func(r rune) bool { return r <= 0x20 })
	s = strings.ReplaceAll(s, "\t", "")

	if strings.HasPrefix(s, "//") {
		// With the tabs gone this names a host. Every slash that follows
		// belongs to the separator.
		rest := strings.TrimLeft(s, "/")
		end := strings.IndexAny(rest, "/?#")
		if end < 0 {
			end = len(rest)
		}
		if !isLocalAuthority(rest[:end]) {
			return "", "", "", false
		}
		s = rest[end:]
	}

	path, fragment, hasFragment := strings.Cut(s, "#")
	path, query, hasQuery := strings.Cut(path, "?")

	var segments []string
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
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
	pathname = "/" + strings.Join(segments, "/")
	if hasQuery && query != "" {
		search = "?" + percentEncode(query, queryEncodeSet)
	}
	if hasFragment && fragment != "" {
		hash = "#" + percentEncode(fragment, fragmentEncodeSet)
	}
	return pathname, search, hash, true
}

// isLocalAuthority reports whether an authority (userinfo, host, port)
// names https://betterdesk.local: that host, in any letter case or
// percent-encoded, with no port or port 443. Other spellings a browser
// would map to the same host are not recognised.
func isLocalAuthority(authority string) bool {
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	host, port := authority, ""
	if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		host, port = authority[:colon], authority[colon+1:]
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n != 443 {
			return false
		}
	}
	return strings.ToLower(percentDecode(host)) == rdClientHost
}

func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isSingleDot(segment string) bool {
	return segment == "." || strings.EqualFold(segment, "%2e")
}

func isDoubleDot(segment string) bool {
	switch strings.ToLower(segment) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

// The characters a browser percent-encodes in each part of a URL, besides
// control characters and everything outside ASCII.
const (
	fragmentEncodeSet = " \"<>`"
	queryEncodeSet    = " \"#<>'"
	pathEncodeSet     = " \"#<>?`{}"
)

const upperHex = "0123456789ABCDEF"

// percentEncode encodes the bytes of s that are control characters, outside
// ASCII or in set. Existing percent signs stay as they are.
func percentEncode(s, set string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= 0x1F || c >= 0x7F || strings.IndexByte(set, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// encodeURIComponent encodes everything but letters, digits and
// - _ . ! ~ * ' ( ) for use as one query value.
func encodeURIComponent(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperHex[c>>4])
			b.WriteByte(upperHex[c&0x0F])
		}
	}
	return b.String()
}
