package ssrfguard

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// URL is a parsed URL with the members of JavaScript's URL object that
// the console reads.
type URL struct {
	// Href is the whole URL in its normal form. It is set for http and
	// https URLs only, the ones a check can let through.
	Href string
	// Protocol is the scheme with its colon: "http:".
	Protocol string
	Username string
	Password string
	// Hostname is the host in its normal form: a domain in lower case, an
	// IPv4 address as four decimal numbers ("http://0x7f.1/" has the
	// hostname "127.0.0.1"), an IPv6 address in brackets.
	Hostname string
	// Port is the port as text, "" for the scheme's own port.
	Port string
	// Pathname, Search and Hash are set for http and https URLs.
	Pathname string
	Search   string
	Hash     string
}

// defaultPorts are the schemes that have a host by rule ("special"
// schemes) and their ports; file has none.
var defaultPorts = map[string]int{"ftp": 21, "file": -1, "http": 80, "https": 443, "ws": 80, "wss": 443}

type parseState int

const (
	stateSchemeStart parseState = iota
	stateScheme
	stateSpecialAuthoritySlashes
	stateSpecialAuthorityIgnoreSlashes
	statePathOrAuthority
	stateAuthority
	stateHost
	statePort
	stateFile
	stateFileSlash
	stateFileHost
	statePathStart
	statePath
	stateVerbatimPathEnd
	stateOpaquePath
	stateQuery
	stateFragment
)

const eof = rune(-1)

