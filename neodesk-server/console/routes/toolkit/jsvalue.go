package toolkit

import (
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// The text tools take text from a JSON body and hand text back, and what
// they answer depends on how JavaScript holds text: as UTF-16 code units,
// where half of a surrogate pair can stand alone. A text is kept here as
// a Go string in which such a lone half is written as its three bytes
// (WTF-8), so a text can be counted, cut and written out as the old
// routes did. Parsed JSON is held as
//
//	nil            null
//	bool           true, false
//	float64        a number
//	string         a string
//	[]any          an array
//	*object        an object with its keys in JavaScript order

// isLoneHalf reports the three bytes of a lone surrogate half at s[i:].
func isLoneHalf(s string, i int) bool {
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
		if isLoneHalf(s, i) {
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

// fromUnits builds a text from UTF-16 code units.
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

// utf16Len is text.length.
func utf16Len(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			n++
			i++
			continue
		}
		if isLoneHalf(s, i) {
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

// utf8Bytes is Buffer.from(text, 'utf8'): a lone surrogate half becomes
// the replacement character.
func utf8Bytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] >= utf8.RuneSelf && isLoneHalf(s, i) {
			out = append(out, 0xEF, 0xBF, 0xBD)
			i += 3
			continue
		}
		out = append(out, s[i])
		i++
	}
	return out
}

// decodeUTF8 is buffer.toString('utf8'): each broken sequence becomes one
// replacement character.
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

// bodyText is body[key] when typeof body[key] === 'string', as JavaScript
// read it: an escaped lone surrogate half stays one.
func bodyText(body middleware.JSONObject, key string) (string, bool) {
	raw := body.Raw(key)
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	v, err := parseJSON(string(raw))
	if err != nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// jsText is a text that goes into a JSON answer the way JSON.stringify
// writes it.
type jsText string

// MarshalJSON writes the text as JSON.stringify would.
func (t jsText) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	quoteJSON(&b, string(t))
	return []byte(b.String()), nil
}

// jsJSON is a parsed JSON value that goes into a JSON answer the way
// JSON.stringify writes it.
type jsJSON struct{ v any }

// MarshalJSON writes the value as JSON.stringify would.
func (j jsJSON) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	writeJSON(&b, j.v)
	return []byte(b.String()), nil
}

// echo is a body value sent back in an answer: res.json of what
// JSON.parse made of it.
func echo(v middleware.Value) jsJSON {
	parsed, err := parseJSON(string(v.Raw()))
	if err != nil {
		return jsJSON{nil}
	}
	return jsJSON{parsed}
}

// object is a JSON object that keeps its keys in the order JavaScript
// lists them: array indexes ascending, then the other keys as they were
// added.
type object struct {
	m     map[string]any
	order []string
}

func newObject() *object { return &object{m: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

// set adds a key or replaces its value; a replaced key keeps its place.
func (o *object) set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.order = append(o.order, key)
	}
	o.m[key] = value
}

func (o *object) keys() []string {
	type indexed struct {
		n   uint64
		key string
	}
	var idx []indexed
	var rest []string
	for _, k := range o.order {
		if n, ok := arrayIndex(k); ok {
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

// arrayIndex reads a key that is a whole number from 0 to 2^32-2 written
// without leading zeros.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, n <= 4294967294
}

// truthy is JavaScript's truthiness of a parsed JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
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

// jsString is String(v) for a parsed JSON value. ok is false where
// JavaScript throws "Cannot convert object to primitive value": an object
// whose own toString key hides the method.
func jsString(v any) (text string, ok bool) {
	switch x := v.(type) {
	case nil:
		return "null", true
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
			if e == nil {
				continue
			}
			s, ok := jsString(e)
			if !ok {
				return "", false
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), true
	case *object:
		if _, own := x.get("toString"); own {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

var (
	decimalLiteral = regexp.MustCompile(`^[+-]?(?:Infinity|[0-9]+\.?[0-9]*(?:[eE][+-]?[0-9]+)?|\.[0-9]+(?:[eE][+-]?[0-9]+)?)$`)
	radixLiteral   = regexp.MustCompile(`^0(?:[xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
)

// isJSSpace reports the white space and line ends JavaScript trims.
func isJSSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

// stringToNumber is Number(text): the empty text is 0, a decimal, hex,
// octal or binary literal is its value, anything else is NaN.
func stringToNumber(s string) float64 {
	s = strings.TrimFunc(s, isJSSpace)
	switch {
	case s == "":
		return 0
	case radixLiteral.MatchString(s):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		n, _ := new(big.Int).SetString(s[2:], base)
		f, _ := new(big.Float).SetInt(n).Float64()
		return f
	case decimalLiteral.MatchString(s):
		if strings.HasSuffix(s, "Infinity") {
			if s[0] == '-' {
				return math.Inf(-1)
			}
			return math.Inf(1)
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return math.NaN()
}

// toNumber is Number(v) for a parsed JSON value, with ok as in jsString.
func toNumber(v any) (n float64, ok bool) {
	switch x := v.(type) {
	case nil:
		return 0, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, true
	case string:
		return stringToNumber(x), true
	}
	text, ok := jsString(v)
	if !ok {
		return 0, false
	}
	return stringToNumber(text), true
}

// parseInt is parseInt(String(v), 10) for a body value: white space, a
// sign and digits from the start of the text; NaN when there are none.
func parseInt(v middleware.Value) float64 {
	var text string
	switch v.Kind() {
	case middleware.KindUndefined:
		return math.NaN()
	case middleware.KindString:
		text, _ = v.Str()
	default:
		parsed, err := parseJSON(string(v.Raw()))
		if err != nil {
			return math.NaN()
		}
		var ok bool
		if text, ok = jsString(parsed); !ok {
			return math.NaN()
		}
	}
	text = strings.TrimLeftFunc(text, isJSSpace)
	sign := 1.0
	if text != "" && (text[0] == '+' || text[0] == '-') {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	end := 0
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == 0 {
		return math.NaN()
	}
	n, _ := strconv.ParseFloat(text[:end], 64)
	return sign * n
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

// writeJSON is JSON.stringify(v) for a parsed JSON value.
func writeJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
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
	case *object:
		b.WriteByte('{')
		for i, k := range x.keys() {
			if i > 0 {
				b.WriteByte(',')
			}
			quoteJSON(b, k)
			b.WriteByte(':')
			writeJSON(b, x.m[k])
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

const hexDigits = "0123456789abcdef"

// quoteJSON writes a text as a JSON string: control characters and lone
// surrogate halves escaped, everything else as it is.
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
		case c >= utf8.RuneSelf && isLoneHalf(s, i):
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
