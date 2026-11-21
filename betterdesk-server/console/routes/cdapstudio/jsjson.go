package cdapstudio

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// A flow is JSON that the editor in the browser wrote and reads back. The
// routes parse it, look into it and write it out again, and the stored
// text has to be what the old console stored. So this file reads and
// writes JSON the way JavaScript does:
//
//	nil        null
//	bool       true, false
//	float64    a number
//	string     a string; half of a surrogate pair is kept as its three bytes
//	[]any      an array
//	*object    an object with its keys in JavaScript's order
//	missing    a property that is not there (undefined)

type missingValue struct{}

// missing is the value of a property an object does not have.
var missing = missingValue{}

// object is a JSON object. Its keys come in the order JavaScript lists
// them: array indexes ascending, then the others as they were added. A
// key given twice keeps its first place and its last value.
type object struct {
	m     map[string]any
	order []string
}

func newObject() *object { return &object{m: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

func (o *object) set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.order = append(o.order, key)
	}
	o.m[key] = value
}

func (o *object) keys() []string {
	var indexes, names []string
	for _, key := range o.order {
		if isArrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			names = append(names, key)
		}
	}
	if len(indexes) == 0 {
		return names
	}
	sort.Slice(indexes, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexes[i], 10, 64)
		b, _ := strconv.ParseUint(indexes[j], 10, 64)
		return a < b
	})
	return append(indexes, names...)
}

// isArrayIndex reports whether key is a whole number from 0 to 2^32-2
// written without leading zeros.
func isArrayIndex(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + uint64(c-'0')
	}
	return n <= 4294967294
}

// maxDepth is how deep arrays and objects may be nested in JSON that is
// read. Deeper JSON counts as not JSON, so a flow nested deeper is
// refused as "Invalid flow JSON". A flow of the editor is a few levels
// deep.
const maxDepth = 200

var errJSON = errors.New("not JSON")

// parseJSON is JSON.parse(text), for text nested no deeper than maxDepth.
func parseJSON(text string) (any, error) {
	p := &parser{s: text}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.s) {
		return nil, errJSON
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

func (p *parser) literal(word string, v any) (any, error) {
	if !strings.HasPrefix(p.s[p.i:], word) {
		return nil, errJSON
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) value(depth int) (any, error) {
	if p.i >= len(p.s) {
		return nil, errJSON
	}
	switch c := p.s[p.i]; {
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		return p.str()
	case c == 't':
		return p.literal("true", true)
	case c == 'f':
		return p.literal("false", false)
	case c == 'n':
		return p.literal("null", nil)
	case c == '-' || c >= '0' && c <= '9':
		return p.number()
	}
	return nil, errJSON
}

func (p *parser) object(depth int) (any, error) {
	if depth > maxDepth {
		return nil, errJSON
	}
	p.i++
	out := newObject()
	p.space()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, errJSON
		}
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, errJSON
		}
		p.i++
		p.space()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		out.set(key, v)
		p.space()
		if p.i >= len(p.s) {
			return nil, errJSON
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			return out, nil
		default:
			return nil, errJSON
		}
	}
}

func (p *parser) array(depth int) (any, error) {
	if depth > maxDepth {
		return nil, errJSON
	}
	p.i++
	out := []any{}
	p.space()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		v, err := p.value(depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.space()
		if p.i >= len(p.s) {
			return nil, errJSON
		}
		switch p.s[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return out, nil
		default:
			return nil, errJSON
		}
	}
}

func (p *parser) number() (any, error) {
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
		return nil, errJSON
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if !digits() {
			return nil, errJSON
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if !digits() {
			return nil, errJSON
		}
	}
	// A number too large for a float64 is infinite, as in JavaScript
	f, _ := strconv.ParseFloat(p.s[start:p.i], 64)
	return f, nil
}

func (p *parser) hex4() (uint16, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	n, err := strconv.ParseUint(p.s[p.i:p.i+4], 16, 16)
	if err != nil || strings.ContainsAny(p.s[p.i:p.i+4], "+-_") {
		return 0, false
	}
	p.i += 4
	return uint16(n), true
}

func (p *parser) str() (string, error) {
	p.i++
	var b strings.Builder
	for {
		if p.i >= len(p.s) {
			return "", errJSON
		}
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c < 0x20:
			return "", errJSON
		case c != '\\':
			b.WriteByte(c)
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
			unit, ok := p.hex4()
			if !ok {
				return "", errJSON
			}
			if unit >= 0xD800 && unit < 0xDC00 && strings.HasPrefix(p.s[p.i:], `\u`) {
				// The second half of a pair, when one follows
				save := p.i
				p.i += 2
				if low, ok := p.hex4(); ok && low >= 0xDC00 && low < 0xE000 {
					b.WriteRune(utf16.DecodeRune(rune(unit), rune(low)))
					continue
				}
				p.i = save
			}
			writeUnit(&b, unit)
		default:
			return "", errJSON
		}
	}
}

// writeUnit writes one UTF-16 unit that is not part of a pair: a
// character, or half a pair as its three bytes.
func writeUnit(b *strings.Builder, unit uint16) {
	if unit >= 0xD800 && unit < 0xE000 {
		b.WriteByte(0xE0 | byte(unit>>12))
		b.WriteByte(0x80 | byte(unit>>6)&0x3F)
		b.WriteByte(0x80 | byte(unit)&0x3F)
		return
	}
	b.WriteRune(rune(unit))
}