func isASCIIAlpha(c rune) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }
func isASCIIHex(c rune) bool {
	return isASCIIDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// ParseURL is new URL(text) without a base: the URL parser every browser
// and Node share. ok is false where the constructor throws.
//
// A host that needs the rules for international domain names (letters
// outside ASCII, or a label that starts with "xn--") does not parse here.
func ParseURL(text string) (*URL, bool) {
	u, err := parseURL(text)
	return u, err == nil
}

// errInvalidURL is a text the URL parser refuses. errNeedsIDNA is a host
// it refuses only because the rules for international domain names are
// not built in.
var (
	errInvalidURL = errors.New("invalid URL")
	errNeedsIDNA  = errors.New("host needs the rules for international domain names")
)

func parseURL(text string) (*URL, error) {
	input := []rune(strings.TrimFunc(text, func(r rune) bool { return r >= 0 && r <= 0x20 }))
	kept := input[:0]
	for _, c := range input {
		if c != '\t' && c != '\n' && c != '\r' {
			kept = append(kept, c)
		}
	}
	input = kept

	var (
		u                          = &URL{}
		scheme                     string
		special                    bool
		state                      = stateSchemeStart
		schemeBuf                  strings.Builder
		buffer                     []rune
		atSignSeen, insideBrackets bool
		passwordTokenSeen          bool
		username, password         strings.Builder
		host                       string
		port                       = -1
		path                       []string
		query, fragment            strings.Builder
		hasQuery, hasFragment      bool
		querySet                   = isQueryEncode
		verbatimPath               string
		hasVerbatimPath            bool
	)
	at := func(i int) rune {
		if i < 0 || i >= len(input) {
			return eof
		}
		return input[i]
	}
	isDriveLetter := func(b []rune) bool {
		return len(b) == 2 && isASCIIAlpha(b[0]) && (b[1] == ':' || b[1] == '|')
	}

	for p := 0; ; p++ {
		c := at(p)
		switch state {
		case stateSchemeStart:
			if !isASCIIAlpha(c) {
				return nil, errInvalidURL
			}
			schemeBuf.WriteRune(lowerASCII(c))
			state = stateScheme

		case stateScheme:
			switch {
			case isASCIIAlpha(c) || isASCIIDigit(c) || c == '+' || c == '-' || c == '.':
				schemeBuf.WriteRune(lowerASCII(c))
			case c == ':':
				scheme = schemeBuf.String()
				_, special = defaultPorts[scheme]
				if special {
					querySet = isSpecialQueryEncode
				}
				switch {
				case scheme == "file":
					state = stateFile
				case special:
					state = stateSpecialAuthoritySlashes
				case at(p+1) == '/':
					state = statePathOrAuthority
					p++
				default:
					state = stateOpaquePath
				}
			default:
				// Without a scheme the text would be a relative reference,
				// and there is nothing it could be relative to
				return nil, errInvalidURL
			}

		case stateSpecialAuthoritySlashes:
			if c == '/' && at(p+1) == '/' {
				p++
			} else {
				p--
			}
			state = stateSpecialAuthorityIgnoreSlashes

		case stateSpecialAuthorityIgnoreSlashes:
			if c != '/' && c != '\\' {
				state = stateAuthority
				p--
			}

		case statePathOrAuthority:
			if c == '/' {
				state = stateAuthority
			} else {
				state = statePath
				p--
			}

		case stateAuthority:
			switch {
			case c == '@':
				if atSignSeen {
					buffer = append([]rune("%40"), buffer...)
				}
				atSignSeen = true
				for _, cp := range buffer {
					if cp == ':' && !passwordTokenSeen {
						passwordTokenSeen = true
						continue
					}
					if passwordTokenSeen {
						percentEncode(&password, cp, isUserinfoEncode)
					} else {
						percentEncode(&username, cp, isUserinfoEncode)
					}
				}
				buffer = buffer[:0]
			case c == eof || c == '/' || c == '?' || c == '#' || (special && c == '\\'):
				if atSignSeen && len(buffer) == 0 {
					return nil, errInvalidURL
				}
				p -= len(buffer) + 1
				buffer = buffer[:0]
				state = stateHost
			default:
				buffer = append(buffer, c)
			}

		case stateHost:
			switch {
			case c == ':' && !insideBrackets:
				if len(buffer) == 0 {
					return nil, errInvalidURL
				}
				parsed, err := parseHost(string(buffer), !special)
				if err != nil {
					return nil, err
				}
				host = parsed
				buffer = buffer[:0]
				state = statePort
			case c == eof || c == '/' || c == '?' || c == '#' || (special && c == '\\'):
				p--
				if special && len(buffer) == 0 {
					return nil, errInvalidURL
				}
				parsed, err := parseHost(string(buffer), !special)
				if err != nil {
					return nil, err
				}
				host = parsed
				buffer = buffer[:0]
				state = statePathStart
			default:
				if c == '[' {
					insideBrackets = true
				}
				if c == ']' {
					insideBrackets = false
				}
				buffer = append(buffer, c)
			}

		case statePort:
			switch {
			case isASCIIDigit(c):
				buffer = append(buffer, c)
			case c == eof || c == '/' || c == '?' || c == '#' || (special && c == '\\'):
				if len(buffer) != 0 {
					n := 0
					for _, digit := range buffer {
						n = n*10 + int(digit-'0')
						if n > 65535 {
							return nil, errInvalidURL
						}
					}
					port = n
					if def, ok := defaultPorts[scheme]; ok && def == n {
						port = -1
					}
					buffer = buffer[:0]
				}
				state = statePathStart
				p--
			default:
				return nil, errInvalidURL
			}

		case stateFile:
			if c == '/' || c == '\\' {
				state = stateFileSlash
			} else {
				state = statePath
				p--
			}

		case stateFileSlash:
			if c == '/' || c == '\\' {
				state = stateFileHost
			} else {
				state = statePath
				p--
			}

		case stateFileHost:
			if c == eof || c == '/' || c == '\\' || c == '?' || c == '#' {
				p--
				switch {
				case isDriveLetter(buffer):
					state = statePath
				case len(buffer) == 0:
					state = statePathStart
				default:
					parsed, err := parseHost(string(buffer), false)
					if err != nil {
						return nil, err
					}
					if parsed == "localhost" {
						parsed = ""
					}
					host = parsed
					buffer = buffer[:0]
					state = statePathStart
				}
			} else {
				buffer = append(buffer, c)
			}

		case statePathStart:
			switch {
			case special:
				state = statePath
				if c != '/' && c != '\\' {
					p--
				}
				end := p + 1
				for end < len(input) && input[end] != '?' && input[end] != '#' {
					end++
				}
				if scheme != "file" && keptVerbatim(input[p+1:end]) {
					verbatimPath, hasVerbatimPath = string(input[p+1:end]), true
					state = stateVerbatimPathEnd
					p = end - 1
				}
			case c == '?':
				hasQuery = true
				state = stateQuery
			case c == '#':
				hasFragment = true
				state = stateFragment
			case c != eof:
				state = statePath
				if c != '/' {
					p--
				}
			}

		case statePath:
			slash := c == '/' || (special && c == '\\')
			if c == eof || slash || c == '?' || c == '#' {
				segment := string(buffer)
				switch {
				case isDoubleDot(segment):
					if len(path) > 0 && !(scheme == "file" && len(path) == 1 && isNormalizedDriveLetter(path[0])) {
						path = path[:len(path)-1]
					}
					if !slash {
						path = append(path, "")
					}
				case isSingleDot(segment):
					if !slash {
						path = append(path, "")
					}
				default:
					if scheme == "file" && len(path) == 0 && isDriveLetter(buffer) {
						segment = string(buffer[0]) + ":"
					}
					path = append(path, segment)
				}
				buffer = buffer[:0]
				if c == '?' {
					hasQuery = true
					state = stateQuery
				}
				if c == '#' {
					hasFragment = true
					state = stateFragment
				}
			} else {
				var encoded strings.Builder
				percentEncode(&encoded, c, isPathEncode)
				buffer = append(buffer, []rune(encoded.String())...)
			}

		case stateVerbatimPathEnd, stateOpaquePath:
			switch {
			case c == '?':
				hasQuery = true
				state = stateQuery
			case c == '#':
				hasFragment = true
				state = stateFragment
			}

		case stateQuery:
			if c == '#' || c == eof {
				for _, cp := range buffer {
					percentEncode(&query, cp, querySet)
				}
				buffer = buffer[:0]
				if c == '#' {
					hasFragment = true
					state = stateFragment
				}
			} else {
				buffer = append(buffer, c)
			}

		case stateFragment:
			if c != eof {
				percentEncode(&fragment, c, isFragmentEncode)
			}
		}
		// A state that steps back over the end reads it once more
		if p >= len(input) {
			break
		}
	}

	u.Protocol = scheme + ":"
	u.Username = username.String()
	u.Password = password.String()
	u.Hostname = host
	if port >= 0 {
		u.Port = strconv.Itoa(port)
	}
	if scheme != "http" && scheme != "https" {
		return u, nil
	}
	u.Pathname = "/" + strings.Join(path, "/")
	if hasVerbatimPath {
		u.Pathname = "/" + verbatimPath
	}
	if hasQuery && query.Len() > 0 {
		u.Search = "?" + query.String()
	}
	if hasFragment && fragment.Len() > 0 {
		u.Hash = "#" + fragment.String()
	}
	var href strings.Builder
	href.WriteString(scheme + "://")
	if u.Username != "" || u.Password != "" {
		href.WriteString(u.Username)
		if u.Password != "" {
			href.WriteString(":" + u.Password)
		}
		href.WriteString("@")
	}
	href.WriteString(host)
	if port >= 0 {
		href.WriteString(":" + u.Port)
	}
	href.WriteString(u.Pathname)
	if hasQuery {
		href.WriteString("?" + query.String())
	}
	if hasFragment {
		href.WriteString("#" + fragment.String())
	}
	u.Href = href.String()
	return u, nil
}

// keptVerbatim reports a path that Node's URL parser takes as it is
// although it may hold dot segments, where a browser would resolve them.
// view is the path after its first slash. Node looks at the first "/."
// only: when the path has dots but nothing to encode, does not start with
// a dot, and that first "/." starts an ordinary name ("/.well-known"), it
// does not look at the segments after it, so "/.well-known/../x" stays
// what it is.
func keptVerbatim(view []rune) bool {
	if len(view) == 0 || view[0] == '.' {
		return false
	}
	first := -1
	for i, c := range view {
		if c == '\\' || c == '%' || isPathEncode(c) {
			return false
		}
		if first < 0 && c == '.' && i > 0 && view[i-1] == '/' {
			first = i
		}
	}
	if first < 0 {
		return false
	}
	after := first + 1
	return after < len(view) && view[after] != '.' && view[after] != '/'
}

func lowerASCII(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
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

func isNormalizedDriveLetter(segment string) bool {
	return len(segment) == 2 && isASCIIAlpha(rune(segment[0])) && segment[1] == ':'
}

// The percent-encode sets say which characters a part of a URL writes as
// %XX. Each set holds the one before it.

func isC0Encode(c rune) bool { return c < 0x20 || c > 0x7E }

func isFragmentEncode(c rune) bool {
	return isC0Encode(c) || c == ' ' || c == '"' || c == '<' || c == '>' || c == '`'
}

func isQueryEncode(c rune) bool {
	return isC0Encode(c) || c == ' ' || c == '"' || c == '#' || c == '<' || c == '>'
}

func isSpecialQueryEncode(c rune) bool { return isQueryEncode(c) || c == '\'' }

func isPathEncode(c rune) bool {
	return isQueryEncode(c) || c == '?' || c == '`' || c == '{' || c == '}'
}

func isUserinfoEncode(c rune) bool {
	return isPathEncode(c) || c == '/' || c == ':' || c == ';' || c == '=' || c == '@' || (c >= '[' && c <= '^') || c == '|'
}

const upperHex = "0123456789ABCDEF"

// percentEncode writes c, as %XX for each of its UTF-8 bytes when the set
// holds it.
func percentEncode(b *strings.Builder, c rune, inSet func(rune) bool) {
	if !inSet(c) {
		b.WriteRune(c)
		return
	}
	var raw [utf8.UTFMax]byte
	n := utf8.EncodeRune(raw[:], c)
	for _, octet := range raw[:n] {
		b.WriteByte('%')
		b.WriteByte(upperHex[octet>>4])
		b.WriteByte(upperHex[octet&0xF])
	}
}

// percentDecode turns every %XX into its byte and leaves a % that is not
// followed by two hex digits as it is.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isASCIIHex(rune(s[i+1])) && isASCIIHex(rune(s[i+2])) {
			out = append(out, hexNibble(s[i+1])<<4|hexNibble(s[i+2]))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

func isForbiddenHost(c rune) bool {
	switch c {
	case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

func isForbiddenDomain(c rune) bool {
	return isForbiddenHost(c) || c <= 0x1F || c == '%' || c == 0x7F
}

// parseHost reads the host of a URL into its normal form. opaque is true
// for schemes that have no rules for their host.
func parseHost(input string, opaque bool) (string, error) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") {
			return "", errInvalidURL
		}
		address, ok := parseIPv6([]rune(input[1 : len(input)-1]))
		if !ok {
			return "", errInvalidURL
		}
		return "[" + serializeIPv6(address) + "]", nil
	}
	if opaque {
		var b strings.Builder
		for _, c := range input {
			if isForbiddenHost(c) {
				return "", errInvalidURL
			}
			percentEncode(&b, c, isC0Encode)
		}
		return b.String(), nil
	}
	decoded := percentDecode(input)
	for _, octet := range decoded {
		if octet >= utf8.RuneSelf {
			return "", errNeedsIDNA
		}
	}
	domain := strings.ToLower(string(decoded))
	for _, label := range strings.Split(domain, ".") {
		if strings.HasPrefix(label, "xn--") {
			return "", errNeedsIDNA
		}
	}
	for _, c := range domain {
		if isForbiddenDomain(c) {
			return "", errInvalidURL
		}
	}
	if endsInNumber(domain) {
		address, ok := parseIPv4(domain)
		if !ok {
			return "", errInvalidURL
		}
		return address, nil
	}
	return domain, nil
}

// endsInNumber reports whether the last label of a domain is a number, in
// which case the whole domain has to be an IPv4 address.
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

// parseIPv4Number reads one part of an IPv4 address: decimal, octal with
// a leading 0, or hex with a leading 0x.
func parseIPv4Number(part string) (*big.Int, bool) {
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
		c := part[i]
		switch {
		case radix == 10 && c >= '0' && c <= '9':
		case radix == 8 && c >= '0' && c <= '7':
		case radix == 16 && isASCIIHex(rune(c)):
		default:
			return nil, false
		}
	}
	n, ok := new(big.Int).SetString(part, radix)
	return n, ok
}

