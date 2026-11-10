package branding

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Branding settings arrive as JSON from the browser, from theme files and
// from backups, and the rules that clean them are those of JavaScript
// values. The values are kept the way a JavaScript engine holds them, so
// a setting of the wrong type is treated as the web console expects:
//
//	nil            null
//	Undefined      a missing value
//	bool           true, false
//	float64        a number
//	string         a string; a lone surrogate is kept as its three WTF-8 bytes
//	[]any          an array
//	*Object        an object with its keys in JavaScript order
type undefinedType struct{}

// Undefined is JavaScript's undefined: the value of a key that is not
// there. In JSON it is left out of an object and written as null in an
// array.
var Undefined any = undefinedType{}

// Object is a JSON object that keeps its keys in the order JavaScript
// lists them: array indexes ascending, then the other keys as inserted.
type Object struct {
	m       map[string]any
	order   []string
	indexes int
}

// NewObject returns an empty object.
func NewObject() *Object {
	return &Object{m: map[string]any{}}
}

// Get returns the value of an own key. The reading methods also work on a
// nil *Object, which has no keys.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.m[key]
	return v, ok
}

// Has reports whether the object has the key itself.
func (o *Object) Has(key string) bool {
	_, ok := o.Get(key)
	return ok
}

// Text returns the value of a key when it is a string, and "" otherwise.
func (o *Object) Text(key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// Set adds a key or replaces its value; a replaced key keeps its place.
func (o *Object) Set(key string, value any) {
	if o.m == nil {
		o.m = map[string]any{}
	}
	if _, ok := o.m[key]; !ok {
		o.order = append(o.order, key)
		if isArrayIndex(key) {
			o.indexes++
		}
	}
	o.m[key] = value
}

// Len is the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.order)
}

// Keys lists the keys in JavaScript order. The slice must not be changed.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	if o.indexes == 0 {
		return o.order
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
	return append(out, rest...)
}

// MarshalJSON writes the object as JSON.stringify would.
func (o *Object) MarshalJSON() ([]byte, error) {
	return []byte(Stringify(o)), nil
}

// object builds an Object from key and value pairs.
func object(pairs ...any) *Object {
	o := NewObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
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

// isSurrogateAt reports whether the three bytes at s[i:] are a lone
// surrogate in WTF-8.
func isSurrogateAt(s string, i int) bool {
	return s[i] == 0xED && i+2 < len(s) && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF
}

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
		if isSurrogateAt(s, i) {
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
		if isSurrogateAt(s, i) {
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

// substring is s.substring(0, n) in JavaScript: the first n UTF-16 code
// units. A cut through a character outside the basic plane leaves half of
// it, a lone surrogate.
func substring(s string, n int) string {
	if len(s) <= n {
		return s
	}
	u := units(s)
	if len(u) <= n {
		return s
	}
	return fromUnits(u[:n])
}

// wellFormed replaces every lone surrogate by U+FFFD, which is what a
// JavaScript string becomes when it is written out as UTF-8.
func wellFormed(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] == 0xED && isSurrogateAt(s, i) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0xED && isSurrogateAt(s, i) {
			b.WriteString("\uFFFD")
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// decodeUTF8 turns file or database bytes into text the way Node does:
// each broken sequence becomes one U+FFFD.
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

// --- conversions ---

// Truthy is JavaScript's truthiness.
func Truthy(v any) bool {
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

// isObjectLike is typeof v === 'object' for a value that is not null.
func isObjectLike(v any) bool {
	switch v.(type) {
	case *Object, []any:
		return true
	}
	return false
}

// toString is String(v). It throws the TypeError JavaScript throws for an
// object whose own toString key hides the method.
func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case undefinedType:
		return "undefined"
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return numberString(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil || e == Undefined {
				continue
			}
			parts[i] = toString(e)
		}
		return strings.Join(parts, ",")
	case *Object:
		if x.Has("toString") {
			throwType("Cannot convert object to primitive value")
		}
		return "[object Object]"
	}
	throwType("Cannot convert object to primitive value")
	return ""
}

// or is a || b.
func or(a, b any) any {
	if Truthy(a) {
		return a
	}
	return b
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

// toFixed2 is Number.prototype.toFixed(2): the exact value rounded to two
// decimals, halves away from zero.
func toFixed2(f float64) string {
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
	r.Mul(r, big.NewRat(100, 1))
	r.Add(r, big.NewRat(1, 2))
	n := new(big.Int).Quo(r.Num(), r.Denom())
	text := n.String()
	for len(text) < 3 {
		text = "0" + text
	}
	out := text[:len(text)-2] + "." + text[len(text)-2:]
	if neg {
		// A negative number that rounds to zero keeps its sign.
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

// spread copies the own keys of src into dst: { ...dst, ...src }.
func spread(dst *Object, src any) *Object {
	entries(src, func(k string, v any) { dst.Set(k, v) })
	return dst
}

// property is v[key] for the keys data can have: own keys of objects,
// indexes and length of arrays and strings. Reading a key of null or
// undefined throws, as in JavaScript. Methods that JavaScript finds on the
// prototypes are not modelled and read as undefined.
func property(v any, key string) any {
	switch x := v.(type) {
	case nil:
		throwType("Cannot read properties of null (reading '" + key + "')")
	case undefinedType:
		throwType("Cannot read properties of undefined (reading '" + key + "')")
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
	return Undefined
}

// hasOwn is Object.prototype.hasOwnProperty.call(v, key).
func hasOwn(v any, key string) bool {
	switch x := v.(type) {
	case *Object:
		return x.Has(key)
	case []any, string:
		return property(x, key) != Undefined
	}
	return false
}

// --- JSON.stringify ---

// Stringify is JSON.stringify(v): keys in JavaScript order, a key whose
// value is undefined left out, numbers and strings written as JavaScript
// writes them.
func Stringify(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v any) {
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
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, e)
		}
		b.WriteByte(']')
	case *Object:
		if x == nil {
			b.WriteString("null")
			return
		}
		b.WriteByte('{')
		first := true
		for _, k := range x.Keys() {
			if x.m[k] == Undefined {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			quoteJSON(b, k)
			b.WriteByte(':')
			writeJSON(b, x.m[k])
		}
		b.WriteByte('}')
	case jsonMarshaler:
		raw, err := x.MarshalJSON()
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(raw)
	default:
		// a Go value of the caller: written as encoding/json writes it,
		// without escaping <, > and &
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(x); err != nil {
			b.WriteString("null")
			return
		}
		b.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	}
}

type jsonMarshaler interface {
	MarshalJSON() ([]byte, error)
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
		case c == 0xED && isSurrogateAt(s, i):
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

// ToString is String(v): text as it is, numbers and booleans as
// JavaScript writes them, null and undefined by name, a list as its items
// joined by commas, an object as "[object Object]". It fails with a
// TypeError for an object that has a key named toString.
func ToString(v any) (text string, err error) {
	defer catch(&err)
	return toString(v), nil
}

// Field is v?.[key]: the value of an own key of an object, and Undefined
// for a key that is not there and for anything that is no object.
func Field(v any, key string) any {
	if o, ok := v.(*Object); ok {
		if value, has := o.Get(key); has {
			return value
		}
	}
	return Undefined
}

// Trim is String.prototype.trim: it removes what JavaScript counts as
// white space, which is more than ASCII, from both ends.
func Trim(s string) string { return trim(s) }
