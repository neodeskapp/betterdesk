package jsjson

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxDepth is how deep arrays and objects may be nested in a text that
// Parse reads. A deeper text counts as no JSON.
const MaxDepth = 10000

// ErrSyntax is the SyntaxError of JSON.parse.
var ErrSyntax = errors.New("not JSON")

// Parse is JSON.parse(text). A number too large for a double is infinite,
// of two members with one name the last one counts, and an escape for
// half of a surrogate pair is kept as that half.
func Parse(text string) (Value, error) {
	if !utf8.ValidString(text) {
		// What is no UTF-8 reached JavaScript as the replacement character
		text = string([]rune(text))
	}
	p := &parser{s: text}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return Value{}, err
	}
	p.space()
	if p.i != len(p.s) {
		return Value{}, ErrSyntax
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) space() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

// peek is the next byte, 0 at the end.
func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) word(word string, v Value) (Value, error) {
	if !strings.HasPrefix(p.s[p.i:], word) {
		return Value{}, ErrSyntax
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) value(depth int) (Value, error) {
	switch c := p.peek(); {
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		s, err := p.str()
		return OfText(s), err
	case c == 't':
		return p.word("true", Value{kind: Bool, flag: true})
	case c == 'f':
		return p.word("false", Value{kind: Bool})
	case c == 'n':
		return p.word("null", NullValue)
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	}
	return Value{}, ErrSyntax
}

func (p *parser) object(depth int) (Value, error) {
	if depth > MaxDepth {
		return Value{}, ErrSyntax
	}
	p.i++
	out := Value{kind: Object, members: map[string]Value{}}
	var order []string
	p.space()
	if p.peek() == '}' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		if p.peek() != '"' {
			return Value{}, ErrSyntax
		}
		key, err := p.str()
		if err != nil {
			return Value{}, err
		}
		p.space()
		if p.peek() != ':' {
			return Value{}, ErrSyntax
		}
		p.i++
		p.space()
		v, err := p.value(depth)
		if err != nil {
			return Value{}, err
		}
		if _, seen := out.members[key]; !seen {
			order = append(order, key)
		}
		out.members[key] = v
		p.space()
		switch p.peek() {
		case ',':
			p.i++
		case '}':
			p.i++
			out.keys = propertyOrder(order)
			return out, nil
		default:
			return Value{}, ErrSyntax
		}
	}
}

// propertyOrder puts keys in the order JavaScript lists the properties
// of an object: the ones that are array indexes by their number, then
// the others as they were added.
func propertyOrder(added []string) []string {
	var indexes, names []string
	for _, key := range added {
		if _, ok := arrayIndex(key); ok {
			indexes = append(indexes, key)
		} else {
			names = append(names, key)
		}
	}
	if len(indexes) == 0 {
		return names
	}
	sort.Slice(indexes, func(a, b int) bool {
		x, _ := arrayIndex(indexes[a])
		y, _ := arrayIndex(indexes[b])
		return x < y
	})
	return append(indexes, names...)
}

// arrayIndex reads a key that is an array index: a whole number from 0
// to 4294967294 written without zeros in front.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || len(key) > 1 && key[0] == '0' {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
		n = n*10 + uint64(key[i]-'0')
	}
	return n, n <= 4294967294
}

func (p *parser) array(depth int) (Value, error) {
	if depth > MaxDepth {
		return Value{}, ErrSyntax
	}
	p.i++
	out := Value{kind: Array, items: []Value{}}
	p.space()
	if p.peek() == ']' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		v, err := p.value(depth)
		if err != nil {
			return Value{}, err
		}
		out.items = append(out.items, v)
		p.space()
		switch p.peek() {
		case ',':
			p.i++
		case ']':
			p.i++
			return out, nil
		default:
			return Value{}, ErrSyntax
		}
	}
}

func (p *parser) digits() bool {
	from := p.i
	for c := p.peek(); c >= '0' && c <= '9'; c = p.peek() {
		p.i++
	}
	return p.i > from
}

func (p *parser) number() (Value, error) {
	start := p.i
	if p.peek() == '-' {
		p.i++
	}
	if p.peek() == '0' {
		p.i++
	} else if !p.digits() {
		return Value{}, ErrSyntax
	}
	if p.peek() == '.' {
		p.i++
		if !p.digits() {
			return Value{}, ErrSyntax
		}
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.i++
		if c := p.peek(); c == '+' || c == '-' {
			p.i++
		}
		if !p.digits() {
			return Value{}, ErrSyntax
		}
	}
	// Out of range is an infinity or zero with the sign, as in JavaScript
	f, _ := strconv.ParseFloat(p.s[start:p.i], 64)
	return OfNumber(f), nil
}

// unit reads the four hex digits of a \u escape.
func (p *parser) unit() (rune, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	var u rune
	for _, c := range []byte(p.s[p.i : p.i+4]) {
		switch {
		case c >= '0' && c <= '9':
			u = u<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			u = u<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			u = u<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	p.i += 4
	return u, true
}

func (p *parser) str() (string, error) {
	p.i++
	var b strings.Builder
	for {
		if p.i >= len(p.s) {
			return "", ErrSyntax
		}
		c := p.s[p.i]
		p.i++
		switch {
		case c == '"':
			return b.String(), nil
		case c < 0x20:
			return "", ErrSyntax
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		if p.i >= len(p.s) {
			return "", ErrSyntax
		}
		esc := p.s[p.i]
		p.i++
		switch esc {
		case '"', '\\', '/':
			b.WriteByte(esc)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			u, ok := p.unit()
			if !ok {
				return "", ErrSyntax
			}
			if u >= 0xD800 && u < 0xDC00 && strings.HasPrefix(p.s[p.i:], `\u`) {
				// The first half of a pair, when the second half follows
				at := p.i
				p.i += 2
				if low, ok := p.unit(); ok && low >= 0xDC00 && low < 0xE000 {
					b.WriteRune(0x10000 + (u-0xD800)<<10 + (low - 0xDC00))
					continue
				}
				p.i = at
			}
			writeUnit(&b, u)
		default:
			return "", ErrSyntax
		}
	}
}

// writeUnit appends one UTF-16 unit: a character, or half of a pair in
// the three bytes its code point would have.
func writeUnit(b *strings.Builder, u rune) {
	if u >= 0xD800 && u < 0xE000 {
		b.Write([]byte{0xED, 0x80 | byte(u>>6&0x3F), 0x80 | byte(u&0x3F)})
		return
	}
	b.WriteRune(u)
}
