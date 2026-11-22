package branding

import (
	"math/big"
	"strconv"
	"strings"
)

// ValidateBrandingURL checks the address of a logo, background or slide
// picture. It accepts an address on this server (one leading slash, no
// ".." and no control character anywhere), which is returned as written,
// and an http or https URL, which is returned the way a browser writes it
// out. Nothing and the empty text give "". ok is false for everything
// else: other schemes, "//host/..." and text that is no URL. A control
// character would end the quoted url("...") of a background in the
// stylesheet.
func ValidateBrandingURL(value any) (url string, ok bool, err error) {
	defer catch(&err)
	url, ok = validateBrandingURL(value)
	return url, ok, nil
}

func validateBrandingURL(value any) (string, bool) {
	if value == nil || value == Undefined {
		return "", true
	}
	trimmed := trim(toString(value))
	if trimmed == "" {
		return "", true
	}
	if strings.HasPrefix(trimmed, "//") {
		return "", false
	}
	if strings.HasPrefix(trimmed, "/") {
		if strings.Contains(trimmed, "..") || strings.ContainsFunc(trimmed, isControl) {
			return "", false
		}
		return trimmed, true
	}
	return parseHTTPURL(trimmed)
}

// isControl reports the C0 control characters and DEL.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// The sets of ASCII characters each part of a URL writes as %XX. Every
// byte above 0x7E and every control character is written that way too.
const (
	encodeFragment     = " \"<>`"
	encodeQuery        = " \"#<>'"
	encodePath         = " \"#<>?`{}"
	encodeUserinfo     = encodePath + "/:;=@[\\]^|"
	forbiddenInDomain  = " #/:<>?@[\\]^|%"
	upperHexDigits     = "0123456789ABCDEF"
	asciiTabAndNewline = "\t\n\r"
)

