package mail

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// The stored SMTP configuration is JSON that a request wrote almost
// unchecked, so every member can be of any JSON type, and the console
// worked on it with JavaScript's rules: what counts as set (truthiness),
// what text a value becomes (String), what number (Number), and how it is
// written back (JSON.stringify). value carries a member through those
// rules.

type kind uint8

const (
	kindUndefined kind = iota
	kindNull
	kindBool
	kindNumber
	kindString
	kindArray
	kindObject
)

// value is a JavaScript value as JSON.parse returns it, or undefined (the
// zero value).
//
// A string is UTF-8, except that half of a surrogate pair standing alone
// (which JSON can spell as "\ud800" and JavaScript keeps) is held as the
// three bytes its code point would encode to. wellFormed turns those into
// U+FFFD where text leaves the package.
type value struct {
	kind  kind
	num   float64
	str   string
	flag  bool
	items []value
	// keys are the member names in the order they first appeared
	keys    []string
	members map[string]value
	// ownToString marks an object whose member "toString" is set. It is
	// all a value taken from a jsbind.Value knows about its members.
	ownToString bool
}

var (
	undefined = value{}
	null      = value{kind: kindNull}
)

func stringValue(s string) value  { return value{kind: kindString, str: s} }
func numberValue(f float64) value { return value{kind: kindNumber, num: f} }
func boolValue(b bool) value      { return value{kind: kindBool, flag: b} }

func objectValue(pairs ...any) value {
	v := value{kind: kindObject, members: map[string]value{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		v.set(pairs[i].(string), pairs[i+1].(value))
	}
	return v
}

func (v *value) set(key string, member value) {
	if _, seen := v.members[key]; !seen {
		v.keys = append(v.keys, key)
	}
	v.members[key] = member
	if key == "toString" {
		v.ownToString = true
	}
}

// member is v[name] for a name no prototype defines: the member of an
// object, undefined for everything else.
func (v value) member(name string) value {
	if v.kind != kindObject {
		return undefined
	}
	return v.members[name]
}

func (v value) isUndefined() bool { return v.kind == kindUndefined }

// truthy is !!v.
func (v value) truthy() bool {
	switch v.kind {
	case kindUndefined, kindNull:
		return false
	case kindBool:
		return v.flag
	case kindNumber:
		return v.num != 0 && !math.IsNaN(v.num)
	case kindString:
		return v.str != ""
	}
	return true
}

// errNotPrimitive is the TypeError JavaScript throws when it needs text or
// a number of an object that has a member named toString: the member
// stands in the way of the method every object has.
var errNotPrimitive = errors.New("Cannot convert object to primitive value")

// toString is String(v).
func (v value) toString() (string, error) {
	switch v.kind {
	case kindUndefined:
		return "undefined", nil
	case kindNull:
		return "null", nil
	case kindBool:
		if v.flag {
			return "true", nil
		}
		return "false", nil
	case kindNumber:
		return jsbind.NumberString(v.num), nil
	case kindString:
		return v.str, nil
	case kindArray:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			if item.kind == kindUndefined || item.kind == kindNull {
				continue
			}
			text, err := item.toString()
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	}
	if v.ownToString {
		return "", errNotPrimitive
	}
	return "[object Object]", nil
}

// toNumber is Number(v).
func (v value) toNumber() (float64, error) {
	switch v.kind {
	case kindUndefined:
		return math.NaN(), nil
	case kindNull:
		return 0, nil
	case kindBool:
		if v.flag {
			return 1, nil
		}
		return 0, nil
	case kindNumber:
		return v.num, nil
	}
	text, err := v.toString()
	if err != nil {
		return 0, err
	}
	return jsbind.ParseNumber(text), nil
}

// orderedKeys lists the members as JavaScript does: names that are array
// indexes first, in numeric order, then the others as they first appeared.
func (v value) orderedKeys() []string {
	keys := append([]string(nil), v.keys...)
	sort.SliceStable(keys, func(a, b int) bool {
		ia, oka := arrayIndex(keys[a])
		ib, okb := arrayIndex(keys[b])
		if oka && okb {
			return ia < ib
		}
		return oka && !okb
	})
	return keys
}

// arrayIndex reports whether a member name is an array index: a whole
// number from 0 to 4294967294, written without a sign or a leading zero.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n > 4294967294 {
		return 0, false
	}
	return n, true
}

