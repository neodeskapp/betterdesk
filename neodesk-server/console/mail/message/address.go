package message

import (
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/addressparser"
)

// normalizeAddress brings an address into the form that goes into the
// header and the envelope.
//
// Control characters, '<' and '>' become spaces, so no address can end a
// header line or an SMTP command. The address is split at its last '@', or
// in front of a domain literal ("[...]") at the end. The local part stays
// as it is when it is a dot-atom or a complete quoted string and is put
// into quotes otherwise. The domain is lowered and normalized, see
// normalizeDomain; next to a local part that is not ASCII it stays Unicode.
func normalizeAddress(t *unicodeTables, address string) string {
	address = jsTrim(replaceRuns(address, func(c byte) bool { return c < 0x20 || c == 0x7f || c == '<' || c == '>' }))
	if address == "" {
		return ""
	}
	lastAt := strings.LastIndexByte(address, '@')
	if address[len(address)-1] == ']' {
		// '@' may stand inside a domain literal, so the split is at the '@'
		// in front of the bracket
		open := strings.LastIndexByte(address, '[')
		if open >= 1 && address[open-1] == '@' && !strings.ContainsAny(address[open+1:len(address)-1], "]\\") {
			lastAt = open - 1
		}
	}
	if lastAt < 0 {
		return normalizeLocalPart(address)
	}
	user, domain := address[:lastAt], address[lastAt+1:]
	encoded, err := normalizeDomain(t, jsToLower(t, domain), !isASCII(user))
	if err != nil {
		encoded = domain
	}
	return normalizeLocalPart(user) + "@" + encoded
}

// replaceRuns replaces every run of bytes that match by one space.
func replaceRuns(s string, match func(byte) bool) string {
	first := strings.IndexFunc(s, func(r rune) bool { return r < 0x80 && match(byte(r)) })
	if first < 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	out.WriteString(s[:first])
	inRun := false
	for i := first; i < len(s); i++ {
		if match(s[i]) {
			if !inRun {
				out.WriteByte(' ')
			}
			inRun = true
			continue
		}
		inRun = false
		out.WriteByte(s[i])
	}
	return out.String()
}

// normalizeLocalPart returns a local part that can stand in an address as
// it is: a dot-atom or a complete quoted string unchanged, anything else as
// a quoted string. That keeps "user@evil.example@good.example" from being
// read as an address at another domain.
func normalizeLocalPart(user string) string {
	if isDotAtom(user) || isQuotedString(user) {
		return user
	}
	return quoteString(user)
}

// isDotAtom reports whether s is made of atoms joined by single dots, where
// an atom holds letters, digits, the characters !#$%&'*+-/=?^_`{|}~ and
// anything outside ASCII.
func isDotAtom(s string) bool {
	previousDot := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.':
			if previousDot {
				return false
			}
			previousDot = true
			continue
		case c >= 0x80, c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0:
		default:
			return false
		}
		previousDot = false
	}
	return !previousDot
}

// isQuotedString reports whether s is one complete quoted string: it starts
// and ends with a quote, and in between a quote only follows a backslash.
func isQuotedString(s string) bool {
	if len(s) < 2 || s[0] != '"' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '"':
			return i == len(s)-1
		case '\\':
			// the backslash takes the next character with it
			i++
		}
	}
	return false
}

// quoteString wraps a value into quotes and puts a backslash in front of
// every quote and backslash inside.
func quoteString(s string) string {
	var out strings.Builder
	out.Grow(len(s) + 2)
	out.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			out.WriteByte('\\')
		}
		out.WriteByte(s[i])
	}
	out.WriteByte('"')
	return out.String()
}

// isPlainAddress reports whether an address can stand in a header without
// angle brackets: one '@' with something on both sides, and no white space
// or one of "(),:;<>[\] anywhere.
func isPlainAddress(address string) bool {
	at := strings.IndexByte(address, '@')
	if at <= 0 || at == len(address)-1 || strings.IndexByte(address[at+1:], '@') >= 0 {
		return false
	}
	return !strings.ContainsFunc(address, func(r rune) bool {
		return isJSSpace(r) || (r < 0x80 && strings.IndexByte("\"(),:;<>[\\]", byte(r)) >= 0)
	})
}

// parseAddresses reads the address lists of a header value, one list per
// string, and normalizes every address in them, group members included.
func parseAddresses(t *unicodeTables, values []string) []addressparser.Address {
	var list []addressparser.Address
	for _, value := range values {
		parsed := addressparser.Parse(value)
		normalizeParsed(t, parsed)
		list = append(list, parsed...)
	}
	return list
}

func normalizeParsed(t *unicodeTables, list []addressparser.Address) {
	for i := range list {
		if list[i].Address != "" {
			list[i].Address = normalizeAddress(t, list[i].Address)
		} else if list[i].IsGroup {
			normalizeParsed(t, list[i].Group)
		}
	}
}

// recipients collects the addresses of a header in order, each one once.
type recipients struct {
	list []string
	seen map[string]bool
}

func (r *recipients) add(address string) {
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	if !r.seen[address] {
		r.seen[address] = true
		r.list = append(r.list, address)
	}
}

// formatAddresses writes a parsed address list as a header value and adds
// every address to unique, which may be nil.
//
// An address without a name stands alone, in angle brackets when it holds a
// character that could be read as a separator. With a name it is
// `Name <address>`. A group is `Name:member, member;`. An entry without an
// address that is no group is left out.
func (b *builder) formatAddresses(list []addressparser.Address, unique *recipients) string {
	values := make([]string, 0, len(list))
	for _, entry := range list {
		switch {
		case entry.Address != "":
			// The address is normalized a second time here, after the pass
			// that parseAddresses made. A domain the second pass reads
			// differently comes out differently.
			address := normalizeAddress(b.tables, entry.Address)
			switch {
			case entry.Name != "":
				values = append(values, b.encodeAddressName(entry.Name)+" <"+address+">")
			case isPlainAddress(address):
				values = append(values, address)
			default:
				values = append(values, "<"+address+">")
			}
			if unique != nil {
				unique.add(address)
			}
		case entry.IsGroup:
			members := ""
			if len(entry.Group) > 0 {
				members = jsTrim(b.formatAddresses(entry.Group, unique))
			}
			values = append(values, b.encodeAddressName(entry.Name)+":"+members+";")
		}
	}
	return strings.Join(values, ", ")
}

// encodeAddressName returns a display name as it stands in front of an
// address: letters, digits, '_' and spaces as they are, other printable
// ASCII as a quoted string, and everything else as encoded words.
func (b *builder) encodeAddressName(name string) string {
	plain, printable := true, true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == ' ') {
			plain = false
		}
		if c < 0x20 || c > 0x7e {
			printable = false
		}
	}
	switch {
	case plain:
		return name
	case printable:
		return quoteString(name)
	}
	return encodeWord(name, textEncoding(name), 52)
}
