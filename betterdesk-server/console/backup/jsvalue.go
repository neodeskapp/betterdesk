package backup

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// Values parsed from a backup are those of package branding: nil (null),
// branding.Undefined, bool, float64, string, []any and *branding.Object,
// with the keys of an object in JavaScript's order. A Buffer that a dump
// turned into {"__buf__": base64} becomes buffer once it is bound.

// buffer is a Buffer of the old console: bytes a driver binds as a BLOB
// or bytea.
type buffer []byte

// jsTypeError is a JavaScript TypeError: its text is what the old console
// reported.
type jsTypeError string

func (e jsTypeError) Error() string { return string(e) }

// The better-sqlite3 errors for values it cannot bind.
var (
	errBindType    = jsbind.ErrBindType
	errTooFew      = jsbind.ErrTooFewValues
	errTooMany     = jsbind.ErrTooManyValues
	errTwoObjects  = jsbind.ErrTwoObjects
	errNullToObjct = jsTypeError("Cannot convert undefined or null to object")
)

// prop is v[key] for a parsed value: the member of an object, undefined
// for anything else. A string has no members a backup uses.
func prop(v any, key string) any {
	o, ok := v.(*branding.Object)
	if !ok || o == nil {
		return branding.Undefined
	}
	value, has := o.Get(key)
	if !has {
		return branding.Undefined
	}
	return value
}

// readProp is v.key where JavaScript throws for null and undefined.
func readProp(v any, key string) (any, error) {
	switch v.(type) {
	case nil:
		return nil, jsTypeError("Cannot read properties of null (reading '" + key + "')")
	}
	if v == branding.Undefined {
		return nil, jsTypeError("Cannot read properties of undefined (reading '" + key + "')")
	}
	return prop(v, key), nil
}

// objectKeys is Object.keys(v): an object's keys, a string's indexes, an
// array's indexes, nothing for a number or a boolean, and a TypeError for
// null and undefined.
func objectKeys(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, errNullToObjct
	case *branding.Object:
		return x.Keys(), nil
	case []any:
		keys := make([]string, len(x))
		for i := range x {
			keys[i] = strconv.Itoa(i)
		}
		return keys, nil
	case string:
		n := len(utf16.Encode([]rune(x)))
		keys := make([]string, n)
		for i := range keys {
			keys[i] = strconv.Itoa(i)
		}
		return keys, nil
	}
	if v == branding.Undefined {
		return nil, errNullToObjct
	}
	return nil, nil
}

// truthy is JavaScript's !!v.
func truthy(v any) bool { return branding.Truthy(v) }

// jsString is String(v), and the TypeError JavaScript throws for an object
// whose own toString key hides the method.
func jsString(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "null", nil
	case bool:
		return strconv.FormatBool(x), nil
	case float64:
		return jsbind.NumberString(x), nil
	case string:
		return x, nil
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e == nil || e == branding.Undefined {
				continue
			}
			s, err := jsString(e)
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return strings.Join(parts, ","), nil
	case *branding.Object:
		if x.Has("toString") {
			return "", jsTypeError("Cannot convert object to primitive value")
		}
		return "[object Object]", nil
	}
	return "undefined", nil
}

// isBufferObject is v && typeof v === 'object' && typeof v.__buf__ ===
// 'string': the form of a Buffer in a dump.
func isBufferObject(v any) (string, bool) {
	o, ok := v.(*branding.Object)
	if !ok || o == nil {
		return "", false
	}
	b, has := o.Get("__buf__")
	s, isText := b.(string)
	return s, has && isText
}