// appendJSON appends JSON.stringify(v). undefined has no JSON: it is left
// out of an object, is null in a list, and at the top appends nothing.
func (v value) appendJSON(buf []byte) []byte {
	switch v.kind {
	case kindUndefined:
		return buf
	case kindNull:
		return append(buf, "null"...)
	case kindBool:
		return strconv.AppendBool(buf, v.flag)
	case kindNumber:
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			return append(buf, "null"...)
		}
		return append(buf, jsbind.NumberString(v.num)...)
	case kindString:
		return appendQuoted(buf, v.str)
	case kindArray:
		buf = append(buf, '[')
		for i, item := range v.items {
			if i > 0 {
				buf = append(buf, ',')
			}
			if item.kind == kindUndefined {
				buf = append(buf, "null"...)
				continue
			}
			buf = item.appendJSON(buf)
		}
		return append(buf, ']')
	}
	buf = append(buf, '{')
	first := true
	for _, key := range v.orderedKeys() {
		member := v.members[key]
		if member.kind == kindUndefined {
			continue
		}
		if !first {
			buf = append(buf, ',')
		}
		first = false
		buf = appendQuoted(buf, key)
		buf = append(buf, ':')
		buf = member.appendJSON(buf)
	}
	return append(buf, '}')
}

func (v value) json() string { return string(v.appendJSON(nil)) }

// appendQuoted appends s as JSON.stringify writes a string: the quote, the
// backslash and control characters are escaped, half a surrogate pair is
// written as its \u escape, everything else stays as it is.
func appendQuoted(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				buf = append(buf, '\\', c)
			case c == '\b':
				buf = append(buf, '\\', 'b')
			case c == '\f':
				buf = append(buf, '\\', 'f')
			case c == '\n':
				buf = append(buf, '\\', 'n')
			case c == '\r':
				buf = append(buf, '\\', 'r')
			case c == '\t':
				buf = append(buf, '\\', 't')
			case c < 0x20:
				buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			default:
				buf = append(buf, c)
			}
			i++
			continue
		}
		if half, ok := loneSurrogate(s[i:]); ok {
			buf = append(buf, '\\', 'u', hex[half>>12], hex[half>>8&0xf], hex[half>>4&0xf], hex[half&0xf])
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf = append(buf, replacement...)
		} else {
			buf = append(buf, s[i:i+size]...)
		}
		i += size
	}
	return append(buf, '"')
}

// loneSurrogate reports whether s starts with the three bytes that hold
// half a surrogate pair (U+D800 to U+DFFF).
func loneSurrogate(s string) (rune, bool) {
	if len(s) < 3 || s[0] != 0xED || s[1] < 0xA0 || s[1] > 0xBF || s[2] < 0x80 || s[2] > 0xBF {
		return 0, false
	}
	return rune(s[0]&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), true
}

func appendSurrogate(buf []byte, half rune) []byte {
	return append(buf, 0xED, 0x80|byte(half>>6&0x3F), 0x80|byte(half&0x3F))
}