// parseHTTPURL parses an absolute URL the way browsers and Node do (the
// WHATWG URL standard) and returns it serialised when its scheme is http
// or https. Everything else, valid or not, gives false.
func parseHTTPURL(input string) (string, bool) {
	s := wellFormed(input)
	// Leading and trailing control characters and spaces do not count,
	// tabs and line breaks do not count anywhere.
	start, end := 0, len(s)
	for start < end && s[start] <= 0x20 {
		start++
	}
	for end > start && s[end-1] <= 0x20 {
		end--
	}
	s = s[start:end]
	if strings.ContainsAny(s, asciiTabAndNewline) {
		s = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(s)
	}

	i := 0
	if s == "" || !isASCIIAlpha(s[0]) {
		return "", false
	}
	for i < len(s) && (isASCIIAlpha(s[i]) || isASCIIDigit(s[i]) || s[i] == '+' || s[i] == '-' || s[i] == '.') {
		i++
	}
	if i >= len(s) || s[i] != ':' {
		return "", false
	}
	scheme := strings.ToLower(s[:i])
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	rest := s[i+1:]
	for rest != "" && (rest[0] == '/' || rest[0] == '\\') {
		rest = rest[1:]
	}

	authEnd := strings.IndexAny(rest, `/\?#`)
	if authEnd < 0 {
		authEnd = len(rest)
	}
	authority, rest := rest[:authEnd], rest[authEnd:]

	var out strings.Builder
	out.WriteString(scheme)
	out.WriteString("://")

	hostPort := authority
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		hostPort = authority[at+1:]
		if hostPort == "" {
			return "", false
		}
		userinfo := strings.ReplaceAll(authority[:at], "@", "%40")
		username, password, _ := strings.Cut(userinfo, ":")
		username = percentEncode(username, encodeUserinfo)
		password = percentEncode(password, encodeUserinfo)
		if username != "" || password != "" {
			out.WriteString(username)
			if password != "" {
				out.WriteByte(':')
				out.WriteString(password)
			}
			out.WriteByte('@')
		}
	}

	hostText, portText, hasPort := hostPort, "", false
	inBrackets := false
	for j := 0; j < len(hostPort) && !hasPort; j++ {
		switch hostPort[j] {
		case '[':
			inBrackets = true
		case ']':
			inBrackets = false
		case ':':
			if !inBrackets {
				hostText, portText, hasPort = hostPort[:j], hostPort[j+1:], true
			}
		}
	}
	if hostText == "" {
		return "", false
	}
	host, ok := parseHost(hostText)
	if !ok {
		return "", false
	}
	out.WriteString(host)
	if portText != "" {
		for j := 0; j < len(portText); j++ {
			if !isASCIIDigit(portText[j]) {
				return "", false
			}
		}
		digits := strings.TrimLeft(portText, "0")
		if len(digits) > 5 {
			return "", false
		}
		port, _ := strconv.Atoi("0" + digits)
		if port > 65535 {
			return "", false
		}
		if !(scheme == "http" && port == 80) && !(scheme == "https" && port == 443) {
			out.WriteByte(':')
			out.WriteString(strconv.Itoa(port))
		}
	}

	fragment, hasFragment := "", false
	if hash := strings.IndexByte(rest, '#'); hash >= 0 {
		rest, fragment, hasFragment = rest[:hash], rest[hash+1:], true
	}
	query, hasQuery := "", false
	if mark := strings.IndexByte(rest, '?'); mark >= 0 {
		rest, query, hasQuery = rest[:mark], rest[mark+1:], true
	}

	// The path: "." keeps the place, ".." steps back, a backslash is a slash
	var segments []string
	path := rest
	if path != "" {
		path = path[1:]
	}
	for {
		cut := strings.IndexAny(path, `/\`)
		last := cut < 0
		segment := path
		if !last {
			segment, path = path[:cut], path[cut+1:]
		}
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
			segments = append(segments, percentEncode(segment, encodePath))
		}
		if last {
			break
		}
	}
	for _, segment := range segments {
		out.WriteByte('/')
		out.WriteString(segment)
	}
	if hasQuery {
		out.WriteByte('?')
		out.WriteString(percentEncode(query, encodeQuery))
	}
	if hasFragment {
		out.WriteByte('#')
		out.WriteString(percentEncode(fragment, encodeFragment))
	}
	return out.String(), true
}

func isASCIIAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

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

// percentEncode writes as %XX every control character, every byte above
// 0x7E and every character of set.
func percentEncode(s, set string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E || strings.IndexByte(set, c) >= 0 {
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
		if c < 0x20 || c > 0x7E || strings.IndexByte(set, c) >= 0 {
			b.WriteByte('%')
			b.WriteByte(upperHexDigits[c>>4])
			b.WriteByte(upperHexDigits[c&0xF])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// percentDecode turns every %XX into its byte and leaves a % that is not
// followed by two hex digits alone.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && unhex(s[i+1]) >= 0 && unhex(s[i+2]) >= 0 {
			out = append(out, byte(unhex(s[i+1])<<4|unhex(s[i+2])))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

// parseHost reads the host of an http or https URL: an IPv6 address in
// brackets, an IPv4 address in any of its spellings, or a domain name.
func parseHost(input string) (string, bool) {
	if input[0] == '[' {
		if input[len(input)-1] != ']' {
			return "", false
		}
		return parseIPv6(input[1 : len(input)-1])
	}
	domain, ok := domainToASCII(decodeUTF8(percentDecode(input)))
	if !ok || domain == "" {
		return "", false
	}
	for i := 0; i < len(domain); i++ {
		if c := domain[i]; c <= 0x20 || c == 0x7F || strings.IndexByte(forbiddenInDomain, c) >= 0 {
			return "", false
		}
	}
	if endsInNumber(domain) {
		return parseIPv4(domain)
	}
	return domain, true
}

// endsInNumber reports whether the last label of a domain is a number, in
// which case the whole host has to be an IPv4 address.
func endsInNumber(domain string) bool {
	parts := strings.Split(domain, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" {
		digits := true
		for i := 0; i < len(last); i++ {
			if !isASCIIDigit(last[i]) {
				digits = false
				break
			}
		}
		if digits {
			return true
		}
	}
	_, ok := parseIPv4Number(last)
	return ok
}

// parseIPv4Number reads one part of an IPv4 address: decimal, octal with
// a leading 0, or hex with 0x.
func parseIPv4Number(s string) (*big.Int, bool) {
	if s == "" {
		return nil, false
	}
	radix := 10
	switch {
	case len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X"):
		s, radix = s[2:], 16
	case len(s) >= 2 && s[0] == '0':
		s, radix = s[1:], 8
	}
	if s == "" {
		return new(big.Int), true
	}
	for i := 0; i < len(s); i++ {
		d := unhex(s[i])
		if d < 0 || d >= radix {
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(s, radix)
	return n, ok
}

func parseIPv4(s string) (string, bool) {
	parts := strings.Split(s, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return "", false
	}
	numbers := make([]*big.Int, len(parts))
	for i, part := range parts {
		n, ok := parseIPv4Number(part)
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
	if last.Cmp(new(big.Int).Lsh(big.NewInt(1), uint(8*(5-len(numbers))))) >= 0 {
		return "", false
	}
	addr := last.Uint64()
	for i, n := range numbers[:len(numbers)-1] {
		addr += n.Uint64() << uint(8*(3-i))
	}
	return strconv.FormatUint(addr>>24, 10) + "." + strconv.FormatUint(addr>>16&0xFF, 10) + "." +
		strconv.FormatUint(addr>>8&0xFF, 10) + "." + strconv.FormatUint(addr&0xFF, 10), true
}

// parseIPv6 reads the text between the brackets of an IPv6 host and
// writes it back in its shortest form, brackets included.
func parseIPv6(s string) (string, bool) {
	var address [8]uint16
	piece, compress := 0, -1
	i := 0
	at := func() int {
		if i < len(s) {
			return int(s[i])
		}
		return -1
	}
	if at() == ':' {
		if i+1 >= len(s) || s[i+1] != ':' {
			return "", false
		}
		i += 2
		piece++
		compress = piece
	}
	for at() >= 0 {
		if piece == 8 {
			return "", false
		}
		if at() == ':' {
			if compress >= 0 {
				return "", false
			}
			i++
			piece++
			compress = piece
			continue
		}
		value, length := 0, 0
		for length < 4 && at() >= 0 && unhex(byte(at())) >= 0 {
			value = value*16 + unhex(byte(at()))
			i++
			length++
		}
		if at() == '.' {
			if length == 0 {
				return "", false
			}
			i -= length
			if piece > 6 {
				return "", false
			}
			seen := 0
			for at() >= 0 {
				part := -1
				if seen > 0 {
					if at() == '.' && seen < 4 {
						i++
					} else {
						return "", false
					}
				}
				if at() < 0 || !isASCIIDigit(byte(at())) {
					return "", false
				}
				for at() >= 0 && isASCIIDigit(byte(at())) {
					number := at() - '0'
					switch {
					case part < 0:
						part = number
					case part == 0:
						return "", false
					default:
						part = part*10 + number
					}
					if part > 255 {
						return "", false
					}
					i++
				}
				address[piece] = address[piece]*0x100 + uint16(part)
				seen++
				if seen == 2 || seen == 4 {
					piece++
				}
			}
			if seen != 4 {
				return "", false
			}
			break
		}
		if at() == ':' {
			i++
			if at() < 0 {
				return "", false
			}
		} else if at() >= 0 {
			return "", false
		}
		address[piece] = uint16(value)
		piece++
	}
	if compress >= 0 {
		swaps := piece - compress
		piece = 7
		for piece != 0 && swaps > 0 {
			address[piece], address[compress+swaps-1] = address[compress+swaps-1], address[piece]
			piece--
			swaps--
		}
	} else if piece != 8 {
		return "", false
	}

	// The longest run of zero pieces, two or more, is written as "::"
	bestStart, bestLen := -1, 0
	for start := 0; start < 8; {
		if address[start] != 0 {
			start++
			continue
		}
		stop := start
		for stop < 8 && address[stop] == 0 {
			stop++
		}
		if stop-start > bestLen {
			bestStart, bestLen = start, stop-start
		}
		start = stop
	}
	if bestLen < 2 {
		bestStart = -1
	}
	var b strings.Builder
	b.WriteByte('[')
	ignore0 := false
	for index := 0; index < 8; index++ {
		if ignore0 && address[index] == 0 {
			continue
		}
		ignore0 = false
		if bestStart == index {
			if index == 0 {
				b.WriteString("::")
			} else {
				b.WriteByte(':')
			}
			ignore0 = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(address[index]), 16))
		if index != 7 {
			b.WriteByte(':')
		}
	}
	b.WriteByte(']')
	return b.String(), true
}
