package discovery

// probeType is the "type" of a probe the responder answers.
const probeType = "betterdesk-discover"

// IsProbe reports whether a datagram is a discovery probe: JSON text, as
// JSON.parse reads it after decoding the bytes as UTF-8, whose value is an
// object with a "type" member that is the text "betterdesk-discover"
// (the last "type" member counts). Nothing else gets an answer.
//
// The grammar is that of JSON.parse, with no limit on nesting.
func IsProbe(msg []byte) bool {
	p := probeParser{s: decodeUTF8(msg)}
	return p.probe()
}

// probeParser checks JSON text without building values.
type probeParser struct {
	s   string
	pos int
}

func (p *probeParser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

// peek returns the byte at the position, 0 at the end.
func (p *probeParser) peek() byte {
	if p.pos < len(p.s) {
		return p.s[p.pos]
	}
	return 0
}

// probe reads the whole text and reports whether it is an object whose
// last "type" member is the probe's type.
func (p *probeParser) probe() bool {
	p.skipSpace()
	if p.peek() != '{' {
		// Valid or not, anything but an object gets no answer
		return false
	}
	p.pos++
	p.skipSpace()
	if p.peek() == '}' {
		return false
	}
	matches := false
	for {
		isType, ok := p.key()
		if !ok {
			return false
		}
		value, ok := p.value()
		if !ok {
			return false
		}
		if isType {
			matches = value
		}
		p.skipSpace()
		switch p.peek() {
		case ',':
			p.pos++
			continue
		case '}':
			p.pos++
			p.skipSpace()
			return p.pos == len(p.s) && matches
		}
		return false
	}
}

// key reads a member name and its colon, and reports whether the name is
// "type".
func (p *probeParser) key() (isType, ok bool) {
	p.skipSpace()
	if p.peek() != '"' {
		return false, false
	}
	start := p.pos
	if !p.str() {
		return false, false
	}
	p.skipSpace()
	if p.peek() != ':' {
		return false, false
	}
	p.pos++
	return p.decodedEquals(start, "type"), true
}

// value reads one value, nested ones included, and reports whether it is
// a string that holds the probe's type.
func (p *probeParser) value() (matches, ok bool) {
	p.skipSpace()
	switch p.peek() {
	case '"':
		start := p.pos
		if !p.str() {
			return false, false
		}
		return p.decodedEquals(start, probeType), true
	case '{', '[':
		return false, p.container()
	}
	return false, p.literal()
}

// container reads an object or array, with everything inside it, without
// recursion.
func (p *probeParser) container() bool {
	var stack []byte
	open := func(c byte) bool {
		p.pos++
		stack = append(stack, c)
		p.skipSpace()
		if (c == '{' && p.peek() == '}') || (c == '[' && p.peek() == ']') {
			p.pos++
			stack = stack[:len(stack)-1]
			return true
		}
		if c == '{' {
			if _, ok := p.key(); !ok {
				return false
			}
		}
		return true
	}
	// Here a value is expected
	for {
		p.skipSpace()
		switch c := p.peek(); c {
		case '{', '[':
			closedAtOnce := len(stack)
			if !open(c) {
				return false
			}
			if len(stack) > closedAtOnce {
				continue
			}
		case '"':
			if !p.str() {
				return false
			}
		default:
			if !p.literal() {
				return false
			}
		}
		// A value ended: a comma, a closing bracket, or the end of the
		// outermost container
		for {
			if len(stack) == 0 {
				return true
			}
			p.skipSpace()
			top := stack[len(stack)-1]
			c := p.peek()
			if c == ',' {
				p.pos++
				if top == '{' {
					if _, ok := p.key(); !ok {
						return false
					}
				}
				break
			}
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				p.pos++
				stack = stack[:len(stack)-1]
				continue
			}
			return false
		}
	}
}

// str reads a string literal at the position.
func (p *probeParser) str() bool {
	p.pos++
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			return true
		case c < 0x20:
			return false
		case c == '\\':
			p.pos++
			switch p.peek() {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p.pos++
			case 'u':
				if p.pos+5 > len(p.s) {
					return false
				}
				for i := p.pos + 1; i < p.pos+5; i++ {
					if hexValue(p.s[i]) < 0 {
						return false
					}
				}
				p.pos += 5
			default:
				return false
			}
		default:
			p.pos++
		}
	}
	return false
}

// decodedEquals reports whether the valid string literal at start decodes
// to want, which is ASCII text.
func (p *probeParser) decodedEquals(start int, want string) bool {
	i := start + 1
	n := 0
	for {
		c := p.s[i]
		r := int(c)
		switch c {
		case '"':
			return n == len(want)
		case '\\':
			switch p.s[i+1] {
			case 'u':
				r = hexValue(p.s[i+2])<<12 | hexValue(p.s[i+3])<<8 | hexValue(p.s[i+4])<<4 | hexValue(p.s[i+5])
				i += 6
			case 'b':
				r, i = '\b', i+2
			case 'f':
				r, i = '\f', i+2
			case 'n':
				r, i = '\n', i+2
			case 'r':
				r, i = '\r', i+2
			case 't':
				r, i = '\t', i+2
			default:
				r, i = int(p.s[i+1]), i+2
			}
		default:
			i++
		}
		// Text beyond ASCII never equals the ASCII want: neither a byte
		// of 0x80 or more nor an escape above 0x7f
		if r > 0x7f || n >= len(want) || byte(r) != want[n] {
			return false
		}
		n++
	}
}

// literal reads a number, true, false or null.
func (p *probeParser) literal() bool {
	for _, word := range [...]string{"true", "false", "null"} {
		if len(p.s)-p.pos >= len(word) && p.s[p.pos:p.pos+len(word)] == word {
			p.pos += len(word)
			return true
		}
	}
	return p.number()
}

// number reads -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func (p *probeParser) number() bool {
	digits := func() int {
		n := 0
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.peek() == '-' {
		p.pos++
	}
	switch c := p.peek(); {
	case c == '0':
		p.pos++
	case c >= '1' && c <= '9':
		digits()
	default:
		return false
	}
	if p.peek() == '.' {
		p.pos++
		if digits() == 0 {
			return false
		}
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.peek(); c == '+' || c == '-' {
			p.pos++
		}
		if digits() == 0 {
			return false
		}
	}
	return true
}

func hexValue(c byte) int {
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