// wellFormed returns s as valid UTF-8: half a surrogate pair and bytes
// that are no UTF-8 become U+FFFD, which is what Node wrote for them
// wherever a string left the process.
func wellFormed(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if _, ok := loneSurrogate(s[i:]); ok {
			b.WriteString(replacement)
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteString(replacement)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// maxDepth is how deep lists and objects may nest in text parseJSON reads.
// JSON.parse has no such limit; nothing the console stores comes near it.
const maxDepth = 5000

var errJSON = errors.New("not JSON")

// parseJSON is JSON.parse(text): one JSON value with nothing but white
// space around it. A member name that appears twice keeps its first place
// and its last value. A number too large for a double is an infinity.
func parseJSON(text string) (value, error) {
	p := jsonParser{s: decodeLossy(text)}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return undefined, err
	}
	p.space()
	if p.i != len(p.s) {
		return undefined, errJSON
	}
	return v, nil
}

type jsonParser struct {
	s string
	i int
}

func (p *jsonParser) space() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jsonParser) word(w string) bool {
	if strings.HasPrefix(p.s[p.i:], w) {
		p.i += len(w)
		return true
	}
	return false
}

func (p *jsonParser) value(depth int) (value, error) {
	if p.i >= len(p.s) || depth > maxDepth {
		return undefined, errJSON
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, err := p.str()
		if err != nil {
			return undefined, err
		}
		return stringValue(s), nil
	case c == 't':
		if p.word("true") {
			return boolValue(true), nil
		}
	case c == 'f':
		if p.word("false") {
			return boolValue(false), nil
		}
	case c == 'n':
		if p.word("null") {
			return null, nil
		}
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return undefined, errJSON
}

func (p *jsonParser) object(depth int) (value, error) {
	p.i++
	v := value{kind: kindObject, members: map[string]value{}}
	p.space()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return v, nil
	}
	for {
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return undefined, errJSON
		}
		key, err := p.str()
		if err != nil {
			return undefined, err
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return undefined, errJSON
		}
		p.i++
		p.space()
		member, err := p.value(depth + 1)
		if err != nil {
			return undefined, err
		}
		v.set(key, member)
		p.space()
		if p.i >= len(p.s) {
			return undefined, errJSON
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return v, nil
		default:
			return undefined, errJSON
		}
	}
}

func (p *jsonParser) array(depth int) (value, error) {
	p.i++
	v := value{kind: kindArray, items: []value{}}
	p.space()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return v, nil
	}
	for {
		p.space()
		item, err := p.value(depth + 1)
		if err != nil {
			return undefined, err
		}
		v.items = append(v.items, item)
		p.space()
		if p.i >= len(p.s) {
			return undefined, errJSON
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return v, nil
		default:
			return undefined, errJSON
		}
	}
}

func (p *jsonParser) number() (value, error) {
	start := p.i
	digits := func() bool {
		from := p.i
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
		}
		return p.i > from
	}
	if p.s[p.i] == '-' {
		p.i++
	}
	switch {
	case p.i < len(p.s) && p.s[p.i] == '0':
		p.i++
	case !digits():
		return undefined, errJSON
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if !digits() {
			return undefined, errJSON
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if !digits() {
			return undefined, errJSON
		}
	}
	// Out of range is an infinity or zero with the sign, as in JavaScript.
	f, _ := strconv.ParseFloat(p.s[start:p.i], 64)
	return numberValue(f), nil
}

