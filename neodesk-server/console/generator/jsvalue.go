package generator

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// The generator's bundle profiles are JavaScript objects that were built,
// stringified, hashed and stored by JavaScript code, and the bytes of the
// results count: the stored profile text, its hash, the signed client
// configuration. The values here are the ones JSON.parse makes: nil
// (null), bool, float64, string, []any and *goapi.Object, whose keys keep
// JavaScript's property order.
//
// A JavaScript string can hold half of a surrogate pair. Such a half
// comes from JSON text ("\ud83d") or from a slice that cuts a character
// outside the Basic Multilingual Plane. Here it is kept in the string as
// the three bytes UTF-8 would give the code unit (WTF-8), so that
// stringify writes it back as JSON.stringify does, "\ud83d", and dbText
// stores it as the database drivers did, as U+FFFD.

// isJSSpace reports what String.prototype.trim removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// units returns the UTF-16 code units of s. A lone surrogate kept as
// WTF-8 is one unit; a byte that is no character is U+FFFD.
func units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for i := 0; i < len(s); {
		if u, ok := loneSurrogateAt(s, i); ok {
			out = append(out, u)
			i += 3
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if r >= 0x10000 {
			hi, lo := utf16.EncodeRune(r)
			out = append(out, uint16(hi), uint16(lo))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}

// loneSurrogateAt reports a surrogate code unit written as WTF-8 at s[i].
func loneSurrogateAt(s string, i int) (uint16, bool) {
	if i+2 >= len(s) || s[i] != 0xED || s[i+1] < 0xA0 || s[i+1] > 0xBF || s[i+2] < 0x80 || s[i+2] > 0xBF {
		return 0, false
	}
	return uint16(0xD000 | uint16(s[i+1]&0x3F)<<6 | uint16(s[i+2]&0x3F)), true
}

// fromUnits builds a string from UTF-16 code units: pairs become their
// character, a lone surrogate becomes WTF-8.
func fromUnits(us []uint16) string {
	var b strings.Builder
	for i := 0; i < len(us); i++ {
		u := us[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(us) && us[i+1] >= 0xDC00 && us[i+1] <= 0xDFFF {
			b.WriteRune(utf16.DecodeRune(rune(u), rune(us[i+1])))
			i++
			continue
		}
		if u >= 0xD800 && u <= 0xDFFF {
			b.WriteByte(0xED)
			b.WriteByte(byte(0x80 | (u>>6)&0x3F))
			b.WriteByte(byte(0x80 | u&0x3F))
			continue
		}
		b.WriteRune(rune(u))
	}
	return b.String()
}

// jsLength is s.length.
func jsLength(s string) int { return len(units(s)) }

// jsSlice is s.slice(0, n) for n >= 0, counted in UTF-16 code units.
func jsSlice(s string, n int) string {
	if len(s) <= n {
		// n bytes are at least n code units
		return s
	}
	us := units(s)
	if len(us) <= n {
		return s
	}
	return fromUnits(us[:n])
}

// dbText is the text the database drivers stored for a JavaScript
// string: each lone surrogate becomes U+FFFD.
func dbText(s string) string {
	if !strings.Contains(s, "\xED") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if _, ok := loneSurrogateAt(s, i); ok {
			b.WriteRune(utf8.RuneError)
			i += 3
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// truthy is JavaScript's truthiness of a parsed value.
func truthy(v any) bool { return goapi.Truthy(v) }

// or is a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// nullish is a ?? b for values where a missing property reads as nil.
func nullish(a, b any) any {
	if a == nil {
		return b
	}
	return a
}

// prop is v.name for a parsed value: the member of an object, nil (which
// stands for undefined too) for anything else.
func prop(v any, name string) any {
	o, _ := v.(*goapi.Object)
	if o == nil {
		return nil
	}
	return o.Get(name)
}

// isObject is v && typeof v === 'object': an object or an array.
func isObject(v any) bool {
	switch t := v.(type) {
	case *goapi.Object:
		return t != nil
	case []any:
		return true
	}
	return false
}

// jsString is String(v).
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsbind.NumberString(t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil {
				parts[i] = jsString(el)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// stringify is JSON.stringify(v): no white space, keys in property
// order, numbers in their shortest form, only what JSON requires
// escaped, lone surrogates as \u escapes.
func stringify(v any) string {
	var b strings.Builder
	writeJSON(&b, v, false)
	return b.String()
}

// stableStringify writes objects with their keys sorted by code unit at
// every level, as agentBundleService.stableStringify and
// customTxtBuilder.sortKeys did.
func stableStringify(v any) string {
	var b strings.Builder
	writeJSON(&b, v, true)
	return b.String()
}

func writeJSON(b *strings.Builder, v any, sorted bool) {
	switch t := v.(type) {
	case json.RawMessage:
		if len(t) == 0 {
			b.WriteString("null")
			return
		}
		b.Write(t)
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			b.WriteString("null")
			return
		}
		b.WriteString(jsbind.NumberString(t))
	case string:
		quote(b, t)
	case []any:
		b.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, el, sorted)
		}
		b.WriteByte(']')
	case *goapi.Object:
		if t == nil {
			b.WriteString("null")
			return
		}
		keys := t.Keys()
		if sorted {
			sortKeys(keys)
		}
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(b, k)
			b.WriteByte(':')
			writeJSON(b, t.Get(k), sorted)
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// stringifyIndent is JSON.stringify(v, null, 2).
func stringifyIndent(v any) string {
	var b strings.Builder
	writeIndented(&b, v, "")
	return b.String()
}

func writeIndented(b *strings.Builder, v any, indent string) {
	inner := indent + "  "
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, el := range t {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			writeIndented(b, el, inner)
		}
		b.WriteString("\n" + indent + "]")
	case *goapi.Object:
		if t == nil {
			b.WriteString("null")
			return
		}
		keys := t.Keys()
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner)
			quote(b, k)
			b.WriteString(": ")
			writeIndented(b, t.Get(k), inner)
		}
		b.WriteString("\n" + indent + "}")
	default:
		writeJSON(b, v, false)
	}
}

// jsNumber is Number(v).
func jsNumber(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case string:
		return jsbind.ParseNumber(t)
	case []any:
		return jsbind.ParseNumber(jsString(t))
	}
	return math.NaN()
}

// spread is {...dst, ...src}: the own keys of src set on dst in order. A
// string spreads its code units under their indexes, a list its items.
func spread(dst *goapi.Object, src any) *goapi.Object {
	switch t := src.(type) {
	case *goapi.Object:
		if t != nil {
			for _, k := range t.Keys() {
				dst.Set(k, t.Get(k))
			}
		}
	case []any:
		for i, el := range t {
			dst.Set(strconv.Itoa(i), el)
		}
	case string:
		for i, u := range units(t) {
			dst.Set(strconv.Itoa(i), fromUnits([]uint16{u}))
		}
	}
	return dst
}

// sortKeys is Array.prototype.sort on strings: by UTF-16 code units.
func sortKeys(keys []string) {
	sort.SliceStable(keys, func(i, j int) bool { return lessUnits(keys[i], keys[j]) })
}

func lessUnits(a, b string) bool {
	ua, ub := units(a), units(b)
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// quote writes s as JSON.stringify writes a string.
func quote(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); {
		if u, ok := loneSurrogateAt(s, i); ok {
			b.WriteString(`\u`)
			b.WriteByte(hex[u>>12])
			b.WriteByte(hex[u>>8&15])
			b.WriteByte(hex[u>>4&15])
			b.WriteByte(hex[u&15])
			i += 3
			continue
		}
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
		case c < 0x80:
			b.WriteByte(c)
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune(utf8.RuneError)
			} else {
				b.WriteString(s[i : i+size])
			}
			i += size
			continue
		}
		i++
	}
	b.WriteByte('"')
}

