package i18n

import (
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The language files are JSON that the web console reads back as JSON, so
// the values are kept the way a JavaScript engine holds them:
//
//	nil            null
//	bool           true, false
//	float64        a number
//	string         a string; a lone surrogate is kept as its three WTF-8 bytes
//	[]any          an array
//	*Object        an object with its keys in JavaScript order
//	undefined      a missing value (never inside parsed JSON)
type undefinedType struct{}

var undefined = undefinedType{}

// Object is a JSON object that keeps its keys in the order JavaScript
// lists them: array indexes ascending, then the other keys as inserted.
type Object struct {
	m       map[string]any
	order   []string
	indexes int
	sorted  []string
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{m: map[string]any{}}
}

// Get returns the value of an own key.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

// Set adds a key or replaces its value; a replaced key keeps its place.
func (o *Object) Set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.order = append(o.order, key)
		if isArrayIndex(key) {
			o.indexes++
		}
		o.sorted = nil
	}
	o.m[key] = value
}

// Len is the number of keys.
func (o *Object) Len() int { return len(o.order) }

// Keys lists the keys in JavaScript order. The slice must not be changed.
func (o *Object) Keys() []string {
	if o.indexes == 0 {
		return o.order
	}
	if o.sorted != nil {
		return o.sorted
	}
	type indexed struct {
		n   uint64
		key string
	}
	idx := make([]indexed, 0, o.indexes)
	rest := make([]string, 0, len(o.order)-o.indexes)
	for _, k := range o.order {
		if isArrayIndex(k) {
			n, _ := strconv.ParseUint(k, 10, 64)
			idx = append(idx, indexed{n, k})
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].n < idx[j].n })
	out := make([]string, 0, len(o.order))
	for _, e := range idx {
		out = append(out, e.key)
	}
	o.sorted = append(out, rest...)
	return o.sorted
}

// MarshalJSON writes the object as JSON.stringify would.
func (o *Object) MarshalJSON() ([]byte, error) {
	return []byte(stringify(o, "")), nil
}

func (o *Object) clone() *Object {
	c := &Object{m: make(map[string]any, len(o.m)), order: append([]string(nil), o.order...), indexes: o.indexes}
	for k, v := range o.m {
		c.m[k] = v
	}
	return c
}

// isArrayIndex reports whether key is a canonical array index: an integer
// from 0 to 2^32-2 written without leading zeros.
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

// --- strings as UTF-16 ---

// units returns the UTF-16 code units of s.
func units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			out = append(out, uint16(c))
			i++
			continue
		}
		if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			out = append(out, 0xD000|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F))
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, 0xD800+uint16(r>>10), 0xDC00+uint16(r&0x3FF))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// fromUnits builds a string from UTF-16 code units.
func fromUnits(u []uint16) string {
	var b strings.Builder
	b.Grow(len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c < 0x80:
			b.WriteByte(byte(c))
		case c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF:
			b.WriteRune(0x10000 + (rune(c)-0xD800)<<10 + (rune(u[i+1]) - 0xDC00))
			i++
		case c >= 0xD800 && c <= 0xDFFF:
			b.WriteByte(0xED)
			b.WriteByte(0x80 | byte(c>>6)&0x3F)
			b.WriteByte(0x80 | byte(c)&0x3F)
		default:
			b.WriteRune(rune(c))
		}
	}
	return b.String()
}

// utf16Len is the length of s as JavaScript counts it.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			n++
			i++
			continue
		}
		if c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			n++
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUnits is s.slice(0, n) in JavaScript.
func sliceUnits(s string, n int) string {
	if len(s) <= n {
		return s
	}
	u := units(s)
	if len(u) <= n {
		return s
	}
	return fromUnits(u[:n])
}

// decodeUTF8 turns file or upload bytes into text the way Node does: each
// broken sequence becomes one U+FFFD.
func decodeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	out.Grow(len(b))
	needed, seen := 0, 0
	var cp rune
	lower, upper := byte(0x80), byte(0xBF)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if needed == 0 {
			switch {
			case c < 0x80:
				out.WriteByte(c)
			case c >= 0xC2 && c <= 0xDF:
				needed, cp = 1, rune(c&0x1F)
			case c >= 0xE0 && c <= 0xEF:
				if c == 0xE0 {
					lower = 0xA0
				}
				if c == 0xED {
					upper = 0x9F
				}
				needed, cp = 2, rune(c&0x0F)
			case c >= 0xF0 && c <= 0xF4:
				if c == 0xF0 {
					lower = 0x90
				}
				if c == 0xF4 {
					upper = 0x8F
				}
				needed, cp = 3, rune(c&0x07)
			default:
				out.WriteRune(0xFFFD)
			}
			continue
		}
		if c < lower || c > upper {
			needed, seen, cp = 0, 0, 0
			lower, upper = 0x80, 0xBF
			out.WriteRune(0xFFFD)
			i--
			continue
		}
		lower, upper = 0x80, 0xBF
		cp = cp<<6 | rune(c&0x3F)
		seen++
		if seen == needed {
			out.WriteRune(cp)
			needed, seen, cp = 0, 0, 0
		}
	}
	if needed != 0 {
		out.WriteRune(0xFFFD)
	}
	return out.String()
}