// halfPair reads half a surrogate pair kept as three bytes at s[i:].
func halfPair(s string, i int) (uint16, bool) {
	if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
		return 0xD000 | uint16(s[i+1]&0x3F)<<6 | uint16(s[i+2]&0x3F), true
	}
	return 0, false
}

// units is the number of UTF-16 units of s: s.length in JavaScript.
func units(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if _, ok := halfPair(s, i); ok {
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r > 0xFFFF {
			n++
		}
		n++
		i += size
	}
	return n
}

// stringify is JSON.stringify(v, null, indent) for a value parseJSON
// returned or one built from such values. A missing value has no text:
// ok is false, as when JavaScript returns undefined.
func stringify(v any, indent string) (text string, ok bool) {
	if v == missing {
		return "", false
	}
	var b strings.Builder
	writeJSON(&b, v, indent, "")
	return b.String(), true
}

func writeJSON(b *strings.Builder, v any, indent, current string) {
	switch t := v.(type) {
	case nil, missingValue:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			b.WriteString("null")
			return
		}
		b.WriteString(numberText(t))
	case string:
		quote(b, t)
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		inner := current + indent
		b.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteByte('\n')
				b.WriteString(inner)
			}
			writeJSON(b, item, indent, inner)
		}
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(current)
		}
		b.WriteByte(']')
	case *object:
		inner := current + indent
		wrote := false
		b.WriteByte('{')
		for _, key := range t.keys() {
			item, _ := t.get(key)
			if item == missing {
				continue
			}
			if wrote {
				b.WriteByte(',')
			}
			wrote = true
			if indent != "" {
				b.WriteByte('\n')
				b.WriteString(inner)
			}
			quote(b, key)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			writeJSON(b, item, indent, inner)
		}
		if wrote && indent != "" {
			b.WriteByte('\n')
			b.WriteString(current)
		}
		b.WriteByte('}')
	}
}

// indentedLen is the length of v written by stringify with indent spaces
// per level, starting depth levels in, counted without writing it. It
// stops counting once the length is over limit.
func indentedLen(v any, indent, depth, limit int) int {
	switch t := v.(type) {
	case nil, missingValue:
		return len("null")
	case bool:
		return len(strconv.FormatBool(t))
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return len("null")
		}
		return len(numberText(t))
	case string:
		return quotedLen(t)
	case []any:
		if len(t) == 0 {
			return 2
		}
		n := 2 + len(t) - 1 + len(t)*indentOverhead(indent, depth+1) + indentOverhead(indent, depth)
		for _, item := range t {
			if n > limit {
				return n
			}
			n += indentedLen(item, indent, depth+1, limit)
		}
		return n
	case *object:
		n, wrote := 2, 0
		for _, key := range t.keys() {
			item, _ := t.get(key)
			if item == missing {
				continue
			}
			if n > limit {
				return n
			}
			wrote++
			n += indentOverhead(indent, depth+1) + quotedLen(key) + 1 + indentedLen(item, indent, depth+1, limit)
			if indent > 0 {
				n++
			}
		}
		if wrote > 0 {
			n += wrote - 1 + indentOverhead(indent, depth)
		}
		return n
	}
	return 0
}

// indentOverhead is the line break and the indentation at depth, none
// without indentation.
func indentOverhead(indent, depth int) int {
	if indent == 0 {
		return 0
	}
	return 1 + indent*depth
}

// quotedLen is the length of s written by quote.
func quotedLen(s string) int {
	var b strings.Builder
	quote(&b, s)
	return b.Len()
}

// quote writes s as a JSON string the way JSON.stringify does: only the
// quote, the backslash and control characters are escaped, and half a
// surrogate pair is written as \udXXX.
func quote(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		case c == 0xED:
			if unit, ok := halfPair(s, i); ok {
				b.WriteString(`\u`)
				for shift := 12; shift >= 0; shift -= 4 {
					b.WriteByte(hex[unit>>shift&15])
				}
				i += 2
				continue
			}
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// numberText is String(n) for a finite number: the shortest digits that
// read back as n, plain up to 21 digits and down to 1e-7, with an
// exponent beyond.
func numberText(f float64) string {
	if f == 0 {
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	mantissa, expText, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mantissa, ".", "", 1)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := strconv.Itoa(n - 1)
	if n-1 > 0 {
		e = "+" + e
	}
	if k == 1 {
		return sign + digits + "e" + e
	}
	return sign + digits[:1] + "." + digits[1:] + "e" + e
}

// toText is String(v). ok is false where JavaScript raises an error: for
// an object with a member named toString, which is found where the
// function is looked for.
func toText(v any) (text string, ok bool) {
	switch t := v.(type) {
	case missingValue:
		return "undefined", true
	case nil:
		return "null", true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		switch {
		case math.IsNaN(t):
			return "NaN", true
		case math.IsInf(t, 1):
			return "Infinity", true
		case math.IsInf(t, -1):
			return "-Infinity", true
		}
		return numberText(t), true
	case string:
		return t, true
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item == nil || item == missing {
				continue
			}
			part, ok := toText(item)
			if !ok {
				return "", false
			}
			parts[i] = part
		}
		return strings.Join(parts, ","), true
	case *object:
		if _, own := t.get("toString"); own {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

// truthy is !!v.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil, missingValue:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return true
}

// property is v[name] for a value that is not null or missing: the member
// of an object, the length of an array or a string, and missing otherwise.
func property(v any, name string) any {
	switch t := v.(type) {
	case *object:
		if member, ok := t.get(name); ok {
			return member
		}
	case []any:
		if name == "length" {
			return float64(len(t))
		}
	case string:
		if name == "length" {
			return float64(units(t))
		}
	}
	return missing
}