// JSON is a value that goes into an answer as JSON.stringify wrote it.
type JSON struct{ V any }

// MarshalJSON implements json.Marshaler.
func (j JSON) MarshalJSON() ([]byte, error) { return []byte(stringify(j.V)), nil }

// errJSON is the SyntaxError of JSON.parse.
var errJSON = errors.New("Unexpected token in JSON")

// parseJSON is JSON.parse(text): the whole text one JSON value with white
// space around it. Escapes of lone surrogates are kept (see above); a
// key seen twice keeps its first place and takes the last value.
func parseJSON(text string) (any, error) {
	p := &jsonParser{s: text}
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

type jsonParser struct {
	s string
	i int
}

// maxDepth keeps a hostile document from exhausting the stack. V8 throws
// a RangeError somewhat deeper; both refuse the document.
const maxDepth = 10000

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

func (p *jsonParser) value(depth int) (any, error) {
	if depth > maxDepth || p.i >= len(p.s) {
		return nil, errJSON
	}
	switch c := p.s[p.i]; {
	case c == '{':
		p.i++
		o := goapi.NewObject()
		p.space()
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return o, nil
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
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			o.Set(key, v)
			p.space()
			if p.i >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == '}' {
				p.i++
				return o, nil
			}
			return nil, errJSON
		}
	case c == '[':
		p.i++
		arr := []any{}
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return arr, nil
		}
		for {
			p.space()
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.space()
			if p.i >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.i] == ',' {
				p.i++
				continue
			}
			if p.s[p.i] == ']' {
				p.i++
				return arr, nil
			}
			return nil, errJSON
		}
	case c == '"':
		return p.str()
	case c == 't':
		return p.word("true", true)
	case c == 'f':
		return p.word("false", false)
	case c == 'n':
		return p.word("null", nil)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, errJSON
}