// sqliteArgs binds values as better-sqlite3 did for a statement with n
// placeholders: numbers as doubles (NaN as NULL), text, Buffers as BLOBs,
// null and undefined as NULL; an array gives one value per element; an
// object is a set of named parameters, which the statements here do not
// have; a boolean cannot be bound.
func sqliteArgs(n int, values ...any) ([]any, error) {
	args := make([]any, 0, len(values))
	object := false
	one := func(v any) error {
		switch x := v.(type) {
		case nil:
			args = append(args, nil)
		case float64:
			if math.IsNaN(x) {
				args = append(args, nil)
			} else {
				args = append(args, x)
			}
		case string:
			args = append(args, x)
		case buffer:
			args = append(args, []byte(x))
		default:
			if v == branding.Undefined {
				args = append(args, nil)
				return nil
			}
			return errBindType
		}
		return nil
	}
	for _, v := range values {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				if err := one(item); err != nil {
					return nil, err
				}
			}
		case *branding.Object:
			if object {
				return nil, errTwoObjects
			}
			object = true
		default:
			if err := one(v); err != nil {
				return nil, err
			}
		}
	}
	switch {
	case len(args) < n:
		return nil, errTooFew
	case len(args) > n:
		return nil, errTooMany
	}
	return args, nil
}

// pgArg binds a value as node-postgres did: NULL for null and undefined,
// the bytes of a Buffer, an array literal for an array, the JSON text of
// an object, and the text of everything else.
func pgArg(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case buffer:
		return []byte(x)
	case []any:
		return pgArray(x)
	case *branding.Object:
		return branding.Stringify(x)
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsbind.NumberString(x)
	}
	if v == branding.Undefined {
		return nil
	}
	return branding.Stringify(v)
}

// pgArray is node-postgres's arrayString.
func pgArray(items []any) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		switch x := item.(type) {
		case nil:
			b.WriteString("NULL")
			continue
		case []any:
			b.WriteString(pgArray(x))
			continue
		case buffer:
			b.WriteString(`"\\x`)
			const hex = "0123456789abcdef"
			for _, c := range x {
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0xf])
			}
			b.WriteByte('"')
			continue
		}
		if item == branding.Undefined {
			b.WriteString("NULL")
			continue
		}
		text, _ := pgArg(item).(string)
		b.WriteByte('"')
		b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// nodeBase64 is Buffer.from(text, 'base64'): the standard and the URL
// alphabet, other characters skipped, the first '=' ends the data, and a
// lone sixth group of bits at the end is dropped.
func nodeBase64(text string) []byte {
	var out []byte
	var acc uint32
	n := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		var v byte
		switch {
		case c >= 'A' && c <= 'Z':
			v = c - 'A'
		case c >= 'a' && c <= 'z':
			v = c - 'a' + 26
		case c >= '0' && c <= '9':
			v = c - '0' + 52
		case c == '+' || c == '-':
			v = 62
		case c == '/' || c == '_':
			v = 63
		case c == '=':
			i = len(text)
			continue
		default:
			continue
		}
		acc = acc<<6 | uint32(v)
		n++
		if n == 4 {
			out = append(out, byte(acc>>16), byte(acc>>8), byte(acc))
			acc, n = 0, 0
		}
	}
	switch n {
	case 2:
		out = append(out, byte(acc>>4))
	case 3:
		out = append(out, byte(acc>>10), byte(acc>>2))
	}
	return out
}

// ToFixed is (num / den).toFixed(digits) for a positive num and a
// den that is a power of two, so the quotient is exact in binary: rounded
// half up, as JavaScript rounds.
func ToFixed(num, den int64, digits int) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n := new(big.Int).Mul(big.NewInt(num), scale)
	n.Mul(n, big.NewInt(2))
	n.Add(n, big.NewInt(den))
	n.Div(n, big.NewInt(2*den))
	s := n.String()
	if digits == 0 {
		return s
	}
	if len(s) <= digits {
		s = strings.Repeat("0", digits-len(s)+1) + s
	}
	return s[:len(s)-digits] + "." + s[len(s)-digits:]
}

// UTF16Len is the length of s as a JavaScript string.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// errorText is err.message as the old console's catch saw it.
func errorText(err error) string {
	var js *branding.Error
	if errors.As(err, &js) {
		return js.Message
	}
	return jsbind.Message(err)
}