// parseIPv4 reads an IPv4 address of one to four parts, the last of which
// fills the bytes the others leave ("127.1" is 127.0.0.1).
func parseIPv4(domain string) (string, bool) {
	parts := strings.Split(domain, ".")
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
	room := new(big.Int).Exp(big.NewInt(256), big.NewInt(int64(5-len(numbers))), nil)
	if last.Cmp(room) >= 0 {
		return "", false
	}
	address := last.Uint64()
	for i, n := range numbers[:len(numbers)-1] {
		address += n.Uint64() << (8 * uint(3-i))
	}
	return strconv.FormatUint(address>>24, 10) + "." + strconv.FormatUint(address>>16&0xFF, 10) + "." +
		strconv.FormatUint(address>>8&0xFF, 10) + "." + strconv.FormatUint(address&0xFF, 10), true
}

// parseIPv6 reads the text between the brackets of an IPv6 host.
func parseIPv6(input []rune) ([8]uint16, bool) {
	var address [8]uint16
	at := func(i int) rune {
		if i >= len(input) {
			return eof
		}
		return input[i]
	}
	pieceIndex, compress, p := 0, -1, 0
	if at(p) == ':' {
		if at(p+1) != ':' {
			return address, false
		}
		p += 2
		pieceIndex++
		compress = pieceIndex
	}
	for at(p) != eof {
		if pieceIndex == 8 {
			return address, false
		}
		if at(p) == ':' {
			if compress != -1 {
				return address, false
			}
			p++
			pieceIndex++
			compress = pieceIndex
			continue
		}
		value, length := 0, 0
		for length < 4 && isASCIIHex(at(p)) {
			value = value*0x10 + int(hexNibble(byte(at(p))))
			p++
			length++
		}
		if at(p) == '.' {
			if length == 0 {
				return address, false
			}
			p -= length
			if pieceIndex > 6 {
				return address, false
			}
			numbersSeen := 0
			for at(p) != eof {
				piece := -1
				if numbersSeen > 0 {
					if at(p) == '.' && numbersSeen < 4 {
						p++
					} else {
						return address, false
					}
				}
				if !isASCIIDigit(at(p)) {
					return address, false
				}
				for isASCIIDigit(at(p)) {
					number := int(at(p) - '0')
					switch {
					case piece == -1:
						piece = number
					case piece == 0:
						return address, false
					default:
						piece = piece*10 + number
					}
					if piece > 255 {
						return address, false
					}
					p++
				}
				address[pieceIndex] = address[pieceIndex]*0x100 + uint16(piece)
				numbersSeen++
				if numbersSeen == 2 || numbersSeen == 4 {
					pieceIndex++
				}
			}
			if numbersSeen != 4 {
				return address, false
			}
			break
		} else if at(p) == ':' {
			p++
			if at(p) == eof {
				return address, false
			}
		} else if at(p) != eof {
			return address, false
		}
		address[pieceIndex] = uint16(value)
		pieceIndex++
	}
	if compress != -1 {
		swaps := pieceIndex - compress
		pieceIndex = 7
		for pieceIndex != 0 && swaps > 0 {
			address[pieceIndex], address[compress+swaps-1] = address[compress+swaps-1], address[pieceIndex]
			pieceIndex--
			swaps--
		}
	} else if pieceIndex != 8 {
		return address, false
	}
	return address, true
}

// serializeIPv6 writes an address in lower-case hex with its first
// longest run of two or more zero pieces as "::".
func serializeIPv6(address [8]uint16) string {
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
	ignore0 := false
	for i := 0; i < 8; i++ {
		if ignore0 && address[i] == 0 {
			continue
		}
		ignore0 = false
		if compress == i {
			if i == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			ignore0 = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(address[i]), 16))
		if i != 7 {
			b.WriteString(":")
		}
	}
	return b.String()
}