func (p *jsonParser) word(w string, v any) (any, error) {
	if !strings.HasPrefix(p.s[p.i:], w) {
		return nil, errJSON
	}
	p.i += len(w)
	return v, nil
}

func (p *jsonParser) number() (any, error) {
	start := p.i
	if p.s[p.i] == '-' {
		p.i++
	}
	digits := func() int {
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.i < len(p.s) && p.s[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		return nil, errJSON
	}
	if p.i < len(p.s) && p.s[p.i] == '.' {
		p.i++
		if digits() == 0 {
			return nil, errJSON
		}
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			return nil, errJSON
		}
	}
	f, err := strconv.ParseFloat(p.s[start:p.i], 64)
	if err != nil {
		var numErr *strconv.NumError
		if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
			return f, nil
		}
		return nil, errJSON
	}
	return f, nil
}

func (p *jsonParser) str() (string, error) {
	p.i++ // the opening quote
	var us []uint16
	plainStart := p.i
	flush := func(end int) {
		if end > plainStart {
			us = append(us, units(p.s[plainStart:end])...)
		}
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '"':
			flush(p.i)
			p.i++
			return fromUnits(us), nil
		case c < 0x20:
			return "", errJSON
		case c == '\\':
			flush(p.i)
			if p.i+1 >= len(p.s) {
				return "", errJSON
			}
			e := p.s[p.i+1]
			p.i += 2
			switch e {
			case '"', '\\', '/':
				us = append(us, uint16(e))
			case 'b':
				us = append(us, '\b')
			case 'f':
				us = append(us, '\f')
			case 'n':
				us = append(us, '\n')
			case 'r':
				us = append(us, '\r')
			case 't':
				us = append(us, '\t')
			case 'u':
				if p.i+4 > len(p.s) {
					return "", errJSON
				}
				n, err := strconv.ParseUint(p.s[p.i:p.i+4], 16, 16)
				if err != nil || strings.ContainsAny(p.s[p.i:p.i+4], "+-") {
					return "", errJSON
				}
				us = append(us, uint16(n))
				p.i += 4
			default:
				return "", errJSON
			}
			plainStart = p.i
		default:
			p.i++
		}
	}
	return "", errJSON
}

// byteOrderMark is U+FEFF in UTF-8.
const byteOrderMark = "\xef\xbb\xbf"

func mathNaN() float64 { return math.NaN() }