func hexDigit(c byte) (rune, bool) {
	switch {
	case c >= '0' && c <= '9':
		return rune(c - '0'), true
	case c >= 'a' && c <= 'f':
		return rune(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return rune(c-'A') + 10, true
	}
	return 0, false
}

func (p *jsonParser) unit() (rune, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	var u rune
	for k := 0; k < 4; k++ {
		d, ok := hexDigit(p.s[p.i+k])
		if !ok {
			return 0, false
		}
		u = u<<4 | d
	}
	p.i += 4
	return u, true
}

// str reads a JSON string at the opening quote.
func (p *jsonParser) str() (string, error) {
	p.i++
	start := p.i
	// Most strings have no escape and are valid UTF-8: they are a slice.
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '"' {
			s := p.s[start:p.i]
			p.i++
			return s, nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.i++
	}
	buf := []byte(p.s[start:p.i])
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return string(buf), nil
		case c < 0x20:
			return "", errJSON
		case c != '\\':
			buf = append(buf, c)
			p.i++
			continue
		}
		p.i++
		if p.i >= len(p.s) {
			return "", errJSON
		}
		esc := p.s[p.i]
		p.i++
		switch esc {
		case '"', '\\', '/':
			buf = append(buf, esc)
		case 'b':
			buf = append(buf, '\b')
		case 'f':
			buf = append(buf, '\f')
		case 'n':
			buf = append(buf, '\n')
		case 'r':
			buf = append(buf, '\r')
		case 't':
			buf = append(buf, '\t')
		case 'u':
			u, ok := p.unit()
			if !ok {
				return "", errJSON
			}
			switch {
			case u >= 0xD800 && u <= 0xDBFF:
				// The first half of a pair, when the second follows
				if strings.HasPrefix(p.s[p.i:], `\u`) {
					save := p.i
					p.i += 2
					if low, ok := p.unit(); ok && low >= 0xDC00 && low <= 0xDFFF {
						buf = utf8.AppendRune(buf, 0x10000+(u-0xD800)<<10+(low-0xDC00))
						continue
					}
					p.i = save
				}
				buf = appendSurrogate(buf, u)
			case u >= 0xDC00 && u <= 0xDFFF:
				buf = appendSurrogate(buf, u)
			default:
				buf = utf8.AppendRune(buf, u)
			}
		default:
			return "", errJSON
		}
	}
	return "", errJSON
}

// replacement is U+FFFD, which stands for what is no text.
const replacement = string(utf8.RuneError)

// decodeLossy is how the text of a database row or a request body
// reached JavaScript: bytes that are no UTF-8 are U+FFFD, one for each run
// that could have started a character. After it, half a surrogate pair in
// a string can only come from an escape.
func decodeLossy(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			b.WriteByte(c)
			i++
			continue
		}
		// The bytes that may follow a first byte, by the table of
		// well-formed UTF-8
		need, lo, hi := 0, byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c == 0xE0:
			need, lo = 2, 0xA0
		case c == 0xED:
			need, hi = 2, 0x9F
		case c >= 0xE1 && c <= 0xEF:
			need = 2
		case c == 0xF0:
			need, lo = 3, 0x90
		case c == 0xF4:
			need, hi = 3, 0x8F
		case c >= 0xF1 && c <= 0xF3:
			need = 3
		}
		j := i + 1
		whole := need > 0
		for k := 0; k < need; k++ {
			if j >= len(s) || s[j] < lo || s[j] > hi {
				whole = false
				break
			}
			j++
			lo, hi = 0x80, 0xBF
		}
		if whole {
			b.WriteString(s[i:j])
		} else {
			b.WriteString(replacement)
		}
		i = j
	}
	return b.String()
}

// fromBind is a value a caller hands in as a jsbind.Value. Of an object
// only what String() needs is kept: whether it has a member "toString".
func fromBind(v jsbind.Value) value {
	switch {
	case v.IsUndefined():
		return undefined
	case v.IsNull():
		return null
	case v.IsString():
		return stringValue(v.Str())
	case v.IsNumber():
		return numberValue(v.ToNumber())
	case v.IsArray():
		items := make([]value, len(v.Items()))
		for i, item := range v.Items() {
			items[i] = fromBind(item)
		}
		return value{kind: kindArray, items: items}
	case v.IsObject():
		return value{kind: kindObject, ownToString: !v.Member("toString").IsUndefined()}
	}
	return boolValue(v.Truthy())
}

// parseInt10 is parseInt(text, 10): the whole number the text starts with,
// after white space and a sign, and NaN when it starts with none.
func parseInt10(text string) float64 {
	s := strings.TrimLeftFunc(text, jsbind.IsSpace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	// More digits than a double holds round to the nearest one, and past
	// its range to an infinity.
	f, _ := strconv.ParseFloat(s[:end], 64)
	if negative {
		return -f
	}
	return f
}
