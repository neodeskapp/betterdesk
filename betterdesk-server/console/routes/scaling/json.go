package scaling

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/middleware"
)

// Relays and rules are stored as JSON text and handed to the client as
// they are stored, with whatever members an older version or an editor
// left in them. So they are kept as JSON values, objects with their
// members in order:
//
//	nil            null
//	bool           true, false
//	json.Number    a number that was stored
//	float64        a number this package set
//	string         a string
//	[]any          an array
//	*object        an object

type object struct {
	keys []string
	m    map[string]any
}

func newObject() *object { return &object{m: map[string]any{}} }

// set adds a member or replaces its value; a replaced member keeps its
// place.
func (o *object) set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.m[key] = value
}

func (o *object) clone() *object {
	c := &object{keys: append([]string(nil), o.keys...), m: make(map[string]any, len(o.m))}
	for k, v := range o.m {
		c.m[k] = v
	}
	return c
}

// MarshalJSON writes the object as JSON.stringify would.
func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	writeValue(&b, o)
	return b.Bytes(), nil
}

// list is a stored array as it goes into an answer.
type list []any

// MarshalJSON writes the array as JSON.stringify would.
func (l list) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	writeValue(&b, []any(l))
	return b.Bytes(), nil
}

// parseArray is JSON.parse(text) for a stored list: the elements when the
// text is a JSON array, nothing when it is another JSON value, and an
// error when it is no JSON.
func parseArray(text string) ([]any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	value, err := readValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected text after the JSON value")
	}
	items, _ := value.([]any)
	return items, nil
}

func readValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('['):
		items := []any{}
		for dec.More() {
			item, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		_, err := dec.Token()
		return items, err
	case json.Delim('{'):
		obj := newObject()
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			member, err := readValue(dec)
			if err != nil {
				return nil, err
			}
			obj.set(key.(string), member)
		}
		_, err := dec.Token()
		return obj, err
	}
	return tok, nil
}

const hexDigits = "0123456789abcdef"

func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
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
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xF])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

func writeValue(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case json.Number:
		f, _ := x.Float64()
		writeNumber(b, f)
	case float64:
		writeNumber(b, x)
	case string:
		writeString(b, x)
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeValue(b, item)
		}
		b.WriteByte(']')
	case *object:
		b.WriteByte('{')
		for i, key := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, key)
			b.WriteByte(':')
			writeValue(b, x.m[key])
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// writeNumber writes a number as JSON.stringify does: the shortest digits
// that read back as the same number, an exponent only beyond 21 digits or
// below a millionth, and null for what is no finite number.
func writeNumber(b *bytes.Buffer, f float64) {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		b.WriteString("null")
		return
	case f == 0:
		b.WriteByte('0')
		return
	case f < 0:
		b.WriteByte('-')
		f = -f
	}
	mantissa, expText, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.Replace(mantissa, ".", "", 1)
	k, n := len(digits), exp+1
	switch {
	case k <= n && n <= 21:
		b.WriteString(digits + strings.Repeat("0", n-k))
	case 0 < n && n <= 21:
		b.WriteString(digits[:n] + "." + digits[n:])
	case -6 < n && n <= 0:
		b.WriteString("0." + strings.Repeat("0", -n) + digits)
	default:
		b.WriteString(digits[:1])
		if k > 1 {
			b.WriteString("." + digits[1:])
		}
		if n-1 < 0 {
			b.WriteString("e-" + strconv.Itoa(1-n))
		} else {
			b.WriteString("e+" + strconv.Itoa(n-1))
		}
	}
}

// errNotAnObject is the TypeError JavaScript throws when a member is read
// from null: a stored list with a null in it fails the routes that look
// into its elements.
var errNotAnObject = errors.New("cannot read a member of null")

// member is element[key] as JavaScript reads it: the value for an
// object, nothing for a number, a text or a list, and an error for null.
func member(element any, key string) (any, error) {
	switch x := element.(type) {
	case nil:
		return nil, errNotAnObject
	case *object:
		return x.m[key], nil
	}
	return nil, nil
}

// isJSSpace reports the white space and line ends of JavaScript's trim and
// of \s in its patterns.
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

// utf16Len is text.length.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// text is String(value || fallback).trim() for a body member: a text as
// it is, a number or a boolean as JavaScript writes it, a list as its
// elements joined with commas, an object as "[object Object]".
func text(v middleware.Value, fallback string) string {
	if !v.Truthy() {
		return fallback
	}
	return strings.TrimFunc(stringOf(v), isJSSpace)
}

func stringOf(v middleware.Value) string {
	switch v.Kind() {
	case middleware.KindString, middleware.KindNumber, middleware.KindBool:
		return v.Text("")
	case middleware.KindArray:
		items, _ := v.Array()
		parts := make([]string, len(items))
		for i, item := range items {
			if item.Kind() != middleware.KindNull {
				parts[i] = stringOf(item)
			}
		}
		return strings.Join(parts, ",")
	case middleware.KindObject:
		return "[object Object]"
	case middleware.KindNull:
		return "null"
	}
	return "undefined"
}

// parseInt is parseInt(value, 10) for a body member: white space, a sign
// and digits from the start of its text. ok is false when there are none
// (NaN).
func parseInt(v middleware.Value) (n float64, ok bool) {
	return parseIntText(stringOf(v))
}

func parseIntText(s string) (n float64, ok bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	sign := 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, _ = strconv.ParseFloat(s[:end], 64)
	if math.IsInf(n, 0) {
		return 0, false
	}
	return sign * n, true
}