// latin1 reads header bytes the way Node does: every byte is one character.
func latin1(s string) string {
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
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

// --- conversions ---

// truthy is JavaScript's truthiness.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil, undefinedType:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// jsString is String(v). ok is false where JavaScript throws a TypeError:
// an object whose own toString key hides the method.
func jsString(v any) (text string, ok bool) {
	switch x := v.(type) {
	case nil:
		return "null", true
	case undefinedType:
		return "undefined", true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		return numberString(x), true
	case string:
		return x, true
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil || e == undefined {
				continue
			}
			s, ok := jsString(e)
			if !ok {
				return "", false
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), true
	case *Object:
		if _, own := x.Get("toString"); own {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

// numberString is Number.prototype.toString for base 10.
func numberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	// d.ddddde±xx with the shortest digits that read back as f
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, expText, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mant, ".", "", 1)
	k := len(digits)
	n := exp + 1
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	out := sign + digits[:1]
	if k > 1 {
		out += "." + digits[1:]
	}
	if n-1 < 0 {
		return out + "e-" + strconv.Itoa(1-n)
	}
	return out + "e+" + strconv.Itoa(n-1)
}

// toFixed1 is Number.prototype.toFixed(1): the exact value rounded to one
// decimal, halves up.
func toFixed1(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 0) || math.Abs(f) >= 1e21 {
		return numberString(f)
	}
	neg := f < 0
	if neg {
		f = -f
	}
	r := new(big.Rat).SetFloat64(f)
	r.Mul(r, big.NewRat(10, 1))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	text := n.String()
	if len(text) < 2 {
		text = "0" + text
	}
	out := text[:len(text)-1] + "." + text[len(text)-1:]
	if neg {
		return "-" + out
	}
	return out
}

// entries is Object.entries: the own keys of an object, the indexes of an
// array, the code units of a string, nothing for anything else.
func entries(v any, each func(key string, value any)) {
	switch x := v.(type) {
	case *Object:
		for _, k := range x.Keys() {
			each(k, x.m[k])
		}
	case []any:
		for i, e := range x {
			each(strconv.Itoa(i), e)
		}
	case string:
		for i, u := range units(x) {
			each(strconv.Itoa(i), fromUnits([]uint16{u}))
		}
	}
}

// property is v[key] for the keys data can have: own keys of objects,
// indexes and length of arrays and strings. Methods that JavaScript finds
// on the prototypes are not modelled and read as undefined.
func property(v any, key string) any {
	switch x := v.(type) {
	case *Object:
		if value, ok := x.Get(key); ok {
			return value
		}
	case []any:
		if key == "length" {
			return float64(len(x))
		}
		if isArrayIndex(key) {
			if n, _ := strconv.Atoi(key); n < len(x) {
				return x[n]
			}
		}
	case string:
		if key == "length" {
			return float64(utf16Len(x))
		}
		if isArrayIndex(key) {
			u := units(x)
			if n, _ := strconv.Atoi(key); n < len(u) {
				return fromUnits(u[n : n+1])
			}
		}
	}
	return undefined
}

// --- JSON.stringify ---

// stringify is JSON.stringify(v, null, indent) for parsed JSON.
func stringify(v any, indent string) string {
	var b strings.Builder
	writeJSON(&b, v, indent, "")
	return b.String()
}

func writeJSON(b *strings.Builder, v any, indent, current string) {
	switch x := v.(type) {
	case nil, undefinedType:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(numberString(x))
		}
	case string:
		quoteJSON(b, x)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		inner := current + indent
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteByte('\n')
				b.WriteString(inner)
			}
			writeJSON(b, e, indent, inner)
		}
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(current)
		}
		b.WriteByte(']')
	case *Object:
		keys := x.Keys()
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		inner := current + indent
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if indent != "" {
				b.WriteByte('\n')
				b.WriteString(inner)
			}
			quoteJSON(b, k)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			writeJSON(b, x.m[k], indent, inner)
		}
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(current)
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

const hexDigits = "0123456789abcdef"

func quoteJSON(b *strings.Builder, s string) {
	b.WriteByte('"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		var esc string
		size := 1
		switch {
		case c == '"':
			esc = `\"`
		case c == '\\':
			esc = `\\`
		case c == '\b':
			esc = `\b`
		case c == '\f':
			esc = `\f`
		case c == '\n':
			esc = `\n`
		case c == '\r':
			esc = `\r`
		case c == '\t':
			esc = `\t`
		case c < 0x20:
			esc = `\u00` + string(hexDigits[c>>4]) + string(hexDigits[c&0xF])
		case c == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF:
			u := 0xD000 | uint16(s[i+1]&0x3F)<<6 | uint16(s[i+2]&0x3F)
			esc = `\u` + string(hexDigits[u>>12]) + string(hexDigits[u>>8&0xF]) + string(hexDigits[u>>4&0xF]) + string(hexDigits[u&0xF])
			size = 3
		}
		if esc == "" {
			i++
			continue
		}
		b.WriteString(s[start:i])
		b.WriteString(esc)
		i += size
		start = i
	}
	b.WriteString(s[start:])
	b.WriteByte('"')
}
